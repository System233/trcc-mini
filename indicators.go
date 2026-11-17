// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"math"
	"strings"
)

type colorStop struct {
	At    float64 `json:"at"`
	Color string  `json:"color"`
}
type colorRule struct {
	Mode  string      `json:"mode"` // solid|gradient|threshold
	Value string      `json:"value,omitempty"`
	Stops []colorStop `json:"stops,omitempty"`
}
type indicatorConfig struct {
	Unit    string     `json:"unit,omitempty"` // C/F/percent/W/GiB/MiB
	Symbols []string   `json:"symbols"`        // hardware CPU,GPU,C,F,% glyphs; [] disables all
	Color   *colorRule `json:"color,omitempty"`
}

var builtinUnits = map[string]string{
	"cpu.temperature": "C", "gpu.temperature": "C",
	"cpu.load": "percent", "gpu.load": "percent",
	"cpu.power": "W", "gpu.power": "W",
	"memory.used": "GiB", "memory.total": "GiB", "memory.load": "percent",
	"gpu.memory.used": "GiB", "gpu.memory.total": "GiB", "gpu.memory.load": "percent",
}

func supportedMetric(name string, derived map[string]derivedConfig) bool {
	if metricNativeUnit(name) != "" {
		return true
	}
	_, ok := derived[name]
	return ok
}

func unitForMetric(name string, indicators map[string]indicatorConfig, derived map[string]derivedConfig) string {
	native := metricNativeUnit(name)
	if _, ok := derived[name]; ok || strings.HasSuffix(name, ".estimated") {
		native = "W"
	}
	if v := indicators[name].Unit; v != "" {
		return v
	}
	return native
}

func validateColorRule(c colorRule) error {
	switch c.Mode {
	case "":
		return fmt.Errorf("color.mode is required when color is configured")
	case "solid":
		if c.Value == "" || len(c.Stops) != 0 {
			return fmt.Errorf("solid color requires value and no stops")
		}
		if _, err := parseColor(c.Value); err != nil {
			return err
		}
	case "gradient", "threshold":
		if c.Value != "" {
			return fmt.Errorf("%s color cannot specify value", c.Mode)
		}
		need := 1
		if c.Mode == "gradient" {
			need = 2
		}
		if len(c.Stops) < need || len(c.Stops) > 32 {
			return fmt.Errorf("%s color needs %d..32 stops", c.Mode, need)
		}
		for i, stop := range c.Stops {
			if math.IsNaN(stop.At) || math.IsInf(stop.At, 0) {
				return fmt.Errorf("color stop %d has invalid at", i)
			}
			if _, err := parseColor(stop.Color); err != nil {
				return fmt.Errorf("color stop %d: %w", i, err)
			}
			if i > 0 && stop.At <= c.Stops[i-1].At {
				return fmt.Errorf("color stops must be strictly ascending")
			}
		}
	default:
		return fmt.Errorf("unsupported color mode %q (solid/gradient/threshold)", c.Mode)
	}
	return nil
}

func validateSlotIndicators(slots map[string]slotConfig, indicators map[string]indicatorConfig, derived map[string]derivedConfig) error {
	if len(indicators) > 128 {
		return fmt.Errorf("too many indicators")
	}
	for name, setting := range indicators {
		if !supportedMetric(name, derived) {
			return fmt.Errorf("indicators.%s is not a defined metric", name)
		}
		unit := unitForMetric(name, indicators, derived)
		native := metricNativeUnit(name)
		if _, ok := derived[name]; ok {
			native = "W"
		}
		switch native {
		case "C":
			if unit != "C" && unit != "F" {
				return fmt.Errorf("indicators.%s.unit must be C or F", name)
			}
		case "GiB":
			if unit != "GiB" && unit != "MiB" {
				return fmt.Errorf("indicators.%s.unit must be GiB or MiB", name)
			}
		default:
			if unit != native {
				return fmt.Errorf("indicators.%s.unit cannot change %s into %s", name, native, unit)
			}
		}
		if setting.Color != nil {
			if err := validateColorRule(*setting.Color); err != nil {
				return fmt.Errorf("indicators.%s.color: %w", name, err)
			}
		}
		seen := make(map[string]bool)
		for _, symbol := range setting.Symbols {
			if symbol != "CPU" && symbol != "GPU" && symbol != "C" && symbol != "F" && symbol != "%" {
				return fmt.Errorf("indicators.%s.symbols: unknown hardware symbol %q (CPU/GPU/C/F/%%)", name, symbol)
			}
			if seen[symbol] {
				return fmt.Errorf("indicators.%s.symbols: duplicate %q", name, symbol)
			}
			seen[symbol] = true
		}
	}
	for name, slot := range slots {
		for _, metric := range slot.Metrics {
			if !supportedMetric(metric, derived) {
				return fmt.Errorf("display.slots.%s: unknown metric %q", name, metric)
			}
			if setting, exists := indicators[metric]; exists && setting.Symbols != nil {
				for _, symbol := range setting.Symbols {
					if err := validatePhysicalSymbol(name, symbol); err != nil {
						return fmt.Errorf("display.slots.%s: indicators.%s.symbols: %w", name, metric, err)
					}
				}
			}
		}
	}
	return nil
}

// The PA120 has only one CPU word on the upper row and one GPU word on
// the lower row. Unit glyphs C/F are on three-digit groups, % on
// two-digit groups. It cannot draw the opposite row's word.
func validatePhysicalSymbol(slot, symbol string) error {
	switch symbol {
	case "CPU":
		if slot == cpuPrimary || slot == cpuSecondary {
			return nil
		}
	case "GPU":
		if slot == gpuPrimary || slot == gpuSecondary {
			return nil
		}
	case "C", "F":
		if primarySlot(slot) {
			return nil
		}
	case "%":
		if !primarySlot(slot) {
			return nil
		}
	}
	return fmt.Errorf("symbol %q has no physical LED in %s", symbol, slot)
}

func validateRowLabels(labels map[string]string) error {
	for name, mode := range labels {
		if name != "cpu" && name != "gpu" {
			return fmt.Errorf("display.rowLabels: unknown row %q (cpu/gpu)", name)
		}
		if mode != "auto" && mode != "on" && mode != "off" {
			return fmt.Errorf("display.rowLabels.%s: expected auto, on or off, got %q", name, mode)
		}
	}
	return nil
}

func displayValue(name string, value float64, indicators map[string]indicatorConfig, derived map[string]derivedConfig) float64 {
	native := metricNativeUnit(name)
	if _, ok := derived[name]; ok {
		native = "W"
	}
	unit := unitForMetric(name, indicators, derived)
	switch {
	case native == "C" && unit == "F":
		return value*9/5 + 32
	case native == "GiB" && unit == "MiB":
		return value * 1024
	}
	return value
}

func metricSymbols(name string, slot string, indicators map[string]indicatorConfig, derived map[string]derivedConfig) []string {
	setting, ok := indicators[name]
	if ok && setting.Symbols != nil {
		return setting.Symbols
	}
	// Labels are physical row-level LEDs shared by primary and secondary.
	// Defaults follow the metric source, and explicit [] disables all glyphs
	// requested by this metric, including the CPU/GPU row label.
	var symbols []string
	kind, _, _, _ := metricDevice(name)
	if kind == "cpu" && (slot == cpuPrimary || slot == cpuSecondary) {
		symbols = append(symbols, "CPU")
	} else if kind == "gpu" && (slot == gpuPrimary || slot == gpuSecondary) {
		symbols = append(symbols, "GPU")
	}
	unit := unitForMetric(name, indicators, derived)
	if primarySlot(slot) {
		if unit == "C" || unit == "F" {
			symbols = append(symbols, unit)
		}
	} else if unit == "percent" {
		symbols = append(symbols, "%")
	}
	return symbols
}

// Hex RGB stops are interpreted against the numeric value displayed in the
// selected unit, so a Fahrenheit metric uses Fahrenheit thresholds as well.
func colorFromRule(rule colorRule, value float64) (rgb, bool) {
	if rule.Mode == "" {
		return rgb{}, false
	}
	if rule.Mode == "solid" {
		return parseRGB(rule.Value), true
	}
	stops := rule.Stops
	if value <= stops[0].At {
		return parseRGB(stops[0].Color), true
	}
	for i := 1; i < len(stops); i++ {
		if value < stops[i].At {
			if rule.Mode == "threshold" {
				return parseRGB(stops[i-1].Color), true
			}
			before, after := parseRGB(stops[i-1].Color), parseRGB(stops[i].Color)
			t := (value - stops[i-1].At) / (stops[i].At - stops[i-1].At)
			return rgb{
				interpByte(before.R, after.R, t),
				interpByte(before.G, after.G, t),
				interpByte(before.B, after.B, t),
			}, true
		}
	}
	return parseRGB(stops[len(stops)-1].Color), true
}

func metricColorOverrides(colors []rgb, selected map[string]slotSelection, indicators map[string]indicatorConfig) {
	indices := map[string][]int{
		cpuPrimary:   append([]int{4, 5}, indexRange(10, 31)...),
		cpuSecondary: append([]int{6, 80, 81}, indexRange(31, 45)...),
		gpuPrimary:   append([]int{7, 8}, indexRange(45, 66)...),
		gpuSecondary: append([]int{9, 82, 83}, indexRange(66, 80)...),
	}
	for slot, part := range indices {
		selection := selected[slot]
		if !selection.Valid {
			continue
		}
		style, ok := indicators[selection.Metric]
		if !ok {
			continue
		}
		if style.Color == nil {
			continue
		}
		chosen, ok := colorFromRule(*style.Color, selection.Value)
		if !ok {
			continue
		}
		for _, idx := range part {
			colors[idx] = chosen
		}
	}
	// CPU/GPU word LEDs are shared by the two numeric groups of each
	// row. Prefer the primary metric's explicit color when both request
	// the same word, then the secondary metric. Never depend on map order.
	for _, row := range []struct {
		word               string
		primary, secondary string
		led1, led2         int
	}{
		{"CPU", cpuPrimary, cpuSecondary, 0, 1},
		{"GPU", gpuPrimary, gpuSecondary, 2, 3},
	} {
		for _, slot := range []string{row.primary, row.secondary} {
			s := selected[slot]
			if !s.Valid {
				continue
			}
			found := false
			for _, symbol := range s.Symbols {
				if symbol == row.word {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			style, ok := indicators[s.Metric]
			if !ok || style.Color == nil {
				continue
			}
			c, ok := colorFromRule(*style.Color, s.Value)
			if !ok {
				continue
			}
			colors[row.led1], colors[row.led2] = c, c
			break
		}
	}
}
