//go:build darwin

// SPDX-License-Identifier: GPL-3.0-or-later
package main

func discoverHardware() ([]hardwareDevice, error) {
	var found []hardwareDevice
	for i := 0; i < physicalCPUCount(); i++ {
		found = append(found, hardwareDevice{Kind: "cpu", Index: i, Name: "CPU package", Driver: "sysctl/powermetrics"})
	}
	// The OS may expose multiple Intel/AMD/Apple GPUs. Do not advertise a
	// guessed gpu0 identity until IOKit PCI/AGX enumeration is implemented.
	return found, nil
}
