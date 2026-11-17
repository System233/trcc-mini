//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"strings"
)

func discoverHardware() ([]hardwareDevice, error) {
	gpu, err := discoverWindowsGPUs()
	if err != nil {
		return nil, err
	}
	cpus := physicalCPUCount()
	name := strings.TrimSpace(windowsCPUName())
	if name == "" {
		name = "CPU package"
	}
	vendor := windowsCPUVendor()
	found := make([]hardwareDevice, 0, cpus+len(gpu))
	provider := "OS load / PawnIO (optional, package identity verified for single socket)"
	if cpus > 1 {
		provider = "OS load / MSR temperature and watts unbound (N/A)"
	}
	for i := 0; i < cpus; i++ {
		found = append(found, hardwareDevice{Kind: "cpu", Index: i, Name: name, Vendor: vendor, Driver: provider})
	}
	for _, d := range gpu {
		found = append(found, d)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no CPU or physical GPU identified")
	}
	return found, nil
}
