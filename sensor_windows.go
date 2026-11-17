//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

var pSystemTimes = dllKernel.NewProc("GetSystemTimes")
var pIoctl = dllKernel.NewProc("DeviceIoControl")
var pCreateMutex = dllKernel.NewProc("CreateMutexW")
var pReleaseMutex = dllKernel.NewProc("ReleaseMutex")

type fileTime struct {
	Low  uint32
	High uint32
}

func (f fileTime) Uint64() uint64 { return uint64(f.High)<<32 | uint64(f.Low) }

type cpuLoadSampler struct {
	idle  uint64
	total uint64
	valid bool
	last  int
}

func (s *cpuLoadSampler) Sample() int {
	var idle, kernel, user fileTime
	ok, _, _ := pSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if ok == 0 {
		return s.last
	}
	i := idle.Uint64()
	t := kernel.Uint64() + user.Uint64()
	if s.valid && t > s.total && i >= s.idle {
		delta := t - s.total
		busy := int((100*(delta-(i-s.idle)) + delta/2) / delta)
		if busy < 0 {
			busy = 0
		}
		if busy > 100 {
			busy = 100
		}
		s.last = busy
	}
	s.idle = i
	s.total = t
	s.valid = true
	return s.last
}

//go:embed assets/AMDFamily17.bin
var amdSignedModule []byte

type pawnSensor struct {
	handle   syscall.Handle
	pciMutex syscall.Handle
	lastTemp float64
	haveLast bool
}

func openPawn() (*pawnSensor, error) {
	name, err := syscall.UTF16PtrFromString(`\\?\GLOBALROOT\Device\PawnIO`)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("PawnIO driver missing or access denied (%w); install official signed driver once and run elevated", err)
	}
	p := &pawnSensor{handle: h}
	if _, err = p.call(0xA1B22084, amdSignedModule, 0); err != nil {
		p.Close()
		return nil, fmt.Errorf("PawnIO AMDFamily17 signed module load: %w", err)
	}
	mutexName, _ := syscall.UTF16PtrFromString(`Global\Access_PCI`)
	mh, _, e := pCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(mutexName)))
	if mh == 0 {
		p.Close()
		return nil, fmt.Errorf("CreateMutex Access_PCI: %w", winError(e))
	}
	p.pciMutex = syscall.Handle(mh)
	return p, nil
}
func (p *pawnSensor) Close() {
	if p != nil && p.pciMutex != 0 {
		syscall.CloseHandle(p.pciMutex)
		p.pciMutex = 0
	}
	if p != nil && p.handle != 0 {
		syscall.CloseHandle(p.handle)
		p.handle = 0
	}
}
func (p *pawnSensor) call(code uint32, input []byte, outputBytes int) ([]byte, error) {
	out := make([]byte, outputBytes)
	var inPtr, outPtr uintptr
	if len(input) > 0 {
		inPtr = uintptr(unsafe.Pointer(&input[0]))
	}
	if len(out) > 0 {
		outPtr = uintptr(unsafe.Pointer(&out[0]))
	}
	var n uint32
	ok, _, e := pIoctl.Call(uintptr(p.handle), uintptr(code), inPtr, uintptr(len(input)), outPtr, uintptr(len(out)), uintptr(unsafe.Pointer(&n)), 0)
	if ok == 0 {
		return nil, winError(e)
	}
	if int(n) != outputBytes {
		return nil, fmt.Errorf("PawnIO IOCTL response: got %d expected %d", n, outputBytes)
	}
	return out, nil
}
func (p *pawnSensor) Read() (float64, error) {
	// CPU Tctl is an indexed AMD SMN read: serialize against HWiNFO,
	// LibreHardwareMonitor and other consumers using the same PCI mutex.
	// Do not block the display loop if a different monitor is busy.
	status, _, _ := pWait.Call(uintptr(p.pciMutex), 10)
	if status != 0 && status != 128 {
		if p.haveLast {
			return p.lastTemp, nil
		}
		return 0, fmt.Errorf("PCI bus busy (mutex timeout)")
	}
	defer pReleaseMutex.Call(uintptr(p.pciMutex))
	var b [40]byte
	copy(b[:32], "ioctl_read_smn")
	binary.LittleEndian.PutUint64(b[32:], 0x00059800)
	raw, err := p.call(0xA1B22104, b[:], 8)
	if err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint64(raw)
	t := float64(v>>21) * 0.125
	if v&0x80000 != 0 {
		t -= 49
	}
	if t < 1 || t > 130 {
		return 0, fmt.Errorf("invalid AMD Tctl %.2f°C (raw 0x%x)", t, v)
	}
	p.lastTemp = t
	p.haveLast = true
	return t, nil
}
