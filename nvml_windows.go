//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// NVIDIA NVML is part of the installed GPU driver, NOT bundled by this EXE.
package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

type nvmlSensor struct {
	dll      *syscall.DLL
	device   uintptr
	shutdown *syscall.Proc
	getTemp  *syscall.Proc
	getLoad  *syscall.Proc
	getPower *syscall.Proc
}
type nvmlUtil struct{ GPU, Memory uint32 }

func openNVML() (*nvmlSensor, error) {
	dll, err := syscall.LoadDLL("nvml.dll")
	if err != nil {
		return nil, err
	}
	cleanup := func(e error) (*nvmlSensor, error) { dll.Release(); return nil, e }
	init, err := dll.FindProc("nvmlInit_v2")
	if err != nil {
		return cleanup(err)
	}
	shutdown, err := dll.FindProc("nvmlShutdown")
	if err != nil {
		return cleanup(err)
	}
	handle, err := dll.FindProc("nvmlDeviceGetHandleByIndex_v2")
	if err != nil {
		return cleanup(err)
	}
	temp, err := dll.FindProc("nvmlDeviceGetTemperature")
	if err != nil {
		return cleanup(err)
	}
	load, err := dll.FindProc("nvmlDeviceGetUtilizationRates")
	if err != nil {
		return cleanup(err)
	}
	power, err := dll.FindProc("nvmlDeviceGetPowerUsage")
	if err != nil {
		return cleanup(err)
	}
	ok, _, _ := init.Call()
	if ok != 0 {
		return cleanup(fmt.Errorf("NVML init returned %d", ok))
	}
	var device uintptr
	ok, _, _ = handle.Call(0, uintptr(unsafe.Pointer(&device)))
	if ok != 0 || device == 0 {
		shutdown.Call()
		return cleanup(fmt.Errorf("NVML GPU[0] unavailable: code %d", ok))
	}
	return &nvmlSensor{dll: dll, device: device, shutdown: shutdown, getTemp: temp, getLoad: load, getPower: power}, nil
}
func (n *nvmlSensor) Read() (temp float64, tempValid bool, load int, loadValid bool) {
	if n == nil {
		return
	}
	var t uint32
	ok, _, _ := n.getTemp.Call(n.device, 0, uintptr(unsafe.Pointer(&t)))
	if ok == 0 && t > 0 && t < 130 {
		temp = float64(t)
		tempValid = true
	}
	var util nvmlUtil
	ok, _, _ = n.getLoad.Call(n.device, uintptr(unsafe.Pointer(&util)))
	if ok == 0 && util.GPU <= 100 {
		load = int(util.GPU)
		loadValid = true
	}
	return
}
func (n *nvmlSensor) ReadPower() (float64, bool) {
	if n == nil {
		return 0, false
	}
	var milliwatts uint32
	code, _, _ := n.getPower.Call(n.device, uintptr(unsafe.Pointer(&milliwatts)))
	if code != 0 || milliwatts > 1000000 {
		return 0, false
	}
	return float64(milliwatts) / 1000, true
}
func (n *nvmlSensor) Close() {
	if n != nil && n.dll != nil {
		n.shutdown.Call()
		n.dll.Release()
		n.dll = nil
	}
}
