// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"math"
	"testing"
)

func TestDerivedIgnoresUnavailableInputs(t *testing.T) {
	defs := map[string]derivedConfig{
		"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}, Offset: 8},
		"total.power.estimated":  {Operation: "sum", Sources: []string{"system.power.estimated", "gpu2.power"}, Offset: 2},
	}
	m := metrics{CPUPower: 85, CPUPowerValid: true}
	applyDerived(&m, defs)
	if got, ok := readMetric(m, "system.power.estimated"); !ok || got != 93 {
		t.Fatalf("missing GPU should not propagate N/A, got %.2f valid=%t", got, ok)
	}
	if got, ok := readMetric(m, "total.power.estimated"); !ok || got != 95 {
		t.Fatalf("nested derived estimate should survive unavailable input, got %.2f valid=%t", got, ok)
	}

	m = metrics{}
	applyDerived(&m, defs)
	if _, ok := readMetric(m, "system.power.estimated"); ok {
		t.Fatal("all N/A must remain N/A, even with offset")
	}
	if _, ok := readMetric(m, "total.power.estimated"); ok {
		t.Fatal("nested all N/A must remain N/A")
	}

	m = metrics{GPUPower: 50, GPUPowerValid: true}
	applyDerived(&m, defs)
	if got, ok := readMetric(m, "system.power.estimated"); !ok || got != 58 {
		t.Fatalf("GPU-only aggregate %.2f valid=%t", got, ok)
	}
	m = metrics{Extras: map[string]sampleValue{"gpu2.power": {Value: math.NaN(), Valid: true}}, GPUPower: 20, GPUPowerValid: true}
	applyDerived(&m, defs)
	if got, ok := readMetric(m, "total.power.estimated"); !ok || got != 30 {
		t.Fatalf("nonfinite metric must be ignored, got %.2f valid=%t", got, ok)
	}
}
