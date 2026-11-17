// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "strconv"

// Always expose actual unindexed readings. A driver that cannot select a
// particular socket must not label its reading cpu0/cpu1, but that does not
// make the unindexed temperature or power reading unavailable.
func populateCPUIndexed(m *metrics) {
	populateCPUIndexedWithCount(m, physicalCPUCount())
}

func populateCPUIndexedWithCount(m *metrics, count int) {
	if count == 1 {
		// Do not assign an arbitrary CCD temperature to package Tctl.
		// The indexed OS sampler supplies cpu0.load only after a valid
		// delta between two logical-processor counter snapshots.
		if m.CPUPowerValid {
			if value, alreadyValid := m.Extras["cpu0.power"]; !alreadyValid || !value.Valid {
				m.setExtra("cpu0.power", m.CPUPower)
			}
		}
	}
	if count > 0 {
		// If package-specific readings exist, they take precedence for the
		// matching aggregate. With none, keep whatever unindexed telemetry
		// the driver returned instead of turning valid data into N/A.
		aggregateIndexedMetrics(m, map[string]int{"cpu": count})
		// Legacy render layouts use CPUPower directly instead of readMetric.
		// Give them the same available aggregates as the configurable slots.
		if !m.TempValid {
			if temp, ok := m.Extras["cpu.temperature"]; ok && temp.Valid {
				m.Temp, m.TempValid = temp.Value, true
			}
		}
		if power, ok := m.Extras["cpu.power"]; ok && power.Valid {
			m.CPUPower, m.CPUPowerValid = power.Value, true
		}
	}
}

// Indices are decimal and zero-based, stable until the hardware topology
// changes; there is no vendor-specific namespace for individual CPU/GPU IDs.
func indexedMetricName(kind string, index int, suffix string) string {
	return kind + strconv.Itoa(index) + "." + suffix
}
