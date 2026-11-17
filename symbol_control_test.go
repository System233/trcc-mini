package main

import (
	"testing"
	"time"
)

func TestAllPA120SymbolsIndividuallyConfigurable(t *testing.T) {
	c := defaultAppConfig()
	c.Display.Slots[cpuPrimary] = slotConfig{Metrics: []string{"cpu.power"}}
	c.Display.Slots[cpuSecondary] = slotConfig{Metrics: []string{"cpu.load"}}
	c.Display.Slots[gpuPrimary] = slotConfig{Metrics: []string{"gpu.power"}}
	c.Display.Slots[gpuSecondary] = slotConfig{Metrics: []string{"gpu.load"}}
	c.Indicators = map[string]indicatorConfig{
		"cpu.power": {Symbols: []string{"CPU", "C", "F"}},
		"cpu.load":  {Symbols: []string{"CPU", "%"}},
		"gpu.power": {Symbols: []string{"GPU", "C", "F"}},
		"gpu.load":  {Symbols: []string{"GPU", "%"}},
	}
	if err := validateAppConfig(c); err != nil {
		t.Fatal(err)
	}
	m := completeSlotMetrics()
	selected := resolveDisplaySlots(c.Display.Slots, m, 0, c.Indicators)
	mask := renderPA120Slots(selected, c.Display.RowLabels)
	// Pair-wise CPU/GPU words, upper/lower C/F and separate % LED.
	for i := 0; i < 10; i++ {
		if !mask[i] {
			t.Fatalf("physical symbol LED %d not enabled", i)
		}
	}
	c.Indicators["cpu.power"] = indicatorConfig{Symbols: []string{}}
	c.Indicators["cpu.load"] = indicatorConfig{Symbols: []string{}}
	selected = resolveDisplaySlots(c.Display.Slots, m, 0, c.Indicators)
	mask = renderPA120Slots(selected, c.Display.RowLabels)
	for _, i := range []int{0, 1, 4, 5, 6} {
		if mask[i] {
			t.Fatalf("CPU row LED %d should be off with empty symbols", i)
		}
	}
	for _, i := range []int{2, 3, 7, 8, 9} {
		if !mask[i] {
			t.Fatalf("GPU row LED %d should be unchanged", i)
		}
	}
	// GPU's % symbol can be disabled while leaving the GPU word lit.
	c.Indicators["gpu.load"] = indicatorConfig{Symbols: []string{"GPU"}}
	selected = resolveDisplaySlots(c.Display.Slots, m, 0, c.Indicators)
	mask = renderPA120Slots(selected, c.Display.RowLabels)
	if mask[9] || !mask[2] || !mask[3] {
		t.Fatal("GPU percent was not independent from GPU label")
	}
}
func TestRowLabelsForceAndAuto(t *testing.T) {
	c := defaultAppConfig()
	m := completeSlotMetrics()
	selected := resolveDisplaySlots(c.Display.Slots, m, 0, c.Indicators)
	mask := renderPA120Slots(selected)
	if !mask[0] || !mask[1] || !mask[2] || !mask[3] {
		t.Fatal("auto row labels must use the selected CPU/GPU sources")
	}
	c.Display.RowLabels = map[string]string{"cpu": "off", "gpu": "off"}
	if err := validateAppConfig(c); err != nil {
		t.Fatal(err)
	}
	mask = renderPA120Slots(selected, c.Display.RowLabels)
	for i := 0; i < 4; i++ {
		if mask[i] {
			t.Fatalf("forced-off row icon %d still enabled", i)
		}
	}
	if !mask[4] || !mask[7] || !mask[6] || !mask[9] {
		t.Fatal("disabling row words must not disable temperature or percent")
	}
	c.Display.RowLabels = map[string]string{"cpu": "on", "gpu": "on"}
	selected = map[string]slotSelection{}
	mask = renderPA120Slots(selected, c.Display.RowLabels)
	for i := 0; i < 4; i++ {
		if !mask[i] {
			t.Fatalf("forced-on row icon %d not enabled with missing sensor", i)
		}
	}
	for i := 4; i < 10; i++ {
		if mask[i] {
			t.Fatalf("forced-on row icon must not generate fake units LED %d", i)
		}
	}
	c.Display.RowLabels = map[string]string{"cpu": "auto", "gpu": "auto"}
	mask = renderPA120Slots(selected, c.Display.RowLabels)
	for i := 0; i < 10; i++ {
		if mask[i] {
			t.Fatalf("auto labels should not light any LED without valid readings: %d", i)
		}
	}
}
func TestAutoAndExplicitSymbolsDuringRotation(t *testing.T) {
	c := defaultAppConfig()
	m := completeSlotMetrics()
	c.Display.Slots[cpuPrimary] = slotConfig{Metrics: []string{"gpu.power", "cpu.temperature"}, Interval: "1s"}
	c.Display.Slots[cpuSecondary] = slotConfig{Metrics: []string{"gpu.load", "cpu.load"}, Interval: "1s"}
	selected := resolveDisplaySlots(c.Display.Slots, m, 0, c.Indicators)
	mask := renderPA120Slots(selected)
	if mask[0] || mask[1] || !mask[6] || mask[4] {
		t.Fatal("cross-source GPU readings on upper row must not light CPU label/C, but % still applies to load")
	}
	selected = resolveDisplaySlots(c.Display.Slots, m, time.Second, c.Indicators)
	mask = renderPA120Slots(selected)
	if !mask[0] || !mask[1] || !mask[4] || !mask[6] {
		t.Fatal("rotated CPU readings should enable CPU, C and %")
	}
	// An explicitly requested CPU label works for a derived metric with no
	// native processor association, even if the secondary value is missing.
	m.CPUPowerValid = true
	c.Indicators["gpu.power"] = indicatorConfig{Symbols: []string{"CPU", "C", "F"}}
	selected = resolveDisplaySlots(c.Display.Slots, m, 0, c.Indicators)
	mask = renderPA120Slots(selected)
	if !mask[0] || !mask[1] || !mask[4] || !mask[5] {
		t.Fatal("explicit CPU symbol must override automatic source-name labeling")
	}
}
func TestSymbolAndRowLabelsValidation(t *testing.T) {
	c := defaultAppConfig()
	for _, tc := range []struct {
		slot, metric string
		symbols      []string
	}{
		{cpuPrimary, "cpu.power", []string{"GPU"}},
		{cpuSecondary, "cpu.load", []string{"GPU", "%"}},
		{gpuPrimary, "gpu.power", []string{"CPU"}},
		{gpuSecondary, "gpu.load", []string{"CPU", "%"}},
		{cpuPrimary, "cpu.power", []string{"%"}},
		{gpuPrimary, "gpu.power", []string{"%"}},
		{cpuSecondary, "cpu.load", []string{"C"}},
		{gpuSecondary, "gpu.load", []string{"F"}},
		{cpuPrimary, "cpu.power", []string{"CPU", "CPU"}},
	} {
		c.Indicators = map[string]indicatorConfig{tc.metric: {Symbols: tc.symbols}}
		if err := validateSlotIndicators(c.Display.Slots, c.Indicators, c.Derived); err == nil {
			t.Errorf("accepted invalid %s symbol assignment %#v", tc.slot, tc.symbols)
		}
	}
	for _, row := range []map[string]string{
		{"gpu": "show"}, {"cpu": "hide"}, {"vram": "on"}, {"gpu": ""},
	} {
		if err := validateRowLabels(row); err == nil {
			t.Errorf("accepted invalid rowLabels: %v", row)
		}
	}
}
func TestMetricColorOnSharedRowWord(t *testing.T) {
	c := defaultAppConfig()
	m := completeSlotMetrics()
	c.Indicators = map[string]indicatorConfig{
		"cpu.temperature": {Symbols: []string{"CPU", "C"}, Color: &colorRule{Mode: "solid", Value: "#FF0077"}},
		"cpu.load":        {Symbols: []string{"CPU", "%"}, Color: &colorRule{Mode: "solid", Value: "#00FF00"}},
		"gpu.temperature": {Symbols: []string{"GPU", "C"}, Color: &colorRule{Mode: "solid", Value: "#7777FF"}},
		"gpu.load":        {Symbols: []string{"GPU", "%"}, Color: &colorRule{Mode: "solid", Value: "#00FFFF"}},
	}
	selected := resolveDisplaySlots(c.Display.Slots, m, 0, c.Indicators)
	col := buildPA120Colors(c.Theme, m, selected)
	metricColorOverrides(col, selected, c.Indicators)
	if col[0] != parseRGB("#FF0077") || col[1] != parseRGB("#FF0077") ||
		col[2] != parseRGB("#7777FF") || col[3] != parseRGB("#7777FF") {
		t.Fatalf("row label color must use primary configured color, not arbitrary map ordering: %v", col[:4])
	}
	c.Indicators["cpu.temperature"] = indicatorConfig{Symbols: []string{"C"}, Color: &colorRule{Mode: "solid", Value: "#FF0077"}}
	selected = resolveDisplaySlots(c.Display.Slots, m, 0, c.Indicators)
	col = buildPA120Colors(c.Theme, m, selected)
	metricColorOverrides(col, selected, c.Indicators)
	if col[0] != parseRGB("#00FF00") {
		t.Fatalf("secondary metric explicitly requesting CPU label must set its color: %v", col[0])
	}
	// Masked-off labels still emit zero on the wire regardless of palette.
	c.Display.RowLabels = map[string]string{"cpu": "off"}
	mask := renderPA120Slots(selected, c.Display.RowLabels)
	frame := makeFrameColors(2, 0, mask, col, 75)
	for _, i := range []int{0, 1} {
		for k, logical := range remap[2] {
			if logical == i {
				for _, channel := range frame[20+k*3 : 20+k*3+3] {
					if channel != 0 {
						t.Fatal("disabled CPU label leaked a colored wire segment")
					}
				}
			}
		}
	}
}
