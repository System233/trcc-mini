//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Per-CPU procfs counters grouped by physical_package_id. On Linux the
// cpuN.coreM index is a zero-based logical processor within CPU package N.
package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type linuxCPUUsage struct{ busy, total uint64 }
type cpuDetailSampler struct {
	last map[int]linuxCPUUsage
}

func linuxProcessorPackages() map[int][]int {
	cpuPaths, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/topology/physical_package_id")
	packages := map[int][]int{}
	for _, path := range cpuPaths {
		parent := filepath.Base(filepath.Dir(filepath.Dir(path)))
		if !strings.HasPrefix(parent, "cpu") {
			continue
		}
		logical, e := strconv.Atoi(strings.TrimPrefix(parent, "cpu"))
		if e != nil || logical < 0 {
			continue
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		packageID, e := strconv.Atoi(strings.TrimSpace(string(raw)))
		if e != nil || packageID < 0 {
			continue
		}
		packages[packageID] = append(packages[packageID], logical)
	}
	for key := range packages {
		sort.Ints(packages[key])
	}
	return packages
}

func parseLinuxProcessorTimes(data []byte) map[int]linuxCPUUsage {
	result := map[int]linuxCPUUsage{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 || !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}
		cpu, e := strconv.Atoi(strings.TrimPrefix(fields[0], "cpu"))
		if e != nil || cpu < 0 {
			continue
		}
		var ticks [8]uint64
		valid := true
		for i := range ticks {
			v, e := strconv.ParseUint(fields[i+1], 10, 64)
			if e != nil {
				valid = false
				break
			}
			ticks[i] = v
		}
		if !valid {
			continue
		}
		var total uint64
		for _, v := range ticks {
			total += v
		}
		idle := ticks[3] + ticks[4]
		if total < idle {
			continue
		}
		result[cpu] = linuxCPUUsage{busy: total - idle, total: total}
	}
	return result
}

func (s *cpuDetailSampler) Sample(m *metrics) {
	cpuPackages := linuxProcessorPackages()
	if len(cpuPackages) == 0 {
		return
	}
	buf, e := os.ReadFile("/proc/stat")
	if e != nil {
		return
	}
	current := parseLinuxProcessorTimes(buf)
	if len(current) == 0 {
		return
	}
	if len(s.last) == 0 {
		s.last = current
		return
	}
	packages := make([]int, 0, len(cpuPackages))
	for id := range cpuPackages {
		packages = append(packages, id)
	}
	sort.Ints(packages)
	for socket, packageID := range packages {
		var busySum, totalSum uint64
		for core, logical := range cpuPackages[packageID] {
			last, oldOK := s.last[logical]
			now, newOK := current[logical]
			if !oldOK || !newOK || now.total <= last.total || now.busy < last.busy {
				continue
			}
			total, busy := now.total-last.total, now.busy-last.busy
			if total == 0 || busy > total {
				continue
			}
			m.setExtra(indexedMetricName("cpu", socket, "core"+strconv.Itoa(core)+".load"),
				100*float64(busy)/float64(total))
			busySum += busy
			totalSum += total
		}
		if totalSum > 0 {
			m.setExtra(indexedMetricName("cpu", socket, "load"), 100*float64(busySum)/float64(totalSum))
		}
	}
	s.last = current
}

func listPlatformCPUDetailMetrics() []string {
	packages := linuxProcessorPackages()
	if len(packages) == 0 {
		return nil
	}
	IDs := make([]int, 0, len(packages))
	for id := range packages {
		IDs = append(IDs, id)
	}
	sort.Ints(IDs)
	var catalog []string
	for socket, id := range IDs {
		for logical := range packages[id] {
			catalog = append(catalog, indexedMetricName("cpu", socket, "core"+strconv.Itoa(logical)+".load"))
		}
	}
	return catalog
}
