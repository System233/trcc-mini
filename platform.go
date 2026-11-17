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
