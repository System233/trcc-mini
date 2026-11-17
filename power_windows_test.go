//go:build windows

package main

import (
	"math"
	"testing"
	"time"
)

func TestEnergyPower(t *testing.T) {
	unit := math.Ldexp(1, -16)
	// 65536 counts per joule; 100 joules consumed over 1 second = 100 W.
	p, ok := energyWatts(100000, 100000+65536*100, unit, time.Second)
	if !ok || math.Abs(p-100) > 0.001 {
		t.Fatalf("100W sample: %v %v", p, ok)
	}
	// uint32 rollover must remain a smooth 1W measurement.
	p, ok = energyWatts(0xffff0000, 0, unit, time.Second)
	if !ok || math.Abs(p-1) > 0.001 {
		t.Fatalf("rollover: %v %v", p, ok)
	}
	if _, ok = energyWatts(1, 2, unit, time.Millisecond); ok {
		t.Fatal("must reject too-short interval")
	}
	if _, ok = energyWatts(1, 2, 0, time.Second); ok {
		t.Fatal("must reject invalid units")
	}
}

func TestWattStatus(t *testing.T) {
	if got := wattStatus(0, false); got != "N/A" {
		t.Fatalf("invalid zero must not be called real 0W: %q", got)
	}
	if got := wattStatus(29.9, true); got != "29.9W" {
		t.Fatalf("valid GPU power: %q", got)
	}
}
func TestDedicatedWattSlots(t *testing.T) {
	m := metrics{Temp: 62, TempValid: true, Load: 37, CPUPower: 140, CPUPowerValid: true}
	for _, s := range []int{3, 5, 6} {
		mask, _ := renderWithPower(s, m, 0, true, false)
		begin, end := 6, 26
		if s == 5 || s == 6 {
			begin, end = 28, 48
		}
		any := false
		for i := begin; i <= end; i++ {
			any = any || mask[i]
		}
		if !any {
			t.Fatalf("style %d watts slot remains blank", s)
		}
	}
}
