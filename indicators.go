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
	Symbols []string   `json:"symbols"`        // hardware C,F,% glyphs; [] disables all
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
	if _, ok := builtinUnits[name]; ok {
		return true
	}
	_, ok := derived[name]
	return ok
}

func unitForMetric(name string, indicators map[string]indicatorConfig, derived map[string]derivedConfig) string {
	native := builtinUnits[name]
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
		native := builtinUnits[name]
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
			if symbol != "C" && symbol != "F" && symbol != "%" {
				return fmt.Errorf("indicators.%s.symbols: unknown hardware symbol %q (C/F/%%)", name, symbol)
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
					if primarySlot(name) && symbol == "%" {
						return fmt.Errorf("display.slots.%s: %s requires %% LED but this three-digit region has no %% symbol", name, metric)
					}
					if !primarySlot(name) && symbol != "%" {
						return fmt.Errorf("display.slots.%s: %s requires %s LED but this two-digit region only has %%", name, metric, symbol)
					}
				}
			}
		}
	}
	return nil
}

func displayValue(name string, value float64, indicators map[string]indicatorConfig, derived map[string]derivedConfig) float64 {
	native := builtinUnits[name]
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
	unit := unitForMetric(name, indicators, derived)
	if primarySlot(slot) {
		if unit == "C" || unit == "F" {
			return []string{unit}
		}
	} else if unit == "percent" {
		return []string{"%"}
	}
	return nil
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
}
