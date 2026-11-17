// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "strconv"

// The current low-level CPU temperature and MSR readers do not target a
// physical socket. Only assign them to cpu0 if the OS confirms there is
// exactly one socket; a multi-socket value must never be mislabeled cpu0.
func populateCPUIndexed(m *metrics) {
	count := physicalCPUCount()
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
		if count > 1 {
			// Current MSR/PawnIO sensor does not select the package on
			// multi-socket machines; its reading has unknown provenance.
			// Never present a random socket's watts or temperature as a
			// comprehensive total.
			m.TempValid = false
			m.CPUPowerValid = false
		}
		// The OS-weighted total cpu.load remains available on all socket
		// counts. Per-package load comes from actual per-thread deltas.
		aggregateIndexedMetrics(m, map[string]int{"cpu": count})
		// Unindexed temperature now reflects available physical package
		// Tctl values (maximum), not the selected AMD CCD legacy source.
	}
}

// Indices are decimal and zero-based, stable until the hardware topology
// changes; there is no vendor-specific namespace for individual CPU/GPU IDs.
func indexedMetricName(kind string, index int, suffix string) string {
	return kind + strconv.Itoa(index) + "." + suffix
}
