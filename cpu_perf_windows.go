//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Per-logical-processor CPU times from Windows NT processor counters, joined
// to physical socket topology reported by GetLogicalProcessorInformationEx.
// Only one-processor-group topologies are sampled until group-wide counter
// ordering is explicitly verified. No guessed package ownership.
package main

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"sort"
	"strconv"
	"syscall"
	"unsafe"
)

const processorPerfInfoClass = 8

type cpuTimes struct {
	Idle           int64
	Kernel         int64
	User           int64
	DPC            int64
	Interrupt      int64
	InterruptCount uint32
	Padding        uint32
}
type windowsCPUDetailSampler struct {
	packages [][]int
	prev     []cpuTimes
	primed   bool
}

type cpuDetailSampler = windowsCPUDetailSampler

var ntQueryCPUPerformance = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQuerySystemInformation")
var getProcessorGroupCount = syscall.NewLazyDLL("kernel32.dll").NewProc("GetActiveProcessorGroupCount")

func readPackageTopologyBuffer() ([]byte, error) {
	const relationProcessorPackage = 3
	var needed uint32
	_, _, _ = procCPUInformationEx.Call(relationProcessorPackage, 0, uintptr(unsafe.Pointer(&needed)))
	if needed < 32 || needed > 4*1024*1024 {
		return nil, errors.New("package topology unavailable")
	}
	buffer := make([]byte, needed)
	ok, _, _ := procCPUInformationEx.Call(relationProcessorPackage,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return nil, errors.New("GetLogicalProcessorInformationEx failed")
	}
	return buffer[:needed], nil
}

func parseWindowsCPUPackages(data []byte) ([][]int, error) {
	if len(data) < 32 {
		return nil, errors.New("incomplete package topology")
	}
	packages := [][]int{}
	used := map[int]bool{}
	for off := 0; off+8 <= len(data); {
		rel := binary.LittleEndian.Uint32(data[off:])
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		if size < 8 || off+size > len(data) {
			return nil, errors.New("malformed processor relationship size")
		}
		if rel == 3 {
			// PROCESSOR_RELATIONSHIP is 24 bytes after the 8-byte header.
			// GroupCount is at record offset 30, GROUP_AFFINITY at 32.
			if size < 48 {
				return nil, errors.New("incomplete PROCESSOR_RELATIONSHIP")
			}
			groups := int(binary.LittleEndian.Uint16(data[off+30:]))
			if groups < 1 || groups > 64 || size < 32+groups*16 {
				return nil, errors.New("invalid processor group count")
			}
			var ids []int
			for i := 0; i < groups; i++ {
				addr := off + 32 + i*16
				mask := binary.LittleEndian.Uint64(data[addr:])
				group := binary.LittleEndian.Uint16(data[addr+8:])
				// Group 0 is the only group understood by the native
				// counter API's flat array. Never flatten group 1 as
				// though it were more CPUs in group 0.
				if group != 0 {
					return nil, errors.New("multi-group topology requires group-aware counters")
				}
				for mask != 0 {
					bit := bits.TrailingZeros64(mask)
					mask &^= 1 << bit
					if used[bit] {
						return nil, errors.New("logical CPU belongs to multiple sockets")
					}
					used[bit] = true
					ids = append(ids, bit)
				}
			}
			if len(ids) == 0 {
				return nil, errors.New("empty CPU socket affinity mask")
			}
			sort.Ints(ids)
			packages = append(packages, ids)
		}
		off += size
	}
	if len(packages) == 0 {
		return nil, errors.New("no physical CPU sockets")
	}
	// Windows reports sockets in the topology enumeration order. Sort by
	// the first logical processor to make cpu0/cpu1 deterministic.
	sort.Slice(packages, func(i, j int) bool { return packages[i][0] < packages[j][0] })
	return packages, nil
}

func readWindowsPerCPUTimes(count int) ([]cpuTimes, bool) {
	if count < 1 || count > 64 {
		return nil, false
	}
	times := make([]cpuTimes, count)
	var ret uint32
	status, _, _ := ntQueryCPUPerformance.Call(processorPerfInfoClass,
		uintptr(unsafe.Pointer(&times[0])),
		uintptr(len(times))*unsafe.Sizeof(times[0]),
		uintptr(unsafe.Pointer(&ret)))
	if int32(status) != 0 {
		return nil, false
	}
	if ret != 0 && int(ret) < len(times)*int(unsafe.Sizeof(times[0])) {
		return nil, false
	}
	return times, true
}

func cpuTimesDelta(previous, current cpuTimes) (busy, total uint64, ok bool) {
	// KernelTime includes idle time on Windows. All three values are
	// cumulative 100ns intervals. Treat resets and zero deltas as N/A.
	if previous.Kernel < 0 || previous.User < 0 || previous.Idle < 0 ||
		current.Kernel < previous.Kernel || current.User < previous.User || current.Idle < previous.Idle {
		return 0, 0, false
	}
	kernel := uint64(current.Kernel - previous.Kernel)
	user := uint64(current.User - previous.User)
	idle := uint64(current.Idle - previous.Idle)
	if kernel > ^uint64(0)-user {
		return 0, 0, false
	}
	total = kernel + user
	if total == 0 || idle > total {
		return 0, 0, false
	}
	return total - idle, total, true
}

func (s *windowsCPUDetailSampler) ensureTopology() bool {
	if len(s.packages) > 0 {
		return true
	}
	groups, _, _ := getProcessorGroupCount.Call()
	if groups != 1 {
		return false
	}
	data, e := readPackageTopologyBuffer()
	if e != nil {
		return false
	}
	packages, e := parseWindowsCPUPackages(data)
	if e != nil {
		return false
	}
	if len(packages) > 64 {
		return false
	}
	maxCPU := -1
	for _, ids := range packages {
		for _, id := range ids {
			if id > maxCPU {
				maxCPU = id
			}
		}
	}
	if maxCPU < 0 || maxCPU >= 64 {
		return false
	}
	count, _, _ := winCPUCountAPI.Call(0)
	if count == 0 || count > 64 || int(count) != maxCPU+1 {
		return false
	}
	total := 0
	for _, ids := range packages {
		total += len(ids)
	}
	if total != int(count) {
		return false
	}
	s.packages = packages
	return true
}

func (s *windowsCPUDetailSampler) Sample(m *metrics) {
	if !s.ensureTopology() {
		return
	}
	logicalCount := 0
	for _, pkg := range s.packages {
		logicalCount += len(pkg)
	}
	if logicalCount < 1 || logicalCount > 64 {
		return
	}

	// Retrieve OS power information once and assign frequencies by its
	// reported processor Number, rather than by array position.
	clocks := make([]processorPowerInfo, logicalCount)
	rc, _, _ := winCPUClockAPI.Call(11, 0, 0, uintptr(unsafe.Pointer(&clocks[0])),
		uintptr(len(clocks))*unsafe.Sizeof(clocks[0]))
	if rc == 0 {
		byLogical := make(map[int]float64, len(clocks))
		seen := map[int]bool{}
		for _, p := range clocks {
			i := int(p.Number)
			if i < 0 || i >= logicalCount {
				continue
			}
			if seen[i] {
				delete(byLogical, i)
				continue
			}
			seen[i] = true
			if p.CurrentMhz > 0 && p.CurrentMhz < 15000 {
				byLogical[i] = float64(p.CurrentMhz)
			}
		}
		allSum, allCount, allMax := 0.0, 0, 0.0
		for socket, ids := range s.packages {
			sum, n, max := 0.0, 0, 0.0
			for logicalIndex, id := range ids {
				value, ok := byLogical[id]
				if !ok {
					continue
				}
				m.setExtra(indexedMetricName("cpu", socket, coreSuffix(logicalIndex, "clock")), value)
				sum += value
				n++
				if value > max {
					max = value
				}
			}
			if n > 0 {
				m.setExtra(indexedMetricName("cpu", socket, "clock"), sum/float64(n))
				m.setExtra(indexedMetricName("cpu", socket, "clock.max"), max)
				allSum += sum
				allCount += n
				if max > allMax {
					allMax = max
				}
			}
		}
		if allCount > 0 {
			m.setExtra("cpu.clock", allSum/float64(allCount))
			m.setExtra("cpu.clock.max", allMax)
		}
	}

	times, ok := readWindowsPerCPUTimes(logicalCount)
	if !ok {
		return
	}
	if !s.primed || len(s.prev) != len(times) {
		s.prev = times
		s.primed = true
		return
	}
	populateWindowsCPUUsage(m, s.packages, s.prev, times)
	s.prev = times
}

// Weighted per-socket load, omitting invalid/missing logical CPUs.
func populateWindowsCPUUsage(m *metrics, packages [][]int, previous, current []cpuTimes) {
	for socket, ids := range packages {
		var busySum, totalSum uint64
		for logicalIndex, id := range ids {
			if id < 0 || id >= len(previous) || id >= len(current) {
				continue
			}
			busy, total, valid := cpuTimesDelta(previous[id], current[id])
			if !valid {
				continue
			}
			value := 100 * float64(busy) / float64(total)
			m.setExtra(indexedMetricName("cpu", socket, coreSuffix(logicalIndex, "load")), value)
			busySum += busy
			totalSum += total
		}
		if totalSum > 0 {
			m.setExtra(indexedMetricName("cpu", socket, "load"),
				100*float64(busySum)/float64(totalSum))
		}
	}
}

func coreSuffix(index int, metric string) string {
	// Core indices here are logical processors, not physical core IDs.
	return "core" + strconv.Itoa(index) + "." + metric
}

func listPlatformCPUDetailMetrics() []string {
	var sampler cpuDetailSampler
	if !sampler.ensureTopology() {
		return nil
	}
	var catalog []string
	for socket, ids := range sampler.packages {
		for logical := range ids {
			catalog = append(catalog,
				indexedMetricName("cpu", socket, coreSuffix(logical, "load")),
				indexedMetricName("cpu", socket, coreSuffix(logical, "clock")))
		}
	}
	return catalog
}
