// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "strconv"

// The current low-level CPU temperature and MSR readers do not target a
// physical socket. Only assign them to cpu0 if the OS confirms there is
// exactly one socket; a multi-socket value must never be mislabeled cpu0.
func populateCPUIndexed(m *metrics) {
	count := physicalCPUCount()
	if count == 1 {
		if m.TempValid && !m.Extras["cpu0.temperature"].Valid {
			m.setExtra("cpu0.temperature", m.Temp)
		}
		m.setExtra("cpu0.load", float64(m.Load))
		if m.CPUPowerValid {
			m.setExtra("cpu0.power", m.CPUPower)
		}
	}
	if count > 0 {
		// Preserve the OS-weighted overall CPU load even on multi-socket
		// machines. Individual package loads require their own counters.
		aggregateIndexedMetrics(m, map[string]int{"cpu": count})
		// Preserve the existing explicit cpuTemperature selector for
		// legacy cpu.temperature, while cpu0.temperature always means
		// verified package Tctl and CCDs have their own metric names.
		if m.TempValid {
			delete(m.Extras, "cpu.temperature")
		}
		if count > 1 {
			delete(m.Extras, "cpu.load")
		}
	}
}

// Indices are decimal and zero-based, stable until the hardware topology
// changes; there is no vendor-specific namespace for individual CPU/GPU IDs.
func indexedMetricName(kind string, index int, suffix string) string {
	return kind + strconv.Itoa(index) + "." + suffix
}
