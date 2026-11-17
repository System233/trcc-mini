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
	for i := 0; i < cpus; i++ {
		found = append(found, hardwareDevice{Kind: "cpu", Index: i, Name: name, Vendor: vendor, Driver: "native/PawnIO"})
	}
	for _, d := range gpu {
		found = append(found, d)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no CPU or physical GPU identified")
	}
	return found, nil
}
