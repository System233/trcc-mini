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

func writeCPUPackageFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestLinuxPhysicalPackageLabels(t *testing.T) {
	good := map[string]int{
		"Package id 0": 0, "Package id 1": 1,
		"package-1": 1, "package-12": 12, "PACKAGE 5": 5,
	}
	for label, want := range good {
		n, ok := parseLinuxPackageLabel(label)
		if !ok || n != want {
			t.Errorf("%q => %d/%v wanted %d", label, n, ok, want)
		}
	}
	for _, bad := range []string{"Package id 01", "Package id -1", "package--1", "package-", "core", "core-0", "package-0:0", "package 0 extra", "package-99999999999999999999"} {
		if n, ok := parseLinuxPackageLabel(bad); ok {
			t.Errorf("ambiguous/non-package label %q identified as %d", bad, n)
		}
	}
}

func TestLinuxNUMANodeMustBelongToUniqueSocket(t *testing.T) {
	packages := map[int][]int{0: {0, 1, 2, 3}, 1: {4, 5, 6, 7}}
	for text, want := range map[string]int{
		"0-3": 0, "0-1,3": 0, "4-7": 1, "4,5-6": 1,
	} {
		if got, ok := linuxNUMAPhysicalPackage(text, packages); !ok || got != want {
			t.Errorf("NUMA cpulist %q => %d/%v; want socket %d", text, got, ok, want)
		}
	}
	for _, bad := range []string{"0-4", "3,4", "8", "-1", "0-", "0-4096", "0,,1", "4-3", "0-3,8-11", ""} {
		if got, ok := linuxNUMAPhysicalPackage(bad, packages); ok {
			t.Errorf("invalid or cross-socket NUMA list %q wrongly mapped to socket %d", bad, got)
		}
	}
}

func TestLinuxAMDHWMonNUMAExactSocketBinding(t *testing.T) {
	root := t.TempDir()
	packages := map[int][]int{0: {0, 1}, 1: {2, 3}}
	chip := filepath.Join(root, "hwmon", "hwmon0")
	writeCPUPackageFixture(t, filepath.Join(chip, "name"), "k10temp\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "temp1_input"), "58000\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "temp1_label"), "Tctl\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "temp2_label"), "Tccd1\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "temp2_input"), "49000\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "temp3_label"), "Tccd2\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "temp3_input"), "61000\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "device", "numa_node"), "1\n")
	nodes := filepath.Join(root, "nodes")
	writeCPUPackageFixture(t, filepath.Join(nodes, "node1", "cpulist"), "2-3\n")
	source := discoverLinuxPackageTelemetryWithNodes(root, nodes, packages)
	if source.devices[0].tempPath != "" || source.devices[1].tempPath == "" {
		t.Fatalf("AMD PCI NUMA node must map to physical cpu1 only: %+v", source.devices)
	}
	var m metrics
	source.Sample(&m)
	if temp, ok := readMetric(m, "cpu1.temperature"); !ok || temp != 58 {
		t.Fatalf("AMD CPU1 Tctl %.2f valid=%v", temp, ok)
	}
	for name, want := range map[string]float64{
		"cpu1.ccd0.temperature": 49,
		"cpu1.ccd1.temperature": 61,
		"cpu1.temperature.max":  61,
	} {
		got, valid := readMetric(m, name)
		if !valid || got != want {
			t.Errorf("%s = %.1f valid=%v expected %.1f", name, got, valid, want)
		}
	}
	writeCPUPackageFixture(t, filepath.Join(nodes, "node1", "cpulist"), "1-2\n")
	source = discoverLinuxPackageTelemetryWithNodes(root, nodes, packages)
	if source.devices[1].tempPath != "" {
		t.Fatal("NUMA node spanning CPU0 and CPU1 must not bind to CPU1")
	}
}
func TestLinuxMultiSocketTemperatureAndRAPL(t *testing.T) {
	root := t.TempDir()
	// Real physical package IDs need not be zero-based or consecutive.
	packages := map[int][]int{2: {0, 1}, 7: {2, 3}}
	chip0 := filepath.Join(root, "hwmon", "hwmon0")
	chip1 := filepath.Join(root, "hwmon", "hwmon1")
	writeCPUPackageFixture(t, filepath.Join(chip0, "name"), "coretemp\n")
	writeCPUPackageFixture(t, filepath.Join(chip0, "temp1_label"), "Package id 2\n")
	writeCPUPackageFixture(t, filepath.Join(chip0, "temp1_input"), "55000\n")
	writeCPUPackageFixture(t, filepath.Join(chip1, "name"), "coretemp\n")
	writeCPUPackageFixture(t, filepath.Join(chip1, "temp1_label"), "Package id 7\n")
	writeCPUPackageFixture(t, filepath.Join(chip1, "temp1_input"), "72000\n")
	power0 := filepath.Join(root, "powercap", "intel-rapl:0")
	power1 := filepath.Join(root, "powercap", "intel-rapl:1")
	writeCPUPackageFixture(t, filepath.Join(power0, "name"), "package-2\n")
	writeCPUPackageFixture(t, filepath.Join(power0, "energy_uj"), "50000000\n")
	writeCPUPackageFixture(t, filepath.Join(power0, "max_energy_range_uj"), "1000000000\n")
	writeCPUPackageFixture(t, filepath.Join(power1, "name"), "package-7\n")
	writeCPUPackageFixture(t, filepath.Join(power1, "energy_uj"), "20000000\n")
	writeCPUPackageFixture(t, filepath.Join(power1, "max_energy_range_uj"), "1000000000\n")
	source := discoverLinuxPackageTelemetry(root, packages)
	if source.packages != 2 || len(source.devices) != 2 {
		t.Fatalf("bad package discovery: %+v", source)
	}
	if source.devices[0].tempPath == "" || source.devices[1].tempPath == "" {
		t.Fatal("package temperatures missing")
	}
	if source.devices[0].power.energy == "" || source.devices[1].power.energy == "" {
		t.Fatal("RAPL probes missing")
	}
	// Fake elapsed interval without sleeping or requiring Linux hardware.
	for _, d := range source.devices {
		d.power.lastAt = time.Now().Add(-2 * time.Second)
	}
	writeCPUPackageFixture(t, filepath.Join(power0, "energy_uj"), "90000000\n")
	writeCPUPackageFixture(t, filepath.Join(power1, "energy_uj"), "80000000\n")
	var m metrics
	source.Sample(&m)
	aggregateIndexedMetrics(&m, map[string]int{"cpu": 2})
	for name, want := range map[string]float64{
		"cpu0.temperature": 55, "cpu1.temperature": 72, "cpu.temperature": 72,
		"cpu0.power": 20, "cpu1.power": 30, "cpu.power": 50,
	} {
		got, ok := readMetric(m, name)
		if !ok || math.Abs(got-want) > 0.3 {
			t.Errorf("%s got %.3f valid=%v want %.3f", name, got, ok, want)
		}
	}
}

func TestLinuxIndexedSensorsSkipMissingAndAmbiguous(t *testing.T) {
	root := t.TempDir()
	packages := map[int][]int{0: {0}, 1: {1}, 2: {2}}
	chip := filepath.Join(root, "hwmon", "hwmon0")
	writeCPUPackageFixture(t, filepath.Join(chip, "name"), "coretemp\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "temp1_label"), "Package id 0\n")
	writeCPUPackageFixture(t, filepath.Join(chip, "temp1_input"), "68000\n")
	// A second source for the same physical socket is ambiguous.
	duplicate := filepath.Join(root, "hwmon", "hwmon1")
	writeCPUPackageFixture(t, filepath.Join(duplicate, "name"), "coretemp\n")
	writeCPUPackageFixture(t, filepath.Join(duplicate, "temp1_label"), "Package id 0\n")
	writeCPUPackageFixture(t, filepath.Join(duplicate, "temp1_input"), "71000\n")
	rapl := filepath.Join(root, "powercap", "intel-rapl:0")
	writeCPUPackageFixture(t, filepath.Join(rapl, "name"), "package-1\n")
	writeCPUPackageFixture(t, filepath.Join(rapl, "energy_uj"), "2000000\n")
	read := discoverLinuxPackageTelemetry(root, packages)
	if read.devices[0].tempPath != "" {
		t.Fatal("duplicate temperature sources must not bind cpu0")
	}
	if read.devices[1].power.energy == "" {
		t.Fatal("legitimately scoped cpu1 RAPL lost")
	}
	if read.devices[2].power.energy != "" {
		t.Fatal("missing cpu2 RAPL invented")
	}
	m := metrics{}
	read.Sample(&m)
	aggregateIndexedMetrics(&m, map[string]int{"cpu": 3})
	for _, name := range []string{"cpu.temperature", "cpu0.temperature", "cpu1.power", "cpu2.power"} {
		if _, valid := readMetric(m, name); valid {
			t.Errorf("%s must be N/A", name)
		}
	}
}

func TestLinuxRAPLEnergyWrapAndNA(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "energy_uj")
	writeCPUPackageFixture(t, file, "200000\n")
	probe := cpuPowerProbe{energy: file, energyRange: 1000000, primed: true, lastEnergy: 900000, lastAt: time.Now().Add(-time.Second)}
	value, ok := readLinuxPackagePower(&probe)
	if !ok || math.Abs(value-0.3) > 0.01 {
		t.Fatalf("RAPL rollover = %.4f valid=%v want 0.3W", value, ok)
	}
	writeCPUPackageFixture(t, file, "not-a-number\n")
	if _, ok := readLinuxPackagePower(&probe); ok {
		t.Fatal("invalid RAPL must be N/A")
	}
	writeCPUPackageFixture(t, file, "20\n")
	probe.energyRange = 0
	if _, ok := readLinuxPackagePower(&probe); ok {
		t.Fatal("counter reset without wrap range must be N/A")
	}
}
