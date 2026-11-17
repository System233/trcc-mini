//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Native Linux /proc and hwmon data sources, no privileged register I/O.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func splitLines(s string) []string { return strings.Split(strings.TrimSpace(s), "\n") }

func validCPUTempSource(source string) bool {
	switch source {
	case "tctl", "ccd1", "ccd2", "ccd-max", "ccd-avg":
		return true
	}
	return false
}

type cpuLoadSampler struct {
	lastIdle, lastTotal uint64
	ready               bool
	last                int
}

func (s *cpuLoadSampler) Sample() int {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return s.last
	}
	lines := splitLines(string(raw))
	if len(lines) == 0 {
		return s.last
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return s.last
	}
	var counters [8]uint64
	var total uint64
	// guest/guest_nice are already included in user/nice and must not be
	// counted twice in the aggregate tick total.
	for i := 1; i < len(fields) && i <= 8; i++ {
		n, e := strconv.ParseUint(fields[i], 10, 64)
		if e != nil {
			return s.last
		}
		counters[i-1] = n
		total += n
	}
	// idle and iowait are not utilization; count irq/softirq/steal as busy.
	idle := counters[3] + counters[4]
	if s.ready && total > s.lastTotal && idle >= s.lastIdle {
		d := total - s.lastTotal
		di := idle - s.lastIdle
		if di <= d {
			s.last = int(math.Round(100 * float64(d-di) / float64(d)))
		}
	}
	s.lastIdle, s.lastTotal, s.ready = idle, total, true
	return s.last
}

type hwmonChip struct {
	directory, name string
}

func scanHWMon() []hwmonChip {
	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	chips := make([]hwmonChip, 0, len(dirs))
	for _, dir := range dirs {
		b, e := os.ReadFile(filepath.Join(dir, "name"))
		if e != nil {
			continue
		}
		chips = append(chips, hwmonChip{dir, strings.TrimSpace(string(b))})
	}
	sort.Slice(chips, func(i, j int) bool { return chips[i].directory < chips[j].directory })
	return chips
}
func readSysFloat(path string) (float64, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return 0, e
	}
	f, e := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("invalid numeric value in %s", path)
	}
	return f, nil
}
func readHWMonTemps(chip hwmonChip) (map[string]float64, error) {
	files, _ := filepath.Glob(filepath.Join(chip.directory, "temp*_input"))
	readings := make(map[string]float64)
	for _, path := range files {
		base := strings.TrimSuffix(filepath.Base(path), "_input")
		raw, e := readSysFloat(path)
		if e != nil {
			continue
		}
		t := raw / 1000
		if t < 1 || t > 130 {
			continue
		}
		label, e := os.ReadFile(filepath.Join(chip.directory, base+"_label"))
		if e != nil {
			if chip.name == "coretemp" {
				continue
			}
			// AMD k10temp temp1 may omit a label.
			if chip.name == "k10temp" && base == "temp1" {
				readings["tctl"] = t
			}
			continue
		}
		text := strings.ToLower(strings.TrimSpace(string(label)))
		switch {
		case text == "tctl":
			readings["tctl"] = t
		case text == "tdie":
			readings["tdie"] = t
		case strings.HasPrefix(text, "tccd"):
			number := strings.TrimSpace(strings.TrimPrefix(text, "tccd"))
			if idx, e := strconv.Atoi(number); e == nil && idx > 0 {
				readings[fmt.Sprintf("ccd%d", idx)] = t
			}
		case text == "package id 0":
			readings["tctl"] = t
		}
	}
	return readings, nil
}

type cpuPowerProbe struct {
	hwmon       string
	energy      string
	energyRange float64
	lastEnergy  float64
	lastAt      time.Time
	primed      bool
}
type linuxCPUSensor struct {
	tempSensors []hwmonChip
	power       cpuPowerProbe
}

func openCPUSensor() (cpuTelemetry, error) {
	p := &linuxCPUSensor{}
	for _, chip := range scanHWMon() {
		if chip.name == "k10temp" || chip.name == "coretemp" {
			p.tempSensors = append(p.tempSensors, chip)
		}
	}
	// Prefer CPU-package RAPL energy counters if accessible; otherwise
	// accept a driver-provided CPU package hwmon power metric.
	domains, _ := filepath.Glob("/sys/class/powercap/*/energy_uj")
	sort.Strings(domains)
	for _, path := range domains {
		dir := filepath.Dir(path)
		name, _ := os.ReadFile(filepath.Join(dir, "name"))
		if !(strings.Contains(strings.ToLower(string(name)), "package-0") || strings.Contains(strings.ToLower(string(name)), "package 0")) {
			continue
		}
		if _, e := readSysFloat(path); e != nil {
			continue
		}
		p.power.energy = path
		p.power.energyRange, _ = readSysFloat(filepath.Join(dir, "max_energy_range_uj"))
		break
	}
	if p.power.energy == "" {
		for _, chip := range scanHWMon() {
			if chip.name != "k10temp" && chip.name != "zenpower" {
				continue
			}
			for _, filename := range []string{"power1_average", "power1_input"} {
				path := filepath.Join(chip.directory, filename)
				if _, e := readSysFloat(path); e == nil {
					p.power.hwmon = path
					break
				}
			}
			if p.power.hwmon != "" {
				break
			}
		}
	}
	if len(p.tempSensors) == 0 && p.power.energy == "" && p.power.hwmon == "" {
		return nil, errors.New("Linux CPU hwmon (k10temp/coretemp) and RAPL unavailable; temperature/power N/A")
	}
	return p, nil
}
func (p *linuxCPUSensor) Close() {}
func (p *linuxCPUSensor) ReadTemp(source string) (float64, error) {
	if !validCPUTempSource(source) {
		return 0, fmt.Errorf("unknown temperature source %q", source)
	}
	all := make(map[string]float64)
	for _, chip := range p.tempSensors {
		m, _ := readHWMonTemps(chip)
		for k, v := range m {
			if _, exists := all[k]; !exists {
				all[k] = v
			}
		}
	}
	if source == "tctl" {
		if val, ok := all["tctl"]; ok {
			return val, nil
		}
		if val, ok := all["tdie"]; ok {
			return val, nil
		}
	} else if source == "ccd1" || source == "ccd2" {
		if val, ok := all[source]; ok {
			return val, nil
		}
	} else {
		var sum, max float64
		count := 0
		for name, val := range all {
			if strings.HasPrefix(name, "ccd") {
				count++
				sum += val
				if val > max {
					max = val
				}
			}
		}
		if count > 0 {
			if source == "ccd-avg" {
				return sum / float64(count), nil
			}
			return max, nil
		}
	}
	return 0, fmt.Errorf("Linux hwmon CPU temperature %s unavailable", source)
}
func (p *linuxCPUSensor) ReadPower() (float64, bool, error) {
	if p.power.hwmon != "" {
		val, e := readSysFloat(p.power.hwmon)
		if e != nil {
			return 0, false, e
		}
		val /= 1000000 // µW -> W
		if val >= 0 && val <= 1000 {
			return val, true, nil
		}
		return 0, false, errors.New("invalid hwmon CPU power")
	}
	if p.power.energy == "" {
		return 0, false, errors.New("CPU package power not available via RAPL/hwmon")
	}
	now := time.Now()
	energy, e := readSysFloat(p.power.energy)
	if e != nil {
		return 0, false, e
	}
	if !p.power.primed {
		p.power.lastAt, p.power.lastEnergy, p.power.primed = now, energy, true
		return 0, false, nil
	}
	delta := energy - p.power.lastEnergy
	if delta < 0 && p.power.energyRange > 0 {
		delta += p.power.energyRange
	}
	dt := now.Sub(p.power.lastAt).Seconds()
	p.power.lastEnergy, p.power.lastAt = energy, now
	watts := delta / 1e6 / dt
	if dt <= 0 || watts < 0 || watts > 1000 || math.IsNaN(watts) || math.IsInf(watts, 0) {
		return 0, false, nil
	}
	return watts, true, nil
}

func readSystemMemory() (usedGiB, totalGiB, percent float64, valid bool) {
	f, e := os.Open("/proc/meminfo")
	if e != nil {
		return
	}
	defer f.Close()
	var total, available uint64
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = n
		case "MemAvailable:":
			available = n
		}
	}
	if total == 0 || available > total {
		return
	}
	const kibGiB = 1024 * 1024
	return float64(total-available) / kibGiB, float64(total) / kibGiB, 100 * float64(total-available) / float64(total), true
}
