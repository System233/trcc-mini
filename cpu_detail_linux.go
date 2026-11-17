//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"strconv"
	"strings"
)

func populatePlatformCPUMetrics(m *metrics, sensor cpuTelemetry) {
	if native, ok := sensor.(*linuxCPUSensor); ok && native.packages != nil {
		native.packages.Sample(m)
	}
	if data, e := os.ReadFile("/proc/cpuinfo"); e == nil {
		var sum, max float64
		n := 0
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.SplitN(line, ":", 2)
			if len(fields) != 2 || strings.TrimSpace(fields[0]) != "cpu MHz" {
				continue
			}
			mhz, e := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
			if e != nil || mhz <= 0 || mhz > 15000 {
				continue
			}
			sum += mhz
			if mhz > max {
				max = mhz
			}
			n++
		}
		if n > 0 {
			m.setExtra("cpu.clock", sum/float64(n))
			m.setExtra("cpu.clock.max", max)
			if physicalCPUCount() == 1 {
				m.setExtra("cpu0.clock", sum/float64(n))
				m.setExtra("cpu0.clock.max", max)
			}
		}
	}
	if sensor == nil {
		return
	}
	singleSocket := physicalCPUCount() == 1
	var maxTemp float64
	if value, e := sensor.ReadTemp("tctl"); e == nil && value > 0 {
		if singleSocket {
			m.setExtra("cpu0.temperature", value)
		}
		maxTemp = value
	}
	for i, source := range []string{"ccd1", "ccd2"} {
		if value, e := sensor.ReadTemp(source); e == nil && value > 0 {
			suffix := "ccd" + strconv.Itoa(i) + ".temperature"
			m.setExtra("cpu."+suffix, value)
			if singleSocket {
				m.setExtra(indexedMetricName("cpu", 0, suffix), value)
			}
			if value > maxTemp {
				maxTemp = value
			}
		}
	}
	if maxTemp > 0 {
		m.setExtra("cpu.temperature.max", maxTemp)
		if singleSocket {
			m.setExtra("cpu0.temperature.max", maxTemp)
		}
	}
}
