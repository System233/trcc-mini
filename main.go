// SPDX-License-Identifier: GPL-3.0-or-later
// Minimal Windows HID segment-display controller, ported from
// Lexonight1/thermalright-trcc-linux (GPL-3.0).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
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
	fmt.Println(`TRCC Mini — portable Thermalright 0416:8001 HID CLI (Windows, no GUI)
Usage:
  trcc-mini detect                           Read-only USB enumeration
  trcc-mini probe                            One-time handshake; SAVE model to cache
  trcc-mini run   [-pm 16] [options]          CPU load + temperature display
  trcc-mini once  -pm 16 -dry-run             Generate a frame without USB writes
Options:
  -pm N                  Known product-model byte (bypasses one-time handshake)
  -style N               Explicit LED layout 1..13 (if PM unknown)
  -period 1s             Sensor sampling interval (min 200ms)
  -phase 3s              Time between temp/load rotating labels
  -color ff8800          Static RGB color (also #ff8800)
  -link static|temp|load|gpu-temp|gpu-load  RGB sensor-linked color
  -brightness 75         0..100, wire scaling includes original x0.4
  -temp 42               OVERRIDE sensor temperature for testing
  -gpu-temp 42           OVERRIDE GPU temperature for testing
  -gpu-load 42           OVERRIDE GPU load for testing
  -gpu                   Enable optional NVIDIA NVML GPU sensor
  -power off|rotate|replace  PA120: rotate/replace 3-digit temperatures with watts
  -power-period 4s       Each PA120 temperature/power display interval
  -power-color ff9a00   Color of watt digits (load digits keep regular color)
  -cpu-power 125         Test-only power override; requires -dry-run
  -gpu-power 140         Test-only power override; requires -dry-run
  -force-probe           Allow handshake despite an existing cached profile
  -dry-run               Don't open or write to USB
  -timeout 400ms          HID I/O timeout
  -verbose               Print changed readings/frames

Temperature: built-in signed AMD PawnIO module; requires separately installed
official PawnIO driver and Administrator permission. Without it, never
substitutes an unrelated motherboard temperature or fake 0°C.
Always stop the vendor TRCC software before sending frames or probing.`)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" {
		usage()
		return nil
	}
	verb := strings.ToLower(os.Args[1])
	if verb != "detect" && verb != "probe" && verb != "run" && verb != "once" {
		usage()
		return fmt.Errorf("unknown command %q", verb)
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	pm := fs.Int("pm", 0, "product model byte")
	sty := fs.Int("style", 0, "layout ID")
	period := fs.Duration("period", time.Second, "sensor interval")
	phasePeriod := fs.Duration("phase", 3*time.Second, "phase interval")
	colorFlag := fs.String("color", "ffffff", "RGB color")
	linked := fs.String("link", "static", "static|temp|load|gpu-temp|gpu-load")
	brightness := fs.Int("brightness", 75, "brightness 0..100")
	tempOverride := fs.Float64("temp", -999, "explicit test temperature")
	gpuTempOverride := fs.Float64("gpu-temp", -999, "explicit test GPU temperature")
	gpuLoadOverride := fs.Int("gpu-load", -1, "explicit test GPU load")
	gpuEnabled := fs.Bool("gpu", false, "enable NVML GPU sensor (costs additional RAM)")
	powerMode := fs.String("power", "off", "off|rotate|replace")
	powerPeriod := fs.Duration("power-period", 4*time.Second, "temperature/power interval")
	powerColorHex := fs.String("power-color", "ff9a00", "RGB watt digits")
	cpuPowerOverride := fs.Float64("cpu-power", -1, "dry-run CPU power value")
	gpuPowerOverride := fs.Float64("gpu-power", -1, "dry-run GPU power value")
	forceProbe := fs.Bool("force-probe", false, "repeat a one-time handshake")
	dryRun := fs.Bool("dry-run", false, "no USB writes")
	timeout := fs.Duration("timeout", 400*time.Millisecond, "USB I/O timeout")
	verbose := fs.Bool("verbose", false, "status logs")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *period < 200*time.Millisecond || *phasePeriod < 200*time.Millisecond {
		return errors.New("period and phase must be >=200ms")
	}
	if *powerPeriod < 200*time.Millisecond {
		return errors.New("power-period must be >=200ms")
	}
	if *powerMode != "off" && *powerMode != "rotate" && *powerMode != "replace" {
		return errors.New("power must be off, rotate or replace")
	}
	if (*cpuPowerOverride >= 0 || *gpuPowerOverride >= 0) && !*dryRun {
		return errors.New("power overrides require -dry-run")
	}
	if *brightness < 0 || *brightness > 100 {
		return errors.New("brightness must be 0..100")
	}
	if *linked != "static" && *linked != "temp" && *linked != "load" && *linked != "gpu-temp" && *linked != "gpu-load" {
		return errors.New("link must be static, temp, load, gpu-temp, or gpu-load")
	}
	if *timeout < 100*time.Millisecond {
		return errors.New("timeout must be >=100ms")
	}
	baseColor, err := parseColor(*colorFlag)
	if err != nil {
		return err
	}
	powerColor, err := parseColor(*powerColorHex)
	if err != nil {
		return err
	}
	paths, err := enumerateHID()
	if err != nil {
		return err
	}
	if verb == "detect" {
		if len(paths) == 0 {
			fmt.Println("No Thermalright 0416:8001 HID present")
			return nil
		}
		cached, _ := loadConfig()
		for i, p := range paths {
			fmt.Printf("[%d] HID 0416:8001  %s\n", i, p)
			if strings.EqualFold(p, cached.Path) {
				fmt.Printf("    cache: PM=%d style=%d (%s)\n", cached.PM, cached.Style, cached.Model)
			}
		}
		return nil
	}
	if len(paths) == 0 && !*dryRun {
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
		if *dryRun {
			return errors.New("probe requires real USB access")
		}
		if cacheErr == nil && cached.Style > 0 && strings.EqualFold(path, cached.Path) && !*forceProbe {
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
		if err := d.Send(payload[:], *timeout); err != nil {
			return fmt.Errorf("handshake write: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
		resp, err := d.Receive(*timeout)
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
	style := *sty
	sub := 0
	if *pm > 0 {
		var name string
		style, name = styleFromPM(*pm, nil)
		if style == 0 {
			return fmt.Errorf("unknown product-model byte %d", *pm)
		}
		fmt.Printf("PM %d -> %s (style %d)\n", *pm, name, style)
		if *pm == 129 || *pm == 176 {
			sub = 1
		}
	}
	if *sty != 0 {
		if style != 0 && style != *sty {
			return fmt.Errorf("-style %d conflicts with PM style %d", *sty, style)
		}
		style = *sty
	}
	if style == 0 && cacheErr == nil && strings.EqualFold(path, cached.Path) {
		style = cached.Style
		if cached.PM == 129 || cached.PM == 176 {
			sub = 1
		}
		fmt.Printf("Using cached device: PM=%d style=%d %s\n", cached.PM, style, cached.Model)
	}
	if style == 0 {
		return errors.New("device style unknown: run 'probe' once (after closing vendor app), or specify -pm N / -style N")
	}
	if !supportsStyle(style) {
		return fmt.Errorf("style %d has a different use (clock/disk/memory) or is not yet supported by this CPU display CLI", style)
	}
	if *powerMode != "off" && style != 2 && style != 3 && style != 5 && style != 6 {
		return errors.New("watt display is implemented for PA120, AK120, LF8 and LF12; first identify actual device PM")
	}
	if verb == "once" {
		*verbose = true
	}
	var d *hidDevice
	if !*dryRun {
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
	var pawn *pawnSensor
	if *tempOverride <= -200 && *cpuPowerOverride < 0 {
		var terr error
		pawn, terr = openPawn()
		if terr != nil {
			fmt.Fprintln(os.Stderr, "NOTE: CPU Tctl unavailable:", terr, "; CPU temp digits will be blank")
		}
	}
	if pawn != nil {
		defer pawn.Close()
	}
	var gpu *nvmlSensor
	if *gpuEnabled || ((*linked == "gpu-temp" && *gpuTempOverride <= -200) || (*linked == "gpu-load" && *gpuLoadOverride < 0)) {
		var gerr error
		gpu, gerr = openNVML()
		if gerr != nil {
			fmt.Fprintln(os.Stderr, "NOTE: NVIDIA GPU sensor unavailable:", gerr)
		}
	}
	if gpu != nil {
		defer gpu.Close()
	}
	var previous []byte
	start := time.Now()
	nextPhase := -1
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Printf("Running: style=%d, sampling=%s, link=%s, brightness=%d%%, dry=%v\n", style, period.String(), *linked, *brightness, *dryRun)
	ticker := time.NewTicker(*period)
	defer ticker.Stop()
	for {
		m := metrics{Load: cpu.Sample()}
		if *tempOverride > -200 {
			m.Temp = *tempOverride
			m.TempValid = true
		} else if pawn != nil {
			if t, e := pawn.Read(); e == nil {
				m.Temp = t
				m.TempValid = true
			} else if *verbose {
				fmt.Fprintln(os.Stderr, "sensor:", e)
			}
		}
		if pawn != nil && *powerMode != "off" {
			watts, ok, e := pawn.ReadPower()
			if e == nil {
				m.CPUPower = watts
				m.CPUPowerValid = ok
			} else if *verbose {
				fmt.Fprintln(os.Stderr, "CPU watts:", e)
			}
		}
		if *cpuPowerOverride >= 0 {
			m.CPUPower = *cpuPowerOverride
			m.CPUPowerValid = true
		}
		if gpu != nil {
			m.GPUTemp, m.GPUTempValid, m.GPULoad, m.GPULoadValid = gpu.Read()
			if *powerMode != "off" {
				m.GPUPower, m.GPUPowerValid = gpu.ReadPower()
			}
		}
		if *gpuTempOverride > -200 {
			m.GPUTemp = *gpuTempOverride
			m.GPUTempValid = true
		}
		if *gpuLoadOverride >= 0 && *gpuLoadOverride <= 100 {
			m.GPULoad = *gpuLoadOverride
			m.GPULoadValid = true
		}
		if *gpuPowerOverride >= 0 {
			m.GPUPower = *gpuPowerOverride
			m.GPUPowerValid = true
		}
		phase := int(time.Since(start) / *phasePeriod)
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
		showPower := *powerMode == "replace" || (*powerMode == "rotate" && int(time.Since(start) / *powerPeriod)%2 == 1)
		cpuWatts := showPower && m.CPUPowerValid
		gpuWatts := showPower && m.GPUPowerValid
		if style == 3 || style == 5 || style == 6 {
			cpuWatts = m.CPUPowerValid
			gpuWatts = m.GPUPowerValid
		}
		mask, e := renderWithPower(style, m, phase, cpuWatts, gpuWatts)
		if e != nil {
			return e
		}
		color := baseColor
		if *linked == "load" {
			color = thresholdColor(float64(m.Load))
		}
		if *linked == "temp" && m.TempValid {
			color = thresholdColor(m.Temp)
		}
		if *linked == "gpu-temp" && m.GPUTempValid {
			color = thresholdColor(m.GPUTemp)
		}
		if *linked == "gpu-load" && m.GPULoadValid {
			color = thresholdColor(float64(m.GPULoad))
		}
		packet := makeFramePower(style, sub, mask, color, *brightness, cpuWatts, gpuWatts, powerColor)
		changed := !bytesEqual(previous, packet)
		if changed || time.Since(start) < *period || phase != nextPhase {
			if *dryRun {
				if *verbose {
					fmt.Printf("DRY frame: %d bytes, activeLED=%d, CPU %d%%/%s/%.1fW(%t), GPU %d%%/%s/%.1fW(%t), wattsSlots CPU=%t GPU=%t, phase=%d, prefix=%x\n", len(packet), activeCount(mask), m.Load, m.TempString(), m.CPUPower, m.CPUPowerValid, m.GPULoad, m.GPUTempString(), m.GPUPower, m.GPUPowerValid, cpuWatts, gpuWatts, phase, packet[:minInt(len(packet), 22)])
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
					e = d.SendFrame(packet, *timeout)
				}
				if e != nil {
					fmt.Fprintln(os.Stderr, "USB frame failed:", e)
					if d != nil {
						d.Close()
						d = nil
					}
					previous = nil // retry next tick even if the data is unchanged
				} else {
					if *verbose {
						fmt.Printf("CPU load=%d%% temp=%s; GPU load=%d%% temp=%s; phase=%d LEDs=%d\n", m.Load, m.TempString(), m.GPULoad, m.GPUTempString(), phase, activeCount(mask))
					}
					previous = packet
				}
			}
			if *dryRun {
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
