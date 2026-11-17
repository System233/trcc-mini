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

func TestPowerFrameVisible(t *testing.T) {
	period := 4 * time.Second
	for _, tc := range []struct {
		mode       string
		elapsed    time.Duration
		once, want bool
	}{
		{"off", 6 * time.Second, false, false},
		{"off", 0, true, false},
		{"replace", 0, false, true},
		{"replace", 0, true, true},
		{"rotate", 0, false, false},
		{"rotate", 5 * time.Second, false, true},
		{"rotate", 9 * time.Second, false, false},
		{"rotate", 0, true, true},
	} {
		got := powerFrameVisible(tc.mode, tc.elapsed, period, tc.once)
		if got != tc.want {
			t.Errorf("mode=%s elapsed=%s once=%v: got %v want %v", tc.mode, tc.elapsed, tc.once, got, tc.want)
		}
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
func TestPA120WattModes(t *testing.T) {
	m := metrics{Temp: 58, TempValid: true, Load: 67, CPUPower: 185, CPUPowerValid: true,
		GPUTemp: 49, GPUTempValid: true, GPULoad: 34, GPULoadValid: true, GPUPower: 133, GPUPowerValid: true}
	tempMask, _ := renderWithPower(2, m, 0, false, false)
	wattMask, _ := renderWithPower(2, m, 0, true, true)
	if !tempMask[4] || !tempMask[7] {
		t.Fatal("temperature unit indicators must be on")
	}
	if wattMask[4] || wattMask[7] {
		t.Fatal("temperature unit indicators must be off in watts mode")
	}
	// Percentage region must be unchanged when the power is shown.
	for i := 31; i <= 44; i++ {
		if tempMask[i] != wattMask[i] {
			t.Fatalf("CPU load changed at LED %d", i)
		}
	}
	for i := 66; i <= 83; i++ {
		if tempMask[i] != wattMask[i] {
			t.Fatalf("GPU load changed at LED %d", i)
		}
	}
	base := rgb{255, 255, 255}
	amber := rgb{255, 160, 0}
	packet := makeFramePower(2, 0, wattMask, base, 100, true, true, amber)
	if len(packet) != 272 {
		t.Fatal("wrong PA120 frame size")
	}
	assertPhysicalColor := func(logical int, want rgb) {
		t.Helper()
		phy := -1
		for i, l := range remap[2] {
			if l == logical {
				phy = i
				break
			}
		}
		if phy < 0 {
			t.Fatalf("LED %d not in remap", logical)
		}
		got := packet[20+phy*3 : 20+phy*3+3]
		expected := []byte{byte(float64(want.R) * 0.4), byte(float64(want.G) * 0.4), byte(float64(want.B) * 0.4)}
		if got[0] != expected[0] || got[1] != expected[1] || got[2] != expected[2] {
			t.Fatalf("LED %d physical=%d got=%v expected=%v", logical, phy, got, expected)
		}
	}
	// Pick a lit CPU watts segment and a lit GPU watts segment; verify
	// only those 3-digit regions change color, not the load percentage.
	firstLit := func(start, end int) int {
		for i := start; i <= end; i++ {
			if wattMask[i] {
				return i
			}
		}
		return -1
	}
	assertPhysicalColor(firstLit(10, 30), amber)
	assertPhysicalColor(firstLit(45, 65), amber)
	assertPhysicalColor(firstLit(31, 44), base)
	assertPhysicalColor(firstLit(66, 79), base)
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
