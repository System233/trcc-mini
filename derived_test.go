package main

import (
	"math"
	"testing"
	"time"
)

func TestDerivedPowerEstimate(t *testing.T) {
	definitions := map[string]derivedConfig{
		"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}, Offset: 30},
		"other.power.estimated":  {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}, Offset: 0},
	}
	if err := validateDerived(definitions); err != nil {
		t.Fatal(err)
	}
	m := metrics{CPUPower: 90, CPUPowerValid: true, GPUPower: 45, GPUPowerValid: true}
	applyDerived(&m, definitions)
	got, ok := readMetric(m, "system.power.estimated")
	if !ok || got != 165 {
		t.Fatalf("estimated package/GPU+30W: value=%v valid=%v", got, ok)
	}
	total, ok := readMetric(m, "other.power.estimated")
	if !ok || total != 135 {
		t.Fatalf("second estimate: %v %v", total, ok)
	}
	if unitForMetric("system.power.estimated", nil, definitions) != "W" {
		t.Fatal("estimated watts unit lost")
	}

	m.GPUPowerValid = false
	applyDerived(&m, definitions)
	if _, ok = readMetric(m, "system.power.estimated"); ok {
		t.Fatal("must NOT estimate using an absent GPU power source")
	}
	if _, ok = readMetric(m, "other.power.estimated"); ok {
		t.Fatal("downstream derived source must be invalid")
	}
}
func TestDerivedInvalidConfig(t *testing.T) {
	bad := []map[string]derivedConfig{
		{"system.power.estimated": {Operation: "product", Sources: []string{"cpu.power", "gpu.power"}}},
		{"system.power": {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}}},
		{"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.temperature", "gpu.power"}}},
		{"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "cpu.power"}}},
		{"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "missing"}}},
		{"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}, Offset: -2}},
		{"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}, Offset: math.NaN()}},
		{"a.power.estimated": {Operation: "sum", Sources: []string{"b.power.estimated", "cpu.power"}}, "b.power.estimated": {Operation: "sum", Sources: []string{"a.power.estimated", "gpu.power"}}},
		{"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}}, "double.power.estimated": {Operation: "sum", Sources: []string{"system.power.estimated", "gpu.power"}}},
	}
	for i, v := range bad {
		if err := validateDerived(v); err == nil {
			t.Errorf("invalid derived case %d accepted", i)
		}
	}
}
func TestDerivedDependencySampled(t *testing.T) {
	d := map[string]derivedConfig{"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}}}
	slots := defaultDisplaySlots()
	slots[cpuPrimary] = slotConfig{Metrics: []string{"system.power.estimated"}}
	slots[gpuPrimary] = slotConfig{Metrics: []string{"gpu.temperature"}}
	cpu, gpu := powerSensorsRequired(slots, 2, d)
	if !cpu || !gpu {
		t.Fatal("derived metric must trigger sampling of both dependencies")
	}
	m := completeSlotMetrics()
	applyDerived(&m, d)
	selection := selectSlot(slots[cpuPrimary], m, cpuPrimary, 10*time.Second)
	if !selection.Valid || selection.Value != 175.1 || selection.Unit != "W" {
		t.Fatalf("estimated power slot failed: %+v", selection)
	}
}
