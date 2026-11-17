//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Intel i915/xe GPU telemetry. These drivers expose hwmon readings on
// supported devices. No privileged MSR access or external helper.
package main

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type intelEnergySampler struct {
	path  string
	last  float64
	at    time.Time
	valid bool
}

func (e *intelEnergySampler) Watts(path string) (float64, bool) {
	raw, err := readSysFloat(path)
	if err != nil || raw < 0 {
		return 0, false
	}
	now := time.Now()
	if path != e.path {
		e.path = path
		e.valid = false
	}
	if !e.valid {
		e.last, e.at, e.valid = raw, now, true
		return 0, false
	}
	delta := raw - e.last
	seconds := now.Sub(e.at).Seconds()
	// A counter reset, suspected rollover or absurdly long interval must
	// not be interpreted as negative or enormous real device power.
	e.last, e.at = raw, now
	if delta < 0 || seconds < 0.1 || seconds > 30 {
		return 0, false
	}
	watts := delta / 1e6 / seconds // sysfs energy1_input is in microjoules
	if watts < 0 || watts > 1500 || math.IsNaN(watts) || math.IsInf(watts, 0) {
		return 0, false
	}
	return watts, true
}

func firstValidIntelBytes(paths ...string) (float64, bool) {
	for _, path := range paths {
		n, e := readSysFloat(path)
		if e == nil && n >= 0 {
			return n, true
		}
	}
	return 0, false
}

// Reading from a device-level hwmon (i915 or xe) prevents double-counting
// i915_gtN counters that represent subdomains of the same GPU.
func intelGPUHWMonDir(device string) string {
	dirs, _ := filepath.Glob(filepath.Join(device, "hwmon", "hwmon*"))
	sort.Strings(dirs)
	for _, dir := range dirs {
		b, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(string(b)))
		if name == "i915" || name == "xe" {
			return dir
		}
	}
	// Some kernel versions name only GT-level hwmon. They may carry
	// legitimate readings but must not be mistaken for device-wide watts.
	return ""
}

func (g *linuxGPU) collectIntel() linuxGPUSnapshot {
	var s linuxGPUSnapshot
	hwmon := intelGPUHWMonDir(g.device)
	if hwmon != "" {
		// GPU package temperature; xe exposes additional sensors on some
		// models, but only the primary device sensor maps to gpu.temperature.
		temperatureSensors := []string{"temp1_input", "temp2_input"}
		// Xe exposes package temperature through temp2_input (6.15+).
		// i915 uses temp1_input. Do not accidentally read Xe VRAM temp
		// (temp3_input) as GPU core/package temperature.
		if name, err := os.ReadFile(filepath.Join(hwmon, "name")); err == nil && strings.TrimSpace(string(name)) == "xe" {
			temperatureSensors = []string{"temp2_input", "temp1_input"}
		}
		for _, name := range temperatureSensors {
			if value, e := readSysFloat(filepath.Join(hwmon, name)); e == nil && value > 0 && value < 130000 {
				s.temp, s.tempValid = value/1000, true
				break
			}
		}
		for _, name := range []string{"power1_average", "power1_input"} {
			if value, e := readSysFloat(filepath.Join(hwmon, name)); e == nil && value >= 0 && value <= 1500000000 {
				s.watts, s.powerValid = value/1e6, true
				break
			}
		}
		if !s.powerValid {
			// Intel i915/xe device-level energy is cumulative µJ; two
			// samples are required. power1_max is a LIMIT, not draw.
			if power, ok := g.intelEnergy.Watts(filepath.Join(hwmon, "energy1_input")); ok {
				s.watts, s.powerValid = power, true
			}
		}
	}

	// Arc dedicated memory only. Integrated Intel GPUs share host RAM;
	// never report a fake VRAM capacity based on system memory.
	used, usedOK := firstValidIntelBytes(
		filepath.Join(g.device, "mem_info_vram_used"),
		filepath.Join(g.device, "lmem_used_bytes"),
		filepath.Join(g.device, "tile0", "vram0", "used_bytes"),
	)
	total, totalOK := firstValidIntelBytes(
		filepath.Join(g.device, "mem_info_vram_total"),
		filepath.Join(g.device, "lmem_total_bytes"),
		filepath.Join(g.device, "tile0", "vram0", "total_bytes"),
	)
	if usedOK && totalOK && total > 0 && used <= total {
		const gib = 1024 * 1024 * 1024
		s.used, s.total, s.memoryValid = used/gib, total/gib, true
	}
	// Global Intel GPU utilization cannot be inferred from frequency:
	// without a reliable busy counter it remains N/A.
	return s
}
