//go:build darwin

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"time"
)

func sensorDiagnostics() error {
	fmt.Println("macOS CPU/RAM diagnostics; GPU power is optional (powermetrics), GPU VRAM unavailable with unified memory:")
	var cpu cpuLoadSampler
	cpu.Sample()
	time.Sleep(time.Second)
	fmt.Printf("CPU load: %d%%\n", cpu.Sample())
	c, e := openCPUSensor()
	if e != nil {
		fmt.Println("CPU:", e)
	} else {
		defer c.Close()
		if value, e := c.ReadTemp("tctl"); e == nil {
			fmt.Printf("CPU die: %.1f°C\n", value)
		} else {
			fmt.Println("CPU die: N/A:", e)
		}
		if value, ok, e := c.ReadPower(); e == nil && ok {
			fmt.Printf("CPU package: %.1fW\n", value)
		} else {
			fmt.Println("CPU package: N/A")
		}
	}
	if used, total, pct, ok := readSystemMemory(); ok {
		fmt.Printf("RAM (reclaimable-adjusted): %.1f / %.1f GiB (%.1f%%)\n", used, total, pct)
	} else {
		fmt.Println("RAM: N/A")
	}
	g, e := openGPUSensor()
	if e == nil {
		defer g.Close()
		if watts, ok := g.ReadPower(); ok {
			fmt.Printf("GPU: %.1fW\n", watts)
		}
	}
	return nil
}
