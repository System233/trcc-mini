package main

import (
	"math"
	"strings"
	"testing"
)

func TestIndicatorColorsAndThresholdBoundaries(t *testing.T) {
	cases := []struct {
		label string
		rule  colorRule
		at    float64
		want  string
	}{
		{"solid", colorRule{Mode: "solid", Value: "#E02030"}, 77, "#E02030"},
		{"gradient midpoint", colorRule{Mode: "gradient", Stops: []colorStop{{At: 0, Color: "#000000"}, {At: 100, Color: "#FFFFFF"}}}, 50, "#808080"},
		{"gradient below", colorRule{Mode: "gradient", Stops: []colorStop{{At: 0, Color: "#101010"}, {At: 100, Color: "#FFFFFF"}}}, -5, "#101010"},
		{"gradient above", colorRule{Mode: "gradient", Stops: []colorStop{{At: 0, Color: "#101010"}, {At: 100, Color: "#EFEFEF"}}}, 105, "#EFEFEF"},
		{"threshold before", colorRule{Mode: "threshold", Stops: []colorStop{{At: 40, Color: "#008800"}, {At: 70, Color: "#FFAA00"}, {At: 90, Color: "#FF0000"}}}, 69.99, "#008800"},
		{"threshold boundary", colorRule{Mode: "threshold", Stops: []colorStop{{At: 40, Color: "#008800"}, {At: 70, Color: "#FFAA00"}, {At: 90, Color: "#FF0000"}}}, 70, "#FFAA00"},
		{"threshold above", colorRule{Mode: "threshold", Stops: []colorStop{{At: 40, Color: "#008800"}, {At: 70, Color: "#FFAA00"}, {At: 90, Color: "#FF0000"}}}, 100, "#FF0000"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if err := validateColorRule(c.rule); err != nil {
				t.Fatal(err)
			}
			got, ok := colorFromRule(c.rule, c.at)
			if !ok || got != parseRGB(c.want) {
				t.Fatalf("got %v ok=%v expected %s", got, ok, c.want)
			}
		})
	}
}
func TestInvalidColorRules(t *testing.T) {
	cases := []colorRule{
		{},
		{Value: "#FFFFFF"},
		{Mode: "rainbow", Value: "#FFFFFF"},
		{Mode: "solid"},
		{Mode: "solid", Value: "#F00"},
		{Mode: "solid", Value: "#FFFFFF", Stops: []colorStop{{At: 0, Color: "#000000"}}},
		{Mode: "gradient", Stops: []colorStop{{At: 0, Color: "#FFFFFF"}}},
		{Mode: "gradient", Stops: []colorStop{{At: 0, Color: "#FFFFFF"}, {At: 0, Color: "#000000"}}},
		{Mode: "gradient", Stops: []colorStop{{At: 10, Color: "#FFFFFF"}, {At: 5, Color: "#000000"}}},
		{Mode: "threshold", Stops: []colorStop{{At: 10, Color: "red"}}},
		{Mode: "threshold", Stops: []colorStop{{At: math.Inf(1), Color: "#FFFFFF"}}},
	}
	for _, c := range cases {
		if err := validateColorRule(c); err == nil {
			t.Errorf("invalid color rule accepted: %+v", c)
		}
	}
}
func TestMetricDisplayUnitsAndSymbols(t *testing.T) {
	m := completeSlotMetrics()
	settings := map[string]indicatorConfig{
		"cpu.temperature": {Unit: "F", Symbols: []string{"C", "F"}},
		"gpu.power":       {Symbols: []string{"C", "F"}},
		"memory.used":     {Unit: "MiB"},
	}
	m.setExtra("memory.used", 31.5)
	c := defaultDisplaySlots()
	c[cpuPrimary] = slotConfig{Metrics: []string{"cpu.temperature"}}
	c[gpuPrimary] = slotConfig{Metrics: []string{"gpu.power"}}
	if err := validateSlotIndicators(c, settings, nil); err != nil {
		t.Fatal(err)
	}
	selected := resolveDisplaySlots(c, m, 0, settings)
	if selected[cpuPrimary].Value != m.Temp*9/5+32 || selected[cpuPrimary].Unit != "F" {
		t.Fatalf("temperature conversion: %+v", selected[cpuPrimary])
	}
	mask := renderPA120Slots(selected)
	if !mask[4] || !mask[5] || !mask[7] || !mask[8] {
		t.Fatalf("C+F symbols must both illuminate (4/5/7/8): %v", mask)
	}
	if got := selectSlot(slotConfig{Metrics: []string{"memory.used"}}, m, cpuPrimary, 0, settings); got.Valid {
		t.Fatalf("31.5 GiB -> 32256 MiB cannot fit in three digits, got %+v", got)
	}
	settings["memory.used"] = indicatorConfig{Unit: "GiB"}
	if got := selectSlot(slotConfig{Metrics: []string{"memory.used"}}, m, cpuPrimary, 0, settings); !got.Valid || got.Value != 31.5 {
		t.Fatalf("GiB must fit into three digits: %+v", got)
	}
	// Explicit [] means no indicators, not automatic Celsius/percentage.
	settings["cpu.temperature"] = indicatorConfig{Symbols: []string{}}
	selected = resolveDisplaySlots(c, m, 0, settings)
	mask = renderPA120Slots(selected)
	if mask[4] || mask[5] {
		t.Fatal("explicit empty symbols must switch off both temperature LEDs")
	}
}
func TestInvalidSymbolAssignments(t *testing.T) {
	cases := []struct {
		slot, metric string
		symbols      []string
	}{
		{cpuPrimary, "cpu.power", []string{"%"}},
		{gpuSecondary, "gpu.load", []string{"C"}},
		{gpuSecondary, "gpu.load", []string{"C", "F"}},
		{gpuPrimary, "gpu.power", []string{"W"}},
		{gpuPrimary, "gpu.power", []string{"F", "F"}},
	}
	for _, c := range cases {
		slots := defaultDisplaySlots()
		slots[c.slot] = slotConfig{Metrics: []string{c.metric}}
		cfg := map[string]indicatorConfig{c.metric: {Symbols: c.symbols}}
		if err := validateSlotIndicators(slots, cfg, nil); err == nil {
			t.Errorf("invalid %s in %s accepted", strings.Join(c.symbols, ","), c.slot)
		}
	}
}
func TestIndicatorColorOverrideFollowsMetricNotSlot(t *testing.T) {
	config := defaultAppConfig()
	m := completeSlotMetrics()
	config.Display.Slots[cpuPrimary] = slotConfig{Metrics: []string{"gpu.power"}}
	config.Display.Slots[gpuPrimary] = slotConfig{Metrics: []string{"cpu.temperature"}}
	config.Indicators = map[string]indicatorConfig{
		"gpu.power":       {Color: &colorRule{Mode: "solid", Value: "#FF007F"}, Symbols: []string{"C", "F"}},
		"cpu.temperature": {Color: &colorRule{Mode: "gradient", Stops: []colorStop{{At: 40, Color: "#000000"}, {At: 80, Color: "#FFFFFF"}}}},
	}
	selected := resolveDisplaySlots(config.Display.Slots, m, 0, config.Indicators)
	colors := buildPA120Colors(config.Theme, m, selected)
	metricColorOverrides(colors, selected, config.Indicators)
	if colors[10] != parseRGB("#FF007F") || colors[45] != parseRGB("#8B8B8B") {
		// CPU 61.8°C -> 139/255 (round) for each channel.
		t.Fatalf("metric color not mapped by selected metric: upper=%v lower=%v", colors[10], colors[45])
	}
	if colors[31] != parseRGB("#36C9E8") {
		t.Fatalf("unconfigured CPU load color lost: %v", colors[31])
	}
	frame := makeFrameColors(2, 0, renderPA120Slots(selected), colors, 75)
	if len(frame) != 272 {
		t.Fatalf("frame bytes %d", len(frame))
	}
}
