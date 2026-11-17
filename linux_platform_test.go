//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxHIDEventIdentification(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"HID_ID=0003:00000416:00008001\n", true},
		{"HID_ID=0005:00000416:00008001\n", false},
		{"HID_ID=0003:00000416:00008000\n", false},
		{"HID_ID=0003:00001234:00008001\n", false},
		{"HID_NAME=vendor device\n", false},
	}
	for _, c := range cases {
		if got := isThermalrightUevent(c.raw); got != c.want {
			t.Errorf("event %q: got=%t want=%t", c.raw, got, c.want)
		}
	}
}

func TestLinuxNvidiaTelemetryParsing(t *testing.T) {
	temp, ok, load, lok, watts, pok, used, total, mok := parseNvidiaSnapshot("52, 23, 107.45, 4096, 12288\n")
	if !ok || temp != 52 || !lok || load != 23 || !pok || watts != 107.45 || !mok || used != 4 || total != 12 {
		t.Fatalf("NVIDIA parse: temp=%v %v load=%v %v power=%v %v memory=%v/%v %v", temp, ok, load, lok, watts, pok, used, total, mok)
	}
	_, valid, _, loadValid, _, powerValid, _, _, memoryValid := parseNvidiaSnapshot("N/A, 50, N/A, N/A, 12288")
	if valid || !loadValid || powerValid || memoryValid {
		t.Fatal("N/A fields should remain missing without fabricated zero")
	}
	_, _, _, _, _, _, _, _, memoryValid = parseNvidiaSnapshot("52, 23, 107.45, 15000, 12000")
	if memoryValid {
		t.Fatal("used VRAM greater than total should be invalid")
	}
}

func TestLinuxNVMLPCIBusIdCanonicalization(t *testing.T) {
	for input, want := range map[string]string{
		"0000:01:00.0":     "00000000:01:00.0",
		"0001:af:02.0":     "00000001:af:02.0",
		"00000000:01:00.0": "00000000:01:00.0",
		"invalid":          "",
	} {
		if got := normalizedNVIDIABusID(input); got != want {
			t.Errorf("PCI ID %q => %q (want %q)", input, got, want)
		}
	}
}

func TestLinuxGPUBackgroundRefreshDoesNotBlockHIDLoop(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "updated")
	script := "#!/bin/sh\nsleep 0.3\nif [ -f '" + flag + "' ]; then\n  echo '60, 33, 90.0, 5120, 12288'\nelse\n  echo '40, 10, 30.0, 4096, 12288'\nfi\n"
	if err := os.WriteFile(filepath.Join(dir, "nvidia-smi"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	gIface, err := openGPUSensor()
	if err != nil {
		t.Fatal(err)
	}
	defer gIface.Close()
	g := gIface.(*linuxGPU)
	if temp, ok, _, _ := g.Read(); !ok || temp != 40 {
		t.Fatalf("initial NVIDIA reading: %.1f valid=%t", temp, ok)
	}
	if err := os.WriteFile(flag, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	g.lastAttempt.Store(time.Now().Add(-2 * time.Second).UnixNano())
	start := time.Now()
	if temp, ok, _, _ := g.Read(); !ok || temp != 40 {
		t.Fatal("cached data must remain valid during asynchronous refresh")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("HID display read blocked on NVIDIA process: %s", elapsed)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if temp, ok, _, _ := g.Read(); ok && temp == 60 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("asynchronous NVIDIA refresh never applied")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLinuxHWMonLabelSelection(t *testing.T) {
	dir := t.TempDir()
	for file, content := range map[string]string{
		"temp1_input": "75500\n",
		"temp1_label": "Tctl\n",
		"temp2_input": "65900\n",
		"temp2_label": "Tccd1\n",
		"temp3_input": "71000\n",
		"temp3_label": "Tccd2\n",
		"temp4_input": "1250000\n", // reject invalid
		"temp4_label": "Tccd3\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	chip := hwmonChip{directory: dir, name: "k10temp"}
	readings, err := readHWMonTemps(chip)
	if err != nil {
		t.Fatal(err)
	}
	if readings["tctl"] != 75.5 || readings["ccd1"] != 65.9 || readings["ccd2"] != 71 || len(readings) != 3 {
		t.Fatalf("invalid hwmon readings: %+v", readings)
	}
	cpu := &linuxCPUSensor{tempSensors: []hwmonChip{chip}}
	for source, want := range map[string]float64{
		"tctl": 75.5, "ccd1": 65.9, "ccd2": 71, "ccd-avg": 68.45, "ccd-max": 71,
	} {
		value, err := cpu.ReadTemp(source)
		if err != nil || math.Abs(value-want) > 1e-8 {
			t.Errorf("%s: got %.3f expected %.3f: %v", source, value, want, err)
		}
	}
}

func TestLinuxRAPLEnergyDeltaAndRollover(t *testing.T) {
	file := filepath.Join(t.TempDir(), "energy_uj")
	if err := os.WriteFile(file, []byte("90000000"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &linuxCPUSensor{power: cpuPowerProbe{energy: file, energyRange: 100000000}}
	if _, valid, err := c.ReadPower(); valid || err != nil {
		t.Fatalf("first RAPL value should prime: valid=%v err=%v", valid, err)
	}
	c.power.lastAt = time.Now().Add(-time.Second)
	if err := os.WriteFile(file, []byte("10000000"), 0600); err != nil {
		t.Fatal(err)
	}
	watts, valid, err := c.ReadPower()
	if !valid || err != nil || math.Abs(watts-20) > 0.5 {
		t.Fatalf("RAPL counter wrap expected 20W, got %.2f valid=%t err=%v", watts, valid, err)
	}
}

func TestLinuxSystemdServiceUnit(t *testing.T) {
	unit := linuxServiceUnitText("/usr/local/libexec/trcc-mini/trcc-mini", "/etc/trcc-mini/trcc-mini.json")
	for _, needle := range []string{
		"ExecStart=\"/usr/local/libexec/trcc-mini/trcc-mini\" service-run --config \"/etc/trcc-mini/trcc-mini.json\"",
		"Restart=on-failure", "WantedBy=multi-user.target", "ProtectSystem=strict",
	} {
		if !strings.Contains(unit, needle) {
			t.Errorf("systemd unit missing %s", needle)
		}
	}
}
