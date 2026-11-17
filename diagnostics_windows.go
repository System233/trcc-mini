//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"time"
)

// sensorDiagnostics never enumerates or opens the HID device. Use this to
// compare real sensor sources before selecting one for the display.
func sensorDiagnostics() error {
	fmt.Println("CPU temperature sources (read-only; no USB writes):")
	vendor := windowsCPUVendor()
	pawn, err := openCPUSensor()
	if err != nil {
		fmt.Println("  CPU sensor: unavailable:", err)
	} else {
		defer pawn.Close()
		sources := []string{"tctl", "ccd1", "ccd2", "ccd-max", "ccd-avg"}
		if vendor == "GenuineIntel" {
			sources = []string{"tctl"} // package sensor, not AMD CCD
			fmt.Println("  Intel processor: signed PawnIO IntelMSR (read-only)")
		}
		for _, source := range sources {
			value, e := pawn.ReadTemp(source)
			if e != nil {
				fmt.Printf("  %-9s N/A (%v)\n", source, e)
			} else {
				fmt.Printf("  %-9s %6.1f°C\n", source, value)
			}
		}
		_, _, _ = pawn.ReadPower() // prime the energy counter
	}
	fmt.Println("GPU (NVIDIA NVML):")
	gpu, gerr := openNVML()
	if gerr != nil {
		fmt.Println("  NVML: unavailable:", gerr)
	} else {
		defer gpu.Close()
		temp, valid, load, loadValid := gpu.Read()
		watts, powerValid := gpu.ReadPower()
		if valid {
			fmt.Printf("  GPU temp:   %.1f°C\n", temp)
		} else {
			fmt.Println("  GPU temp:   N/A")
		}
		if loadValid {
			fmt.Printf("  GPU load:   %d%%\n", load)
		} else {
			fmt.Println("  GPU load:   N/A")
		}
		if powerValid {
			fmt.Printf("  GPU power:  %.1fW\n", watts)
		} else {
			fmt.Println("  GPU power:  N/A")
		}
		used, total, percent, ok := gpu.ReadMemory()
		if ok {
			fmt.Printf("  GPU VRAM:   %.1f / %.1f GiB (%.1f%%)\n", used, total, percent)
		} else {
			fmt.Println("  GPU VRAM:   N/A (NVML memory information not available)")
		}
	}
	if used, total, percent, ok := readSystemMemory(); ok {
		fmt.Printf("System RAM:    %.1f / %.1f GiB (%.1f%%)\n", used, total, percent)
	} else {
		fmt.Println("System RAM:    N/A")
	}
	if pawn != nil {
		time.Sleep(500 * time.Millisecond)
		watts, ok, e := pawn.ReadPower()
		if e != nil {
			fmt.Println("  CPU power:  N/A (", e, ")")
		} else if ok {
			fmt.Printf("  CPU power:  %.1fW\n", watts)
		} else {
			fmt.Println("  CPU power:  N/A (energy counter warming up)")
		}
	}
	if vendor != "GenuineIntel" {
		fmt.Println("Note: CCD1/CCD2 are AMD chiplet sensors; ASUS CPU Diode is a distinct motherboard sensor.")
	}
	return nil
}
