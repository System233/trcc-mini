//go:build darwin

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"math"
	"strings"
	"testing"
)

func TestMacPowermetricsParsing(t *testing.T) {
	sample := parsePowermetrics("CPU Power: 3541 mW\nGPU Power: 1392 mW\nCPU die temperature: 56.12 C\nGPU Active residency: 41.25%\n")
	if !sample.cpuPowerValid || math.Abs(sample.cpuW-3.541) > 1e-9 ||
		!sample.gpuPowerValid || math.Abs(sample.gpuW-1.392) > 1e-9 ||
		!sample.cpuTempValid || math.Abs(sample.cpuTemp-56.12) > 1e-9 ||
		!sample.gpuLoadValid || math.Abs(sample.gpuLoad-41.25) > 1e-9 {
		t.Fatalf("powermetrics parse: %+v", sample)
	}
	missing := parsePowermetrics("CPU Power: N/A\nGPU Active residency: 101%\n")
	if missing.cpuPowerValid || missing.gpuLoadValid || missing.gpuPowerValid {
		t.Fatalf("invalid power readings accepted: %+v", missing)
	}
}
func TestMacLaunchdPlistEscapesPaths(t *testing.T) {
	plist := macServicePlistText("/usr/local/bin/trcc-mini", "/Users/Test & Co/config <x>.json")
	if !strings.Contains(plist, "Test &amp; Co/config &lt;x&gt;.json") {
		t.Fatalf("launchd XML not escaped: %s", plist)
	}
	if !strings.Contains(plist, "<key>RunAtLoad</key><true/>") || !strings.Contains(plist, "<key>ProgramArguments</key>") {
		t.Fatalf("launchd plist missing daemon attributes: %s", plist)
	}
}
