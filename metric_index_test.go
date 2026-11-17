// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"
)

func TestIndexedMetricsValidationAndUnits(t *testing.T) {
	accepted := map[string]string{
		"cpu0.temperature":          "C",
		"cpu1.power":                "W",
		"cpu63.load":                "percent",
		"cpu0.ccd0.temperature":     "C",
		"cpu0.ccd15.temperature":    "C",
		"cpu0.core12.clock":         "MHz",
		"cpu.temperature.max":       "C",
		"gpu0.power":                "W",
		"gpu1.power.limit":          "W",
		"gpu17.temperature.hotspot": "C",
		"gpu0.clock.core":           "MHz",
		"gpu.memory.used":           "GiB",
		"gpu0.memory.used":          "GiB",
		"gpu0.fan.speed":            "RPM",
		"gpu0.pcie.tx":              "MB/s",
	}
	for name, unit := range accepted {
		if got := metricNativeUnit(name); got != unit {
			t.Errorf("%q unit=%q want=%q", name, got, unit)
		}
		if !supportedMetric(name, nil) {
			t.Errorf("%q rejected", name)
		}
	}
	for _, name := range []string{
		"gpu01.power", "cpu00.load", "gpu64.power", "cpu100.power",
		"gpu.power.fake", "cpu0.temperature.fake", "gpu-1.power",
		"gpu0.fan.power", "cpu0.ccd01.temperature", "cpu0.core01.clock",
		"cpu0.ccd-1.temperature", "cpu0.ccd16.temperature",
		"cpu0.core-1.load", "cpu0.core4096.clock",
		"cpu0.ccd.temperature", "gpu0.load.temperature", "cpu0.core0.invalid",
		"gpu0", "gpu.",
	} {
		if supportedMetric(name, nil) {
			t.Errorf("invalid metric %q accepted", name)
		}
	}
	for _, name := range []string{"gpu0.power", "cpu0.power", "gpu1.power.limit", "cpu1.power.cores"} {
		if !metricIsPower(name) {
			t.Errorf("%q must be recognized as wattage", name)
		}
	}
	if metricIsPower("gpu0.load") || metricIsPower("cpu0.temperature") {
		t.Fatal("non-power metric accepted as power")
	}
}

func TestAggregateIndexedMetricsIgnoresNA(t *testing.T) {
	var m metrics
	m.setExtra("gpu0.temperature", 53)
	m.setExtra("gpu1.temperature", 67)
	m.setExtra("gpu0.power", 45)
	m.setExtra("gpu1.load", 90)
	m.setExtra("gpu0.load", 10)
	m.setExtra("gpu0.memory.used", 2)
	m.setExtra("gpu0.memory.total", 8)
	m.setExtra("gpu1.memory.used", 3)
	m.setExtra("gpu1.memory.total", 12)
	m.setExtra("cpu0.power", 70)
	m.setExtra("cpu0.temperature", 60)
	// GPU2 has no actual telemetry: no zero or N/A propagation.
	aggregateIndexedMetrics(&m, map[string]int{"gpu": 3, "cpu": 2})
	for name, want := range map[string]float64{
		"gpu.temperature":  67,
		"gpu.power":        45,
		"gpu.load":         90,
		"gpu.memory.used":  5,
		"gpu.memory.total": 20,
		"gpu.memory.load":  25,
		"cpu.power":        70,
		"cpu.temperature":  60,
	} {
		got, valid := readMetric(m, name)
		if !valid || math.Abs(got-want) > 1e-9 {
			t.Errorf("%s got=%v valid=%t want=%v", name, got, valid, want)
		}
	}
	if _, ok := readMetric(m, "gpu.power.limit"); ok {
		t.Fatal("all N/A must remain N/A")
	}
	if _, ok := readMetric(m, "cpu.clock"); ok {
		t.Fatal("missing clock must stay N/A")
	}
}

func TestIndexedDerivedPowerSumAndOverlap(t *testing.T) {
	valid := map[string]derivedConfig{
		"gpu.total.estimated": {Operation: "sum", Sources: []string{"gpu0.power", "gpu1.power"}},
	}
	if err := validateDerived(valid); err != nil {
		t.Fatal(err)
	}
	var m metrics
	m.setExtra("gpu0.power", 53)
	applyDerived(&m, valid)
	if v, ok := readMetric(m, "gpu.total.estimated"); !ok || v != 53 {
		t.Fatalf("ignored N/A gpu1 should give 53W: got %.2f valid=%v", v, ok)
	}
	m = metrics{}
	applyDerived(&m, valid)
	if _, ok := readMetric(m, "gpu.total.estimated"); ok {
		t.Fatal("all unavailable sources must produce N/A")
	}
}

func TestAggregateMemoryLoadUsesPairedValues(t *testing.T) {
	var m metrics
	m.setExtra("gpu0.memory.used", 2)
	m.setExtra("gpu0.memory.total", 8)
	m.setExtra("gpu1.memory.used", 30) // no total; exclude from fraction
	aggregateIndexedMetrics(&m, map[string]int{"gpu": 2})
	if v, ok := readMetric(m, "gpu.memory.load"); !ok || v != 25 {
		t.Fatalf("memory load must use valid pairs, got %.1f valid=%v", v, ok)
	}
	m = metrics{}
	m.setExtra("gpu0.memory.used", 2)
	m.setExtra("gpu0.memory.load", 92) // a percentage alone is insufficient
	aggregateIndexedMetrics(&m, map[string]int{"gpu": 2})
	if _, ok := readMetric(m, "gpu.memory.load"); ok {
		t.Fatal("memory ratio without denominator must be N/A")
	}
}

func TestIndexedSlotRenderAndExplicitConfiguration(t *testing.T) {
	cfg := defaultAppConfig()
	cfg.Display.Slots[gpuPrimary] = slotConfig{Metrics: []string{"gpu0.power", "gpu1.power"}, Interval: "2s"}
	cfg.Display.Slots[gpuSecondary] = slotConfig{Metrics: []string{"gpu0.load"}}
	cfg.Indicators["gpu0.power"] = indicatorConfig{Symbols: []string{"GPU", "C", "F"}, Color: &colorRule{Mode: "solid", Value: "#FFAA33"}}
	cfg.Indicators["gpu1.power"] = indicatorConfig{Symbols: []string{"GPU", "C", "F"}}
	if err := validateAppConfig(cfg); err != nil {
		t.Fatal(err)
	}
	var m metrics
	m.setExtra("gpu0.power", 42)
	slots := resolveDisplaySlots(cfg.Display.Slots, m, 0, cfg.Indicators)
	selected := slots[gpuPrimary]
	if !selected.Valid || selected.Metric != "gpu0.power" || selected.Value != 42 {
		t.Fatalf("indexed GPU value did not reach PA120: %+v", selected)
	}
	if len(renderPA120Slots(slots)) != 84 {
		t.Fatal("unexpected PA120 layout change")
	}
	// No fallback from gpu1 to gpu0; unavailable gpu1 is skipped.
	if got := selectSlot(cfg.Display.Slots[gpuPrimary], m, gpuPrimary, 3*time.Second, cfg.Indicators); got.Metric != "gpu0.power" {
		t.Fatalf("unavailable gpu1 incorrectly substituted: %+v", got)
	}
	if u := unitForMetric("gpu0.temperature", map[string]indicatorConfig{"gpu0.temperature": {Unit: "F"}}, nil); u != "F" {
		t.Fatalf("indexed temperature unit conversion lost: %s", u)
	}
	if v := displayValue("gpu0.temperature", 25, map[string]indicatorConfig{"gpu0.temperature": {Unit: "F"}}, nil); v != 77 {
		t.Fatalf("indexed Fahrenheit transform %.1f", v)
	}
}

func TestSensorsListReadOnlyOutputShape(t *testing.T) {
	// The listing only uses native sensor APIs and never opens a HID device.
	var output bytes.Buffer
	// There is no requirement for hardware to exist on CI. If the OS cannot
	// enumerate any devices, skip rather than fabricating a test device.
	if err := printSensorList(&output, false); err != nil {
		t.Skipf("native inventory unavailable: %v", err)
	}
	if !strings.Contains(output.String(), "Metrics (N/A") || !strings.Contains(output.String(), "cpu.power") {
		t.Fatalf("sensor listing is missing standard metric rows")
	}
}
