//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
	"unsafe"
)

func fakeWindowsSocketRecord(mask uint64, group uint16) []byte {
	b := make([]byte, 48)
	binary.LittleEndian.PutUint32(b[0:4], 3)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)))
	binary.LittleEndian.PutUint16(b[30:32], 1)
	binary.LittleEndian.PutUint64(b[32:40], mask)
	binary.LittleEndian.PutUint16(b[40:42], group)
	return b
}

type syntheticProcessorMask struct {
	mask  uint64
	group uint16
}

func fakeWindowsMultiGroupSocket(masks ...syntheticProcessorMask) []byte {
	b := make([]byte, 32+16*len(masks))
	binary.LittleEndian.PutUint32(b[0:4], 3)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)))
	binary.LittleEndian.PutUint16(b[30:32], uint16(len(masks)))
	for i, item := range masks {
		offset := 32 + i*16
		binary.LittleEndian.PutUint64(b[offset:offset+8], item.mask)
		binary.LittleEndian.PutUint16(b[offset+8:offset+10], item.group)
	}
	return b
}
func TestWindowsCPUTopologyPackageMasks(t *testing.T) {
	raw := append(fakeWindowsSocketRecord(0b00001111, 0), fakeWindowsSocketRecord(0b11110000, 0)...)
	packages, err := parseWindowsCPUPackages(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 || len(packages[0]) != 4 || len(packages[1]) != 4 ||
		packages[0][0] != 0 || packages[1][0] != 4 {
		t.Fatalf("package mapping %+v", packages)
	}
	if _, err := parseWindowsCPUPackages(append(fakeWindowsSocketRecord(3, 0), fakeWindowsSocketRecord(2, 0)...)); err == nil {
		t.Fatal("overlapping physical package masks must be rejected")
	}
	if packages, err := parseWindowsCPUPackages(fakeWindowsSocketRecord(3, 1)); err != nil || len(packages) != 1 || packages[0][0] != 64 || packages[0][1] != 65 {
		t.Fatalf("group 1 must map to distinct processor keys 64/65: %v, %v", packages, err)
	}
	if _, err := parseWindowsCPUPackages(fakeWindowsSocketRecord(1, 64)); err == nil {
		t.Fatal("out-of-range group must be rejected")
	}
	if _, err := parseWindowsCPUPackages(append(fakeWindowsSocketRecord(1, 0), fakeWindowsSocketRecord(1, 1)...)); err != nil {
		t.Errorf("same bit number in separate groups must not overlap: %v", err)
	}
	if _, err := parseWindowsCPUPackages(fakeWindowsSocketRecord(0, 0)); err == nil {
		t.Fatal("package with no logical processors must be rejected")
	}
	bad := fakeWindowsSocketRecord(3, 0)
	binary.LittleEndian.PutUint32(bad[4:8], 4096)
	if _, err := parseWindowsCPUPackages(bad); err == nil {
		t.Fatal("out-of-bounds processor record accepted")
	}
}

func TestWindowsCrossGroupPackageTopology(t *testing.T) {
	// CPU0 spans two processor groups. CPU1 stays within group 1.
	data := append(fakeWindowsMultiGroupSocket(
		syntheticProcessorMask{mask: 0b11, group: 0},
		syntheticProcessorMask{mask: 0b11, group: 1}),
		fakeWindowsSocketRecord(0b1100, 1)...)
	packages, e := parseWindowsCPUPackages(data)
	if e != nil {
		t.Fatal(e)
	}
	if len(packages) != 2 || len(packages[0]) != 4 || len(packages[1]) != 2 ||
		packages[0][0] != 0 || packages[0][2] != 64 || packages[1][0] != 66 {
		t.Fatalf("failed to map cross-group socket: %+v", packages)
	}
	if !validWindowsPackageGroups(packages, []int{2, 4}) {
		t.Fatal("complete two-group topology falsely rejected")
	}
	for _, counts := range [][]int{{2, 3}, {2, 5}, {4, 4}, {2, 0}} {
		if validWindowsPackageGroups(packages, counts) {
			t.Errorf("mismatched active group counts %v accepted", counts)
		}
	}
	if validWindowsPackageGroups([][]int{{0, 1}, {1, 64, 65, 66, 67}}, []int{2, 4}) {
		t.Fatal("duplicate logical CPU across packages accepted")
	}
	// Use a group*64-strided counter array, as returned by the Ex API.
	prev, current := make([]cpuTimes, 128), make([]cpuTimes, 128)
	for _, id := range packages[0] {
		prev[id] = cpuTimes{Kernel: 100, Idle: 50}
		current[id] = cpuTimes{Kernel: 200, Idle: 75} // 75% busy
	}
	for _, id := range packages[1] {
		prev[id] = cpuTimes{Kernel: 100, Idle: 50}
		current[id] = cpuTimes{Kernel: 200, Idle: 125} // 25% busy
	}
	var m metrics
	populateWindowsCPUUsage(&m, packages, prev, current)
	for key, expected := range map[string]float64{
		"cpu0.load":       75,
		"cpu1.load":       25,
		"cpu.load":        350.0 / 6, // 4*75% + 2*25% over six logical CPUs
		"cpu0.core2.load": 75,
		"cpu1.core0.load": 25,
	} {
		value, valid := readMetric(m, key)
		if !valid || math.Abs(value-expected) > 1e-5 {
			t.Errorf("%s got %.4f valid=%v want %.4f", key, value, valid, expected)
		}
	}
}
func TestWindowsPerCPUCounterMath(t *testing.T) {
	if n := unsafe.Sizeof(cpuTimes{}); n != 48 {
		t.Fatalf("NT per-processor ABI %d, need 48 bytes", n)
	}
	prev := cpuTimes{Idle: 30, Kernel: 100, User: 70}
	next := cpuTimes{Idle: 60, Kernel: 180, User: 90}
	busy, total, valid := cpuTimesDelta(prev, next)
	if !valid || busy != 70 || total != 100 {
		t.Fatalf("unexpected load %.2f valid=%v", 100*float64(busy)/float64(total), valid)
	}
	for _, invalid := range []cpuTimes{
		{Idle: 10, Kernel: 180, User: 90}, // counter reset
		{Idle: 60, Kernel: 90, User: 90},
		{Idle: 30, Kernel: 100, User: 70},  // no elapsed time
		{Idle: 131, Kernel: 180, User: 90}, // idle grows faster than total
	} {
		if _, _, ok := cpuTimesDelta(prev, invalid); ok {
			t.Errorf("invalid processor time accepted: %+v", invalid)
		}
	}
}

func TestWindowsMultiSocketWeightedLoadAndNA(t *testing.T) {
	packages := [][]int{{0, 1}, {2, 3}}
	prev := []cpuTimes{
		{Idle: 40, Kernel: 100}, {Idle: 40, Kernel: 100},
		{Idle: 40, Kernel: 100}, {Idle: 40, Kernel: 100},
	}
	next := []cpuTimes{
		{Idle: 40, Kernel: 200},  // CPU0 logical0 100/100 busy
		{Idle: 240, Kernel: 400}, // CPU0 logical1 100/300 busy
		{Idle: 30, Kernel: 150},  // CPU1 logical0 counter reset -> N/A
		{Idle: 140, Kernel: 200}, // CPU1 logical1 0/100 busy
	}
	var m metrics
	populateWindowsCPUUsage(&m, packages, prev, next)
	if v, ok := readMetric(m, "cpu0.load"); !ok || v != 50 {
		t.Fatalf("socket0 must sum busy/total time, got %.2f valid=%v", v, ok)
	}
	if v, ok := readMetric(m, "cpu1.load"); !ok || v != 0 {
		t.Fatalf("socket1 valid 0%% must remain valid, got %.2f valid=%v", v, ok)
	}
	if _, ok := readMetric(m, "cpu1.core0.load"); ok {
		t.Fatal("reset counter cannot be represented as 0%%")
	}
	if v, ok := readMetric(m, "cpu0.core1.load"); !ok || math.Abs(v-100.0/3) > 0.001 {
		t.Fatalf("per-thread utilization %.3f valid=%v", v, ok)
	}
	m = metrics{}
	populateWindowsCPUUsage(&m, packages, prev, prev)
	for _, name := range []string{"cpu.load", "cpu0.load", "cpu1.load", "cpu0.core0.load"} {
		if _, ok := readMetric(m, name); ok {
			t.Errorf("%s expected N/A on all-invalid deltas", name)
		}
	}
}
func TestWindowsNativePerCoreSampler(t *testing.T) {
	var sampler cpuDetailSampler
	if !sampler.ensureTopology() {
		t.Skip("Windows topology is multi-group or inaccessible")
	}
	var n int
	for _, pkg := range sampler.packages {
		n += len(pkg)
	}
	raw, ok := readWindowsPerCPUTimes(n)
	if !ok {
		t.Skip("OS does not expose per-logical-processor performance information")
	}
	if len(raw) != n || len(sampler.packages) != physicalCPUCount() {
		t.Fatalf("native CPU topology: groups=%v times=%d packages=%d", sampler.packages, len(raw), physicalCPUCount())
	}
	var m metrics
	sampler.Sample(&m) // first sample is a baseline, not a fabricated 0% CPU load
	if _, valid := readMetric(m, "cpu0.core0.load"); valid {
		t.Fatal("first per-core sample must be N/A until time elapses")
	}
	time.Sleep(270 * time.Millisecond)
	m = metrics{}
	sampler.Sample(&m)
	valid := 0
	for socket, ids := range sampler.packages {
		var packagesLoaded int
		for logical := range ids {
			key := indexedMetricName("cpu", socket, coreSuffix(logical, "load"))
			if v, ok := readMetric(m, key); ok {
				valid++
				packagesLoaded++
				if v < 0 || v > 100 || math.IsNaN(v) {
					t.Errorf("%s invalid value %.3f", key, v)
				}
			}
		}
		if packagesLoaded > 0 {
			if value, ok := readMetric(m, indexedMetricName("cpu", socket, "load")); !ok || value < 0 || value > 100 {
				t.Errorf("socket %d has per-core readings but no valid aggregate", socket)
			}
		}
	}
	if valid == 0 {
		t.Fatal("Windows native per-CPU counters returned no valid load readings")
	}
	t.Logf("sockets=%d logical processors=%d valid per-core samples=%d clock0=%v",
		len(sampler.packages), n, valid, m.Extras["cpu0.core0.clock"])
}

func TestWindowsNativeExGroupZeroABI(t *testing.T) {
	count, _, _ := winCPUCountAPI.Call(0)
	if count < 1 || count > 64 {
		t.Skipf("unsupported Windows group size: %d", count)
	}
	samples, ok := readWindowsCPUGroupWithEx(0, int(count))
	if !ok {
		t.Skip("NtQuerySystemInformationEx processor-group performance query unavailable")
	}
	if len(samples) != int(count) {
		t.Fatalf("group0 returned %d entries, wanted %d", len(samples), count)
	}
	for i, sample := range samples {
		if sample.Kernel <= 0 || sample.User < 0 || sample.Idle < 0 {
			t.Fatalf("group0 processor%d returned invalid time counters: %+v", i, sample)
		}
	}
	t.Logf("native group0 NtQuerySystemInformationEx returned %d valid counters", len(samples))
}
