// SPDX-License-Identifier: GPL-3.0-or-later
// OS boundaries. No platform-dependent types escape into the render engine.
package main

import "time"

// HID transport is the sole hardware boundary used by the protocol loop.
// Each OS supplies discovery and openHID(), but never its own renderer.
type hidTransport interface {
	Send([]byte, time.Duration) error
	Receive(time.Duration) ([]byte, error)
	SendFrame([]byte, time.Duration) error
	Close()
}

type cpuTelemetry interface {
	ReadTemp(source string) (float64, error)
	ReadPower() (float64, bool, error)
	Close()
}

type gpuTelemetry interface {
	Read() (temp float64, tempValid bool, load int, loadValid bool)
	ReadPower() (float64, bool)
	ReadMemory() (usedGiB, totalGiB, percent float64, valid bool)
	Close()
}

// Optional multi-GPU samples; legacy providers can remain unindexed until
// their platform-specific device enumeration is implemented.
type indexedGPU interface {
	SampleIndexed(*metrics)
}

// Discovery metadata is separate from actual sensor validity. In particular,
// an Intel/AMD GPU with no active Windows provider still occupies its index.
type hardwareDevice struct {
	Kind        string `json:"kind"`
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Vendor      string `json:"vendor"`
	Bus         string `json:"bus,omitempty"`
	PNP         string `json:"pnp,omitempty"`
	Driver      string `json:"driver"`
	PCIValid    bool   `json:"-"`
	PCIBus      uint32 `json:"-"`
	PCIDevice   uint32 `json:"-"`
	PCIFunction uint32 `json:"-"`
	PCIVendor   uint16 `json:"-"`
}
