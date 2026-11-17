// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"math"
	"strconv"
	"strings"
)

// Indexed metrics never require device aliases. Indexed GPU and CPU numbers
// are assigned by platform discovery; unindexed names are aggregates.
// A supported name is a capability request, not a promise of availability.
var cpuMetricUnits = map[string]string{
	"temperature": "C", "temperature.die": "C", "temperature.max": "C",
	"power": "W", "power.cores": "W", "power.soc": "W",
	"load": "percent", "clock": "MHz", "clock.max": "MHz", "voltage": "V",
}
var gpuMetricUnits = map[string]string{
	"temperature": "C", "temperature.hotspot": "C", "temperature.memory": "C",
	"power": "W", "power.board": "W", "power.limit": "W",
	"load": "percent", "load.compute": "percent", "load.memory": "percent",
	"clock.core": "MHz", "clock.memory": "MHz",
	"memory.used": "GiB", "memory.total": "GiB", "memory.load": "percent",
	"memory.shared.used": "GiB",
	"fan.speed":          "RPM", "fan.load": "percent",
	"pcie.tx": "MB/s", "pcie.rx": "MB/s",
}

func metricDevice(name string) (kind string, index int, suffix string, ok bool) {
	dot := strings.IndexByte(name, '.')
	if dot < 1 {
		return "", -1, "", false
	}
	prefix := name[:dot]
	suffix = name[dot+1:]
	if suffix == "" {
		return "", -1, "", false
	}
	kind, index = "", -1
	for _, root := range []string{"cpu", "gpu"} {
		if prefix == root {
			return root, -1, suffix, true
		}
		if !strings.HasPrefix(prefix, root) {
			continue
		}
		numeric := strings.TrimPrefix(prefix, root)
		// Do not silently allow noncanonical gpu01 or excessively large
		// numbers that cannot correspond to real sampled devices.
		if numeric == "" || len(numeric) > 2 || (len(numeric) > 1 && numeric[0] == '0') {
			return "", -1, "", false
		}
		value, err := strconv.Atoi(numeric)
		if err != nil || value < 0 || value > 63 {
			return "", -1, "", false
		}
		return root, value, suffix, true
	}
	return "", -1, "", false
}

func metricNativeUnit(name string) string {
	if unit, ok := builtinUnits[name]; ok {
		return unit
	}
	kind, _, suffix, ok := metricDevice(name)
	if !ok {
		return ""
	}
	if kind == "cpu" {
		if unit, ok := cpuMetricUnits[suffix]; ok {
			return unit
		}
		if strings.HasPrefix(suffix, "ccd") && strings.HasSuffix(suffix, ".temperature") {
			index := strings.TrimSuffix(strings.TrimPrefix(suffix, "ccd"), ".temperature")
			if n, err := strconv.Atoi(index); err == nil && n >= 0 && n <= 15 && index != "" && (len(index) == 1 || index[0] != '0') {
				return "C"
			}
		}
		if strings.HasPrefix(suffix, "core") {
			dot := strings.IndexByte(suffix, '.')
			if dot > 4 {
				number := suffix[4:dot]
				if n, err := strconv.Atoi(number); err == nil && n >= 0 && n <= 4095 && number != "" && (len(number) == 1 || number[0] != '0') {
					switch suffix[dot+1:] {
					case "load":
						return "percent"
					case "clock":
						return "MHz"
					case "temperature":
						return "C"
					}
				}
			}
		}
		return ""
	}
	return gpuMetricUnits[suffix]
}

func metricIsPower(name string) bool {
	if strings.HasSuffix(name, ".estimated") {
		return true
	}
	_, _, suffix, ok := metricDevice(name)
	return ok && (suffix == "power" || strings.HasPrefix(suffix, "power."))
}

// A power limit is a cap, not power draw. Do not admit it to measured
// power sums just because the unit happens to be watts.
func metricIsMeasuredPower(name string) bool {
	if !metricIsPower(name) {
		return false
	}
	_, _, suffix, ok := metricDevice(name)
	return !ok || suffix != "power.limit"
}

// Sum inputs that describe overlapping coverage are invalid even when the
// source names differ. A generic aggregate overlaps with any matching
// indexed power draw, and package/device power overlaps its subdomains.
func powerSourcesOverlap(a, b string) bool {
	ak, ai, as, aok := metricDevice(a)
	bk, bi, bs, bok := metricDevice(b)
	if !aok || !bok || ak != bk || !metricIsPower(a) || !metricIsPower(b) {
		return false
	}
	if ai >= 0 && bi >= 0 && ai != bi {
		return false
	}
	if as == bs {
		return true
	}
	// At least one source is the whole package/device; the other can be
	// included within it. Exact disjoint domains may be explicitly added.
	return as == "power" || bs == "power" || as == "power.board" || bs == "power.board"
}

// Aggregators omit invalid readings. A cumulative metric is not valid just
// because its value defaults to zero. Percentage ratios are calculated from
// paired numerator/denominator, never as an average of unrelated percentages.
type metricAcc struct {
	sum, max float64
	count    int
}

func (a *metricAcc) add(v float64, ok bool) {
	if !ok || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	a.sum += v
	if a.count == 0 || v > a.max {
		a.max = v
	}
	a.count++
}
func (a metricAcc) value(mode string) (float64, bool) {
	if a.count == 0 {
		return 0, false
	}
	if mode == "max" {
		return a.max, true
	}
	if mode == "avg" {
		return a.sum / float64(a.count), true
	}
	return a.sum, true
}

func aggregateIndexedMetrics(m *metrics, deviceCount map[string]int) {
	if m.Extras == nil {
		return
	}
	for kind, count := range deviceCount {
		if count == 0 {
			continue
		}
		units := gpuMetricUnits
		if kind == "cpu" {
			units = cpuMetricUnits
		}
		for suffix := range units {
			// Percentages of different sized memory pools are not additive.
			// CPU load is provided by the OS as a properly weighted average;
			// max/mean across uneven sockets would silently change semantics.
			if (kind == "gpu" && suffix == "memory.load") || (kind == "cpu" && suffix == "load") {
				continue
			}
			var a metricAcc
			for i := 0; i < count; i++ {
				key := kind + strconv.Itoa(i) + "." + suffix
				s, exists := m.Extras[key]
				if exists {
					a.add(s.Value, s.Valid)
				}
			}
			mode := "sum"
			if suffix == "temperature" || strings.HasPrefix(suffix, "temperature.") ||
				suffix == "clock.max" || suffix == "load" || strings.HasPrefix(suffix, "load.") {
				mode = "max"
			}
			if suffix == "clock" {
				mode = "avg"
			}
			if v, valid := a.value(mode); valid {
				m.setExtra(kind+"."+suffix, v)
			}
		}
		if kind == "gpu" {
			// GPU memory.load is total used / total capacity of the SAME
			// valid GPU pairs. Avoid summing percentages.
			used, total := 0.0, 0.0
			for i := 0; i < count; i++ {
				prefix := "gpu" + strconv.Itoa(i) + ".memory."
				u, uok := m.Extras[prefix+"used"]
				t, tok := m.Extras[prefix+"total"]
				if uok && tok && u.Valid && t.Valid && t.Value > 0 && u.Value <= t.Value {
					used += u.Value
					total += t.Value
				}
			}
			if total > 0 {
				m.setExtra("gpu.memory.load", 100*used/total)
			}
		}
	}
}
