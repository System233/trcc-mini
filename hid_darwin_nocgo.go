//go:build darwin && !cgo

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"time"
)

var macHIDUnavailable = errors.New("macOS HID requires a native build with CGO_ENABLED=1 and Xcode Command Line Tools (IOKit)")

type hidDevice struct{}

func enumerateHID() ([]string, error)                    { return nil, macHIDUnavailable }
func openHID(string) (*hidDevice, error)                 { return nil, macHIDUnavailable }
func (*hidDevice) Close()                                {}
func (*hidDevice) Send([]byte, time.Duration) error      { return macHIDUnavailable }
func (*hidDevice) Receive(time.Duration) ([]byte, error) { return nil, macHIDUnavailable }
func (*hidDevice) SendFrame([]byte, time.Duration) error { return macHIDUnavailable }
