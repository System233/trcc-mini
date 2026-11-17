// SPDX-License-Identifier: GPL-3.0-or-later
// Digit/segment layouts are a source-level port of GPL-3.0 led_segment.py.
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type rgb struct{ R, G, B byte }
type metrics struct {
	Load          int
	Temp          float64
	TempValid     bool
	CPUPower      float64
	CPUPowerValid bool
	GPULoad       int
	GPUTemp       float64
	GPULoadValid  bool
	GPUTempValid  bool
	GPUPower      float64
	GPUPowerValid bool
}

func (m metrics) TempString() string {
	if !m.TempValid {
		return "N/A"
	}
	return fmt.Sprintf("%.1f°C", m.Temp)
}
func (m metrics) GPUTempString() string {
	if !m.GPUTempValid {
		return "N/A"
	}
	return fmt.Sprintf("%.1f°C", m.GPUTemp)
}
func (m metrics) GPULoadString() string {
	if !m.GPULoadValid {
		return "N/A"
	}
	return fmt.Sprintf("%d%%", m.GPULoad)
}
func parseColor(s string) (rgb, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return rgb{}, fmt.Errorf("invalid color %q: expected RRGGBB", s)
	}
	v, e := strconv.ParseUint(s, 16, 24)
	if e != nil {
		return rgb{}, e
	}
	return rgb{byte(v >> 16), byte(v >> 8), byte(v)}, nil
}
func thresholdColor(value float64) rgb {
	switch {
	case value < 30:
		return rgb{0, 255, 255}
	case value < 50:
		return rgb{0, 255, 0}
	case value < 70:
		return rgb{255, 255, 0}
	case value < 90:
		return rgb{255, 110, 0}
	default:
		return rgb{255, 0, 0}
	}
}
func supportsStyle(style int) bool {
	switch style {
	case 1, 2, 3, 5, 6, 7, 8, 13:
		return true
	}
	return false
}

// a,b,c,d,e,f,g = bit 0..6.
var digitBits = [10]byte{63, 6, 91, 79, 102, 109, 125, 7, 127, 111}

func mark(mask []bool, n int) {
	if n >= 0 && n < len(mask) {
		mask[n] = true
	}
}
func encodeDigit(mask []bool, base int, digit int) {
	if digit < 0 || digit > 9 {
		return
	}
	for seg := 0; seg < 7; seg++ {
		if digitBits[digit]&(1<<seg) != 0 {
			mark(mask, base+seg)
		}
	}
}
func encodeNum(mask []bool, value, count, start int) {
	if count < 1 {
		return
	}
	if value < 0 {
		value = 0
	}
	max := 1
	for i := 0; i < count; i++ {
		max *= 10
	}
	if value >= max {
		value = max - 1
	}
	str := fmt.Sprintf("%0*d", count, value)
	for i, ch := range str {
		if i < count-1 && str[i] == '0' && value < ipow10(count-i-1) {
			continue
		}
		encodeDigit(mask, start+i*7, int(ch-'0'))
	}
}
func ipow10(n int) int {
	v := 1
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
}
func encodePartial(mask []bool, value, start int, extra0, extra1 int) {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	if value == 100 {
		mark(mask, extra0)
		mark(mask, extra1)
		encodeDigit(mask, start, 0)
		encodeDigit(mask, start+7, 0)
		return
	}
	encodeNum(mask, value, 2, start)
}
func render(style int, m metrics, phase int) ([]bool, error) {
	return renderWithPower(style, m, phase, false, false)
}
func renderWithPower(style int, m metrics, phase int, cpuPower, gpuPower bool) ([]bool, error) {
	var out []bool
	switch style {
	case 1: // AX120: 3 digits, rotate CPU Tctl/CPU load/GPU Tctl/GPU load
		out = make([]bool, 30)
		mark(out, 0)
		mark(out, 1)
		switch phase % 4 {
		case 0:
			if m.TempValid {
				mark(out, 2)
				mark(out, 3)
				mark(out, 6)
				encodeNum(out, int(m.Temp), 3, 9)
			}
		case 1:
			mark(out, 2)
			mark(out, 3)
			mark(out, 8)
			encodeNum(out, m.Load, 3, 9)
		case 2:
			if m.GPUTempValid {
				mark(out, 4)
				mark(out, 5)
				mark(out, 6)
				encodeNum(out, int(m.GPUTemp), 3, 9)
			}
		case 3:
			if m.GPULoadValid {
				mark(out, 4)
				mark(out, 5)
				mark(out, 8)
				encodeNum(out, m.GPULoad, 3, 9)
			}
		}
	case 3: // AK120: 3 digit power, temp, 2 digit load, CPU/GPU rotation.
		out = make([]bool, 64)
		mark(out, 4)
		tval, valid, load := m.Temp, m.TempValid, m.Load
		if phase%2 == 1 {
			mark(out, 5)
			tval = m.GPUTemp
			valid = m.GPUTempValid
			load = m.GPULoad
		} else {
			mark(out, 0)
		}
		power, hasPower := m.CPUPower, m.CPUPowerValid
		if phase%2 == 1 {
			power, hasPower = m.GPUPower, m.GPUPowerValid
		}
		if hasPower {
			mark(out, 1)
			encodeNum(out, int(math.Round(power)), 3, 6)
		}
		if valid {
			mark(out, 2)
			encodeNum(out, int(tval), 3, 27)
		}
		if phase%2 == 0 || m.GPULoadValid {
			encodePartial(out, load, 48, 62, 63)
		}
	case 5, 6: // LF8 / LF12
		n := 93
		if style == 6 {
			n = 124
		}
		out = make([]bool, n)
		mark(out, 6)
		tval, valid, load := m.Temp, m.TempValid, m.Load
		if phase%2 == 1 {
			mark(out, 1)
			tval = m.GPUTemp
			valid = m.GPUTempValid
			load = m.GPULoad
		} else {
			mark(out, 0)
		}
		power, hasPower := m.CPUPower, m.CPUPowerValid
		if phase%2 == 1 {
			power, hasPower = m.GPUPower, m.GPUPowerValid
		}
		if hasPower {
			mark(out, 4)
			encodeNum(out, int(math.Round(power)), 3, 28)
		}
		if valid {
			mark(out, 2)
			encodeNum(out, int(tval), 3, 7)
		}
		if phase%2 == 0 || m.GPULoadValid {
			encodePartial(out, load, 77, 91, 92)
		}
		if style == 6 {
			for i := 93; i < 124; i++ {
				mark(out, i)
			}
		}
	case 7: // LF10, 13-segment temperature digits and LED decoration strip
		out = make([]bool, 116)
		mark(out, 0)
		if m.TempValid {
			mark(out, 1)
			encode13Number(out, int(m.Temp), 6)
		}
		if m.GPUTempValid {
			mark(out, 3)
			mark(out, 4)
			encode13Number(out, int(m.GPUTemp), 45)
		}
		for i := 84; i < 116; i++ {
			mark(out, i)
		}
	case 8: // CZ1, 2 digits and 4 diagnostic indicator lamps
		out = make([]bool, 18)
		switch phase % 4 {
		case 0:
			if m.TempValid {
				mark(out, 0)
				encodeNum(out, int(m.Temp), 2, 4)
			}
		case 1:
			mark(out, 2)
			encodeNum(out, m.Load, 2, 4)
		case 2:
			if m.GPUTempValid {
				mark(out, 1)
				encodeNum(out, int(m.GPUTemp), 2, 4)
			}
		case 3:
			if m.GPULoadValid {
				mark(out, 3)
				encodeNum(out, m.GPULoad, 2, 4)
			}
		}
	case 13: // Magic Qube: 2x seven-seg x3 LEDs per segment + indicator pairs + border
		out = make([]bool, 65)
		switch phase % 4 {
		case 0:
			if m.TempValid {
				mark(out, 42)
				mark(out, 43)
				encodeMagic(out, int(m.Temp))
			}
		case 1:
			mark(out, 48)
			mark(out, 49)
			encodeMagic(out, m.Load)
		case 2:
			if m.GPUTempValid {
				mark(out, 44)
				mark(out, 45)
				encodeMagic(out, int(m.GPUTemp))
			}
		case 3:
			if m.GPULoadValid {
				mark(out, 46)
				mark(out, 47)
				encodeMagic(out, m.GPULoad)
			}
		}
		for i := 50; i < 65; i++ {
			mark(out, i)
		}
	default:
		return nil, fmt.Errorf("unsupported segment layout %d", style)
	}
	return out, nil
}

var seg13 = [10]string{
	"abcdefhijkl", "cdefg", "abcdeghijkm", "abcdefghikm", "acdefgklm",
	"abcefghiklm", "abcefghijklm", "abcdefg", "abcdefghijklm", "abcdefghi klm",
}

func encode13Number(mask []bool, value, start int) {
	if value < 0 {
		value = 0
	}
	if value > 999 {
		value = 999
	}
	digits := fmt.Sprintf("%03d", value)
	for di, ch := range digits {
		if di < 2 && ch == '0' && value < ipow10(2-di) {
			continue
		}
		segs := seg13[int(ch-'0')]
		for i, c := range "abcdefghijklm" {
			if strings.ContainsRune(segs, c) {
				mark(mask, start+di*13+i)
			}
		}
	}
}
func encodeMagic(mask []bool, v int) {
	if v < 0 {
		v = 0
	}
	if v > 99 {
		v = 99
	}
	tens := v / 10
	units := v % 10
	// Wire: c,d,e,g,b,a,f; right digit 0..20, left 21..41.
	order := []byte{'c', 'd', 'e', 'g', 'b', 'a', 'f'}
	for di, digit := range []int{units, tens} {
		if di == 1 && tens == 0 {
			continue
		}
		base := 0
		if di == 1 {
			base = 21
		}
		bits := digitBits[digit]
		for wi, seg := range order {
			if bits&(1<<(seg-'a')) != 0 {
				for i := 0; i < 3; i++ {
					mark(mask, base+wi*3+i)
				}
			}
		}
	}
}
func makeFrame(style, sub int, mask []bool, color rgb, brightness int) []byte {
	return makeFramePower(style, sub, mask, color, brightness, false, false, rgb{255, 160, 0})
}

func makeFramePower(style, sub int, mask []bool, color rgb, brightness int, cpuPower, gpuPower bool, powerColor rgb) []byte {
	order := remap[style]
	if style == 5 && sub == 1 {
		order = remapLF8Sub1
	}
	n := len(mask)
	if len(order) > 0 {
		n = len(order)
	}
	body := make([]byte, n*3)
	scale := float64(brightness) / 100.0 * 0.4
	for i := 0; i < n; i++ {
		logical := i
		if len(order) > 0 {
			logical = order[i]
		}
		if logical >= 0 && logical < len(mask) && mask[logical] {
			c := color
			switch style {
			case 3:
				if (cpuPower || gpuPower) && logical >= 6 && logical <= 26 {
					c = powerColor
				}
			case 5, 6:
				if (cpuPower || gpuPower) && logical >= 28 && logical <= 48 {
					c = powerColor
				}
			}
			body[3*i] = byte(math.Trunc(float64(c.R) * scale))
			body[3*i+1] = byte(math.Trunc(float64(c.G) * scale))
			body[3*i+2] = byte(math.Trunc(float64(c.B) * scale))
		}
	}
	result := make([]byte, 20+len(body))
	result[0] = 0xda
	result[1] = 0xdb
	result[2] = 0xdc
	result[3] = 0xdd
	result[12] = 2
	binary.LittleEndian.PutUint16(result[16:], uint16(len(body)))
	copy(result[20:], body)
	return result
}
