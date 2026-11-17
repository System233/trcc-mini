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

func validateDisplaySlots(slots map[string]slotConfig, derived ...map[string]derivedConfig) error {
	var derivedMetrics map[string]derivedConfig
	if len(derived) > 0 {
		derivedMetrics = derived[0]
	}
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
			if !supportedMetric(metric, derivedMetrics) {
				return fmt.Errorf("display.slots.%s: unknown metric %q", name, metric)
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
	Metric  string
	Value   float64
	Valid   bool
	Symbols []string
	Unit    string
}

func (s slotSelection) String() string {
	if !s.Valid {
		return "N/A"
	}
	suffix := s.Unit
	if suffix == "" {
		suffix = metricNativeUnit(s.Metric)
	}
	if suffix == "percent" {
		suffix = "%"
	}
	if suffix == "C" || suffix == "F" {
		suffix = "°" + suffix
	}
	if len(s.Symbols) > 0 {
		return fmt.Sprintf("%s=%.1f%s [LED:%s]", s.Metric, s.Value, suffix, strings.Join(s.Symbols, "+"))
	}
	return fmt.Sprintf("%s=%.1f%s", s.Metric, s.Value, suffix)
}

func metricKind(metric string) string {
	if strings.HasSuffix(metric, ".estimated") {
		return "power"
	}
	_, _, suffix, ok := metricDevice(metric)
	if ok {
		switch {
		case suffix == "power" || strings.HasPrefix(suffix, "power."):
			return "power"
		case strings.Contains(suffix, "temperature"):
			return "temperature"
		case suffix == "load" || strings.HasPrefix(suffix, "load.") || strings.HasSuffix(suffix, ".load"):
			return "load"
		}
	}
	if i := strings.IndexByte(metric, '.'); i >= 0 {
		return metric[i+1:]
	}
	return ""
}

func readMetric(m metrics, name string) (float64, bool) {
	if s, ok := m.Extras[name]; ok {
		return s.Value, s.Valid
	}
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
		s, ok := m.Extras[name]
		return s.Value, ok && s.Valid
	}
}

func primarySlot(name string) bool {
	return name == cpuPrimary || name == gpuPrimary
}

// Two-digit slots can show 0..99 and exactly 100 using dedicated hundreds
// segments. Larger values cannot be rendered without inventing/clamping.
func selectableMetric(m metrics, metric string, primary bool, options ...map[string]indicatorConfig) (slotSelection, bool) {
	v, ok := readMetric(m, metric)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return slotSelection{}, false
	}
	var indicators map[string]indicatorConfig
	if len(options) > 0 {
		indicators = options[0]
	}
	v = displayValue(metric, v, indicators, nil)
	rounded := math.Round(v)
	limit := 100.0
	if primary {
		limit = 999
	}
	if rounded < 0 || rounded > limit {
		return slotSelection{}, false
	}
	return slotSelection{Metric: metric, Value: v, Valid: true, Unit: displayUnitLabel(metric, indicators)}, true
}

// Missing or unrepresentable values are skipped. No zero-value fabrication.
// Independent intervals do not share a global phase or metric index.
func selectSlot(config slotConfig, m metrics, name string, elapsed time.Duration, options ...map[string]indicatorConfig) slotSelection {
	var indicators map[string]indicatorConfig
	if len(options) > 0 {
		indicators = options[0]
	}
	available := make([]slotSelection, 0, len(config.Metrics))
	for _, metric := range config.Metrics {
		if s, ok := selectableMetric(m, metric, primarySlot(name), indicators); ok {
			s.Symbols = metricSymbols(metric, name, indicators, nil)
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

func resolveDisplaySlots(config map[string]slotConfig, m metrics, elapsed time.Duration, options ...map[string]indicatorConfig) map[string]slotSelection {
	var indicators map[string]indicatorConfig
	if len(options) > 0 {
		indicators = options[0]
	}
	selected := make(map[string]slotSelection, len(slotNames))
	for _, name := range slotNames {
		selected[name] = selectSlot(config[name], m, name, elapsed, indicators)
	}
	return selected
}

func powerSensorsRequired(slots map[string]slotConfig, style int, definitions ...map[string]derivedConfig) (cpu, gpu bool) {
	var derived map[string]derivedConfig
	if len(definitions) > 0 {
		derived = definitions[0]
	}
	if style == 3 || style == 5 || style == 6 {
		return true, true
	} // dedicated power digits
	if style != 2 {
		return false, false
	}
	for _, name := range slotNames {
		for _, metric := range slots[name].Metrics {
			var visit func(string)
			visit = func(source string) {
				kind, _, suffix, isDevice := metricDevice(source)
				if isDevice && (suffix == "power" || strings.HasPrefix(suffix, "power.")) {
					if kind == "cpu" {
						cpu = true
					} else {
						gpu = true
					}
				} else {
					for _, dependency := range derived[source].Sources {
						visit(dependency)
					}
				}
			}
			visit(metric)
		}
	}
	return
}

// Memory telemetry is obtained only when an active slot can show it.
func memorySensorsRequired(slots map[string]slotConfig, style int) (ram, vram bool) {
	if style != 2 {
		return
	}
	for _, name := range slotNames {
		for _, metric := range slots[name].Metrics {
			if strings.HasPrefix(metric, "memory.") {
				ram = true
			}
			kind, _, suffix, ok := metricDevice(metric)
			if ok && kind == "gpu" && strings.HasPrefix(suffix, "memory.") {
				vram = true
			}
		}
	}
	return
}

func renderPA120Slots(slots map[string]slotSelection, rowLabels ...map[string]string) []bool {
	out := make([]bool, 84)
	for _, name := range slotNames {
		s := slots[name]
		if !s.Valid {
			continue
		}
		value := int(math.Round(s.Value))
		switch name {
		case cpuPrimary:
			for _, sym := range s.Symbols {
				if sym == "C" {
					mark(out, 4)
				}
				if sym == "F" {
					mark(out, 5)
				}
			}
			encodeNum(out, value, 3, 10)
		case cpuSecondary:
			for _, sym := range s.Symbols {
				if sym == "%" {
					mark(out, 6)
				}
			}
			encodePartial(out, value, 31, 80, 81)
		case gpuPrimary:
			for _, sym := range s.Symbols {
				if sym == "C" {
					mark(out, 7)
				}
				if sym == "F" {
					mark(out, 8)
				}
			}
			encodeNum(out, value, 3, 45)
		case gpuSecondary:
			for _, sym := range s.Symbols {
				if sym == "%" {
					mark(out, 9)
				}
			}
			encodePartial(out, value, 66, 82, 83)
		}
	}
	var overrides map[string]string
	if len(rowLabels) > 0 {
		overrides = rowLabels[0]
	}
	for _, row := range []struct {
		name          string
		symbol        string
		first, second string
		led1, led2    int
	}{
		{"cpu", "CPU", cpuPrimary, cpuSecondary, 0, 1},
		{"gpu", "GPU", gpuPrimary, gpuSecondary, 2, 3},
	} {
		lit := false
		for _, slot := range []string{row.first, row.second} {
			s := slots[slot]
			if !s.Valid {
				continue
			}
			for _, symbol := range s.Symbols {
				if symbol == row.symbol {
					lit = true
				}
			}
		}
		switch overrides[row.name] {
		case "on":
			lit = true
		case "off":
			lit = false
		}
		if lit {
			mark(out, row.led1)
			mark(out, row.led2)
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
