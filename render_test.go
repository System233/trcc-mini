package main

import (
	"encoding/binary"
	"testing"
)

func TestKnownModels(t *testing.T) {
	cases := []struct{ pm, style int }{
		{3, 1}, {16, 2}, {23, 2}, {31, 2}, {32, 3}, {48, 5}, {80, 6}, {96, 7},
		{112, 9}, {128, 4}, {129, 10}, {144, 11}, {160, 12}, {176, 5}, {208, 8},
	}
	for _, tt := range cases {
		s, _ := styleFromPM(tt.pm, nil)
		if s != tt.style {
			t.Errorf("PM %d -> %d, want %d", tt.pm, s, tt.style)
		}
	}
	s, _ := styleFromPM(208, []byte{0xdc, 0xdd, 0xaa, 1})
	if s != 13 {
		t.Errorf("Qube fingerprint -> %d", s)
	}
}
func TestFrameProtocol(t *testing.T) {
	out, err := render(2, metrics{Load: 42, Temp: 53, TempValid: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 84 {
		t.Fatal("PA120 mask length", len(out))
	}
	frame := makeFrame(2, 0, out, rgb{255, 0, 0}, 75)
	if len(frame) != 272 {
		t.Fatal("PA120 packet length", len(frame))
	}
	if binary.LittleEndian.Uint32(frame[:4]) != 0xdddcdbda {
		t.Fatal("bad magic header")
	}
	if frame[12] != 2 || binary.LittleEndian.Uint16(frame[16:18]) != 252 {
		t.Fatal("invalid LED protocol header")
	}
	if frame[20] != 76 && frame[20] != 0 {
		t.Fatal("invalid PWM scaling", frame[20])
	}
}
func TestAX120Masks(t *testing.T) {
	for _, tc := range []struct {
		phase      int
		temp       bool
		c, percent bool
	}{
		{0, true, true, false}, {1, true, false, true}, {0, false, false, false},
	} {
		mask, err := render(1, metrics{Temp: 67, TempValid: tc.temp, Load: 25}, tc.phase)
		if err != nil {
			t.Fatal(err)
		}
		if len(mask) != 30 || mask[6] != tc.c || mask[8] != tc.percent {
			t.Fatal(tc.phase, mask)
		}
	}
}
func TestDigitalStyles(t *testing.T) {
	for _, s := range []int{1, 2, 3, 5, 6, 7, 8, 13} {
		mask, err := render(s, metrics{Temp: 62, TempValid: true, Load: 85}, 0)
		if err != nil {
			t.Fatal(err)
		}
		packet := makeFrame(s, 0, mask, rgb{255, 255, 255}, 100)
		if packet[12] != 2 || len(packet) != 20+int(binary.LittleEndian.Uint16(packet[16:])) {
			t.Fatalf("style %d invalid packet", s)
		}
	}
}

func TestGPUZones(t *testing.T) {
	m := metrics{Temp: 52, TempValid: true, Load: 44, GPUTemp: 49, GPUTempValid: true, GPULoad: 37, GPULoadValid: true}
	pa, _ := render(2, m, 0)
	if !(pa[2] && pa[3] && pa[7] && pa[9]) {
		t.Fatal("PA120 GPU indicators missing")
	}
	ax, _ := render(1, m, 2)
	if !(ax[4] && ax[5] && ax[6] && !ax[8]) {
		t.Fatal("AX120 GPU temp indicator incorrect")
	}
	ax, _ = render(1, m, 3)
	if !(ax[4] && ax[5] && ax[8] && !ax[6]) {
		t.Fatal("AX120 GPU load indicator incorrect")
	}
	missing, _ := render(2, metrics{Load: 44}, 0)
	if missing[2] || missing[3] || missing[7] || missing[9] {
		t.Fatal("fake GPU values illuminated")
	}
}
