//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// CPU vendor detection uses a local Windows hardware registry value, not
// processor name substring matching or a remote utility.
package main

import (
	"strings"
	"syscall"
	"unsafe"
)

func windowsCPUVendor() string {
	return windowsCPURegistryValue("VendorIdentifier")
}

func windowsCPUName() string {
	return windowsCPURegistryValue("ProcessorNameString")
}

func windowsCPURegistryValue(valueName string) string {
	const hklm = syscall.Handle(0x80000002)
	const keyRead = 0x20019
	path, e := syscall.UTF16PtrFromString(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`)
	if e != nil {
		return ""
	}
	var handle syscall.Handle
	if e = syscall.RegOpenKeyEx(hklm, path, 0, keyRead, &handle); e != nil {
		return ""
	}
	defer syscall.RegCloseKey(handle)
	name, _ := syscall.UTF16PtrFromString(valueName)
	buf := make([]uint16, 128)
	size := uint32(len(buf) * 2)
	if e = syscall.RegQueryValueEx(handle, name, nil, nil, (*byte)(unsafe.Pointer(&buf[0])), &size); e != nil {
		return ""
	}
	return strings.TrimSpace(syscall.UTF16ToString(buf))
}
