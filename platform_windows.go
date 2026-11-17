//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "os"

func serviceSignals() []os.Signal { return []os.Signal{os.Interrupt} }

// Return a nil interface on failure; wrapping a typed nil pointer into an
// interface would otherwise crash the sampling loop on missing PawnIO/NVML.
func openCPUSensor() (cpuTelemetry, error) {
	if windowsCPUVendor() == "GenuineIntel" {
		return openIntelPawn()
	}
	p, err := openPawn()
	if err != nil {
		return nil, err
	}
	return p, nil
}
func openGPUSensor() (gpuTelemetry, error) {
	g, err := openNVML()
	if err != nil {
		return nil, err
	}
	return g, nil
}
