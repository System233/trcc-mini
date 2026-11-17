//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
	"unsafe"
)

func TestWindowsDisplayClassEnumeration(t *testing.T) {
	if unsafe.Sizeof(nativeDeviceInfoData{}) != 32 {
		t.Fatalf("incorrect x64 SP_DEVINFO_DATA ABI: %d", unsafe.Sizeof(nativeDeviceInfoData{}))
	}
	if v := parsePCIVendor(`PCI\VEN_10DE&DEV_2786`); v != 0x10de {
		t.Fatalf("invalid NVIDIA vendor: %#x", v)
	}
	if v := parsePCIVendor(`PCI\VEN_8086&DEV_1234`); v != 0x8086 {
		t.Fatalf("invalid Intel vendor: %#x", v)
	}
	if v := parsePCIVendor(`ROOT\DISPLAY\0000`); v != 0 {
		t.Fatalf("virtual display wrongly classified: %#x", v)
	}
	devices, err := discoverWindowsGPUs()
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range devices {
		t.Logf("GPU%d %s %s PCI=%s valid=%t PNP=%s", i, d.Vendor, d.Name, d.Bus, d.PCIValid, d.PNP)
		if d.Index != i {
			t.Fatalf("noncanonical GPU index %d != %d", d.Index, i)
		}
		if strings.Contains(d.PNP, "ROOT\\DISPLAY") {
			t.Fatal("virtual adapter entered physical device inventory")
		}
	}
	t.Logf("physical CPU packages: %d", physicalCPUCount())
}

func TestWindowsNVMLPCIExactBinding(t *testing.T) {
	p, err := openWindowsGPUInventory()
	if err != nil {
		t.Skipf("no hardware inventory: %v", err)
	}
	defer p.Close()
	inventory := p.(*windowsGPUInventory)
	for _, d := range inventory.Devices() {
		t.Logf("GPU%d: %s; driver=%s; pci=%s", d.Index, d.Name, d.Driver, d.Bus)
	}
	if len(inventory.bound) > len(inventory.devices) {
		t.Fatal("NVML handle count exceeds OS GPU count")
	}
	for index, s := range inventory.bound {
		if inventory.devices[index].PCIVendor != 0x10de || s.device == 0 {
			t.Fatalf("incorrect NVML-to-OS binding at gpu%d", index)
		}
	}
}
