// SPDX-License-Identifier: GPL-3.0-or-later
// Minimal Windows HID segment-display controller, ported from
// Lexonight1/thermalright-trcc-linux (GPL-3.0).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

type deviceConfig struct {
	Path  string `json:"path"`
	PM    int    `json:"pm"`
	Sub   int    `json:"sub"`
	Style int    `json:"style"`
	Model string `json:"model"`
}

func configFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "trcc-mini-device.json"
	}
	return filepath.Join(dir, "trcc-mini", "device.json")
}
func loadConfig() (deviceConfig, error) {
	var c deviceConfig
	b, err := os.ReadFile(configFile())
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}
func saveConfig(c deviceConfig) error {
	path := configFile()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
func styleFromPM(pm int, magic []byte) (int, string) {
	if len(magic) >= 4 && magic[0] == 0xdc && magic[1] == 0xdd && magic[2] == 0xaa && magic[3] == 0x01 {
		return 13, "Magic Qube"
	}
	switch {
	case pm >= 1 && pm <= 3:
		return 1, "AX120 Digital"
	case pm >= 16 && pm <= 31:
		return 2, "PA120 Digital"
	case pm == 32:
		return 3, "AK120 Digital"
	case pm == 48 || pm == 49 || pm == 176:
		return 5, "LF8/LF25"
	case pm == 80:
		return 6, "LF12"
	case pm == 96:
		return 7, "LF10"
	case pm == 112:
		return 9, "LC2"
	case pm == 128:
		return 4, "LC1"
	case pm == 129:
		return 10, "LF11"
	case pm == 144:
		return 11, "LF15"
	case pm == 160:
		return 12, "LF13"
	case pm == 208:
		return 8, "CZ1"
	}
	return 0, "unknown"
}
func usage() {
	fmt.Println(`TRCC Mini — Windows HID CLI

Usage:
  trcc-mini config init                 Create trcc-mini.json beside the EXE
  trcc-mini config check                Validate configuration without USB
  trcc-mini config show                 Print effective settings
  trcc-mini config path                 Print configuration path
  trcc-mini run                         Run the configured CPU/GPU display
  trcc-mini once                        Render one frame (never writes USB)
  trcc-mini sensors                     Read-only CPU/GPU diagnostics
  trcc-mini detect                      Read-only HID enumeration
  trcc-mini probe                       Detect and cache hardware model
  trcc-mini help                        Show help

Options:
  -c, --config FILE                     Explicit JSON configuration path
  -v, --verbose                         Detailed sensor/frame logging
  -n, --dry-run                         Do not write USB (run only)
  -h, --help                            Show help
  --force-probe                         Force a new handshake (probe only)

Sensor sources, GPU, power, brightness, and zone colors are configured
exclusively in trcc-mini.json. No legacy CLI flags or override aliases.
Stop the original TRCC software before sending USB frames.`)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		return nil
	}
	verb := os.Args[1]
	if verb == "config" {
		return configCommand(os.Args[2:])
	}
	if verb == "sensors" {
		if len(os.Args) == 2 {
			return sensorDiagnostics()
		}
		if len(os.Args) == 3 && (os.Args[2] == "-h" || os.Args[2] == "--help") {
			usage()
			return nil
		}
		return errors.New("usage: trcc-mini sensors")
	}
	if verb != "detect" && verb != "probe" && verb != "run" && verb != "once" {
		return fmt.Errorf("unknown command %q (run 'trcc-mini help')", verb)
	}
	opts, err := parseCommandOptions(verb, os.Args[2:])
	if err != nil {
		return err
	}
	if opts.Help {
		usage()
		return nil
	}
	if verb == "once" {
		opts.DryRun = true
	}
	if verb == "detect" {
		return printDevices()
	}

	var config appConfig
	if verb == "probe" {
		config = defaultAppConfig()
	} else {
		path := resolveSettingsPath(opts.ConfigPath)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("configuration %q unavailable (%w): run 'trcc-mini config init'", path, err)
		}
		config, err = readAppConfig(path)
		if err != nil {
			return err
		}
	}
	period, _ := time.ParseDuration(config.Display.Interval)
	phasePeriod, _ := time.ParseDuration(config.Display.PhaseInterval)
	baseColor, err := parseColor(config.Theme.Color)
	if err != nil {
		return err
	}
	powerColor, err := parseColor(config.Theme.PowerColor)
	if err != nil {
		return err
	}
	const timeout = 400 * time.Millisecond
	paths, err := enumerateHID()
	if err != nil {
		return err
	}
	if len(paths) == 0 && !opts.DryRun {
		return errors.New("USB 0416:8001 not found")
	}
	path := ""
	if len(paths) > 0 {
		path = paths[0]
	}
	if len(paths) > 1 {
		return fmt.Errorf("multiple 0416:8001 devices (%d); refusing ambiguous device selection", len(paths))
	}
	cached, cacheErr := loadConfig()
	if verb == "probe" {
		if opts.DryRun {
			return errors.New("probe requires real USB access")
		}
		if cacheErr == nil && cached.Style > 0 && strings.EqualFold(path, cached.Path) && !opts.ForceProbe {
			return fmt.Errorf("known device PM=%d style=%d already cached; refusing a second one-time handshake (use -force-probe only after power cycling)", cached.PM, cached.Style)
		}
		d, err := openHID(path)
		if err != nil {
			return fmt.Errorf("open HID: %w (close vendor TRCC first)", err)
		}
		defer d.Close()
		fmt.Println("Sending the one-time 64-byte HID handshake; do not repeatedly probe this power cycle.")
		payload := [64]byte{0xda, 0xdb, 0xdc, 0xdd}
		payload[12] = 1
		time.Sleep(50 * time.Millisecond)
		if err := d.Send(payload[:], timeout); err != nil {
			return fmt.Errorf("handshake write: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
		resp, err := d.Receive(timeout)
		if err != nil {
			return fmt.Errorf("handshake read: %w; device may already have answered once this power cycle", err)
		}
		modelPM, sub := int(resp[5]), int(resp[4])
		style, model := styleFromPM(modelPM, resp[:4])
		fmt.Printf("response: %x PM=%d SUB=%d => style=%d %s\n", resp[:16], modelPM, sub, style, model)
		if style == 0 {
			return errors.New("unknown PM: refusing to store an unsupported model")
		}
		if err := saveConfig(deviceConfig{Path: path, PM: modelPM, Sub: sub, Style: style, Model: model}); err != nil {
			return err
		}
		fmt.Println("Cached model. Next launches skip the one-time handshake.")
		return nil
	}
	style := 0
	sub := 0
	if config.Device.PM > 0 {
		var name string
		style, name = styleFromPM(config.Device.PM, nil)
		if style == 0 {
			return fmt.Errorf("unknown product-model byte %d", config.Device.PM)
		}
		fmt.Printf("PM %d -> %s (style %d)\n", config.Device.PM, name, style)
		if config.Device.PM == 129 || config.Device.PM == 176 {
			sub = 1
		}
	}
	if style == 0 && cacheErr == nil && strings.EqualFold(path, cached.Path) {
		style = cached.Style
		if cached.PM == 129 || cached.PM == 176 {
			sub = 1
		}
		fmt.Printf("Using cached device: PM=%d style=%d %s\n", cached.PM, style, cached.Model)
	}
	if style == 0 {
		return errors.New("device style unknown: run 'probe' once (after closing vendor app), or set device.pm in trcc-mini.json")
	}
	if !supportsStyle(style) {
		return fmt.Errorf("style %d has a different use (clock/disk/memory) or is not yet supported by this CPU display CLI", style)
	}
	needCPUPower, needGPUPower := powerSensorsRequired(config.Display.Slots, style, config.Derived)
	needRAM, needVRAM := memorySensorsRequired(config.Display.Slots, style)
	if verb == "once" {
		opts.Verbose = true
	}
	var d *hidDevice
	if !opts.DryRun {
		d, err = openHID(path)
		if err != nil {
			return fmt.Errorf("open HID: %w (close vendor TRCC first)", err)
		}
		defer func() {
			if d != nil {
				d.Close()
			}
		}()
	}
	var cpu cpuLoadSampler
	pawn, err := openPawn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NOTE: CPU temperature source %s unavailable: %v; CPU temperature digits will be blank\n", config.Sensors.CPUTemperature, err)
	} else {
		defer pawn.Close()
	}
	var gpu *nvmlSensor
	if config.Sensors.GPU {
		gpu, err = openNVML()
		if err != nil {
			fmt.Fprintln(os.Stderr, "NOTE: NVIDIA GPU sensor unavailable:", err)
		} else {
			defer gpu.Close()
		}
	} else if style == 2 {
		fmt.Fprintln(os.Stderr, "NOTE: PA120 GPU row disabled in config (sensors.gpu=false)")
	}
	// A single frame cannot derive power from a cumulative energy counter:
	// prime once, then allow a real interval before taking the render sample.
	// This is read-only and affects only the explicit one-shot diagnostic.
	if verb == "once" && pawn != nil && needCPUPower {
		_, _, e := pawn.ReadPower()
		if e != nil && opts.Verbose {
			fmt.Fprintln(os.Stderr, "CPU watts:", e)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var previous []byte
	start := time.Now()
	nextPhase := -1
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Printf("Running: style=%d, sampling=%s, cpu-temp-source=%s, link=%s, brightness=%d%%, dry=%v\n", style, period.String(), config.Sensors.CPUTemperature, config.Theme.Link, config.Display.Brightness, opts.DryRun)
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		m := metrics{Load: cpu.Sample()}
		if pawn != nil {
			if t, e := pawn.ReadTemp(config.Sensors.CPUTemperature); e == nil {
				m.Temp = t
				m.TempValid = true
			} else if opts.Verbose {
				fmt.Fprintln(os.Stderr, "CPU temperature:", e)
			}
			if needCPUPower {
				watts, ok, e := pawn.ReadPower()
				if e == nil {
					m.CPUPower = watts
					m.CPUPowerValid = ok
				} else if opts.Verbose {
					fmt.Fprintln(os.Stderr, "CPU watts:", e)
				}
			}
		}
		if gpu != nil {
			m.GPUTemp, m.GPUTempValid, m.GPULoad, m.GPULoadValid = gpu.Read()
			if needGPUPower {
				m.GPUPower, m.GPUPowerValid = gpu.ReadPower()
			}
		}
		// Memory sensors are native Windows/NVML APIs; no third-party
		// service, privileged driver, or background process is required.
		if needRAM {
			if used, total, usage, ok := readSystemMemory(); ok {
				m.setExtra("memory.used", used)
				m.setExtra("memory.total", total)
				m.setExtra("memory.load", usage)
			}
		}
		if needVRAM && gpu != nil {
			if used, total, usage, ok := gpu.ReadMemory(); ok {
				m.setExtra("gpu.memory.used", used)
				m.setExtra("gpu.memory.total", total)
				m.setExtra("gpu.memory.load", usage)
			}
		}
		applyDerived(&m, config.Derived)
		phase := int(time.Since(start) / phasePeriod)
		switch style {
		case 1, 8, 13:
			if m.GPUTempValid && m.GPULoadValid {
				phase %= 4
			} else {
				phase %= 2
			}
			if !m.TempValid && phase == 0 {
				phase = 1
			}
		case 3, 5, 6:
			if m.GPUTempValid && m.GPULoadValid {
				phase %= 2
			} else {
				phase = 0
			}
		default:
			phase = 0
		}
		var selected map[string]slotSelection
		var mask []bool
		if style == 2 {
			selected = resolveDisplaySlots(config.Display.Slots, m, time.Since(start), config.Indicators)
			mask = renderPA120Slots(selected, config.Display.RowLabels)
		} else {
			mask, err = renderWithPower(style, m, phase, m.CPUPowerValid, m.GPUPowerValid)
			if err != nil {
				return err
			}
		}
		color := baseColor
		if config.Theme.Link == "load" {
			color = thresholdColor(float64(m.Load))
		}
		if config.Theme.Link == "temp" && m.TempValid {
			color = thresholdColor(m.Temp)
		}
		if config.Theme.Link == "gpu-temp" && m.GPUTempValid {
			color = thresholdColor(m.GPUTemp)
		}
		if config.Theme.Link == "gpu-load" && m.GPULoadValid {
			color = thresholdColor(float64(m.GPULoad))
		}
		var packet []byte
		if style == 2 {
			colors := buildPA120Colors(config.Theme, m, selected)
			if config.Theme.Link != "static" {
				colors = globalPA120Colors(config.Theme, selected, color)
			}
			metricColorOverrides(colors, selected, config.Indicators)
			packet = makeFrameColors(style, sub, mask, colors, config.Display.Brightness)
		} else {
			packet = makeFramePower(style, sub, mask, color, config.Display.Brightness, m.CPUPowerValid, m.GPUPowerValid, powerColor)
		}
		changed := !bytesEqual(previous, packet)
		var e error
		if changed || time.Since(start) < period || phase != nextPhase {
			if opts.DryRun {
				if opts.Verbose {
					fmt.Printf("DRY frame: %d bytes, activeLED=%d, CPU load=%d%% temp=%s watts=%s, GPU load=%s temp=%s watts=%s, phase=%d, prefix=%x\n", len(packet), activeCount(mask), m.Load, m.TempString(), wattStatus(m.CPUPower, m.CPUPowerValid), m.GPULoadString(), m.GPUTempString(), wattStatus(m.GPUPower, m.GPUPowerValid), phase, packet[:minInt(len(packet), 22)])
					if style == 2 {
						fmt.Println(slotSummary(selected))
					}
				}
			} else {
				if d == nil {
					var found []string
					found, err = enumerateHID()
					if err == nil && len(found) == 1 {
						path = found[0]
						d, err = openHID(path)
					}
				}
				if d == nil {
					e = errors.New("device unavailable; trying to reconnect next tick")
				} else {
					e = d.SendFrame(packet, timeout)
				}
				if e != nil {
					fmt.Fprintln(os.Stderr, "USB frame failed:", e)
					if d != nil {
						d.Close()
						d = nil
					}
					previous = nil // retry next tick even if the data is unchanged
				} else {
					if opts.Verbose {
						fmt.Printf("CPU load=%d%% temp=%s; GPU load=%s temp=%s; phase=%d LEDs=%d\n", m.Load, m.TempString(), m.GPULoadString(), m.GPUTempString(), phase, activeCount(mask))
						if style == 2 {
							fmt.Println(slotSummary(selected))
						}
					}
					previous = packet
				}
			}
			if opts.DryRun {
				previous = packet
			}
		}
		nextPhase = phase
		if verb == "once" {
			return nil
		}
		select {
		case <-ctx.Done():
			fmt.Println("Stopped")
			return nil
		case <-ticker.C:
		}
	}
}
func wattStatus(value float64, valid bool) string {
	if !valid {
		return "N/A"
	}
	return fmt.Sprintf("%.1fW", value)
}
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func activeCount(mask []bool) int {
	n := 0
	for _, v := range mask {
		if v {
			n++
		}
	}
	return n
}
