//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Linux per-GPU samples are correlated with native DRM PCI topology,
// not with nvidia-smi's unstable ordinal. Missing devices yield N/A.
package main

import (
	"fmt"
	"strconv"
	"strings"
)

func normalizedNVIDIABusID(bus string) string {
	parts := strings.SplitN(bus, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	domain, err := strconv.ParseUint(parts[0], 16, 32)
	if err != nil {
		return ""
	}
	// nvidia-smi expects the full PCI domain, e.g. 00000000:01:00.0.
	return fmt.Sprintf("%08x:%s", domain, parts[1])
}

// Called only by the existing single refresh loop. Each physical adapter
// has its own sampler/cache so GPU0 never borrows GPU1's reading.
func (g *linuxGPU) initializeIndexed() {
	if g.indexed != nil {
		return
	}
	g.indexed = map[int]*linuxGPU{}
	devices, e := discoverHardware()
	if e != nil {
		return
	}
	for _, d := range devices {
		if d.Kind != "gpu" {
			continue
		}
		g.deviceCount++
		var backend, device string
		switch d.Vendor {
		case "NVIDIA":
			backend = "nvidia"
			device = normalizedNVIDIABusID(d.Bus)
			// Unverified PCI ID is not the same as NVML index 0.
			if device == "" {
				continue
			}
		case "AMD":
			backend = "amd"
			device = d.PNP
		case "Intel":
			backend = "intel"
			device = d.PNP
		default:
			continue
		}
		g.indexed[d.Index] = &linuxGPU{backend: backend, device: device}
	}
}

func (g *linuxGPU) SampleIndexed(m *metrics) {
	g.initializeIndexed()
	if g.deviceCount == 0 {
		return
	} // preserve legacy provider when DRM is absent
	if m.Extras != nil {
		// The first discovered provider is NOT an aggregate in a multi-GPU
		// host. Clear it and recompute from PCI-bound indexed sensors.
		for suffix := range gpuMetricUnits {
			delete(m.Extras, "gpu."+suffix)
		}
	}
	m.GPUTempValid = false
	m.GPULoadValid = false
	m.GPUPowerValid = false
	for index, device := range g.indexed {
		s := device.snapshot() // asynchronous; never stall the 200ms HID frame
		prefix := indexedMetricName("gpu", index, "")
		if s.tempValid {
			m.setExtra(prefix+"temperature", s.temp)
		}
		if s.loadValid {
			m.setExtra(prefix+"load", float64(s.load))
		}
		if s.powerValid {
			m.setExtra(prefix+"power", s.watts)
		}
		if s.memoryValid && s.total > 0 {
			m.setExtra(prefix+"memory.used", s.used)
			m.setExtra(prefix+"memory.total", s.total)
			m.setExtra(prefix+"memory.load", 100*s.used/s.total)
		}
	}
	aggregateIndexedMetrics(m, map[string]int{"gpu": g.deviceCount})
	if v, ok := readMetric(*m, "gpu.temperature"); ok {
		m.GPUTemp, m.GPUTempValid = v, true
	}
	if v, ok := readMetric(*m, "gpu.load"); ok {
		m.GPULoad, m.GPULoadValid = int(v+0.5), true
	}
	if v, ok := readMetric(*m, "gpu.power"); ok {
		m.GPUPower, m.GPUPowerValid = v, true
	}
}
