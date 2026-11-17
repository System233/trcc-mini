//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Per-logical-processor CPU times from Windows NT processor counters, joined
// to physical socket topology reported by GetLogicalProcessorInformationEx.
// NtQuerySystemInformationEx reads group-local CPU counters; package masks
// are flattened as group*64+processor only after all active groups validate.
// Unknown or incomplete topology is N/A, never a guessed socket identity.
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
	packages    [][]int
	groupCounts []int
	prev        []cpuTimes
	primed      bool
}

type cpuDetailSampler = windowsCPUDetailSampler

var ntQueryCPUPerformance = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQuerySystemInformation")
var ntQueryCPUPerformanceEx = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQuerySystemInformationEx")
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
				if group >= 64 {
					return nil, errors.New("unsupported processor group index")
				}
				for mask != 0 {
					bit := bits.TrailingZeros64(mask)
					mask &= mask - 1
					id := int(group)*64 + bit
					if used[id] {
						return nil, errors.New("logical CPU belongs to multiple sockets")
					}
					used[id] = true
					ids = append(ids, id)
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

// Multi-group counterpart of readWindowsPerCPUTimes. The Windows NT Ex API
// accepts a USHORT processor-group index and returns the active processors
// of that group in local processor order. Keep a fixed 64-slot stride so a
// package spanning group 0 and group 1 cannot alias two processor numbers.
func readWindowsGroupedTimes(groupCounts []int) ([]cpuTimes, bool) {
	if len(groupCounts) < 1 || len(groupCounts) > 64 {
		return nil, false
	}
	if len(groupCounts) == 1 {
		return readWindowsPerCPUTimes(groupCounts[0])
	}
	result := make([]cpuTimes, len(groupCounts)*64)
	for group, count := range groupCounts {
		samples, valid := readWindowsCPUGroupWithEx(uint16(group), count)
		if !valid {
			return nil, false
		}
		copy(result[group*64:group*64+count], samples)
	}
	return result, true
}

func readWindowsCPUGroupWithEx(group uint16, count int) ([]cpuTimes, bool) {
	if count < 1 || count > 64 {
		return nil, false
	}
	if err := ntQueryCPUPerformanceEx.Find(); err != nil {
		return nil, false
	}
	samples := make([]cpuTimes, count)
	var returned uint32
	size := uint32(unsafe.Sizeof(cpuTimes{}))
	status, _, _ := ntQueryCPUPerformanceEx.Call(
		processorPerfInfoClass,
		uintptr(unsafe.Pointer(&group)), unsafe.Sizeof(group),
		uintptr(unsafe.Pointer(&samples[0])), uintptr(count)*uintptr(size),
		uintptr(unsafe.Pointer(&returned)))
	if int32(status) != 0 || returned != uint32(count)*size {
		return nil, false
	}
	return samples, true
}

// Processor-group flattening can be used only when every active processor
// is represented once by exactly one physical package. This is deliberately
// stricter than trusting the count of packages alone.
func validWindowsPackageGroups(packages [][]int, groupCounts []int) bool {
	if len(packages) == 0 || len(packages) > 64 || len(groupCounts) == 0 || len(groupCounts) > 64 {
		return false
	}
	seen := make([]bool, len(groupCounts)*64)
	for _, pkg := range packages {
		if len(pkg) == 0 {
			return false
		}
		for _, id := range pkg {
			if id < 0 || id >= len(seen) || seen[id] {
				return false
			}
			group, local := id/64, id%64
			if groupCounts[group] < 1 || groupCounts[group] > 64 || local >= groupCounts[group] {
				return false
			}
			seen[id] = true
		}
	}
	for g, count := range groupCounts {
		if count < 1 || count > 64 {
			return false
		}
		for i := 0; i < count; i++ {
			if !seen[g*64+i] {
				return false
			}
		}
	}
	return true
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
	if groups < 1 || groups > 64 {
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
	counts := make([]int, int(groups))
	for group := range counts {
		active, _, _ := winCPUCountAPI.Call(uintptr(group))
		if active == 0 || active > 64 {
			return false
		}
		counts[group] = int(active)
	}
	if !validWindowsPackageGroups(packages, counts) {
		return false
	}
	s.packages = packages
	s.groupCounts = counts
	return true
}

func (s *windowsCPUDetailSampler) Sample(m *metrics) {
	groups, _, _ := getProcessorGroupCount.Call()
	if groups > 1 {
		// GetSystemTimes and legacy NtQuerySystemInformation may cover only
		// the calling processor group. Hide that partial percentage until
		// the Ex query has supplied a complete cross-group sample.
		if m.Extras == nil {
			m.Extras = make(map[string]sampleValue)
		}
		m.Extras["cpu.load"] = sampleValue{}
	}
	if !s.ensureTopology() {
		return
	}
	logicalCount := 0
	for _, pkg := range s.packages {
		logicalCount += len(pkg)
	}
	if logicalCount < 1 || logicalCount > 4096 {
		return
	}

	// Retrieve OS power information once and assign frequencies by its
	// reported processor Number, rather than by array position.
	// CallNtPowerInformation can return a flat array, but its processor
	// numbering across groups is not documented to match group*64+bit.
	// Until that relation is established, leave multi-group per-core
	// clocks as N/A rather than bind a different group's CPU frequency.
	if len(s.groupCounts) == 1 {
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
	}

	times, ok := readWindowsGroupedTimes(s.groupCounts)
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
	if m.Extras == nil {
		m.Extras = make(map[string]sampleValue)
	}
	m.Extras["cpu.load"] = sampleValue{} // all invalid -> N/A, not legacy zero
	var allBusy, allTotal uint64
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
			allBusy += busySum
			allTotal += totalSum
		}
	}
	// No maximum/mean of per-socket percentages: use actual elapsed time
	// from every valid logical CPU across all processor groups.
	if allTotal > 0 {
		m.setExtra("cpu.load", 100*float64(allBusy)/float64(allTotal))
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
