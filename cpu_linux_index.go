//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"strings"
)

func physicalCPUCount() int {
	paths, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/topology/physical_package_id")
	found := map[string]bool{}
	for _, path := range paths {
		raw, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		found[strings.TrimSpace(string(raw))] = true
	}
	return len(found)
}
