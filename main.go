// SPDX-License-Identifier: GPL-3.0-or-later
// Minimal cross-platform HID segment-display controller, ported from
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
	fmt.Println(`TRCC Mini — HID display CLI

Usage:
  trcc-mini config init                 Create trcc-mini.json beside the EXE
  trcc-mini config check                Validate configuration without USB
  trcc-mini config show                 Print effective settings
  trcc-mini config path                 Print configuration path
  trcc-mini run                         Run the configured CPU/GPU display
  trcc-mini once                        Render one frame (never writes USB)
  trcc-mini sensors                     Read-only CPU/GPU diagnostics
  trcc-mini sensors list [--json]       Indexed CPU/GPU inventory and metrics
  trcc-mini detect                      Read-only HID enumeration
  trcc-mini probe                       Detect and cache hardware model
  trcc-mini service install             Install auto-start Windows service (admin)
  trcc-mini service start               Start installed service
  trcc-mini service stop                Stop installed service
  trcc-mini service status              Query Windows service state
  trcc-mini service uninstall           Unregister stopped Windows service
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
		if len(os.Args) > 2 && os.Args[2] == "list" {
			return sensorListCommand(os.Args[3:])
		}
		if len(os.Args) == 3 && (os.Args[2] == "-h" || os.Args[2] == "--help") {
			usage()
			return nil
		}
		return errors.New("usage: trcc-mini sensors")
	}
	if verb == "service" {
		return serviceCommand(os.Args[2:])
	}
	if verb == "service-run" {
		return runManagedService(os.Args[2:])
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
	return runDisplay(verb, opts, context.Background(), nil)
}

// runDisplay is shared by console and Windows Service Control Manager.
// A service supplies a cancellable context and a callback invoked only when
// the device/sensors have been prepared successfully.
func runDisplay(verb string, opts commandOptions, parent context.Context, ready func()) error {
	var config appConfig
	configPath := resolveSettingsPath(opts.ConfigPath)
	if verb == "probe" {
		config = defaultAppConfig()
	} else {
		if _, err := os.Stat(configPath); err != nil {
			return fmt.Errorf("configuration %q unavailable (%w): run 'trcc-mini config init'", configPath, err)
		}
		var err error
		config, err = readAppConfig(configPath)
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
	if len(paths) == 0 && !opts.DryRun && ready == nil {
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
	var d hidTransport
	if !opts.DryRun {
		if path != "" {
			d, err = openHID(path)
			if err != nil && ready == nil {
				return fmt.Errorf("open HID: %w (close vendor TRCC first)", err)
			}
		}
		if d == nil && ready != nil {
			fmt.Fprintln(os.Stderr, "SERVICE: HID not ready; retrying connection without probing or stopping the service")
		}
		defer func() {
			if d != nil {
				d.Close()
			}
		}()
	}
	var cpu cpuLoadSampler
	pawn, err := openCPUSensor()
	defer func() {
		if pawn != nil {
			pawn.Close()
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "NOTE: CPU temperature source %s unavailable: %v; CPU temperature digits will be blank\n", config.Sensors.CPUTemperature, err)
	}
	var gpu gpuTelemetry
	defer func() {
		if gpu != nil {
			gpu.Close()
		}
	}()
	if config.Sensors.GPU {
		gpu, err = openGPUSensor()
		if err != nil {
			fmt.Fprintln(os.Stderr, "NOTE: GPU sensor unavailable:", err)
		}
	} else if style == 2 {
		fmt.Fprintln(os.Stderr, "NOTE: PA120 GPU row disabled in config (sensors.gpu=false)")
	}
	lastPawnRetry, lastGPURetry := time.Now(), time.Now()
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
	var reloader *configReloader
	if verb == "run" {
		reloader = newConfigReloader(configPath, config)
	}
	var previous []byte
	var lastUSBError string
	var lastUSBErrorAt time.Time
	var reconnectAfter time.Time
	start := time.Now()
	nextPhase := -1
	ctx, stop := signal.NotifyContext(parent, serviceSignals()...)
	defer stop()
	fmt.Printf("Running: style=%d, sampling=%s, cpu-temp-source=%s, link=%s, brightness=%d%%, dry=%v\n", style, period.String(), config.Sensors.CPUTemperature, config.Theme.Link, config.Display.Brightness, opts.DryRun)
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	lastConfigCheck := time.Now()
	if ready != nil {
		ready()
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		// USB, GPU and CPU sensors may become available after service boot. Keep
		// the auto-start service alive rather than requiring a manual restart.
		if pawn == nil && time.Since(lastPawnRetry) >= 10*time.Second {
			lastPawnRetry = time.Now()
			if restored, e := openCPUSensor(); e == nil {
				pawn = restored
				fmt.Println("CPU temperature/power sensor connected")
			}
		}
		if gpu == nil && config.Sensors.GPU && time.Since(lastGPURetry) >= 10*time.Second {
			lastGPURetry = time.Now()
			if restored, e := openGPUSensor(); e == nil {
				gpu = restored
				fmt.Println("GPU telemetry sensor connected")
			}
		}
		// Content hash catches atomic rename and same-mtime rewrites. It also
		// keeps invalid partial writes from replacing active state.
		if reloader != nil && time.Since(lastConfigCheck) >= 500*time.Millisecond {
			lastConfigCheck = time.Now()
			var stagedGPU gpuTelemetry
			updated, reloadErr := reloader.check(func(old, next appConfig) error {
				if !old.Sensors.GPU && next.Sensors.GPU {
					var e error
					stagedGPU, e = openGPUSensor()
					if e != nil {
						return fmt.Errorf("GPU enable requires available telemetry provider: %w", e)
					}
				}
				return nil
			})
			if reloadErr != nil {
				fmt.Fprintln(os.Stderr, "CONFIG:", reloadErr)
			}
			if !updated && stagedGPU != nil {
				stagedGPU.Close()
			}
			if updated {
				oldGPUEnabled := config.Sensors.GPU
				config = reloader.current
				if oldGPUEnabled && !config.Sensors.GPU && gpu != nil {
					gpu.Close()
					gpu = nil
				}
				if stagedGPU != nil {
					gpu = stagedGPU
				}
				period, _ = time.ParseDuration(config.Display.Interval)
				phasePeriod, _ = time.ParseDuration(config.Display.PhaseInterval)
				baseColor, _ = parseColor(config.Theme.Color)
				powerColor, _ = parseColor(config.Theme.PowerColor)
				needCPUPower, needGPUPower = powerSensorsRequired(config.Display.Slots, style, config.Derived)
				needRAM, needVRAM = memorySensorsRequired(config.Display.Slots, style)
				ticker.Reset(period)
				start = time.Now()
				previous = nil // force immediate frame after successful reload
				fmt.Printf("CONFIG reloaded: interval=%s brightness=%d%% GPU=%t\n", period, config.Display.Brightness, config.Sensors.GPU)
			}
		}
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
		populatePlatformCPUMetrics(&m, pawn)
		populateCPUIndexed(&m)
		if gpu != nil {
			m.GPUTemp, m.GPUTempValid, m.GPULoad, m.GPULoadValid = gpu.Read()
			if needGPUPower {
				m.GPUPower, m.GPUPowerValid = gpu.ReadPower()
			}
		}
		// RAM/VRAM providers expose a common unit across OS implementations.
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
		if indexed, ok := gpu.(indexedGPU); ok {
			indexed.SampleIndexed(&m)
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
					if !time.Now().Before(reconnectAfter) {
						reconnectAfter = time.Now().Add(2 * time.Second)
						found, enumerateErr := enumerateHID()
						if enumerateErr == nil && len(found) == 1 {
							path = found[0]
							d, err = openHID(path)
						}
					}
				}
				if d == nil {
					e = errors.New("device unavailable; trying to reconnect next tick")
				} else {
					e = d.SendFrame(packet, timeout)
				}
				if e != nil {
					// A long-lived service must not flood its disk log if the USB
					// cable is disconnected or another process owns the device.
					if e.Error() != lastUSBError || time.Since(lastUSBErrorAt) >= 10*time.Second {
						fmt.Fprintln(os.Stderr, "USB frame failed:", e)
						lastUSBError, lastUSBErrorAt = e.Error(), time.Now()
					}
					if d != nil {
						d.Close()
						d = nil
					}
					previous = nil // retry next tick even if the data is unchanged
				} else {
					lastUSBError = ""
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
