//go:build darwin

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os/exec"
	"strconv"
	"strings"
)

func physicalCPUCount() int {
	output, e := exec.Command("sysctl", "-n", "hw.packages").Output()
	if e != nil {
		return 0
	}
	count, e := strconv.Atoi(strings.TrimSpace(string(output)))
	if e != nil || count < 1 {
		return 0
	}
	return count
}
