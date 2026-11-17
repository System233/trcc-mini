//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Match NVML handles to Windows PCI display-class devices before sampling.
// Never bind a different GPU merely because NVML's index happened to be 0.
package main

import (
	"fmt"
	"math"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type nvmlPCIInfo struct {
	Legacy                                         [16]byte
	Domain, Bus, Device, PCIProductID, SubsystemID uint32
	BusID                                          [32]byte
}
type windowsGPUInventory struct {
	devices       []hardwareDevice
	owner         *nvmlSensor
	bound         map[int]*nvmlSensor
	snapshot      metrics
	last          time.Time
	extra         map[string]sampleValue
	getClock      *syscall.Proc
	getPowerLimit *syscall.Proc
	getFan        *syscall.Proc
	getPcie       *syscall.Proc
}

func openWindowsGPUInventory() (gpuTelemetry, error) {
	devices, err := discoverWindowsGPUs()
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("no physical PCI display-class GPU detected")
	}
	out := &windowsGPUInventory{devices: devices, bound: map[int]*nvmlSensor{}, extra: map[string]sampleValue{}}
	owner, err := openNVML()
	if err != nil {
		// Intel/AMD devices remain enumerated but N/A until a corresponding
		// native telemetry backend becomes available. Do not impersonate NVIDIA.
		return out, nil
	}
	out.owner = owner
	out.getClock, _ = owner.dll.FindProc("nvmlDeviceGetClockInfo")
	out.getPowerLimit, _ = owner.dll.FindProc("nvmlDeviceGetPowerManagementLimit")
	out.getFan, _ = owner.dll.FindProc("nvmlDeviceGetFanSpeed")
	out.getPcie, _ = owner.dll.FindProc("nvmlDeviceGetPcieThroughput")
	countProc, e := owner.dll.FindProc("nvmlDeviceGetCount_v2")
	if e != nil {
		return out, nil
	}
	handleProc, e := owner.dll.FindProc("nvmlDeviceGetHandleByIndex_v2")
	if e != nil {
		return out, nil
	}
	pciProc, e := owner.dll.FindProc("nvmlDeviceGetPciInfo_v3")
	if e != nil {
		return out, nil
	}
	var count uint32
	status, _, _ := countProc.Call(uintptr(unsafe.Pointer(&count)))
	if status != 0 || count > 64 {
		return out, nil
	}
	used := map[int]bool{}
	for i := uint32(0); i < count; i++ {
		var handle uintptr
		status, _, _ = handleProc.Call(uintptr(i), uintptr(unsafe.Pointer(&handle)))
		if status != 0 || handle == 0 {
			continue
		}
		var pci nvmlPCIInfo
		status, _, _ = pciProc.Call(handle, uintptr(unsafe.Pointer(&pci)))
		if status != 0 {
			continue
		}
		matched := -1
		candidates := 0
		for j, d := range out.devices {
			if d.PCIVendor == 0x10de && d.PCIValid && d.PCIBus == pci.Bus && d.PCIDevice == pci.Device && !used[j] {
				// NVML PCI ID uses GPU function 0; only accept exact
				// physical coordinates (not vendor name or ordinal).
				if d.PCIFunction != 0 {
					continue
				}
				matched = j
				candidates++
			}
		}
		if candidates != 1 {
			continue
		} // uncertain => N/A; never misbind
		used[matched] = true
		bound := *owner
		bound.device = handle
		bound.dll = nil // DLL lifetime is owned solely by the inventory
		out.bound[matched] = &bound
	}
	return out, nil
}
func (g *windowsGPUInventory) Close() {
	if g == nil {
		return
	}
	if g.owner != nil {
		g.owner.Close()
		g.owner = nil
	}
	g.bound = map[int]*nvmlSensor{}
}
func (g *windowsGPUInventory) Devices() []hardwareDevice {
	out := append([]hardwareDevice(nil), g.devices...)
	for i := range out {
		if out[i].Vendor == "NVIDIA" {
			if _, ok := g.bound[i]; !ok {
				out[i].Driver = "NVML unbound"
			}
		} else {
			out[i].Driver = "N/A (vendor backend not implemented)"
		}
	}
	return out
}
func (g *windowsGPUInventory) sample() {
	if g == nil {
		return
	}
	if !g.last.IsZero() && time.Since(g.last) < 200*time.Millisecond {
		return
	}
	g.last = time.Now()
	g.snapshot = metrics{}
	g.extra = map[string]sampleValue{}
	var globalPower, globalTemperature, globalLoad metricAcc
	var vramUsed, vramTotal float64
	var vramValid bool
	for i, s := range g.bound {
		prefix := fmt.Sprintf("gpu%d.", i)
		t, tOK, load, loadOK := s.Read()
		if tOK {
			g.extra[prefix+"temperature"] = sampleValue{Value: t, Valid: true}
			globalTemperature.add(t, true)
		}
		if loadOK {
			g.extra[prefix+"load"] = sampleValue{Value: float64(load), Valid: true}
			globalLoad.add(float64(load), true)
		}
		if watt, ok := s.ReadPower(); ok {
			g.extra[prefix+"power"] = sampleValue{Value: watt, Valid: true}
			globalPower.add(watt, true)
		}
		if used, total, percent, ok := s.ReadMemory(); ok {
			g.extra[prefix+"memory.used"] = sampleValue{Value: used, Valid: true}
			g.extra[prefix+"memory.total"] = sampleValue{Value: total, Valid: true}
			g.extra[prefix+"memory.load"] = sampleValue{Value: percent, Valid: true}
			vramUsed += used
			vramTotal += total
			vramValid = true
		}
		if s.getLoad != nil {
			var util nvmlUtil
			status, _, _ := s.getLoad.Call(s.device, uintptr(unsafe.Pointer(&util)))
			if status == 0 && util.Memory <= 100 {
				g.extra[prefix+"load.memory"] = sampleValue{Value: float64(util.Memory), Valid: true}
			}
		}
		g.optionalMetrics(prefix, s)
	}
	if v, ok := globalPower.value("sum"); ok {
		g.snapshot.GPUPower, g.snapshot.GPUPowerValid = v, true
	}
	if v, ok := globalTemperature.value("max"); ok {
		g.snapshot.GPUTemp, g.snapshot.GPUTempValid = v, true
	}
	if v, ok := globalLoad.value("max"); ok {
		g.snapshot.GPULoad, g.snapshot.GPULoadValid = int(math.Round(v)), true
	}
	if vramValid && vramTotal > 0 {
		for name, v := range map[string]float64{
			"gpu.memory.used": vramUsed, "gpu.memory.total": vramTotal,
			"gpu.memory.load": 100 * vramUsed / vramTotal,
		} {
			g.extra[name] = sampleValue{Value: v, Valid: true}
		}
	}
}
func (g *windowsGPUInventory) optionalMetrics(prefix string, s *nvmlSensor) {
	read := func(proc *syscall.Proc, selector uintptr, metric string, scale, max float64) {
		if proc == nil {
			return
		}
		var value uint32
		var status uintptr
		if selector == ^uintptr(0) {
			status, _, _ = proc.Call(s.device, uintptr(unsafe.Pointer(&value)))
		} else {
			status, _, _ = proc.Call(s.device, selector, uintptr(unsafe.Pointer(&value)))
		}
		if status != 0 {
			return
		}
		number := float64(value) / scale
		if number < 0 || number > max {
			return
		}
		g.extra[prefix+metric] = sampleValue{Value: number, Valid: true}
	}
	read(g.getClock, 0, "clock.core", 1, 10000)   // NVML_CLOCK_GRAPHICS
	read(g.getClock, 2, "clock.memory", 1, 50000) // NVML_CLOCK_MEM
	read(g.getPowerLimit, ^uintptr(0), "power.limit", 1000, 1500)
	read(g.getFan, ^uintptr(0), "fan.load", 1, 100)
	// NVML throughput values are KiB/s; convert to decimal MB/s.
	read(g.getPcie, 0, "pcie.tx", 1000, 500000)
	read(g.getPcie, 1, "pcie.rx", 1000, 500000)
}
func (g *windowsGPUInventory) Read() (float64, bool, int, bool) {
	g.sample()
	return g.snapshot.GPUTemp, g.snapshot.GPUTempValid, g.snapshot.GPULoad, g.snapshot.GPULoadValid
}
func (g *windowsGPUInventory) ReadPower() (float64, bool) {
	g.sample()
	return g.snapshot.GPUPower, g.snapshot.GPUPowerValid
}
func (g *windowsGPUInventory) ReadMemory() (float64, float64, float64, bool) {
	g.sample()
	u, uok := g.extra["gpu.memory.used"]
	t, tok := g.extra["gpu.memory.total"]
	p, pok := g.extra["gpu.memory.load"]
	if !uok || !tok || !pok {
		return 0, 0, 0, false
	}
	return u.Value, t.Value, p.Value, true
}
func (g *windowsGPUInventory) SampleIndexed(m *metrics) {
	g.sample()
	for key, reading := range g.extra {
		if reading.Valid {
			m.setExtra(key, reading.Value)
		}
	}
	aggregateIndexedMetrics(m, map[string]int{"gpu": len(g.devices)})
}
func (g *windowsGPUInventory) Summary() string {
	var parts []string
	for _, d := range g.Devices() {
		parts = append(parts, fmt.Sprintf("gpu%d=%s (%s)", d.Index, d.Name, d.Bus))
	}
	return strings.Join(parts, ", ")
}
