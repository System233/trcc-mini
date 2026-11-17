// SPDX-License-Identifier: GPL-3.0-or-later
// PA120: independently configurable numeric slots, using the actual
// digit/indicator topology. No hardcoded assignment of a sensor to a slot.
package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	cpuPrimary   = "cpu.primary"
	cpuSecondary = "cpu.secondary"
	gpuPrimary   = "gpu.primary"
	gpuSecondary = "gpu.secondary"
)

var slotNames = [...]string{cpuPrimary, cpuSecondary, gpuPrimary, gpuSecondary}

type slotConfig struct {
	Metrics  []string `json:"metrics"`
	Interval string   `json:"interval,omitempty"`
}

func defaultDisplaySlots() map[string]slotConfig {
	return map[string]slotConfig{
		cpuPrimary:   {Metrics: []string{"cpu.temperature", "cpu.power"}, Interval: "4s"},
		cpuSecondary: {Metrics: []string{"cpu.load"}},
		gpuPrimary:   {Metrics: []string{"gpu.temperature", "gpu.power"}, Interval: "4s"},
		gpuSecondary: {Metrics: []string{"gpu.load"}},
	}
}

func validMetric(metric string) bool {
	switch metric {
	case "cpu.temperature", "cpu.load", "cpu.power", "gpu.temperature", "gpu.load", "gpu.power":
		return true
	default:
		return false
	}
}

func validateDisplaySlots(slots map[string]slotConfig) error {
	if len(slots) != len(slotNames) {
		return fmt.Errorf("display.slots must define exactly four PA120 slots: %s", strings.Join(slotNames[:], ", "))
	}
	for name, slot := range slots {
		validName := false
		for _, allowed := range slotNames {
			if name == allowed {
				validName = true
				break
			}
		}
		if !validName {
			return fmt.Errorf("unknown display.slots key %q (use %s)", name, strings.Join(slotNames[:], ", "))
		}
		if len(slot.Metrics) == 0 {
			return fmt.Errorf("display.slots.%s.metrics requires at least one metric", name)
		}
		seen := make(map[string]bool)
		for _, metric := range slot.Metrics {
			if !validMetric(metric) {
				return fmt.Errorf("display.slots.%s: unknown metric %q (cpu/gpu .temperature/.load/.power)", name, metric)
			}
			if seen[metric] {
				return fmt.Errorf("display.slots.%s: duplicate metric %q", name, metric)
			}
			seen[metric] = true
		}
		if len(slot.Metrics) > 1 && slot.Interval == "" {
			return fmt.Errorf("display.slots.%s.interval is required when rotating multiple metrics", name)
		}
		if slot.Interval != "" {
			interval, err := time.ParseDuration(slot.Interval)
			if err != nil || interval < 200*time.Millisecond {
				return fmt.Errorf("display.slots.%s.interval must be at least 200ms", name)
			}
		}
	}
	return nil
}

// A slotSelection is the particular valid value chosen for a physical region.
// The physical label/icons are chosen from the metric kind, not the region.
type slotSelection struct {
	Metric string
	Value  float64
	Valid  bool
}

func (s slotSelection) String() string {
	if !s.Valid {
		return "N/A"
	}
	suffix := ""
	switch metricKind(s.Metric) {
	case "temperature":
		suffix = "°C"
	case "load":
		suffix = "%"
	case "power":
		suffix = "W"
	}
	return fmt.Sprintf("%s=%.1f%s", s.Metric, s.Value, suffix)
}

func metricKind(metric string) string {
	if i := strings.IndexByte(metric, '.'); i >= 0 {
		return metric[i+1:]
	}
	return ""
}

func readMetric(m metrics, name string) (float64, bool) {
	switch name {
	case "cpu.temperature":
		return m.Temp, m.TempValid
	case "cpu.load":
		return float64(m.Load), true
	case "cpu.power":
		return m.CPUPower, m.CPUPowerValid
	case "gpu.temperature":
		return m.GPUTemp, m.GPUTempValid
	case "gpu.load":
		return float64(m.GPULoad), m.GPULoadValid
	case "gpu.power":
		return m.GPUPower, m.GPUPowerValid
	default:
		return 0, false
	}
}

func primarySlot(name string) bool {
	return name == cpuPrimary || name == gpuPrimary
}

// Two-digit slots can show 0..99 and exactly 100 using dedicated hundreds
// segments. Larger values cannot be rendered without inventing/clamping.
func selectableMetric(m metrics, metric string, primary bool) (slotSelection, bool) {
	v, ok := readMetric(m, metric)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return slotSelection{}, false
	}
	rounded := math.Round(v)
	limit := 100.0
	if primary {
		limit = 999
	}
	if rounded < 0 || rounded > limit {
		return slotSelection{}, false
	}
	return slotSelection{Metric: metric, Value: v, Valid: true}, true
}

// Missing or unrepresentable values are skipped. No zero-value fabrication.
// Independent intervals do not share a global phase or metric index.
func selectSlot(config slotConfig, m metrics, name string, elapsed time.Duration) slotSelection {
	available := make([]slotSelection, 0, len(config.Metrics))
	for _, metric := range config.Metrics {
		if s, ok := selectableMetric(m, metric, primarySlot(name)); ok {
			available = append(available, s)
		}
	}
	if len(available) == 0 {
		return slotSelection{}
	}
	if len(available) == 1 {
		return available[0]
	}
	interval, _ := time.ParseDuration(config.Interval) // config validated at startup
	if interval <= 0 {
		return available[0]
	}
	return available[int(elapsed/interval)%len(available)]
}

func resolveDisplaySlots(config map[string]slotConfig, m metrics, elapsed time.Duration) map[string]slotSelection {
	selected := make(map[string]slotSelection, len(slotNames))
	for _, name := range slotNames {
		selected[name] = selectSlot(config[name], m, name, elapsed)
	}
	return selected
}

func powerSensorsRequired(slots map[string]slotConfig, style int) (cpu, gpu bool) {
	if style == 3 || style == 5 || style == 6 {
		return true, true
	} // dedicated power digits
	if style != 2 {
		return false, false
	}
	for _, name := range slotNames {
		for _, metric := range slots[name].Metrics {
			if metric == "cpu.power" {
				cpu = true
			}
			if metric == "gpu.power" {
				gpu = true
			}
		}
	}
	return
}

func renderPA120Slots(slots map[string]slotSelection) []bool {
	out := make([]bool, 84)
	for _, name := range slotNames {
		s := slots[name]
		if !s.Valid {
			continue
		}
		value := int(math.Round(s.Value))
		kind := metricKind(s.Metric)
		switch name {
		case cpuPrimary:
			if kind == "temperature" {
				mark(out, 4)
			}
			encodeNum(out, value, 3, 10)
		case cpuSecondary:
			if kind == "load" {
				mark(out, 6)
			}
			encodePartial(out, value, 31, 80, 81)
		case gpuPrimary:
			if kind == "temperature" {
				mark(out, 7)
			}
			encodeNum(out, value, 3, 45)
		case gpuSecondary:
			if kind == "load" {
				mark(out, 9)
			}
			encodePartial(out, value, 66, 82, 83)
		}
	}
	// CPU/GPU labels are physical row markers, not properties of a
	// particular digit group. A load-only row still needs its label.
	for _, name := range []string{cpuPrimary, cpuSecondary} {
		if s := slots[name]; s.Valid && strings.HasPrefix(s.Metric, "cpu.") {
			mark(out, 0)
			mark(out, 1)
			break
		}
	}
	for _, name := range []string{gpuPrimary, gpuSecondary} {
		if s := slots[name]; s.Valid && strings.HasPrefix(s.Metric, "gpu.") {
			mark(out, 2)
			mark(out, 3)
			break
		}
	}
	return out
}

func slotSummary(selected map[string]slotSelection) string {
	return fmt.Sprintf("Slots: %s %s; %s %s; %s %s; %s %s",
		cpuPrimary, selected[cpuPrimary].String(),
		cpuSecondary, selected[cpuSecondary].String(),
		gpuPrimary, selected[gpuPrimary].String(),
		gpuSecondary, selected[gpuSecondary].String())
}
