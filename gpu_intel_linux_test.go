//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntelGPUHWMonDeviceNotSubdomain(t *testing.T) {
	root := t.TempDir()
	chip := filepath.Join(root, "hwmon", "hwmon0")
	sub := filepath.Join(root, "hwmon", "hwmon1")
	for _, dir := range []string{chip, sub} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(chip, "name"), []byte("i915\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(sub, "name"), []byte("i915_gt0\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if got := intelGPUHWMonDir(root); got != chip {
		t.Fatalf("selected GT subdomain instead of device-level hwmon: %q", got)
	}
}

func TestIntelGPUEnergyIsNotPowerLimit(t *testing.T) {
	dir := t.TempDir()
	hwmon := filepath.Join(dir, "hwmon", "hwmon0")
	if e := os.MkdirAll(hwmon, 0700); e != nil {
		t.Fatal(e)
	}
	for name, value := range map[string]string{
		"name":          "i915\n",
		"energy1_input": "100000000\n",
		"power1_max":    "180000000\n", // 180W *limit*, never treated as draw
		"temp1_input":   "55000\n",
	} {
		if e := os.WriteFile(filepath.Join(hwmon, name), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	g := &linuxGPU{backend: "intel", device: dir}
	s := g.collectIntel()
	if s.powerValid || !s.tempValid || s.temp != 55 {
		t.Fatalf("first sample must prime energy and read temp, got %+v", s)
	}
	g.intelEnergy.at = time.Now().Add(-time.Second)
	if e := os.WriteFile(filepath.Join(hwmon, "energy1_input"), []byte("125000000\n"), 0600); e != nil {
		t.Fatal(e)
	}
	s = g.collectIntel()
	if !s.powerValid || math.Abs(s.watts-25) > 0.5 {
		t.Fatalf("expected measured 25W, not power limit, got %+v", s)
	}
	if e := os.WriteFile(filepath.Join(hwmon, "energy1_input"), []byte("100000\n"), 0600); e != nil {
		t.Fatal(e)
	}
	s = g.collectIntel()
	if s.powerValid {
		t.Fatal("energy reset must return N/A, not fake negative or huge power")
	}
}

func TestIntelGPUOnlyDedicatedVRAM(t *testing.T) {
	dir := t.TempDir()
	g := &linuxGPU{backend: "intel", device: dir}
	s := g.collectIntel()
	if s.memoryValid {
		t.Fatal("Intel iGPU shared system memory must not become VRAM")
	}
	for name, value := range map[string]string{
		"lmem_total_bytes": "8589934592\n",
		"lmem_used_bytes":  "2147483648\n",
	} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	s = g.collectIntel()
	if !s.memoryValid || s.used != 2 || s.total != 8 {
		t.Fatalf("Intel Arc VRAM parsing: %+v", s)
	}
}
