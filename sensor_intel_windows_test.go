//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"math"
	"testing"
	"time"
)

func TestIntelPackageTemperatureRegisters(t *testing.T) {
	cases := []struct {
		target, status uint64
		temp           float64
		valid          bool
	}{
		{uint64(100) << 16, (1 << 31) | (27 << 16), 73, true},
		{uint64(105) << 16, (1 << 31) | (20 << 16), 85, true},
		{uint64(100) << 16, (27 << 16), 0, false}, // DTS valid bit clear
		{uint64(0) << 16, (1 << 31) | (20 << 16), 0, false},
		{uint64(100) << 16, (1 << 31) | (110 << 16), 0, false},
	}
	for _, c := range cases {
		temp, ok := intelPackageTemperature(c.target, c.status)
		if temp != c.temp || ok != c.valid {
			t.Fatalf("target=%#x status=%#x got %.1f/%v want %.1f/%v", c.target, c.status, temp, ok, c.temp, c.valid)
		}
	}
}
func TestIntelRAPLEnergyWrap(t *testing.T) {
	unit := math.Ldexp(1, -14)
	previous := uint32(0xFFFFFF00)
	current := uint32(0x00000100)
	watts, ok := energyWatts(previous, current, unit, time.Second)
	if !ok || math.Abs(watts-512*unit) > 0.00001 {
		t.Fatalf("RAPL wrap %.8f valid=%v", watts, ok)
	}
}

func TestIntelHardwareVendorDetection(t *testing.T) {
	vendor := windowsCPUVendor()
	if vendor == "" {
		t.Skip("Windows CPU vendor registry unavailable")
	}
	if vendor != "GenuineIntel" && vendor != "AuthenticAMD" {
		t.Logf("unrecognized CPU vendor %q; Intel-only MSRs never loaded unless vendor explicitly matches", vendor)
	}
}
