//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Windows native per-processor OS clock estimates and existing PawnIO CCDs.
// CallNtPowerInformation does not require an elevated kernel helper.
package main

import (
	"math"
	"syscall"
	"unsafe"
)

type processorPowerInfo struct {
	Number           uint32
	MaxMhz           uint32
	CurrentMhz       uint32
	LimitMhz         uint32
	MaxIdleState     uint32
	CurrentIdleState uint32
}

var winCPUClockAPI = syscall.NewLazyDLL("powrprof.dll").NewProc("CallNtPowerInformation")
var winCPUCountAPI = syscall.NewLazyDLL("kernel32.dll").NewProc("GetActiveProcessorCount")

func currentWindowsCPUClocks() (avg, max float64, valid bool) {
	// ALL_PROCESSOR_GROUPS includes >64-thread Intel Xeon/AMD EPYC hosts.
	count, _, _ := winCPUCountAPI.Call(0xffff)
	if count == 0 || count > 4096 {
		return 0, 0, false
	}
	info := make([]processorPowerInfo, int(count))
	rc, _, _ := winCPUClockAPI.Call(11, 0, 0, uintptr(unsafe.Pointer(&info[0])), uintptr(len(info))*unsafe.Sizeof(info[0]))
	if rc != 0 {
		return 0, 0, false
	} // NTSTATUS != STATUS_SUCCESS
	var sum float64
	n := 0
	for _, p := range info {
		// Avoid confusing MaxMhz (nominal maximum) with current frequency.
		v := float64(p.CurrentMhz)
		if v <= 0 || v > 15000 || math.IsInf(v, 0) || math.IsNaN(v) {
			continue
		}
		sum += v
		n++
		if v > max {
			max = v
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return sum / float64(n), max, true
}

// This function only handles metrics whose provenance is verified. Other
// indexed names are legitimate config values but stay N/A without a provider.
func populatePlatformCPUMetrics(m *metrics, sensor cpuTelemetry) {
	if _, clockPresent := m.Extras["cpu.clock"]; !clockPresent {
		// OS fallback when grouped core clocks are not available. Avoid
		// making a second power-information query on every 200ms frame.
		avg, max, ok := currentWindowsCPUClocks()
		if ok {
			m.setExtra("cpu.clock", avg)
			m.setExtra("cpu.clock.max", max)
			if physicalCPUCount() == 1 {
				m.setExtra("cpu0.clock", avg)
				m.setExtra("cpu0.clock.max", max)
			}
		}
	}
	if sensor == nil || physicalCPUCount() != 1 {
		return
	}
	var temperatures []float64
	// cpu0.temperature always denotes package control temperature, not
	// whichever CCD source was selected for the legacy cpu.temperature.
	if value, e := sensor.ReadTemp("tctl"); e == nil && value > 0 {
		m.setExtra("cpu0.temperature", value)
		temperatures = append(temperatures, value)
	}
	// Intel MSR readers refuse CCD reads; AMD chips expose CCD1, CCD2
	// through their existing signed module. The new index is zero-based.
	for i, legacy := range []string{"ccd1", "ccd2"} {
		if value, e := sensor.ReadTemp(legacy); e == nil && value > 0 {
			m.setExtra(indexedMetricName("cpu", 0, "ccd"+string(rune('0'+i))+".temperature"), value)
			temperatures = append(temperatures, value)
		}
	}
	if len(temperatures) > 0 {
		peak := temperatures[0]
		for _, v := range temperatures[1:] {
			if v > peak {
				peak = v
			}
		}
		m.setExtra("cpu0.temperature.max", peak)
	}
}
