//go:build darwin

// SPDX-License-Identifier: GPL-3.0-or-later
package main

func populatePlatformCPUMetrics(m *metrics, sensor cpuTelemetry) {
	if physicalCPUCount() != 1 || sensor == nil {
		return
	}
	if temperature, e := sensor.ReadTemp("tctl"); e == nil && temperature > 0 {
		m.setExtra("cpu0.temperature", temperature)
		m.setExtra("cpu0.temperature.max", temperature)
	}
}
