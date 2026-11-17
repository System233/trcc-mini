//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	dllHID      = syscall.NewLazyDLL("hid.dll")
	pHidGuid    = dllHID.NewProc("HidD_GetHidGuid")
	dllSetup    = syscall.NewLazyDLL("setupapi.dll")
	pGetDevices = dllSetup.NewProc("SetupDiGetClassDevsW")
	pEnum       = dllSetup.NewProc("SetupDiEnumDeviceInterfaces")
	pDetail     = dllSetup.NewProc("SetupDiGetDeviceInterfaceDetailW")
	pDestroy    = dllSetup.NewProc("SetupDiDestroyDeviceInfoList")
	dllKernel   = syscall.NewLazyDLL("kernel32.dll")
	pEvent      = dllKernel.NewProc("CreateEventW")
	pWait       = dllKernel.NewProc("WaitForSingleObject")
	pWrite      = dllKernel.NewProc("WriteFile")
	pRead       = dllKernel.NewProc("ReadFile")
	pCancel     = dllKernel.NewProc("CancelIoEx")
	pResult     = dllKernel.NewProc("GetOverlappedResult")
)

type hidGuid struct {
	A uint32
	B uint16
	C uint16
	D [8]byte
}
type hidInterface struct {
	Size     uint32
	Guid     hidGuid
	Flags    uint32
	Reserved uintptr
}

func winError(err error) error {
	if err != nil && err != syscall.Errno(0) {
		return err
	}
	return syscall.EINVAL
}
func enumerateHID() ([]string, error) {
	var guid hidGuid
	pHidGuid.Call(uintptr(unsafe.Pointer(&guid)))
	const flags = 0x00000002 | 0x00000010 // DIGCF_PRESENT | DIGCF_DEVICEINTERFACE
	h, _, e := pGetDevices.Call(uintptr(unsafe.Pointer(&guid)), 0, 0, flags)
	if h == ^uintptr(0) {
		return nil, fmt.Errorf("SetupDiGetClassDevs: %w", winError(e))
	}
	defer pDestroy.Call(h)
	paths := make([]string, 0, 2)
	for i := 0; i < 128; i++ {
		inf := hidInterface{Size: uint32(unsafe.Sizeof(hidInterface{})), Guid: guid}
		ok, _, err := pEnum.Call(h, 0, uintptr(unsafe.Pointer(&guid)), uintptr(i), uintptr(unsafe.Pointer(&inf)))
		if ok == 0 {
			if err == syscall.Errno(259) {
				break
			} // ERROR_NO_MORE_ITEMS
			return paths, fmt.Errorf("SetupDiEnumDeviceInterfaces[%d]: %w", i, winError(err))
		}
		var required uint32
		pDetail.Call(h, uintptr(unsafe.Pointer(&inf)), 0, 0, uintptr(unsafe.Pointer(&required)), 0)
		if required < 8 || required > 65536 {
			return paths, fmt.Errorf("invalid HID interface detail size %d", required)
		}
		raw := make([]byte, required+2)
		binary.LittleEndian.PutUint32(raw[:4], 8) // SP_DEVICE_INTERFACE_DETAIL_DATA_W x64
		ok, _, err = pDetail.Call(h, uintptr(unsafe.Pointer(&inf)), uintptr(unsafe.Pointer(&raw[0])), uintptr(required), uintptr(unsafe.Pointer(&required)), 0)
		if ok == 0 {
			return paths, fmt.Errorf("SetupDiGetDeviceInterfaceDetail: %w", winError(err))
		}
		units := make([]uint16, 0, 200)
		for j := 4; j+1 < len(raw); j += 2 {
			u := binary.LittleEndian.Uint16(raw[j : j+2])
			if u == 0 {
				break
			}
			units = append(units, u)
		}
		path := syscall.UTF16ToString(units)
		if strings.Contains(strings.ToLower(path), "vid_0416&pid_8001") {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

type hidDevice struct{ handle syscall.Handle }

func openHID(path string) (*hidDevice, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	const overlapped = 0x40000000
	h, err := syscall.CreateFile(ptr, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, overlapped, 0)
	if err != nil {
		return nil, err
	}
	return &hidDevice{handle: h}, nil
}
func (d *hidDevice) Close() {
	if d != nil && d.handle != 0 {
		syscall.CloseHandle(d.handle)
		d.handle = 0
	}
}

// Windows HID report IDs prepend a byte: the 64-byte protocol from upstream
// is sent as [ReportID=0][64 payload bytes]. Writes are cancellable overlapped I/O.
func (d *hidDevice) io(isWrite bool, report []byte, timeout time.Duration) error {
	if len(report) != 65 {
		return fmt.Errorf("HID report expected 65 bytes, got %d", len(report))
	}
	e, _, err := pEvent.Call(0, 1, 0, 0)
	if e == 0 {
		return fmt.Errorf("CreateEvent: %w", winError(err))
	}
	defer syscall.CloseHandle(syscall.Handle(e))
	ol := syscall.Overlapped{HEvent: syscall.Handle(e)}
	var done uint32
	proc := pRead
	if isWrite {
		proc = pWrite
	}
	ok, _, callErr := proc.Call(uintptr(d.handle), uintptr(unsafe.Pointer(&report[0])), uintptr(len(report)), uintptr(unsafe.Pointer(&done)), uintptr(unsafe.Pointer(&ol)))
	if ok == 0 && callErr != syscall.Errno(997) {
		return fmt.Errorf("HID async I/O: %w", winError(callErr))
	}
	if ok == 0 {
		ms := uintptr(timeout.Milliseconds())
		if ms == 0 {
			ms = 1
		}
		status, _, waitErr := pWait.Call(e, ms)
		switch status {
		case 0: // signaled
		case 258: // WAIT_TIMEOUT
			pCancel.Call(uintptr(d.handle), uintptr(unsafe.Pointer(&ol)))
			// Wait for cancellation completion so OVERLAPPED/buffer stay valid.
			pWait.Call(e, 0xffffffff)
			return fmt.Errorf("HID I/O timed out after %s", timeout)
		default:
			pCancel.Call(uintptr(d.handle), uintptr(unsafe.Pointer(&ol)))
			pWait.Call(e, 0xffffffff)
			return fmt.Errorf("HID wait: %w", winError(waitErr))
		}
		ok, _, callErr = pResult.Call(uintptr(d.handle), uintptr(unsafe.Pointer(&ol)), uintptr(unsafe.Pointer(&done)), 0)
		if ok == 0 {
			return fmt.Errorf("GetOverlappedResult: %w", winError(callErr))
		}
	}
	if done != 65 {
		return fmt.Errorf("short HID transfer: %d/65 bytes", done)
	}
	return nil
}
func (d *hidDevice) Send(payload []byte, timeout time.Duration) error {
	if len(payload) != 64 {
		return fmt.Errorf("payload %d !=64", len(payload))
	}
	report := make([]byte, 65)
	copy(report[1:], payload)
	return d.io(true, report, timeout)
}
func (d *hidDevice) Receive(timeout time.Duration) ([]byte, error) {
	report := make([]byte, 65)
	if err := d.io(false, report, timeout); err != nil {
		return nil, err
	}
	return report[1:], nil
}
func (d *hidDevice) SendFrame(packet []byte, timeout time.Duration) error {
	for offset := 0; offset < len(packet); offset += 64 {
		var payload [64]byte
		copy(payload[:], packet[offset:minInt(len(packet), offset+64)])
		if err := d.Send(payload[:], timeout); err != nil {
			return err
		}
	}
	time.Sleep(30 * time.Millisecond) // upstream firmware cooldown between frames
	return nil
}
