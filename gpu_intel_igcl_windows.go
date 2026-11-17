//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Optional Intel Graphics Control Library (IGCL), native x64 ABI.
// The driver supplies ControlLib.dll; the application ships no DLL.
// Only read-only PCI and telemetry APIs are called.
package main

import (
	"fmt"
	"math"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const (
	igclDataUint64  = 7
	igclDataDouble  = 9
	igclDataFloat   = 8
	igclUnitMHz     = 0
	igclUnitVolts   = 3
	igclUnitWatts   = 4
	igclUnitCelsius = 5
	igclUnitJoules  = 6
	igclUnitSeconds = 7
	igclUnitPercent = 11
)

type igclInitArgs struct {
	Size             uint32
	Version          uint8
	_                [3]byte
	AppVersion       uint32
	Flags            uint32
	SupportedVersion uint32
	ApplicationUID   [16]byte
}
type igclPCIAddress struct {
	Size                          uint32
	Version                       uint8
	_                             [3]byte
	Domain, Bus, Device, Function uint32
}
type igclPCISpeed struct {
	Size         uint32
	Version      uint8
	_            [3]byte
	Gen, Width   int32
	MaxBandwidth int64
}
type igclPCIProperties struct {
	Size                  uint32
	Version               uint8
	_                     [3]byte
	Address               igclPCIAddress
	MaxSpeed              igclPCISpeed
	ResizableBarSupported uint8
	ResizableBarEnabled   uint8
	_                     [6]byte
}
type igclTelemetryItem struct {
	Supported uint8
	_         [3]byte
	Units     uint32
	Type      uint32
	_         [4]byte
	Value     uint64 // C union data_value_t; double encoded as IEEE754 bits
}
type igclPSU struct {
	Supported uint8
	_         [3]byte
	PSUType   uint32
	Energy    igclTelemetryItem
	Voltage   igclTelemetryItem
}
type igclPowerTelemetryV2 struct {
	Size                  uint32
	Version               uint8
	_                     [3]byte
	TimeStamp             igclTelemetryItem
	GPUEnergy             igclTelemetryItem
	GPUVoltage            igclTelemetryItem
	GPUClock              igclTelemetryItem
	GPUTemperature        igclTelemetryItem
	GlobalActivity        igclTelemetryItem
	RenderActivity        igclTelemetryItem
	MediaActivity         igclTelemetryItem
	GPUFlags              [5]uint8
	_                     [3]byte
	VRAMEnergy            igclTelemetryItem
	VRAMVoltage           igclTelemetryItem
	VRAMClock             igclTelemetryItem
	VRAMEffectiveClock    igclTelemetryItem
	VRAMReadCounter       igclTelemetryItem
	VRAMWriteCounter      igclTelemetryItem
	VRAMTemperature       igclTelemetryItem
	CardEnergy            igclTelemetryItem
	PSU                   [5]igclPSU
	Fan                   [5]igclTelemetryItem
	GPUVRTemperature      igclTelemetryItem
	VRAMVRTemperature     igclTelemetryItem
	SAVRTemperature       igclTelemetryItem
	GPUEffectiveClock     igclTelemetryItem
	GPUOverVoltagePercent igclTelemetryItem
	GPUPowerPercent       igclTelemetryItem
	GPUTemperaturePercent igclTelemetryItem
	VRAMReadBandwidth     igclTelemetryItem
	VRAMWriteBandwidth    igclTelemetryItem
}

func (item igclTelemetryItem) number(unit uint32) (float64, bool) {
	if item.Supported == 0 || item.Units != unit {
		return 0, false
	}
	var v float64
	switch item.Type {
	case igclDataDouble:
		v = math.Float64frombits(item.Value)
	case igclDataFloat:
		v = float64(math.Float32frombits(uint32(item.Value)))
	case igclDataUint64:
		v = float64(item.Value)
	default:
		return 0, false
	}
	if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}
func (item igclTelemetryItem) measure(unit uint32, max float64) (float64, bool) {
	v, ok := item.number(unit)
	return v, ok && v <= max
}

type igclCounterSample struct {
	energy, timestamp, activity, render, card      float64
	energyOK, timeOK, activityOK, renderOK, cardOK bool
}
type igclGPUState struct {
	first  igclCounterSample
	prevAt time.Time
	primed bool
}

func igclCounterRate(new, old float64, valid bool, seconds float64) (float64, bool) {
	if !valid || seconds <= 0.1 || seconds > 30 || new < old {
		return 0, false
	}
	rate := (new - old) / seconds
	if rate < 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
		return 0, false
	}
	return rate, true
}
func (s *igclGPUState) decode(t igclPowerTelemetryV2) map[string]float64 {
	result := map[string]float64{}
	emit := func(name string, item igclTelemetryItem, unit uint32, max float64) {
		if v, ok := item.measure(unit, max); ok {
			result[name] = v
		}
	}
	emit("temperature", t.GPUTemperature, igclUnitCelsius, 130)
	emit("temperature.memory", t.VRAMTemperature, igclUnitCelsius, 150)
	emit("clock.core", t.GPUClock, igclUnitMHz, 10000)
	emit("clock.memory", t.VRAMClock, igclUnitMHz, 50000)
	emit("voltage", t.GPUVoltage, igclUnitVolts, 3)
	emit("fan.speed", t.Fan[0], 9, 20000)

	current := igclCounterSample{}
	current.energy, current.energyOK = t.GPUEnergy.number(igclUnitJoules)
	current.card, current.cardOK = t.CardEnergy.number(igclUnitJoules)
	current.timestamp, current.timeOK = t.TimeStamp.number(igclUnitSeconds)
	current.activity, current.activityOK = t.GlobalActivity.number(igclUnitSeconds)
	current.render, current.renderOK = t.RenderActivity.number(igclUnitSeconds)
	now := time.Now()
	if s.primed && current.timeOK && s.first.timeOK {
		seconds := current.timestamp - s.first.timestamp
		if watt, ok := igclCounterRate(current.energy, s.first.energy, current.energyOK && s.first.energyOK, seconds); ok && watt <= 1500 {
			// GPU chip energy may overlap CPU package on integrated GPUs.
			// Expose the domain; do not add to generic gpu.power.
			result["power.gfx"] = watt
		}
		if watt, ok := igclCounterRate(current.card, s.first.card, current.cardOK && s.first.cardOK, seconds); ok && watt <= 1500 {
			result["power.board"] = watt
			// IGCL also reports package/card energy on iGPUs; this may
			// overlap CPU package power. Keep it opt-in and unaggregated.
		}
		if rate, ok := igclCounterRate(current.activity, s.first.activity, current.activityOK && s.first.activityOK, seconds); ok && rate <= 1.05 {
			result["load"] = math.Min(100, rate*100)
		}
		if rate, ok := igclCounterRate(current.render, s.first.render, current.renderOK && s.first.renderOK, seconds); ok && rate <= 1.05 {
			result["load.compute"] = math.Min(100, rate*100)
		}
	}
	if current.timeOK {
		s.first = current
		s.prevAt = now
		s.primed = true
	}
	return result
}

type igclWindowsProvider struct {
	dll       *syscall.DLL
	context   uintptr
	close     *syscall.Proc
	telemetry *syscall.Proc
	adapters  map[int]uintptr // native PCI gpu index -> IGCL handle
	state     map[int]*igclGPUState
}

func (p *igclWindowsProvider) Close() {
	if p == nil {
		return
	}
	if p.context != 0 && p.close != nil {
		p.close.Call(p.context)
		p.context = 0
	}
	if p.dll != nil {
		p.dll.Release()
		p.dll = nil
	}
}
func intelIGCLDLLPath() (string, error) {
	base, e := amdSystemDLLPath()
	if e != nil {
		return "", e
	}
	return filepath.Join(filepath.Dir(base), "ControlLib.dll"), nil
}
func matchIGCLPCI(devices []hardwareDevice, pci igclPCIAddress) (int, bool) {
	if pci.Bus > 255 || pci.Device > 31 || pci.Function > 7 {
		return -1, false
	}
	match := -1
	for i, d := range devices {
		if d.PCIVendor != 0x8086 || !d.PCIValid || d.PCIBus != pci.Bus || d.PCIDevice != pci.Device || d.PCIFunction != pci.Function {
			continue
		}
		if match >= 0 {
			return -1, false
		}
		match = i
	}
	return match, match >= 0
}
func openIntelIGCL(devices []hardwareDevice) (*igclWindowsProvider, error) {
	needed := false
	for _, d := range devices {
		if d.PCIVendor == 0x8086 && d.PCIValid {
			needed = true
			break
		}
	}
	if !needed {
		return nil, nil
	}
	path, e := intelIGCLDLLPath()
	if e != nil {
		return nil, e
	}
	dll, e := syscall.LoadDLL(path)
	if e != nil {
		return nil, e
	}
	p := &igclWindowsProvider{dll: dll, adapters: map[int]uintptr{}, state: map[int]*igclGPUState{}}
	fail := func(err error) (*igclWindowsProvider, error) { p.Close(); return nil, err }
	init, e := dll.FindProc("ctlInit")
	if e != nil {
		return fail(e)
	}
	p.close, e = dll.FindProc("ctlClose")
	if e != nil {
		return fail(e)
	}
	enum, e := dll.FindProc("ctlEnumerateDevices")
	if e != nil {
		return fail(e)
	}
	pciProc, e := dll.FindProc("ctlPciGetProperties")
	if e != nil {
		return fail(e)
	}
	p.telemetry, e = dll.FindProc("ctlPowerTelemetryGetV2")
	if e != nil {
		return fail(e)
	}
	args := igclInitArgs{Size: uint32(unsafe.Sizeof(igclInitArgs{})), Version: 0, AppVersion: 0x00010001, Flags: 1}
	result, _, _ := init.Call(uintptr(unsafe.Pointer(&args)), uintptr(unsafe.Pointer(&p.context)))
	if result != 0 || p.context == 0 {
		return fail(fmt.Errorf("IGCL ctlInit: %#x", result))
	}
	var count uint32
	result, _, _ = enum.Call(p.context, uintptr(unsafe.Pointer(&count)), 0)
	if result != 0 || count > 64 {
		return fail(fmt.Errorf("IGCL enumerate: code %#x count %d", result, count))
	}
	if count == 0 {
		return p, nil
	}
	handles := make([]uintptr, count)
	result, _, _ = enum.Call(p.context, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&handles[0])))
	if result != 0 {
		return fail(fmt.Errorf("IGCL enumerate handles: %#x", result))
	}
	conflicts := map[int]bool{}
	for _, handle := range handles {
		if handle == 0 {
			continue
		}
		pci := igclPCIProperties{Size: uint32(unsafe.Sizeof(igclPCIProperties{})), Version: 0}
		pci.Address.Size = uint32(unsafe.Sizeof(igclPCIAddress{}))
		pci.MaxSpeed.Size = uint32(unsafe.Sizeof(igclPCISpeed{}))
		code, _, _ := pciProc.Call(handle, uintptr(unsafe.Pointer(&pci)))
		if code != 0 {
			continue
		}
		index, ok := matchIGCLPCI(devices, pci.Address)
		if !ok {
			continue
		}
		if _, exists := p.adapters[index]; exists {
			conflicts[index] = true
		}
		p.adapters[index] = handle
	}
	for i := range conflicts {
		delete(p.adapters, i)
	}
	return p, nil
}
func (p *igclWindowsProvider) Sample() map[string]float64 {
	values := map[string]float64{}
	if p == nil || p.telemetry == nil {
		return values
	}
	for index, handle := range p.adapters {
		var telemetry igclPowerTelemetryV2
		telemetry.Size = uint32(unsafe.Sizeof(igclPowerTelemetryV2{}))
		telemetry.Version = 0
		result, _, _ := p.telemetry.Call(handle, uintptr(unsafe.Pointer(&telemetry)))
		if result != 0 {
			continue
		}
		state := p.state[index]
		if state == nil {
			state = &igclGPUState{}
			p.state[index] = state
		}
		for name, v := range state.decode(telemetry) {
			values[fmt.Sprintf("gpu%d.%s", index, name)] = v
		}
	}
	return values
}
