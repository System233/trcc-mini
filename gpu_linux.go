//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Linux GPU providers: NVIDIA driver CLI, AMD DRM, Intel i915/xe. Polling is
// asynchronous after detection so a slow nvidia-smi never stalls HID frames.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type linuxGPUSnapshot struct {
	temp        float64
	tempValid   bool
	load        int
	loadValid   bool
	watts       float64
	powerValid  bool
	used, total float64
	memoryValid bool
}

func (s linuxGPUSnapshot) available() bool {
	return s.tempValid || s.loadValid || s.powerValid || s.memoryValid
}

type linuxGPU struct {
	backend     string
	device      string
	latest      atomic.Pointer[linuxGPUSnapshot]
	lastAttempt atomic.Int64
	refreshing  atomic.Bool
	intelEnergy intelEnergySampler
}

func (g *linuxGPU) initialSample() linuxGPUSnapshot {
	initial := g.collect()
	g.latest.Store(&initial)
	g.lastAttempt.Store(time.Now().UnixNano())
	return initial
}
func openGPUSensor() (gpuTelemetry, error) {
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		g := &linuxGPU{backend: "nvidia"}
		if snapshot := g.initialSample(); snapshot.available() {
			return g, nil
		}
	}
	cards, _ := filepath.Glob("/sys/class/drm/card*/device/vendor")
	for _, path := range cards {
		b, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		backend := ""
		switch strings.ToLower(strings.TrimSpace(string(b))) {
		case "0x1002":
			backend = "amd"
		case "0x8086":
			backend = "intel"
		default:
			continue
		}
		g := &linuxGPU{backend: backend, device: filepath.Dir(path)}
		if snapshot := g.initialSample(); snapshot.available() {
			return g, nil
		}
	}
	return nil, errors.New("Linux GPU telemetry unavailable (NVIDIA nvidia-smi, AMD amdgpu, or Intel i915/xe sysfs)")
}
func (g *linuxGPU) Close() {}

func parseNvidiaSnapshot(data string) (temp float64, tempValid bool, load int, loadValid bool, watts float64, powerValid bool, used, total float64, memoryValid bool) {
	line := strings.Split(strings.TrimSpace(data), "\n")[0]
	fields := strings.Split(line, ",")
	if len(fields) < 5 {
		return
	}
	metric := func(i int) (float64, bool) {
		raw := strings.TrimSpace(fields[i])
		if raw == "" || strings.EqualFold(raw, "N/A") {
			return 0, false
		}
		value, e := strconv.ParseFloat(raw, 64)
		if e != nil || value < 0 {
			return 0, false
		}
		return value, true
	}
	if t, ok := metric(0); ok && t < 130 {
		temp, tempValid = t, true
	}
	if l, ok := metric(1); ok && l <= 100 {
		load, loadValid = int(l+0.5), true
	}
	if p, ok := metric(2); ok && p <= 1500 {
		watts, powerValid = p, true
	}
	u, uok := metric(3)
	totalMiB, tok := metric(4)
	if uok && tok && totalMiB > 0 && u <= totalMiB {
		used, total, memoryValid = u/1024, totalMiB/1024, true
	}
	return
}

func (g *linuxGPU) collect() linuxGPUSnapshot {
	var s linuxGPUSnapshot
	if g.backend == "nvidia" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		output, e := exec.CommandContext(ctx, "nvidia-smi",
			"--id=0",
			"--query-gpu=temperature.gpu,utilization.gpu,power.draw,memory.used,memory.total",
			"--format=csv,noheader,nounits").Output()
		if e != nil {
			return s
		}
		s.temp, s.tempValid, s.load, s.loadValid, s.watts, s.powerValid, s.used, s.total, s.memoryValid = parseNvidiaSnapshot(string(output))
		return s
	}
	if g.backend == "intel" {
		return g.collectIntel()
	}
	readAMD := func(name string) (float64, bool) {
		v, e := readSysFloat(filepath.Join(g.device, name))
		return v, e == nil
	}
	if v, ok := readAMD("gpu_busy_percent"); ok && v <= 100 && v >= 0 {
		s.load, s.loadValid = int(v+0.5), true
	}
	u, uok := readAMD("mem_info_vram_used")
	t, tok := readAMD("mem_info_vram_total")
	if uok && tok && t > 0 && u <= t {
		const gib = 1024 * 1024 * 1024
		s.used, s.total, s.memoryValid = u/gib, t/gib, true
	}
	hwmons, _ := filepath.Glob(filepath.Join(g.device, "hwmon", "hwmon*"))
	for _, dir := range hwmons {
		if v, e := readSysFloat(filepath.Join(dir, "temp1_input")); e == nil && v > 0 && v < 130000 {
			s.temp, s.tempValid = v/1000, true
		}
		for _, name := range []string{"power1_average", "power1_input"} {
			if v, e := readSysFloat(filepath.Join(dir, name)); e == nil && v >= 0 && v <= 1500000000 {
				s.watts, s.powerValid = v/1000000, true
				break
			}
		}
		if s.tempValid || s.powerValid {
			break
		}
	}
	return s
}
func (g *linuxGPU) poll() {
	now := time.Now().UnixNano()
	if now-g.lastAttempt.Load() < int64(time.Second) {
		return
	}
	if !g.refreshing.CompareAndSwap(false, true) {
		return
	}
	g.lastAttempt.Store(now)
	go func() {
		snapshot := g.collect()
		g.latest.Store(&snapshot)
		g.refreshing.Store(false)
	}()
}
func (g *linuxGPU) snapshot() linuxGPUSnapshot {
	g.poll()
	s := g.latest.Load()
	if s == nil {
		return linuxGPUSnapshot{}
	}
	return *s
}
func (g *linuxGPU) Read() (float64, bool, int, bool) {
	s := g.snapshot()
	return s.temp, s.tempValid, s.load, s.loadValid
}
func (g *linuxGPU) ReadPower() (float64, bool) {
	s := g.snapshot()
	return s.watts, s.powerValid
}
func (g *linuxGPU) ReadMemory() (float64, float64, float64, bool) {
	s := g.snapshot()
	if !s.memoryValid {
		return 0, 0, 0, false
	}
	return s.used, s.total, 100 * s.used / s.total, true
}
func (g *linuxGPU) String() string { return fmt.Sprintf("%s GPU", g.backend) }
