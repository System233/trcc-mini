//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var pSystemTimes = dllKernel.NewProc("GetSystemTimes")
var pIoctl = dllKernel.NewProc("DeviceIoControl")
var pCreateMutex = dllKernel.NewProc("CreateMutexW")
var pReleaseMutex = dllKernel.NewProc("ReleaseMutex")

var errPCIMutexTimeout = errors.New("PCI bus busy (mutex timeout)")

const displayPCIMutexWait = 10 // milliseconds; never block the HID frame loop

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
	handle       syscall.Handle
	pciMutex     syscall.Handle
	pciMutexWait uint32
	lastTemp     float64
	haveLast     bool
	lastCCDTemp  map[string]float64
	energyUnit   float64
	lastEnergy   uint32
	lastEnergyAt time.Time
	lastWatts    float64
	haveWatts    bool
	powerInitErr error
}

// Diagnostics can afford to wait for another sampler; the live display cannot.
func (p *pawnSensor) SetPCILockTimeout(timeout time.Duration) {
	if timeout > 0 && timeout <= time.Second {
		p.pciMutexWait = uint32(timeout / time.Millisecond)
	}
}

// Windows mutex ownership is tied to an OS THREAD, not a goroutine. Keep
// WaitForSingleObject, the indexed SMN operations and ReleaseMutex on one
// thread; otherwise Go can migrate the goroutine and permanently strand the
// cross-process mutex until the owning thread exits.
func (p *pawnSensor) withPCILock(read func() error) (err error) {
	if p == nil || p.pciMutex == 0 {
		return errors.New("PCI mutex unavailable")
	}
	wait := p.pciMutexWait
	if wait == 0 {
		wait = displayPCIMutexWait
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	status, _, waitErr := pWait.Call(uintptr(p.pciMutex), uintptr(wait))
	switch uint32(status) {
	case 0, 0x80: // WAIT_OBJECT_0, WAIT_ABANDONED: both grant ownership
	case 0x102: // WAIT_TIMEOUT
		return errPCIMutexTimeout
	case 0xffffffff: // WAIT_FAILED
		return fmt.Errorf("WaitForSingleObject(Access_PCI): %w", winError(waitErr))
	default:
		return fmt.Errorf("WaitForSingleObject(Access_PCI): unexpected status %#x", status)
	}
	defer func() {
		if ok, _, releaseErr := pReleaseMutex.Call(uintptr(p.pciMutex)); ok == 0 {
			err = errors.Join(err, fmt.Errorf("ReleaseMutex(Access_PCI): %w", winError(releaseErr)))
		}
	}()
	return read()
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
	// MSR_PWR_UNIT [12:8] gives the joules represented by one
	// package-energy counter increment. An initial energy sample primes
	// the delta counter and must not be mistaken for zero-watt power.
	unit, err := p.readMSR(0xC0010299)
	if err != nil {
		// Power reading is optional: failure must not disable CPU Tctl.
		p.powerInitErr = fmt.Errorf("AMD package energy unit MSR: %w", err)
		return p, nil
	}
	bits := (unit >> 8) & 0x1f
	p.energyUnit = math.Ldexp(1, -int(bits))
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
	var v uint64
	err := p.withPCILock(func() (err error) {
		v, err = p.readSMNLocked(0x00059800)
		return err
	})
	if err != nil {
		if errors.Is(err, errPCIMutexTimeout) && p.haveLast {
			return p.lastTemp, nil
		}
		return 0, err
	}
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

// The register at 0x59B08 + 4*n is the per-CCD Tdie sensor on Zen 4/5
// desktop processors (including Ryzen 9950X, CPUID model 0x44).
// These readings are not synonymous with an ASUS motherboard CPU Diode sensor.
func ccdTemperature(raw uint64) (float64, bool) {
	count := raw & 0xfff
	if count == 0 {
		return 0, false // unpopulated CCD
	}
	temp := float64(count)*0.125 - 305
	if temp < 1 || temp > 125 {
		return 0, false
	}
	return temp, true
}

func validCPUTempSource(source string) bool {
	switch source {
	case "tctl", "ccd1", "ccd2", "ccd-max", "ccd-avg":
		return true
	}
	return false
}

func (p *pawnSensor) ReadTemp(source string) (float64, error) {
	if source == "tctl" {
		return p.Read()
	}
	if !validCPUTempSource(source) {
		return 0, fmt.Errorf("unknown CPU temperature source %q", source)
	}
	count := 8
	start := 0
	if source == "ccd1" || source == "ccd2" {
		count = 1
		if source == "ccd2" {
			start = 1
		}
	}
	var sum, max float64
	var valid int
	var firstErr error
	err := p.withPCILock(func() error {
		for i := start; i < start+count; i++ {
			raw, err := p.readSMNLocked(0x00059B08 + uint64(i)*4)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			value, ok := ccdTemperature(raw)
			if !ok {
				continue
			}
			valid++
			sum += value
			if value > max {
				max = value
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errPCIMutexTimeout) {
			if v, ok := p.lastCCDTemp[source]; ok {
				return v, nil
			}
		}
		return 0, err
	}
	if valid == 0 {
		if firstErr != nil {
			return 0, fmt.Errorf("CCD sensor read: %w", firstErr)
		}
		return 0, fmt.Errorf("CCD sensor %q unavailable", source)
	}
	value := max
	if source == "ccd-avg" {
		value = sum / float64(valid)
	}
	if source == "ccd1" || source == "ccd2" {
		value = sum
	}
	if p.lastCCDTemp == nil {
		p.lastCCDTemp = make(map[string]float64)
	}
	p.lastCCDTemp[source] = value
	return value, nil
}

// Call only while holding the PCI mutex. PawnIO performs an indexed SMN
// operation, so reads from concurrent utilities must be serialized.
func (p *pawnSensor) readSMNLocked(addr uint64) (uint64, error) {
	var b [40]byte
	copy(b[:32], "ioctl_read_smn")
	binary.LittleEndian.PutUint64(b[32:], addr)
	raw, err := p.call(0xA1B22104, b[:], 8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(raw), nil
}

func (p *pawnSensor) readMSR(msr uint64) (uint64, error) {
	var b [40]byte
	copy(b[:32], "ioctl_read_msr")
	binary.LittleEndian.PutUint64(b[32:], msr)
	raw, err := p.call(0xA1B22104, b[:], 8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(raw), nil
}

// energyWatts implements the upstream 32-bit package-energy counter
// subtraction, including rollover. The first reading is a baseline.
func energyWatts(prev, current uint32, unit float64, dt time.Duration) (float64, bool) {
	if dt < 10*time.Millisecond || unit <= 0 {
		return 0, false
	}
	watts := float64(current-prev) * unit / dt.Seconds()
	if math.IsNaN(watts) || math.IsInf(watts, 0) || watts < 0 || watts > 1000 {
		return 0, false
	}
	return watts, true
}

func (p *pawnSensor) ReadPower() (float64, bool, error) {
	if p.powerInitErr != nil {
		return 0, false, p.powerInitErr
	}
	value, err := p.readMSR(0xC001029B)
	if err != nil {
		return 0, false, err
	}
	now := time.Now()
	counter := uint32(value)
	if p.lastEnergyAt.IsZero() {
		p.lastEnergyAt = now
		p.lastEnergy = counter
		return 0, false, nil
	}
	watts, valid := energyWatts(p.lastEnergy, counter, p.energyUnit, now.Sub(p.lastEnergyAt))
	if valid {
		p.lastEnergyAt = now
		p.lastEnergy = counter
		p.lastWatts = watts
		p.haveWatts = true
		return watts, true, nil
	}
	if p.haveWatts {
		return p.lastWatts, true, nil
	}
	return 0, false, nil
}
