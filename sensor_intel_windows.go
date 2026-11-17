//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Intel CPU package sensors via the signed PawnIO IntelMSR module.
// Read-only IA32_PACKAGE_THERM_STATUS and RAPL energy registers.
// No Intel Power Gadget (discontinued / Intel-SA-01037) dependency.
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"math"
	"syscall"
	"time"
)

const (
	intelMSRTempTarget    = 0x1a2
	intelMSRPackageTherm  = 0x1b1
	intelMSRRAPLUnits     = 0x606
	intelMSRPackageEnergy = 0x611
)

//go:embed assets/IntelMSR.bin
var intelSignedModule []byte

type intelPawnSensor struct {
	pawn       *pawnSensor
	energyUnit float64
	lastEnergy uint32
	lastAt     time.Time
	lastWatts  float64
	powerReady bool
	powerError error
}

func openIntelPawn() (cpuTelemetry, error) {
	name, e := syscall.UTF16PtrFromString(`\\?\GLOBALROOT\Device\PawnIO`)
	if e != nil {
		return nil, e
	}
	h, e := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if e != nil {
		return nil, fmt.Errorf("PawnIO driver missing or access denied: %w", e)
	}
	pawn := &pawnSensor{handle: h}
	if _, e = pawn.call(0xA1B22084, intelSignedModule, 0); e != nil {
		pawn.Close()
		return nil, fmt.Errorf("PawnIO IntelMSR module load failed: %w", e)
	}
	s := &intelPawnSensor{pawn: pawn}
	units, e := pawn.readMSR(intelMSRRAPLUnits)
	if e != nil {
		s.powerError = fmt.Errorf("Intel RAPL units: %w", e)
		return s, nil
	}
	energyBits := int((units >> 8) & 0x1f)
	s.energyUnit = math.Ldexp(1, -energyBits)
	_, _, _ = s.ReadPower() // prime; transient failure retries on next sample
	return s, nil
}

func (s *intelPawnSensor) Close() {
	if s != nil && s.pawn != nil {
		s.pawn.Close()
		s.pawn = nil
	}
}

// Package digital sensor: bit31 is reading-valid, bits22:16 contain
// degrees below TjMax. 0x1A2 bits23:16 supply the actual TjMax.
func intelPackageTemperature(target, status uint64) (float64, bool) {
	if status&(1<<31) == 0 {
		return 0, false
	}
	tjMax := int((target >> 16) & 0xff)
	distance := int((status >> 16) & 0x7f)
	temp := tjMax - distance
	if tjMax < 50 || tjMax > 125 || temp < 1 || temp > 125 {
		return 0, false
	}
	return float64(temp), true
}
func (s *intelPawnSensor) ReadTemp(source string) (float64, error) {
	if source != "tctl" {
		return 0, fmt.Errorf("Intel CPU does not expose AMD-specific temperature source %q; select tctl", source)
	}
	if s == nil || s.pawn == nil {
		return 0, errors.New("Intel PawnIO sensor closed")
	}
	target, e := s.pawn.readMSR(intelMSRTempTarget)
	if e != nil {
		return 0, fmt.Errorf("Intel IA32_TEMPERATURE_TARGET: %w", e)
	}
	status, e := s.pawn.readMSR(intelMSRPackageTherm)
	if e != nil {
		return 0, fmt.Errorf("Intel IA32_PACKAGE_THERM_STATUS: %w", e)
	}
	temperature, ok := intelPackageTemperature(target, status)
	if !ok {
		return 0, errors.New("Intel package temperature sensor invalid or unsupported")
	}
	return temperature, nil
}
func (s *intelPawnSensor) ReadPower() (float64, bool, error) {
	if s == nil || s.pawn == nil {
		return 0, false, errors.New("Intel PawnIO sensor closed")
	}
	if s.powerError != nil {
		return 0, false, s.powerError
	}
	counter, e := s.pawn.readMSR(intelMSRPackageEnergy)
	if e != nil {
		return 0, false, fmt.Errorf("Intel PKG_ENERGY_STATUS: %w", e)
	}
	now := time.Now()
	energy := uint32(counter)
	if s.lastAt.IsZero() {
		s.lastAt, s.lastEnergy = now, energy
		return 0, false, nil
	}
	watts, ok := energyWatts(s.lastEnergy, energy, s.energyUnit, now.Sub(s.lastAt))
	s.lastEnergy, s.lastAt = energy, now
	if !ok {
		return 0, false, nil
	}
	s.lastWatts, s.powerReady = watts, true
	return watts, true, nil
}
