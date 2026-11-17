// SPDX-License-Identifier: GPL-3.0-or-later
// Logical per-zone palette for PA120; RGB is mapped to physical wire order
// only after the digit mask and theme have been independently resolved.
package main

import (
	"encoding/binary"
	"math"
	"strings"
)

func parseRGB(s string) rgb {
	v, _ := parseColor(s) // validated once on config load
	return v
}

func presetZone(name, preset string) zoneConfig {
	var color string
	var power string
	if strings.HasPrefix(name, "cpu.") {
		color = "#36C9E8"
		power = "#FFB000"
	} else {
		color = "#A78BFA"
		power = "#FF9200"
	}
	z := zoneConfig{Mode: "solid", Color: color, PowerColor: power}
	switch preset {
	case "thermal":
		if strings.HasSuffix(name, ".temp") {
			z.Mode = "temp"
		} else {
			z.Mode = "load"
		}
	case "night":
		if strings.HasPrefix(name, "cpu.") {
			z.Color = "#3B7183"
		} else {
			z.Color = "#67548E"
		}
		z.PowerColor = "#9C7844"
	}
	return z
}

func effectiveZone(theme themeConfig, name string) zoneConfig {
	zone := presetZone(name, theme.Preset)
	if theme.Preset == "custom" {
		zone.Color = theme.Color
		zone.PowerColor = theme.PowerColor
	}
	if override, ok := theme.Zones[name]; ok {
		if override.Mode != "" {
			zone.Mode = override.Mode
		}
		if override.Color != "" {
			zone.Color = override.Color
		}
		if override.PowerColor != "" {
			zone.PowerColor = override.PowerColor
		}
	}
	return zone
}

type gradientStop struct {
	Value float64
	Color rgb
}

var thermalGradient = []gradientStop{
	{30, rgb{0, 255, 255}},
	{50, rgb{0, 255, 0}},
	{70, rgb{255, 255, 0}},
	{90, rgb{255, 110, 0}},
	{100, rgb{255, 0, 0}},
}

func interpByte(x, y byte, t float64) byte { return byte(math.Round(float64(x)*(1-t) + float64(y)*t)) }
func gradientColor(value float64) rgb {
	if value <= thermalGradient[0].Value {
		return thermalGradient[0].Color
	}
	last := thermalGradient[len(thermalGradient)-1]
	if value >= last.Value {
		return last.Color
	}
	for i := 1; i < len(thermalGradient); i++ {
		a, b := thermalGradient[i-1], thermalGradient[i]
		if value <= b.Value {
			t := (value - a.Value) / (b.Value - a.Value)
			return rgb{interpByte(a.Color.R, b.Color.R, t), interpByte(a.Color.G, b.Color.G, t), interpByte(a.Color.B, b.Color.B, t)}
		}
	}
	return last.Color
}

func resolveZoneColor(z zoneConfig, name string, m metrics) rgb {
	fallback := parseRGB(z.Color)
	switch z.Mode {
	case "temp":
		if strings.HasPrefix(name, "gpu.") {
			if m.GPUTempValid {
				return gradientColor(m.GPUTemp)
			}
		} else if m.TempValid {
			return gradientColor(m.Temp)
		}
	case "load":
		if strings.HasPrefix(name, "gpu.") {
			if m.GPULoadValid {
				return gradientColor(float64(m.GPULoad))
			}
		} else {
			return gradientColor(float64(m.Load))
		}
	}
	return fallback
}

func buildPA120Colors(theme themeConfig, m metrics, selected map[string]slotSelection) []rgb {
	colors := make([]rgb, 84)
	paint := func(name, slot string, indices []int, digits []int) {
		z := effectiveZone(theme, name)
		color := resolveZoneColor(z, name, m)
		for _, i := range indices {
			colors[i] = color
		}
		if selected[slot].Valid && metricKind(selected[slot].Metric) == "power" {
			powerColor := parseRGB(z.PowerColor)
			for _, i := range digits {
				colors[i] = powerColor
			}
		}
	}
	cpuTempDigits := indexRange(10, 31)
	cpuLoadDigits := indexRange(31, 45)
	gpuTempDigits := indexRange(45, 66)
	gpuLoadDigits := indexRange(66, 80)
	paint("cpu.temp", cpuPrimary, append([]int{0, 1, 4, 5}, cpuTempDigits...), cpuTempDigits)
	paint("cpu.load", cpuSecondary, append([]int{6, 80, 81}, cpuLoadDigits...), cpuLoadDigits)
	paint("gpu.temp", gpuPrimary, append([]int{2, 3, 7, 8}, gpuTempDigits...), gpuTempDigits)
	paint("gpu.load", gpuSecondary, append([]int{9, 82, 83}, gpuLoadDigits...), gpuLoadDigits)
	return colors
}

// An explicitly selected global color link overrides each zone's base
// palette, but never mistakes an unrelated powered sensor for the metric
// actually shown in that physical slot.
func globalPA120Colors(theme themeConfig, selected map[string]slotSelection, base rgb) []rgb {
	colors := make([]rgb, 84)
	for i := range colors {
		colors[i] = base
	}
	regions := []struct {
		name, zone string
		begin, end int
	}{
		{cpuPrimary, "cpu.temp", 10, 31},
		{cpuSecondary, "cpu.load", 31, 45},
		{gpuPrimary, "gpu.temp", 45, 66},
		{gpuSecondary, "gpu.load", 66, 80},
	}
	for _, region := range regions {
		if s := selected[region.name]; s.Valid && metricKind(s.Metric) == "power" {
			power := parseRGB(effectiveZone(theme, region.zone).PowerColor)
			for i := region.begin; i < region.end; i++ {
				colors[i] = power
			}
		}
	}
	return colors
}

func indexRange(start, end int) []int {
	out := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, i)
	}
	return out
}

// Encodes a logical RGB array using the exact same remap, on/off mask,
// global brightness, and firmware header as makeFramePower.
func makeFrameColors(style, sub int, mask []bool, colors []rgb, brightness int) []byte {
	order := remap[style]
	if style == 5 && sub == 1 {
		order = remapLF8Sub1
	}
	n := len(mask)
	if len(order) > 0 {
		n = len(order)
	}
	body := make([]byte, n*3)
	scale := float64(brightness) * 0.004
	for i := 0; i < n; i++ {
		logical := i
		if len(order) > 0 {
			logical = order[i]
		}
		if logical < 0 || logical >= len(mask) || logical >= len(colors) || !mask[logical] {
			continue
		}
		c := colors[logical]
		body[3*i] = byte(math.Trunc(float64(c.R) * scale))
		body[3*i+1] = byte(math.Trunc(float64(c.G) * scale))
		body[3*i+2] = byte(math.Trunc(float64(c.B) * scale))
	}
	result := make([]byte, 20+len(body))
	result[0], result[1], result[2], result[3] = 0xda, 0xdb, 0xdc, 0xdd
	result[12] = 2
	binary.LittleEndian.PutUint16(result[16:], uint16(len(body)))
	copy(result[20:], body)
	return result
}
