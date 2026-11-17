//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"syscall"
	"unsafe"
)

// Windows MEMORYSTATUSEX uses 8-byte aligned 64-bit integers.
type memoryStatusEx struct {
	Length                   uint32
	Load                     uint32
	TotalPhysical            uint64
	AvailablePhysical        uint64
	TotalPageFile            uint64
	AvailablePageFile        uint64
	TotalVirtual             uint64
	AvailableVirtual         uint64
	AvailableExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// GiB is deliberately the base unit: typical 32-128 GiB machines still
// fit the PA120's three numeric digits. The display unit can be configured.
func readSystemMemory() (usedGiB, totalGiB, usage float64, valid bool) {
	state := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&state)))
	if ok == 0 || state.TotalPhysical == 0 || state.AvailablePhysical > state.TotalPhysical {
		return
	}
	const gib = 1024 * 1024 * 1024
	used := state.TotalPhysical - state.AvailablePhysical
	return float64(used) / gib, float64(state.TotalPhysical) / gib,
		100 * float64(used) / float64(state.TotalPhysical), true
}
