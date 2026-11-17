//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"math"
	"testing"
	"unsafe"
)

func TestAMDADLInteropABISizes(t *testing.T) {
	if got := unsafe.Sizeof(adlAdapterInfo{}); got != 1572 {
		t.Fatalf("ADL AdapterInfo ABI layout %d, want 1572", got)
	}
	if got := unsafe.Sizeof(adlPMLog{}); got != 2052 {
		t.Fatalf("ADL PMLog ABI layout %d, want 2052", got)
	}
	if got := unsafe.Offsetof(adlPMLog{}.Size); got != 0 {
		t.Fatalf("ADL PMLog size field at offset %d, want 0", got)
	}
}

func TestAMDADLMatchByExactPCI(t *testing.T) {
	devices := []hardwareDevice{
		{PCIValid: true, PCIVendor: 0x1002, PCIBus: 12, PCIDevice: 0, PCIFunction: 0, PNP: "PCI\\VEN_1002&DEV_13C0\\BASE"},
		{PCIValid: true, PCIVendor: 0x1002, PCIBus: 5, PCIDevice: 0, PCIFunction: 0},
		{PCIValid: true, PCIVendor: 0x10de, PCIBus: 1, PCIDevice: 0, PCIFunction: 0},
	}
	sample := []adlAdapterInfo{
		{Vendor: 1002, Bus: 12, Device: 0, Function: 0, Exist: 1, AdapterIndex: 7},
		{Vendor: 0x1002, Bus: 5, Device: 0, Function: 0, Exist: 1, AdapterIndex: 2},
		{Vendor: 1002, Bus: 9, Device: 0, Function: 0, Exist: 1, AdapterIndex: 3},
	}
	matched := matchADLAdapters(devices, sample)
	if len(matched) != 2 || matched[0] != 7 || matched[1] != 2 {
		t.Fatalf("ADL PCI device mapping incorrect: %+v", matched)
	}
	sample = append(sample, adlAdapterInfo{Vendor: 1002, Bus: 12, Device: 0, Function: 0, Exist: 1, AdapterIndex: 9})
	matched = matchADLAdapters(devices, sample)
	if _, ok := matched[0]; ok {
		t.Fatal("ambiguous AMD logical-adapter match should be N/A")
	}
	if matched[1] != 2 {
		t.Fatal("distinct physical AMD GPU incorrectly lost")
	}
	sample[0].PNP = [256]byte{}
	copy(sample[0].PNP[:], devices[0].PNP)
	matched = matchADLAdapters(devices, sample)
	if matched[0] != 7 {
		t.Fatalf("exact PNP identity should resolve ADL's duplicate logical handles: %+v", matched)
	}
}

func TestAMDPMLogNeverUsesASICAsGraphicsPower(t *testing.T) {
	raw := adlPMLog{}
	raw.Size = 2052
	raw.Sensors[adlPowerASIC] = adlSensor{Supported: 1, Value: 105}
	raw.Sensors[adlTempEdge] = adlSensor{Supported: 1, Value: 55}
	raw.Sensors[adlLoadCore] = adlSensor{Supported: 1, Value: 43}
	values := readADLPMLog(raw)
	if _, ok := values["power"]; ok {
		t.Fatalf("APU ASIC power must not be mistaken as GPU-only draw: %+v", values)
	}
	if values["power.asic"] != 105 || values["temperature"] != 55 || values["load"] != 43 {
		t.Fatalf("PMLog mappings: %+v", values)
	}
	raw.Sensors[adlPowerGraphics] = adlSensor{Supported: 1, Value: 17}
	raw.Sensors[adlPowerBoard] = adlSensor{Supported: 1, Value: 145}
	raw.Sensors[adlLoadCore] = adlSensor{Supported: 1, Value: 255} // invalid
	values = readADLPMLog(raw)
	if values["power"] != 145 || values["power.board"] != 145 || values["power.gfx"] != 17 {
		t.Fatalf("Power domain mixup: %+v", values)
	}
	if _, ok := values["load"]; ok {
		t.Fatal("invalid utilization must be N/A")
	}
	if math.IsNaN(values["power"]) {
		t.Fatal("nonfinite power value")
	}
}

func TestAMDADLNativeDeviceProbe(t *testing.T) {
	devices, err := discoverWindowsGPUs()
	if err != nil {
		t.Skipf("Windows GPU enumeration unavailable: %v", err)
	}
	backend, err := openAMDADL(devices)
	if err != nil {
		t.Logf("AMD driver telemetry unavailable: %v", err)
		return
	}
	if backend == nil {
		t.Skip("no AMD GPU found")
	}
	defer backend.Close()
	t.Logf("AMD ADL PCI matched adapters=%v", backend.adapters)
	for name, value := range backend.Sample() {
		t.Logf("ADL %s = %v", name, value)
	}
}
