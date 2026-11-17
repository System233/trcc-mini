//go:build darwin

// SPDX-License-Identifier: GPL-3.0-or-later
package main

// macOS sysctl CPU load reports a system aggregate. Until per-processor
// counters can be bound to physical sockets, keep cpuN.coreM.load as N/A.
type cpuDetailSampler struct{}

func (*cpuDetailSampler) Sample(*metrics)    {}
func listPlatformCPUDetailMetrics() []string { return nil }
