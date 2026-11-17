package main

import (
	"bytes"
	"testing"
)

func TestPA120PresetAndPowerColors(t *testing.T) {
	c := defaultAppConfig()
	m := metrics{Temp: 55, TempValid: true, Load: 42, CPUPower: 135, CPUPowerValid: true,
		GPUTemp: 44, GPUTempValid: true, GPULoad: 30, GPULoadValid: true, GPUPower: 94, GPUPowerValid: true}
	col := buildPA120Colors(c.Theme, m, false, false)
	cyan := parseRGB("#36C9E8")
	purple := parseRGB("#A78BFA")
	for _, i := range []int{0, 1, 4, 5, 10, 16, 23, 30, 31, 37, 44, 80, 81, 6} {
		if col[i] != cyan {
			t.Fatalf("cpu LED %d color %+v want %+v", i, col[i], cyan)
		}
	}
	for _, i := range []int{2, 3, 7, 8, 45, 52, 59, 65, 66, 73, 79, 82, 83, 9} {
		if col[i] != purple {
			t.Fatalf("gpu LED %d color %+v want %+v", i, col[i], purple)
		}
	}
	power := buildPA120Colors(c.Theme, m, true, true)
	if power[10] != parseRGB("#FFB000") || power[45] != parseRGB("#FF9200") {
		t.Fatalf("incorrect CPU/GPU watt colors: %v %v", power[10], power[45])
	}
	if power[31] != cyan || power[66] != purple || power[0] != cyan || power[2] != purple {
		t.Fatal("watts accidentally changed labels or load digits")
	}
	mask, _ := renderWithPower(2, m, 0, true, true)
	frame := makeFrameColors(2, 0, mask, power, 75)
	if len(frame) != 272 {
		t.Fatalf("physical frame size %d", len(frame))
	}
	physical := func(logical int) rgb {
		t.Helper()
		for index, led := range remap[2] {
			if led != logical {
				continue
			}
			raw := frame[20+index*3 : 20+index*3+3]
			return rgb{raw[0], raw[1], raw[2]}
		}
		t.Fatalf("no physical LED for logical %d", logical)
		return rgb{}
	}
	firstLit := func(start, end int) int {
		for i := start; i < end; i++ {
			if mask[i] {
				return i
			}
		}
		t.Fatalf("no illuminated segment between %d and %d", start, end)
		return -1
	}
	cpuW, gpuW := firstLit(10, 31), firstLit(45, 66)
	if physical(cpuW) != scaleRGB(power[cpuW], 75) || physical(gpuW) != scaleRGB(power[gpuW], 75) {
		t.Fatalf("power colors not preserved through remap")
	}
	cpuL, gpuL := firstLit(31, 45), firstLit(66, 80)
	if physical(cpuL) != scaleRGB(power[cpuL], 75) || physical(gpuL) != scaleRGB(power[gpuL], 75) {
		t.Fatal("load digit colors corrupted through remap")
	}
}

func scaleRGB(c rgb, brightness int) rgb {
	s := float64(brightness) * 0.004
	return rgb{byte(float64(c.R) * s), byte(float64(c.G) * s), byte(float64(c.B) * s)}
}

func TestThemePresetSwitchAndZoneOverrides(t *testing.T) {
	c := defaultAppConfig()
	m := metrics{Temp: 85, TempValid: true, Load: 60, GPUTemp: 55, GPUTempValid: true, GPULoad: 34, GPULoadValid: true}
	c.Theme.Preset = "thermal"
	col := buildPA120Colors(c.Theme, m, false, false)
	if col[10] != gradientColor(85) || col[31] != gradientColor(60) ||
		col[45] != gradientColor(55) || col[66] != gradientColor(34) {
		t.Fatalf("thermal gradients not linked to their own sensors")
	}
	c.Theme.Preset = "night"
	night := buildPA120Colors(c.Theme, m, false, false)
	if night[10] != parseRGB("#3B7183") || night[45] != parseRGB("#67548E") {
		t.Fatal("night preset not effective")
	}
	c.Theme.Zones["gpu.temp"] = zoneConfig{Mode: "solid", Color: "#FF0000", PowerColor: "#00FF00"}
	custom := buildPA120Colors(c.Theme, m, true, true)
	if custom[45] != parseRGB("#00FF00") || custom[7] != parseRGB("#FF0000") {
		t.Fatal("custom GPU temperature zone overrides not applied")
	}
}

func TestFrameMappedMatchesSolidLegacy(t *testing.T) {
	m := metrics{Temp: 45, TempValid: true, Load: 23, GPUTemp: 51, GPUTempValid: true, GPULoad: 55, GPULoadValid: true}
	mask, _ := render(2, m, 0)
	white := rgb{255, 255, 255}
	colors := make([]rgb, 84)
	for i := range colors {
		colors[i] = white
	}
	got := makeFrameColors(2, 0, mask, colors, 75)
	want := makeFrame(2, 0, mask, white, 75)
	if !bytes.Equal(got, want) {
		t.Fatal("new color path changed wire encoding")
	}
}
