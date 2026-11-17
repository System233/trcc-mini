//go:build darwin

// SPDX-License-Identifier: GPL-3.0-or-later
// macOS unprivileged memory/CPU metrics, optional root powermetrics.
package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

func validCPUTempSource(source string) bool {
	switch source {
	case "tctl", "ccd1", "ccd2", "ccd-max", "ccd-avg":
		return true
	}
	return false
}

type cpuLoadSampler struct {
	lastIdle, lastTotal uint64
	initialized         bool
	sampled             time.Time
	last                int
}

func (s *cpuLoadSampler) Sample() int {
	if !s.sampled.IsZero() && time.Since(s.sampled) < time.Second {
		return s.last
	}
	s.sampled = time.Now()
	// 'kern.cp_time' is CPU_STATES (user,nice,system,idle) on macOS.
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	out, e := exec.CommandContext(ctx, "sysctl", "-n", "kern.cp_time").Output()
	if e != nil {
		return s.last
	}
	fields := strings.Fields(string(out))
	if len(fields) < 4 {
		return s.last
	}
	var total, idle uint64
	for i, f := range fields {
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return s.last
		}
		total += n
		if i == 3 {
			idle = n
		}
	}
	if s.initialized && total > s.lastTotal && idle >= s.lastIdle {
		delta := total - s.lastTotal
		di := idle - s.lastIdle
		if di <= delta {
			s.last = int(math.Round(100 * float64(delta-di) / float64(delta)))
		}
	}
	s.initialized = true
	s.lastTotal, s.lastIdle = total, idle
	return s.last
}

var macMemoryCache struct {
	sync.Mutex
	at               time.Time
	used, total, pct float64
	valid            bool
}
var vmPagePattern = regexp.MustCompile(`page size of\s+(\d+)\s+bytes`)

func readSystemMemory() (usedGiB, totalGiB, pct float64, ok bool) {
	macMemoryCache.Lock()
	defer macMemoryCache.Unlock()
	if time.Since(macMemoryCache.at) < time.Second {
		return macMemoryCache.used, macMemoryCache.total, macMemoryCache.pct, macMemoryCache.valid
	}
	macMemoryCache.at = time.Now()
	macMemoryCache.valid = false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	totalBytes, e := exec.CommandContext(ctx, "sysctl", "-n", "hw.memsize").Output()
	if e != nil {
		return
	}
	total, e := strconv.ParseFloat(strings.TrimSpace(string(totalBytes)), 64)
	if e != nil || total <= 0 {
		return
	}
	vm, e := exec.CommandContext(ctx, "vm_stat").Output()
	if e != nil {
		return
	}
	pageSize := float64(4096)
	if match := vmPagePattern.FindStringSubmatch(string(vm)); len(match) == 2 {
		if value, e := strconv.ParseFloat(match[1], 64); e == nil && value > 0 {
			pageSize = value
		}
	}
	counters := map[string]float64{}
	for _, line := range strings.Split(string(vm), "\n") {
		pair := strings.SplitN(line, ":", 2)
		if len(pair) != 2 {
			continue
		}
		name := strings.TrimSpace(pair[0])
		number := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(pair[1]), "."))
		value, e := strconv.ParseFloat(number, 64)
		if e == nil {
			counters[name] = value
		}
	}
	free, hasFree := counters["Pages free"]
	inactive, hasInactive := counters["Pages inactive"]
	speculative := counters["Pages speculative"]
	if !hasFree || !hasInactive {
		return
	}
	// macOS unified-memory availability approximation; excludes reclaimable
	// inactive/speculative pages. Not GPU-specific VRAM telemetry.
	available := (free + inactive + speculative) * pageSize
	if available < 0 || available > total {
		return
	}
	const gib = 1024 * 1024 * 1024
	macMemoryCache.used = (total - available) / gib
	macMemoryCache.total = total / gib
	macMemoryCache.pct = 100 * (total - available) / total
	macMemoryCache.valid = true
	return macMemoryCache.used, macMemoryCache.total, macMemoryCache.pct, true
}

type macPowerSample struct {
	at                                                       time.Time
	cpuW, gpuW, cpuTemp, gpuLoad                             float64
	cpuPowerValid, gpuPowerValid, cpuTempValid, gpuLoadValid bool
}

var macPowerState struct {
	sync.Mutex
	value       macPowerSample
	lastAttempt time.Time
	refreshing  bool
}
var (
	macCPUWPattern    = regexp.MustCompile(`(?im)^\s*CPU Power:\s*([\d.]+)\s*mW`)
	macGPUWPattern    = regexp.MustCompile(`(?im)^\s*GPU Power:\s*([\d.]+)\s*mW`)
	macCPUTempPattern = regexp.MustCompile(`(?im)^\s*CPU die temperature:\s*([\d.]+)\s*C`)
	macGPULoadPattern = regexp.MustCompile(`(?im)^\s*GPU Active residency:\s*([\d.]+)%`)
)

func parsePowermetrics(data string) macPowerSample {
	var s macPowerSample
	read := func(re *regexp.Regexp) (float64, bool) {
		result := re.FindStringSubmatch(data)
		if len(result) != 2 {
			return 0, false
		}
		value, e := strconv.ParseFloat(result[1], 64)
		return value, e == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
	}
	if v, valid := read(macCPUWPattern); valid && v < 1000000 {
		s.cpuW, s.cpuPowerValid = v/1000, true
	}
	if v, valid := read(macGPUWPattern); valid && v < 1000000 {
		s.gpuW, s.gpuPowerValid = v/1000, true
	}
	if v, valid := read(macCPUTempPattern); valid && v > 0 && v < 130 {
		s.cpuTemp, s.cpuTempValid = v, true
	}
	if v, valid := read(macGPULoadPattern); valid && v <= 100 {
		s.gpuLoad, s.gpuLoadValid = v, true
	}
	return s
}
func readMacPower() macPowerSample {
	macPowerState.Lock()
	// powermetrics typically samples for 1s and may be slower. Never block
	// 200ms HID frame updates waiting for a privileged external command.
	if os.Geteuid() == 0 && !macPowerState.refreshing &&
		(time.Since(macPowerState.lastAttempt) >= 3*time.Second) {
		macPowerState.lastAttempt = time.Now()
		macPowerState.refreshing = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
			defer cancel()
			data, e := exec.CommandContext(ctx, "powermetrics", "--samplers", "cpu_power,smc", "-n", "1", "-i", "1000").Output()
			s := macPowerSample{at: time.Now()}
			if e == nil {
				s = parsePowermetrics(string(data))
				s.at = time.Now()
			}
			macPowerState.Lock()
			macPowerState.value = s
			macPowerState.refreshing = false
			macPowerState.Unlock()
		}()
	}
	snapshot := macPowerState.value
	macPowerState.Unlock()
	if snapshot.at.IsZero() || time.Since(snapshot.at) > 8*time.Second {
		return macPowerSample{} // never display stale sample forever
	}
	return snapshot
}

type macCPU struct{}

func openCPUSensor() (cpuTelemetry, error) { return &macCPU{}, nil }
func (*macCPU) Close()                     {}
func (*macCPU) ReadTemp(source string) (float64, error) {
	if source != "tctl" {
		return 0, fmt.Errorf("%s: macOS CCD sensors not exposed via standard interface", source)
	}
	value := readMacPower()
	if value.cpuTempValid {
		return value.cpuTemp, nil
	}
	return 0, errors.New("macOS CPU die temperature requires supported powermetrics sampler with privileges")
}
func (*macCPU) ReadPower() (float64, bool, error) {
	p := readMacPower()
	if !p.cpuPowerValid {
		return 0, false, errors.New("macOS CPU package power unavailable (powermetrics privileges/support)")
	}
	return p.cpuW, true, nil
}

type macGPU struct{}

func openGPUSensor() (gpuTelemetry, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("macOS GPU hardware telemetry requires privileged powermetrics; unified memory has no dedicated VRAM capacity")
	}
	return &macGPU{}, nil
}
func (*macGPU) Close() {}
func (*macGPU) Read() (float64, bool, int, bool) {
	p := readMacPower()
	return 0, false, int(p.gpuLoad + 0.5), p.gpuLoadValid
}
func (*macGPU) ReadPower() (float64, bool) {
	p := readMacPower()
	return p.gpuW, p.gpuPowerValid
}
func (*macGPU) ReadMemory() (float64, float64, float64, bool) {
	// Apple unified RAM is NOT dedicated GPU VRAM.
	return 0, 0, 0, false
}
