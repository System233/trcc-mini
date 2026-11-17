//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Native Windows SetupAPI display-class PCI enumeration. Device discovery
// is separate from per-vendor telemetry, bound using PCI coordinates.
package main

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

type nativeDeviceInfoData struct {
	Size      uint32
	ClassGUID syscall.GUID
	DevInst   uint32
	Reserved  uintptr
}

var winSetup = syscall.NewLazyDLL("setupapi.dll")
var winSetupClass = winSetup.NewProc("SetupDiGetClassDevsW")
var winSetupEnum = winSetup.NewProc("SetupDiEnumDeviceInfo")
var winSetupInstance = winSetup.NewProc("SetupDiGetDeviceInstanceIdW")
var winSetupProperty = winSetup.NewProc("SetupDiGetDeviceRegistryPropertyW")
var winSetupDestroy = winSetup.NewProc("SetupDiDestroyDeviceInfoList")

func winSPDRPString(set uintptr, info *nativeDeviceInfoData, property uintptr) string {
	var buffer [512]uint16
	ok, _, _ := winSetupProperty.Call(set, uintptr(unsafe.Pointer(info)), property, 0,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)*2), 0)
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buffer[:])
}
func winSPDRPDWord(set uintptr, info *nativeDeviceInfoData, property uintptr) (uint32, bool) {
	var value uint32
	ok, _, _ := winSetupProperty.Call(set, uintptr(unsafe.Pointer(info)), property, 0,
		uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Sizeof(value)), 0)
	return value, ok != 0
}
func parsePCIVendor(pnp string) uint16 {
	s := strings.ToUpper(pnp)
	idx := strings.Index(s, "PCI\\VEN_")
	if idx < 0 || len(s) < idx+12 {
		return 0
	}
	var vendor uint16
	if _, e := fmt.Sscanf(s[idx+8:idx+12], "%04X", &vendor); e != nil {
		return 0
	}
	return vendor
}
func discoverWindowsGPUs() ([]hardwareDevice, error) {
	// GUID_DEVCLASS_DISPLAY: {4d36e968-e325-11ce-bfc1-08002be10318}
	guid := syscall.GUID{Data1: 0x4d36e968, Data2: 0xe325, Data3: 0x11ce, Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18}}
	set, _, errno := winSetupClass.Call(uintptr(unsafe.Pointer(&guid)), 0, 0, 0x2) // DIGCF_PRESENT
	if set == ^uintptr(0) {
		return nil, fmt.Errorf("SetupDiGetClassDevsW(display): %v", errno)
	}
	defer winSetupDestroy.Call(set)
	devices := []hardwareDevice{}
	for i := uint32(0); i < 4096; i++ {
		info := nativeDeviceInfoData{Size: uint32(unsafe.Sizeof(nativeDeviceInfoData{}))}
		ok, _, _ := winSetupEnum.Call(set, uintptr(i), uintptr(unsafe.Pointer(&info)))
		if ok == 0 {
			break
		}
		var pnp [512]uint16
		success, _, _ := winSetupInstance.Call(set, uintptr(unsafe.Pointer(&info)),
			uintptr(unsafe.Pointer(&pnp[0])), uintptr(len(pnp)), 0)
		if success == 0 {
			continue
		}
		id := syscall.UTF16ToString(pnp[:])
		vendor := parsePCIVendor(id)
		if vendor != 0x10de && vendor != 0x1002 && vendor != 0x8086 {
			continue
		}
		label := winSPDRPString(set, &info, 0x0c) // FRIENDLYNAME
		if label == "" {
			label = winSPDRPString(set, &info, 0x00)
		} // DEVICEDESC
		if label == "" {
			label = id
		}
		name := "Unknown"
		driver := "unavailable"
		switch vendor {
		case 0x10de:
			name = "NVIDIA"
			driver = "NVML"
		case 0x1002:
			name = "AMD"
		case 0x8086:
			name = "Intel"
		}
		bus, bok := winSPDRPDWord(set, &info, 0x15)     // SPDRP_BUSNUMBER
		address, aok := winSPDRPDWord(set, &info, 0x1c) // SPDRP_ADDRESS
		// PCI SPDRP_ADDRESS: high word device, low word function.
		gpu := hardwareDevice{
			Kind: "gpu", Name: label, Vendor: name, PNP: id, Driver: driver,
			PCIValid: bok && aok, PCIBus: bus, PCIDevice: address >> 16, PCIFunction: address & 0xffff,
			PCIVendor: vendor,
		}
		if gpu.PCIValid {
			gpu.Bus = fmt.Sprintf("%02x:%02x.%x", gpu.PCIBus, gpu.PCIDevice, gpu.PCIFunction)
			if gpu.PCIBus > 255 || gpu.PCIDevice > 31 || gpu.PCIFunction > 7 {
				gpu.PCIValid = false
				gpu.Bus = ""
			}
		}
		devices = append(devices, gpu)
	}
	sort.Slice(devices, func(i, j int) bool {
		a, b := devices[i], devices[j]
		if a.PCIValid && b.PCIValid {
			if a.PCIBus != b.PCIBus {
				return a.PCIBus < b.PCIBus
			}
			if a.PCIDevice != b.PCIDevice {
				return a.PCIDevice < b.PCIDevice
			}
			if a.PCIFunction != b.PCIFunction {
				return a.PCIFunction < b.PCIFunction
			}
		}
		if a.PCIValid != b.PCIValid {
			return a.PCIValid
		}
		return a.PNP < b.PNP
	})
	for i := range devices {
		devices[i].Index = i
	}
	return devices, nil
}
