//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Linux native hidraw: report ID zero followed by the 64-byte USBLED packet.
// No libusb, hidapi or cgo dependency.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
	"unsafe"
)

type hidrawInfo struct {
	BusType uint32
	Vendor  uint16
	Product uint16
}

// _IOR('H', 0x03, struct hidraw_devinfo) on Linux.
const hidIOCGRAWINFO = uintptr(0x80084803)

func hidrawIdentity(path string) (bool, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false, err
	}
	defer syscall.Close(fd)
	var info hidrawInfo
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), hidIOCGRAWINFO, uintptr(unsafe.Pointer(&info)))
	if errno != 0 {
		return false, errno
	}
	return info.Vendor == 0x0416 && info.Product == 0x8001, nil
}

func enumerateHID() ([]string, error) {
	paths, err := filepath.Glob("/dev/hidraw*")
	if err != nil {
		return nil, err
	}
	found := make([]string, 0)
	for _, path := range paths {
		ok, e := hidrawIdentity(path)
		if e == nil && ok {
			found = append(found, path)
			continue
		}
		// A device node may deny read access to non-members of the udev
		// group. sysfs remains readable; report it to guide permissions.
		if errors.Is(e, syscall.EACCES) || errors.Is(e, syscall.EPERM) {
			name := filepath.Base(path)
			b, readErr := os.ReadFile(filepath.Join("/sys/class/hidraw", name, "device", "uevent"))
			if readErr == nil && isThermalrightUevent(string(b)) {
				found = append(found, path)
			}
		}
	}
	sort.Strings(found)
	return found, nil
}

func isThermalrightUevent(uevent string) bool {
	// HID_ID=0003:00000416:00008001 (USB bus type 3).
	for _, line := range splitLines(uevent) {
		var bus, vendor, product uint32
		if _, err := fmt.Sscanf(line, "HID_ID=%x:%x:%x", &bus, &vendor, &product); err == nil {
			return bus == 3 && vendor == 0x416 && product == 0x8001
		}
	}
	return false
}

type hidDevice struct{ fd int }

func openHID(path string) (*hidDevice, error) {
	ok, err := hidrawIdentity(path)
	if err != nil {
		return nil, fmt.Errorf("open HID %s: %w (check udev permissions)", path, err)
	}
	if !ok {
		return nil, fmt.Errorf("%s is not a 0416:8001 HID device", path)
	}
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open HID for output: %w (check udev permissions)", err)
	}
	return &hidDevice{fd: fd}, nil
}

func (d *hidDevice) Close() {
	if d != nil && d.fd >= 0 {
		syscall.Close(d.fd)
		d.fd = -1
	}
}

func (d *hidDevice) Send(payload []byte, timeout time.Duration) error {
	if len(payload) != 64 {
		return fmt.Errorf("HID payload must be 64 bytes, got %d", len(payload))
	}
	if d == nil || d.fd < 0 {
		return errors.New("HID closed")
	}
	var report [65]byte
	copy(report[1:], payload)
	deadline := time.Now().Add(timeout)
	for {
		n, err := syscall.Write(d.fd, report[:])
		if err == nil {
			if n != len(report) {
				return fmt.Errorf("short HID write: %d/65", n)
			}
			return nil
		}
		if err != syscall.EAGAIN && err != syscall.EINTR {
			return fmt.Errorf("hidraw write: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("HID write timed out after %s", timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (d *hidDevice) Receive(timeout time.Duration) ([]byte, error) {
	if d == nil || d.fd < 0 {
		return nil, errors.New("HID closed")
	}
	deadline := time.Now().Add(timeout)
	var report [65]byte
	for {
		n, err := syscall.Read(d.fd, report[:])
		if err == nil {
			switch n {
			case 65:
				if report[0] != 0 {
					return nil, fmt.Errorf("unexpected HID report ID %d", report[0])
				}
				return append([]byte(nil), report[1:]...), nil
			case 64:
				return append([]byte(nil), report[:64]...), nil
			default:
				return nil, fmt.Errorf("unexpected HID input size %d (want 64 or 65)", n)
			}
		}
		if err != syscall.EAGAIN && err != syscall.EINTR {
			return nil, fmt.Errorf("hidraw read: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("HID read timed out after %s", timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (d *hidDevice) SendFrame(packet []byte, timeout time.Duration) error {
	for offset := 0; offset < len(packet); offset += 64 {
		var chunk [64]byte
		copy(chunk[:], packet[offset:minInt(offset+64, len(packet))])
		if err := d.Send(chunk[:], timeout); err != nil {
			return err
		}
	}
	time.Sleep(30 * time.Millisecond)
	return nil
}
