//go:build windows

package main

import (
	"math"
	"testing"
)

func TestCCDTemperature(t *testing.T) {
	// Zen 5 CCD format: (low 12 bits)*0.125 - 305 deg C.
	cases := []struct {
		raw   uint64
		want  float64
		valid bool
	}{
		{0xbf8, 78, true}, // example: 3064/8-305 = 78°C
		{0xc80, 95, true},
		{0, 0, false},
		{0xfff, 0, false},
		{0xABC00000 | 0xbf8, 78, true},
	}
	for _, tt := range cases {
		got, ok := ccdTemperature(tt.raw)
		if ok != tt.valid || (ok && math.Abs(got-tt.want) > 0.001) {
			t.Errorf("ccd raw=%#x got=%v valid=%v want=%v valid=%v", tt.raw, got, ok, tt.want, tt.valid)
		}
	}
}

func TestSelectableSources(t *testing.T) {
	for _, s := range []string{"tctl", "ccd1", "ccd2", "ccd-max", "ccd-avg"} {
		if !validCPUTempSource(s) {
			t.Errorf("not accepted: %s", s)
		}
	}
	for _, s := range []string{"diode", "package", "-13", "ccd3", "", "CCD1"} {
		if validCPUTempSource(s) {
			t.Errorf("should reject unsupported sensor: %s", s)
		}
	}
}
