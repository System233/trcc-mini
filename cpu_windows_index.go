//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

var procCPUInformationEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetLogicalProcessorInformationEx")

func physicalCPUCount() int {
	const relationProcessorPackage = 3
	var needed uint32
	_, _, _ = procCPUInformationEx.Call(relationProcessorPackage, 0, uintptr(unsafe.Pointer(&needed)))
	if needed < 8 || needed > 8*1024*1024 {
		return 0
	}
	buffer := make([]byte, needed)
	ok, _, _ := procCPUInformationEx.Call(relationProcessorPackage, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return 0
	}
	count := 0
	for offset := 0; offset+8 <= int(needed); {
		rel := binary.LittleEndian.Uint32(buffer[offset:])
		size := binary.LittleEndian.Uint32(buffer[offset+4:])
		if size < 8 || offset+int(size) > int(needed) {
			return 0
		}
		if rel == relationProcessorPackage {
			count++
		}
		offset += int(size)
	}
	return count
}
