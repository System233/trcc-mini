//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Physical CPU package telemetry from Linux's labeled coretemp and RAPL
// sources. Never assume that hwmon0 or the first RAPL domain belongs to cpu0.
package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type linuxPackageTelemetry struct {
	tempPath string
	amdHWMon string
	power    cpuPowerProbe
}
type linuxPackageTelemetryIndex struct {
	devices  map[int]*linuxPackageTelemetry
	packages int
}

func indexedLinuxPackageIDs(packages map[int][]int) map[int]int {
	ids := make([]int, 0, len(packages))
	for id, cpus := range packages {
		if id >= 0 && len(cpus) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	out := make(map[int]int, len(ids))
	for index, id := range ids {
		out[id] = index
	}
	return out
}

func parseLinuxPackageLabel(label string) (int, bool) {
	s := strings.ToLower(strings.TrimSpace(label))
	prefix := ""
	for _, candidate := range []string{"package id ", "package-", "package "} {
		if strings.HasPrefix(s, candidate) {
			prefix = candidate
			break
		}
	}
	if prefix == "" {
		return 0, false
	}
	tail := strings.TrimPrefix(s, prefix)
	if tail == "" || (len(tail) > 1 && tail[0] == '0') {
		return 0, false
	}
	for _, ch := range tail {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	id, e := strconv.Atoi(tail)
	return id, e == nil && id >= 0
}

// A duplicate physical source is ambiguous, even if both currently report
// the same numerical value. Nothing is promoted into a package metric.
func uniqueLinuxSources(paths map[int][]string) map[int]string {
	out := map[int]string{}
	for id, candidates := range paths {
		seen := map[string]bool{}
		for _, path := range candidates {
			canonical, e := filepath.EvalSymlinks(path)
			if e != nil {
				canonical = path
			}
			seen[canonical] = true
		}
		if len(seen) != 1 {
			continue
		}
		for path := range seen {
			out[id] = path
		}
	}
	return out
}

func discoverLinuxPackageTelemetry(sysClass string, packages map[int][]int) *linuxPackageTelemetryIndex {
	return discoverLinuxPackageTelemetryWithNodes(sysClass, "/sys/devices/system/node", packages)
}

// A NUMA node can contain a subset of a socket's cores, but must never
// contain cores from two physical packages when used to attribute an AMD
// k10temp PCI device. Rejection is preferable to wrong socket telemetry.
func linuxNUMAPhysicalPackage(cpulist string, packages map[int][]int) (int, bool) {
	logicalToPackage := map[int]int{}
	for pkg, cpus := range packages {
		for _, cpu := range cpus {
			logicalToPackage[cpu] = pkg
		}
	}
	found := -1
	count := 0
	for _, item := range strings.Split(strings.TrimSpace(cpulist), ",") {
		if item == "" {
			return 0, false
		}
		rangeParts := strings.Split(item, "-")
		if len(rangeParts) < 1 || len(rangeParts) > 2 {
			return 0, false
		}
		start, e := strconv.Atoi(rangeParts[0])
		if e != nil || start < 0 || start >= 4096 {
			return 0, false
		}
		end := start
		if len(rangeParts) == 2 {
			end, e = strconv.Atoi(rangeParts[1])
			if e != nil || end < start || end >= 4096 {
				return 0, false
			}
		}
		for cpu := start; cpu <= end; cpu++ {
			pkg, ok := logicalToPackage[cpu]
			if !ok || (found >= 0 && found != pkg) {
				return 0, false
			}
			found = pkg
			count++
		}
	}
	return found, found >= 0 && count > 0
}

func discoverLinuxPackageTelemetryWithNodes(sysClass, nodesRoot string, packages map[int][]int) *linuxPackageTelemetryIndex {
	indices := indexedLinuxPackageIDs(packages)
	result := &linuxPackageTelemetryIndex{devices: map[int]*linuxPackageTelemetry{}, packages: len(indices)}
	for _, index := range indices {
		result.devices[index] = &linuxPackageTelemetry{}
	}
	if len(indices) == 0 {
		return result
	}
	// Intel coretemp explicitly provides Package id N. The AMD k10temp
	// device has no package-ID in its generic Tctl label, so multi-socket
	// AMD Tctl stays N/A without a verified PCI/NUMA topology binding.
	temperaturePaths := map[int][]string{}
	chips, _ := filepath.Glob(filepath.Join(sysClass, "hwmon", "hwmon*"))
	sort.Strings(chips)
	for _, chip := range chips {
		name, e := os.ReadFile(filepath.Join(chip, "name"))
		if e != nil {
			continue
		}
		if strings.TrimSpace(string(name)) == "k10temp" {
			// /sys/class/hwmon/hwmonX/device resolves to the PCI function.
			// Its NUMA node is valid only if every CPU in that node maps to
			// one physical package. A node is not automatically a socket.
			numaRaw, e := os.ReadFile(filepath.Join(chip, "device", "numa_node"))
			if e != nil {
				continue
			}
			node, e := strconv.Atoi(strings.TrimSpace(string(numaRaw)))
			if e != nil || node < 0 || node > 4095 {
				continue
			}
			cpulist, e := os.ReadFile(filepath.Join(nodesRoot, fmt.Sprintf("node%d", node), "cpulist"))
			if e != nil {
				continue
			}
			physical, ok := linuxNUMAPhysicalPackage(string(cpulist), packages)
			if !ok {
				continue
			}
			label, e := os.ReadFile(filepath.Join(chip, "temp1_label"))
			if e == nil && strings.TrimSpace(strings.ToLower(string(label))) != "tctl" {
				continue
			}
			input := filepath.Join(chip, "temp1_input")
			milli, e := readSysFloat(input)
			if e != nil || milli < 1000 || milli > 130000 {
				continue
			}
			temperaturePaths[physical] = append(temperaturePaths[physical], input)
			continue
		}
		if strings.TrimSpace(string(name)) != "coretemp" {
			continue
		}
		labels, _ := filepath.Glob(filepath.Join(chip, "temp*_label"))
		sort.Strings(labels)
		for _, labelPath := range labels {
			raw, e := os.ReadFile(labelPath)
			if e != nil {
				continue
			}
			physical, ok := parseLinuxPackageLabel(string(raw))
			if !ok {
				continue
			}
			if _, exists := indices[physical]; !exists {
				continue
			}
			input := strings.TrimSuffix(labelPath, "_label") + "_input"
			temp, e := readSysFloat(input)
			if e != nil || temp < 1000 || temp > 130000 {
				continue
			}
			temperaturePaths[physical] = append(temperaturePaths[physical], input)
		}
	}
	for physical, path := range uniqueLinuxSources(temperaturePaths) {
		device := result.devices[indices[physical]]
		device.tempPath = path
		chipDir := filepath.Dir(path)
		if raw, e := os.ReadFile(filepath.Join(chipDir, "name")); e == nil && strings.TrimSpace(string(raw)) == "k10temp" {
			device.amdHWMon = chipDir
		}
	}

	// Match package-N names exactly. The nested core/uncore RAPL domains
	// must never be confused with the package-wide energy counter.
	energyPaths := map[int][]string{}
	domains, _ := filepath.Glob(filepath.Join(sysClass, "powercap", "*", "energy_uj"))
	sort.Strings(domains)
	for _, path := range domains {
		dir := filepath.Dir(path)
		name, e := os.ReadFile(filepath.Join(dir, "name"))
		if e != nil {
			continue
		}
		physical, ok := parseLinuxPackageLabel(string(name))
		if !ok {
			continue
		}
		if _, exists := indices[physical]; !exists {
			continue
		}
		energy, e := readSysFloat(path)
		if e != nil || energy < 0 {
			continue
		}
		energyPaths[physical] = append(energyPaths[physical], path)
	}
	for physical, path := range uniqueLinuxSources(energyPaths) {
		device := result.devices[indices[physical]]
		device.power.energy = path
		device.power.energyRange, _ = readSysFloat(filepath.Join(filepath.Dir(path), "max_energy_range_uj"))
		// Priming once at discovery ensures the next frame can calculate
		// a genuine rate. A changed device requires a new sensor session.
		first, e := readSysFloat(path)
		if e == nil && first >= 0 {
			device.power.lastEnergy = first
			device.power.lastAt = time.Now()
			device.power.primed = true
		}
	}
	return result
}

func readLinuxPackagePower(p *cpuPowerProbe) (float64, bool) {
	if p == nil || p.energy == "" {
		return 0, false
	}
	now := time.Now()
	current, e := readSysFloat(p.energy)
	if e != nil || current < 0 {
		return 0, false
	}
	if !p.primed {
		p.lastEnergy = current
		p.lastAt = now
		p.primed = true
		return 0, false
	}
	delta := current - p.lastEnergy
	if delta < 0 && p.energyRange > 0 {
		delta += p.energyRange
	}
	dt := now.Sub(p.lastAt).Seconds()
	p.lastEnergy, p.lastAt = current, now
	if dt < 0.01 || dt > 30 || delta < 0 {
		return 0, false
	}
	watts := delta / 1e6 / dt
	return watts, !math.IsNaN(watts) && !math.IsInf(watts, 0) && watts >= 0 && watts <= 1000
}

func (p *linuxPackageTelemetryIndex) Sample(m *metrics) {
	if p == nil || m == nil {
		return
	}
	for index, device := range p.devices {
		maxTemp := 0.0
		if device.tempPath != "" {
			milli, e := readSysFloat(device.tempPath)
			if e == nil && milli >= 1000 && milli <= 130000 {
				maxTemp = milli / 1000
				m.setExtra(indexedMetricName("cpu", index, "temperature"), maxTemp)
			}
		}
		if device.amdHWMon != "" {
			readings, _ := readHWMonTemps(hwmonChip{directory: device.amdHWMon, name: "k10temp"})
			for source, temp := range readings {
				if !strings.HasPrefix(source, "ccd") {
					continue
				}
				physicalCCD, e := strconv.Atoi(strings.TrimPrefix(source, "ccd"))
				if e != nil || physicalCCD < 1 || physicalCCD > 16 {
					continue
				}
				m.setExtra(indexedMetricName("cpu", index, fmt.Sprintf("ccd%d.temperature", physicalCCD-1)), temp)
				if temp > maxTemp {
					maxTemp = temp
				}
			}
		}
		if maxTemp > 0 {
			m.setExtra(indexedMetricName("cpu", index, "temperature.max"), maxTemp)
		}
		if watts, valid := readLinuxPackagePower(&device.power); valid {
			m.setExtra(indexedMetricName("cpu", index, "power"), watts)
		}
	}
}

func (p *linuxPackageTelemetryIndex) Coverage() string {
	if p == nil {
		return "unavailable"
	}
	temp, power := 0, 0
	for _, d := range p.devices {
		if d.tempPath != "" {
			temp++
		}
		if d.power.energy != "" {
			power++
		}
	}
	return fmt.Sprintf("packages=%d, temperature=%d, RAPL=%d", p.packages, temp, power)
}
