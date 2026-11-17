//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func discoverHardware() ([]hardwareDevice, error) {
	list := []hardwareDevice{}
	for i := 0; i < physicalCPUCount(); i++ {
		list = append(list, hardwareDevice{Kind: "cpu", Index: i, Name: "CPU package", Driver: "hwmon/RAPL"})
	}
	nodes, _ := filepath.Glob("/sys/class/drm/card*/device/vendor")
	seen := map[string]bool{}
	gpu := []hardwareDevice{}
	for _, path := range nodes {
		dev, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			continue
		}
		if seen[dev] {
			continue
		}
		seen[dev] = true
		v, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		vendor := ""
		driver := ""
		switch strings.TrimSpace(string(v)) {
		case "0x1002":
			vendor = "AMD"
			driver = "amdgpu"
		case "0x8086":
			vendor = "Intel"
			driver = "i915/xe"
		case "0x10de":
			vendor = "NVIDIA"
			driver = "nvidia-smi"
		default:
			continue
		}
		deviceName := filepath.Base(dev)
		gpu = append(gpu, hardwareDevice{Kind: "gpu", Vendor: vendor, Name: vendor + " GPU", Bus: deviceName, PNP: dev, Driver: driver})
	}
	sort.Slice(gpu, func(i, j int) bool { return gpu[i].Bus < gpu[j].Bus })
	for i := range gpu {
		gpu[i].Index = i
	}
	return append(list, gpu...), nil
}
