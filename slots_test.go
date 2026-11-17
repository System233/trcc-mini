package main

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func completeSlotMetrics() metrics {
	return metrics{Temp: 61.8, TempValid: true, Load: 37, CPUPower: 145.2, CPUPowerValid: true,
		GPUTemp: 47, GPUTempValid: true, GPULoad: 100, GPULoadValid: true,
		GPUPower: 29.9, GPUPowerValid: true}
}

func TestSlotSchema(t *testing.T) {
	slots := defaultDisplaySlots()
	if err := validateDisplaySlots(slots); err != nil {
		t.Fatal(err)
	}
	if len(slots) != 4 {
		t.Fatal("PA120 requires four independent physical regions")
	}
	for _, name := range slotNames {
		if len(slots[name].Metrics) == 0 {
			t.Fatalf("missing %s", name)
		}
	}
	copySlots := func() map[string]slotConfig {
		c := make(map[string]slotConfig)
		for name, slot := range slots {
			c[name] = slot
		}
		return c
	}
	cases := []struct {
		name    string
		makeBad func(map[string]slotConfig)
	}{
		{"unknown slot", func(s map[string]slotConfig) { s["cpu.unknown"] = slotConfig{Metrics: []string{"cpu.load"}} }},
		{"missing slot", func(s map[string]slotConfig) { delete(s, cpuPrimary) }},
		{"empty metrics", func(s map[string]slotConfig) { s[cpuPrimary] = slotConfig{} }},
		{"bad metric", func(s map[string]slotConfig) { s[cpuPrimary] = slotConfig{Metrics: []string{"cpu.diodes"}} }},
		{"missing interval", func(s map[string]slotConfig) {
			s[cpuPrimary] = slotConfig{Metrics: []string{"cpu.temperature", "cpu.power"}}
		}},
		{"zero interval", func(s map[string]slotConfig) {
			s[cpuPrimary] = slotConfig{Metrics: []string{"cpu.temperature", "cpu.power"}, Interval: "0s"}
		}},
		{"too fast", func(s map[string]slotConfig) {
			s[cpuPrimary] = slotConfig{Metrics: []string{"cpu.temperature", "cpu.power"}, Interval: "100ms"}
		}},
		{"duplicate metric", func(s map[string]slotConfig) {
			s[cpuPrimary] = slotConfig{Metrics: []string{"cpu.load", "cpu.load"}, Interval: "3s"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := copySlots()
			tc.makeBad(c)
			if err := validateDisplaySlots(c); err == nil {
				t.Fatal("accepted invalid slot configuration")
			}
		})
	}
}

func TestSlotIndependentRotation(t *testing.T) {
	c := defaultDisplaySlots()
	c[cpuSecondary] = slotConfig{Metrics: []string{"gpu.temperature", "cpu.load", "gpu.power"}, Interval: "2s"}
	c[gpuPrimary] = slotConfig{Metrics: []string{"gpu.temperature", "gpu.load"}, Interval: "6s"}
	m := completeSlotMetrics()
	cases := []struct {
		at                                   time.Duration
		cpuPrimary, cpuSecondary, gpuPrimary string
	}{
		{0, "cpu.temperature", "gpu.temperature", "gpu.temperature"},
		{2 * time.Second, "cpu.temperature", "cpu.load", "gpu.temperature"},
		{4 * time.Second, "cpu.power", "gpu.power", "gpu.temperature"},
		{6 * time.Second, "cpu.power", "gpu.temperature", "gpu.load"},
		{8 * time.Second, "cpu.temperature", "cpu.load", "gpu.load"},
		{12 * time.Second, "cpu.power", "gpu.temperature", "gpu.temperature"},
	}
	for _, tc := range cases {
		selected := resolveDisplaySlots(c, m, tc.at)
		if selected[cpuPrimary].Metric != tc.cpuPrimary || selected[cpuSecondary].Metric != tc.cpuSecondary || selected[gpuPrimary].Metric != tc.gpuPrimary {
			t.Errorf("at %s: got %v", tc.at, selected)
		}
		if selected[gpuSecondary].Metric != "gpu.load" {
			t.Fatal("single-metric slot changed")
		}
	}
}

func TestSkipMissingUnrepresentableMetrics(t *testing.T) {
	c := defaultDisplaySlots()
	m := completeSlotMetrics()
	// Invalid CPU watts must skip to temperature, not fabricate 0 W.
	m.CPUPowerValid = false
	if got := selectSlot(c[cpuPrimary], m, cpuPrimary, 5*time.Second); got.Metric != "cpu.temperature" {
		t.Fatalf("missing watt source should select valid temperature: %v", got)
	}
	m.TempValid = false
	if got := selectSlot(c[cpuPrimary], m, cpuPrimary, 0); got.Valid {
		t.Fatalf("no available metrics should blank slot, not display zero: %v", got)
	}
	m.CPUPowerValid = true
	m.CPUPower = 155
	c[cpuSecondary] = slotConfig{Metrics: []string{"cpu.power", "cpu.load"}, Interval: "1s"}
	if got := selectSlot(c[cpuSecondary], m, cpuSecondary, 0); got.Metric != "cpu.load" {
		t.Fatalf("unrepresentable 155W in two digits should skip: %v", got)
	}
	c[cpuSecondary] = slotConfig{Metrics: []string{"cpu.power"}}
	if got := selectSlot(c[cpuSecondary], m, cpuSecondary, 0); got.Valid {
		t.Fatalf("must not clamp 155W into 100: %v", got)
	}
	m.CPUPower = 0
	if got := selectSlot(c[cpuSecondary], m, cpuSecondary, 0); !got.Valid || got.Value != 0 {
		t.Fatalf("actual 0W should be displayable: %v", got)
	}
	m.CPUPower = math.NaN()
	if got := selectSlot(c[cpuSecondary], m, cpuSecondary, 0); got.Valid {
		t.Fatalf("NaN must not be displayed: %v", got)
	}
	m.CPUPower = -0.25
	if got := selectSlot(c[cpuSecondary], m, cpuSecondary, 0); got.Valid {
		t.Fatalf("negative power must not round into a fabricated zero: %v", got)
	}
}

func TestPA120PhysicalIndicatorsAndCrossSource(t *testing.T) {
	c := defaultDisplaySlots()
	m := completeSlotMetrics()
	temp := resolveDisplaySlots(c, m, 0)
	mt := renderPA120Slots(temp)
	if !mt[0] || !mt[1] || !mt[2] || !mt[3] || !mt[4] || !mt[7] || !mt[6] || !mt[9] {
		t.Fatalf("temperature and percent indicators missing: %v", mt)
	}
	// GPU load=100 uses dedicated hundreds segments in the two-digit region.
	if !mt[82] || !mt[83] {
		t.Fatal("GPU secondary 100% must light both hundreds segments")
	}
	watt := resolveDisplaySlots(c, m, 5*time.Second)
	mw := renderPA120Slots(watt)
	if mw[4] || mw[7] {
		t.Fatal("Celsius indicators must switch off for watts")
	}
	if !mw[0] || !mw[1] || !mw[2] || !mw[3] {
		t.Fatal("CPU and GPU hardware labels missing")
	}
	if !mw[6] || !mw[9] {
		t.Fatal("load percent indicators should remain lit")
	}
	for i := 31; i < 45; i++ {
		if mt[i] != mw[i] {
			t.Fatal("CPU load digits changed with primary rotation")
		}
	}
	for i := 66; i < 84; i++ {
		if mt[i] != mw[i] {
			t.Fatal("GPU load digits changed with primary rotation")
		}
	}

	// Source labels attached to the printed physical rows must never lie.
	c[cpuPrimary] = slotConfig{Metrics: []string{"gpu.temperature"}}
	c[cpuSecondary] = slotConfig{Metrics: []string{"gpu.load"}}
	c[gpuPrimary] = slotConfig{Metrics: []string{"cpu.temperature"}}
	c[gpuSecondary] = slotConfig{Metrics: []string{"cpu.load"}}
	cross := renderPA120Slots(resolveDisplaySlots(c, m, 0))
	if cross[0] || cross[1] || cross[2] || cross[3] || !cross[4] || !cross[7] {
		t.Fatal("cross-source metrics improperly lit physical CPU/GPU labels")
	}
	// If only the load region remains healthy, preserve the row identifier.
	c = defaultDisplaySlots()
	m.TempValid = false
	m.CPUPowerValid = false
	loadOnly := renderPA120Slots(resolveDisplaySlots(c, m, 0))
	if !loadOnly[0] || !loadOnly[1] || loadOnly[4] {
		t.Fatal("CPU load-only row needs CPU label but no Celsius indicator")
	}
}

func TestSlotsEachRegionIndependentAndColorMapped(t *testing.T) {
	c := defaultAppConfig()
	m := completeSlotMetrics()
	c.Display.Slots[cpuPrimary] = slotConfig{Metrics: []string{"gpu.power"}}
	c.Display.Slots[cpuSecondary] = slotConfig{Metrics: []string{"cpu.power"}}
	c.Display.Slots[gpuPrimary] = slotConfig{Metrics: []string{"cpu.load"}}
	c.Display.Slots[gpuSecondary] = slotConfig{Metrics: []string{"gpu.temperature"}}
	m.CPUPower = 99
	selected := resolveDisplaySlots(c.Display.Slots, m, 0)
	mask := renderPA120Slots(selected)
	if mask[4] || mask[7] || mask[6] || mask[9] {
		t.Fatal("stale Celsius or percent icon from original fixed layout")
	}
	if !selected[cpuPrimary].Valid || !selected[cpuSecondary].Valid || !selected[gpuPrimary].Valid || !selected[gpuSecondary].Valid {
		t.Fatal("failed to map arbitrary sensor to each physical slot")
	}
	colors := buildPA120Colors(c.Theme, m, selected)
	if colors[10] != parseRGB("#FFB000") || colors[31] != parseRGB("#FFB000") {
		t.Fatal("watt digits in primary/secondary must take their own zone power color")
	}
	if colors[45] != parseRGB("#A78BFA") || colors[66] != parseRGB("#A78BFA") {
		t.Fatal("non-power digits must preserve zone color")
	}
	packet := makeFrameColors(2, 0, mask, colors, 75)
	if len(packet) != 272 {
		t.Fatalf("bad 84-LED PA120 wire frame length: %d", len(packet))
	}
}

func TestOnlyRequiredPowerSources(t *testing.T) {
	slots := defaultDisplaySlots()
	cpu, gpu := powerSensorsRequired(slots, 2)
	if !cpu || !gpu {
		t.Fatal("default primary slots need both watt sensors")
	}
	slots[cpuPrimary] = slotConfig{Metrics: []string{"gpu.temperature"}}
	slots[gpuPrimary] = slotConfig{Metrics: []string{"cpu.load"}}
	cpu, gpu = powerSensorsRequired(slots, 2)
	if cpu || gpu {
		t.Fatal("should not sample power when no slots request it")
	}
	slots[cpuSecondary] = slotConfig{Metrics: []string{"gpu.power"}}
	cpu, gpu = powerSensorsRequired(slots, 2)
	if cpu || !gpu {
		t.Fatal("GPU-only power routing must not read CPU MSR")
	}
	cpu, gpu = powerSensorsRequired(slots, 3)
	if !cpu || !gpu {
		t.Fatal("dedicated AK120 watt fields should still be sampled")
	}
}

func TestOnlyRequiredMemorySources(t *testing.T) {
	slots := defaultDisplaySlots()
	if ram, vram := memorySensorsRequired(slots, 2); ram || vram {
		t.Fatal("memory read should be disabled if not displayed")
	}
	slots[cpuSecondary] = slotConfig{Metrics: []string{"memory.load"}}
	if ram, vram := memorySensorsRequired(slots, 2); !ram || vram {
		t.Fatal("RAM-only metric should not read VRAM")
	}
	slots[gpuSecondary] = slotConfig{Metrics: []string{"gpu.memory.load"}}
	if ram, vram := memorySensorsRequired(slots, 2); !ram || !vram {
		t.Fatal("both memory sources should now be required")
	}
	if ram, vram := memorySensorsRequired(slots, 3); ram || vram {
		t.Fatal("non-PA120 layout must not sample unused memory")
	}
}

func TestSlotSummary(t *testing.T) {
	selected := resolveDisplaySlots(defaultDisplaySlots(), completeSlotMetrics(), 0)
	summary := slotSummary(selected)
	for _, slot := range slotNames {
		if !strings.Contains(summary, slot) {
			t.Errorf("missing %s in %q", slot, summary)
		}
	}
	if reflect.DeepEqual(selected, make(map[string]slotSelection)) {
		t.Fatal("empty selection")
	}
}
