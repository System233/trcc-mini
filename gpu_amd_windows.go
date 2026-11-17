//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// AMD Display Library ADL2: read-only per-adapter PMLog telemetry.
// AMD driver DLL is loaded only from the Windows system directory.
// AdapterInfo PCI coordinates are matched with the native GPU inventory.
// Never treat a whole APU/ASIC power domain as GPU-only power.
package main

import (
	"bytes"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

type adlAdapterInfo struct {
	Size                          int32
	AdapterIndex                  int32
	UDID                          [256]byte
	Bus, Device, Function, Vendor int32
	Name                          [256]byte
	Display                       [256]byte
	Present, Exist                int32
	DriverPath                    [256]byte
	DriverPathExt                 [256]byte
	PNP                           [256]byte
	DisplayIndex                  int32
}

type adlSensor struct {
	Supported int32
	Value     int32
}
type adlPMLog struct {
	Size    int32 // AMD adl_structures.h:3191 declares size BEFORE sensors.
	Sensors [256]adlSensor
}

const (
	adlClockCore       = 1
	adlClockMemory     = 2
	adlTempEdge        = 8
	adlTempMemory      = 9
	adlFanRPM          = 14
	adlFanPercentage   = 15
	adlLoadCore        = 19
	adlLoadMemory      = 20
	adlPowerASIC       = 23
	adlTempHotspot     = 27
	adlPowerGraphics   = 30
	adlPowerBoard      = 73
	adlTempGraphics    = 28
	adlTempSoC         = 29
	adlPowerSoC        = 17
	adlVoltageGraphics = 21
	adlVoltageSoC      = 16
)

type adlWindowsProvider struct {
	dll      *syscall.DLL
	context  uintptr
	destroy  *syscall.Proc
	metrics  *syscall.Proc
	adapters map[int]int32 // shared GPU index -> ADL adapter index
}

var adlCRT = syscall.NewLazyDLL("ucrtbase.dll")
var adlMalloc = adlCRT.NewProc("malloc")
var adlMallocCallback = syscall.NewCallback(func(size uintptr) uintptr {
	// ADL uses the caller's CRT malloc callback. Never supply Go heap memory.
	if size == 0 || size > 16*1024*1024 {
		return 0
	}
	addr, _, _ := adlMalloc.Call(size)
	return addr
})

func amdSystemDLLPath() (string, error) {
	var buf [32768]uint16
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemDirectoryW")
	n, _, e := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || n >= uintptr(len(buf)) {
		return "", fmt.Errorf("GetSystemDirectoryW failed: %v", e)
	}
	return filepath.Join(syscall.UTF16ToString(buf[:n]), "atiadlxx.dll"), nil
}

func (p *adlWindowsProvider) Close() {
	if p == nil {
		return
	}
	if p.context != 0 && p.destroy != nil {
		p.destroy.Call(p.context)
		p.context = 0
	}
	if p.dll != nil {
		p.dll.Release()
		p.dll = nil
	}
}

func openAMDADL(devices []hardwareDevice) (*adlWindowsProvider, error) {
	var amdCount int
	for _, d := range devices {
		if d.PCIVendor == 0x1002 && d.PCIValid {
			amdCount++
		}
	}
	if amdCount == 0 {
		return nil, nil
	}
	path, err := amdSystemDLLPath()
	if err != nil {
		return nil, err
	}
	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return nil, err
	}
	p := &adlWindowsProvider{dll: dll, adapters: map[int]int32{}}
	cleanup := func(e error) (*adlWindowsProvider, error) { p.Close(); return nil, e }
	create, e := dll.FindProc("ADL2_Main_Control_Create")
	if e != nil {
		return cleanup(e)
	}
	p.destroy, e = dll.FindProc("ADL2_Main_Control_Destroy")
	if e != nil {
		return cleanup(e)
	}
	countProc, e := dll.FindProc("ADL2_Adapter_NumberOfAdapters_Get")
	if e != nil {
		return cleanup(e)
	}
	infoProc, e := dll.FindProc("ADL2_Adapter_AdapterInfo_Get")
	if e != nil {
		return cleanup(e)
	}
	p.metrics, e = dll.FindProc("ADL2_New_QueryPMLogData_Get")
	if e != nil {
		return cleanup(e)
	}
	// Windows x64 syscall.Proc.Call uses the native x64 ABI. The ADL2
	// function returns signed int32, 0 = success.
	result, _, _ := create.Call(adlMallocCallback, 1, uintptr(unsafe.Pointer(&p.context)))
	if int32(result) != 0 || p.context == 0 {
		return cleanup(fmt.Errorf("ADL2 init failed: %d", int32(result)))
	}
	var count int32
	result, _, _ = countProc.Call(p.context, uintptr(unsafe.Pointer(&count)))
	if int32(result) != 0 || count < 0 || count > 250 {
		return cleanup(fmt.Errorf("ADL2 count failed: %d count=%d", int32(result), count))
	}
	if count == 0 {
		return p, nil
	}
	infos := make([]adlAdapterInfo, int(count))
	for i := range infos {
		infos[i].Size = int32(unsafe.Sizeof(adlAdapterInfo{}))
	}
	result, _, _ = infoProc.Call(p.context, uintptr(unsafe.Pointer(&infos[0])), uintptr(len(infos))*unsafe.Sizeof(adlAdapterInfo{}))
	if int32(result) != 0 {
		return cleanup(fmt.Errorf("ADL2 AdapterInfo failed: %d", int32(result)))
	}
	p.adapters = matchADLAdapters(devices, infos)
	return p, nil
}

// One GPU may have multiple ADL logical-adapter handles. Only bind if
// the physical PCI coordinates uniquely identify one native AMD GPU.
func matchADLAdapters(devices []hardwareDevice, infos []adlAdapterInfo) map[int]int32 {
	result := map[int]int32{}
	type logical struct {
		index int32
		exact bool
	}
	matches := map[int][]logical{}
	for _, a := range infos {
		if a.Bus < 0 || a.Bus > 255 || a.Device < 0 || a.Device > 31 || a.Function < 0 || a.Function > 7 || a.Exist == 0 {
			continue
		}
		if a.Vendor != 0x1002 && a.Vendor != 1002 {
			continue
		}
		target := -1
		for i, d := range devices {
			if d.PCIVendor != 0x1002 || !d.PCIValid {
				continue
			}
			if d.PCIBus == uint32(a.Bus) && d.PCIDevice == uint32(a.Device) && d.PCIFunction == uint32(a.Function) {
				if target != -1 {
					target = -1
					break
				}
				target = i
			}
		}
		if target < 0 {
			continue
		}
		pnp := string(a.PNP[:])
		if zero := bytes.IndexByte(a.PNP[:], 0); zero >= 0 {
			pnp = pnp[:zero]
		}
		matches[target] = append(matches[target], logical{
			index: a.AdapterIndex,
			exact: strings.EqualFold(pnp, devices[target].PNP) && pnp != "",
		})
	}
	for i, candidates := range matches {
		// ADL publishes several logical handles for the same physical GPU
		// (often PNP instance-id suffixes &02..&05). Prefer the unique
		// exact OS PNP identity. Do NOT multiply their power readings.
		if len(candidates) == 1 {
			result[i] = candidates[0].index
			continue
		}
		count := 0
		for _, candidate := range candidates {
			if candidate.exact {
				count++
				result[i] = candidate.index
			}
		}
		if count != 1 {
			delete(result, i)
		}
	}
	return result
}

func readADLPMLog(log adlPMLog) map[string]float64 {
	out := map[string]float64{}
	emit := func(id int, name string, min, max float64, scale float64) {
		item := log.Sensors[id]
		if item.Supported == 0 {
			return
		}
		v := float64(item.Value) / scale
		if v < min || v > max || math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		out[name] = v
	}
	// PMLog sensor values are Celsius, MHz, watts, RPM and percent.
	emit(adlTempEdge, "temperature", 1, 130, 1)
	if _, ok := out["temperature"]; !ok {
		emit(adlTempGraphics, "temperature", 1, 130, 1)
	}
	emit(adlTempSoC, "temperature.soc", 1, 130, 1)
	emit(adlTempHotspot, "temperature.hotspot", 1, 150, 1)
	emit(adlTempMemory, "temperature.memory", 1, 150, 1)
	emit(adlLoadCore, "load", 0, 100, 1)
	emit(adlLoadMemory, "load.memory", 0, 100, 1)
	emit(adlClockCore, "clock.core", 0, 10000, 1)
	emit(adlClockMemory, "clock.memory", 0, 50000, 1)
	emit(adlFanRPM, "fan.speed", 0, 20000, 1)
	emit(adlFanPercentage, "fan.load", 0, 100, 1)
	// Critical: ASIC_POWER can include CPU/SoC on an APU, so it must
	// never be used as gpu.power. Keep it explicitly labeled separately.
	emit(adlPowerASIC, "power.asic", 0, 1500, 1)
	emit(adlPowerSoC, "power.soc", 0, 1500, 1)
	// APU graphics/ASIC domain reporting can overlap CPU package energy.
	// Expose domain-specific measurements without mislabeling them as
	// independently measured GPU board draw.
	emit(adlPowerGraphics, "power.gfx", 0, 1500, 1)
	emit(adlVoltageGraphics, "voltage", 0, 2, 1000)
	emit(adlVoltageSoC, "voltage.soc", 0, 2, 1000)
	emit(adlPowerBoard, "power.board", 0, 1500, 1)
	if board, ok := out["power.board"]; ok {
		out["power"] = board
	}
	return out
}

func (p *adlWindowsProvider) Sample() map[string]float64 {
	out := map[string]float64{}
	if p == nil || p.context == 0 || p.metrics == nil {
		return out
	}
	for index, adapter := range p.adapters {
		var raw adlPMLog
		raw.Size = int32(unsafe.Sizeof(raw))
		result, _, _ := p.metrics.Call(p.context, uintptr(uint32(adapter)), uintptr(unsafe.Pointer(&raw)))
		if int32(result) != 0 {
			continue
		}
		for metric, value := range readADLPMLog(raw) {
			out[fmt.Sprintf("gpu%d.%s", index, metric)] = value
		}
	}
	return out
}
