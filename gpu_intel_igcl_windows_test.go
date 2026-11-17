//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"math"
	"os"
	"testing"
	"unsafe"
)

func TestIGCLNativeStructLayout(t *testing.T) {
	cases := []struct {
		name      string
		got, want uintptr
	}{
		{"init", unsafe.Sizeof(igclInitArgs{}), 36},
		{"PCI BDF", unsafe.Sizeof(igclPCIAddress{}), 24},
		{"PCI speed", unsafe.Sizeof(igclPCISpeed{}), 24},
		{"PCI properties", unsafe.Sizeof(igclPCIProperties{}), 64},
		{"telemetry item", unsafe.Sizeof(igclTelemetryItem{}), 24},
		{"PSU", unsafe.Sizeof(igclPSU{}), 56},
		{"telemetry v2", unsafe.Sizeof(igclPowerTelemetryV2{}), 1016},
		{"telemetry item value offset", unsafe.Offsetof(igclTelemetryItem{}.Value), 16},
		{"IGCL telemetry GPU temp offset", unsafe.Offsetof(igclPowerTelemetryV2{}.GPUTemperature), 104},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s Go ABI=%d expected=%d", c.name, c.got, c.want)
		}
	}
}
func intelSample(value float64, units uint32) igclTelemetryItem {
	return igclTelemetryItem{Supported: 1, Units: units, Type: igclDataDouble, Value: math.Float64bits(value)}
}
func TestIGCLTelemetryUnitsAndCounterDeltas(t *testing.T) {
	state := &igclGPUState{}
	first := igclPowerTelemetryV2{}
	first.Size = uint32(unsafe.Sizeof(first))
	first.TimeStamp = intelSample(100, igclUnitSeconds)
	first.GPUEnergy = intelSample(500, igclUnitJoules)
	first.CardEnergy = intelSample(900, igclUnitJoules)
	first.GlobalActivity = intelSample(20, igclUnitSeconds)
	first.RenderActivity = intelSample(12, igclUnitSeconds)
	first.GPUTemperature = intelSample(61, igclUnitCelsius)
	first.GPUClock = intelSample(2450, igclUnitMHz)
	first.GPUVoltage = intelSample(1.15, igclUnitVolts)
	if values := state.decode(first); values["temperature"] != 61 || values["clock.core"] != 2450 {
		t.Fatalf("IGCL instantaneous data missing: %+v", values)
	} else if _, exists := values["power.gfx"]; exists {
		t.Fatalf("first counter must not pretend to provide measured power: %+v", values)
	}
	second := first
	second.TimeStamp = intelSample(102, igclUnitSeconds)
	second.GPUEnergy = intelSample(580, igclUnitJoules)
	second.CardEnergy = intelSample(1020, igclUnitJoules)
	second.GlobalActivity = intelSample(21, igclUnitSeconds)
	second.RenderActivity = intelSample(12.4, igclUnitSeconds)
	values := state.decode(second)
	if values["power.gfx"] != 40 || values["power.board"] != 60 || values["load"] != 50 {
		t.Fatalf("Intel telemetry counter rate calculation failed: %+v", values)
	}
	if _, exists := values["power"]; exists {
		t.Fatalf("unverified Intel integrated card/package power must not enter gpu.power: %+v", values)
	}
	third := second
	third.TimeStamp = intelSample(103, igclUnitSeconds)
	third.GPUEnergy = intelSample(4, igclUnitJoules) // hardware counter reset
	values = state.decode(third)
	if _, exists := values["power.gfx"]; exists {
		t.Fatal("reset counter must return N/A")
	}
}

func TestIGCLPCIExactMatchAndMissingDLL(t *testing.T) {
	devices := []hardwareDevice{
		{PCIVendor: 0x8086, PCIValid: true, PCIBus: 12, PCIDevice: 1, PCIFunction: 0},
		{PCIVendor: 0x1002, PCIValid: true, PCIBus: 12, PCIDevice: 1, PCIFunction: 0},
	}
	if index, ok := matchIGCLPCI(devices, igclPCIAddress{Bus: 12, Device: 1, Function: 0}); !ok || index != 0 {
		t.Fatal("Intel PCI mismatch")
	}
	if _, ok := matchIGCLPCI(devices, igclPCIAddress{Bus: 13, Device: 1, Function: 0}); ok {
		t.Fatal("unknown Intel GPU must not bind")
	}
	if _, ok := matchIGCLPCI(devices, igclPCIAddress{Bus: 12, Device: 1, Function: 1}); ok {
		t.Fatal("wrong PCI function must not bind")
	}
	// No Intel GPU: don't load arbitrary Intel driver or allocate handles.
	p, e := openIntelIGCL(nil)
	if e != nil || p != nil {
		t.Fatalf("absent Intel GPU should not attempt DLL load: %v", e)
	}
	path, e := intelIGCLDLLPath()
	if e != nil || !os.IsPathSeparator(path[2]) {
		t.Fatalf("unsafe IGCL path %q %v", path, e)
	}
}
