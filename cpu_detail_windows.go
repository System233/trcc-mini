//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Windows native per-processor OS clock estimates and existing PawnIO CCDs.
// CallNtPowerInformation does not require an elevated kernel helper.
package main

import (
	"math"
	"strconv"
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
		groups, _, _ := getProcessorGroupCount.Call()
		if groups != 1 {
			// CallNtPowerInformation lacks a documented group-indexed
			// result mapping. Do not present a partial group's frequency
			// as the multi-group machine's aggregate.
		} else {
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
	}
	if sensor == nil {
		return
	}
	singleSocket := physicalCPUCount() == 1
	var temperatures []float64
	// A package temperature can be published under cpu0 only when there is
	// one socket. Still expose the readable generic sensor on other hosts.
	if value, e := sensor.ReadTemp("tctl"); e == nil && value > 0 {
		if singleSocket {
			m.setExtra("cpu0.temperature", value)
		}
		temperatures = append(temperatures, value)
	}
	// CCDs remain readable without socket attribution: cpu.ccdN holds an
	// actual sensor value, cpu0.ccdN is used only for a single socket.
	for i, legacy := range []string{"ccd1", "ccd2"} {
		if value, e := sensor.ReadTemp(legacy); e == nil && value > 0 {
			suffix := "ccd" + strconv.Itoa(i) + ".temperature"
			m.setExtra("cpu."+suffix, value)
			if singleSocket {
				m.setExtra(indexedMetricName("cpu", 0, suffix), value)
			}
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
		m.setExtra("cpu.temperature.max", peak)
		if singleSocket {
			m.setExtra("cpu0.temperature.max", peak)
		}
	}
}
