//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"time"
)

func sensorDiagnostics() error {
	fmt.Println("Linux native sensor diagnostics (no USB writes):")
	var cpu cpuLoadSampler
	_ = cpu.Sample()
	time.Sleep(250 * time.Millisecond)
	fmt.Printf("CPU load: %d%%\n", cpu.Sample())
	c, err := openCPUSensor()
	if err != nil {
		fmt.Println("CPU hwmon:", err)
	} else {
		defer c.Close()
		for _, source := range []string{"tctl", "ccd1", "ccd2", "ccd-max", "ccd-avg"} {
			v, e := c.ReadTemp(source)
			if e != nil {
				fmt.Printf("  %-8s N/A\n", source)
			} else {
				fmt.Printf("  %-8s %.1f°C\n", source, v)
			}
		}
		_, _, _ = c.ReadPower()
	}
	g, err := openGPUSensor()
	if err != nil {
		fmt.Println("GPU:", err)
	} else {
		defer g.Close()
		t, ok, load, loadOK := g.Read()
		p, pOK := g.ReadPower()
		used, total, percent, mOK := g.ReadMemory()
		if ok {
			fmt.Printf("GPU temperature: %.1f°C\n", t)
		}
		if loadOK {
			fmt.Printf("GPU load: %d%%\n", load)
		}
		if pOK {
			fmt.Printf("GPU watts: %.1fW\n", p)
		}
		if mOK {
			fmt.Printf("GPU memory: %.1f / %.1f GiB (%.1f%%)\n", used, total, percent)
		}
	}
	if used, total, pct, ok := readSystemMemory(); ok {
		fmt.Printf("RAM: %.1f / %.1f GiB (%.1f%%)\n", used, total, pct)
	}
	if c != nil {
		time.Sleep(500 * time.Millisecond)
		if w, ok, e := c.ReadPower(); e == nil && ok {
			fmt.Printf("CPU package watts: %.1fW\n", w)
		}
	}
	return nil
}
