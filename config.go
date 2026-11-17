// SPDX-License-Identifier: GPL-3.0-or-later
// Portable user-editable settings. device.json remains a separate
// read-only-ish hardware fingerprint cache, never overwritten by this file.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type appConfig struct {
	Version int `json:"version"`
	Device  struct {
		PM int `json:"pm"` // 0 = hardware detection cache
	} `json:"device"`
	Sensors struct {
		CPUTemperature string `json:"cpuTemperature"`
		GPU            bool   `json:"gpu"`
	} `json:"sensors"`
	Display struct {
		Interval      string                `json:"interval"`
		PhaseInterval string                `json:"phaseInterval"`
		Brightness    int                   `json:"brightness"`
		Slots         map[string]slotConfig `json:"slots"`
	} `json:"display"`
	Theme themeConfig `json:"theme"`
}

type themeConfig struct {
	Preset     string                `json:"preset"`
	Color      string                `json:"color"`
	PowerColor string                `json:"powerColor"`
	Link       string                `json:"link"`
	Zones      map[string]zoneConfig `json:"zones"`
}

type zoneConfig struct {
	Mode       string `json:"mode"` // solid, temp, load
	Color      string `json:"color"`
	PowerColor string `json:"powerColor,omitempty"`
}

var zoneNames = []string{"cpu.temp", "cpu.load", "gpu.temp", "gpu.load"}

func defaultAppConfig() appConfig {
	var c appConfig
	c.Version = 2
	c.Sensors.CPUTemperature = "tctl" // no automatic fabricated 'CPU diode' conversion
	c.Sensors.GPU = true              // PA120 has its own physical GPU row
	c.Display.Interval = "1s"
	c.Display.PhaseInterval = "3s"
	c.Display.Brightness = 75
	c.Display.Slots = defaultDisplaySlots()
	c.Theme.Preset = "minimal"
	c.Theme.Color = "#FFFFFF"
	c.Theme.PowerColor = "#FF9A00"
	c.Theme.Link = "static"
	// Presets establish the base. Only explicit user zone overrides
	// should win, so switching minimal -> thermal/night remains effective.
	c.Theme.Zones = make(map[string]zoneConfig)
	return c
}

func exeConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "trcc-mini.json"
	}
	return filepath.Join(filepath.Dir(exe), "trcc-mini.json")
}

// One explicit configuration location: beside the executable.
// --config chooses a different path without migration or fallback logic.
func resolveSettingsPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return exeConfigPath()
}

func readAppConfig(path string) (appConfig, error) {
	c := defaultAppConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read config %q: %w", path, err)
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return c, fmt.Errorf("invalid config %q: expected an object, got null", path)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid config %q: %w", path, err)
	}
	var trailing any
	if e := d.Decode(&trailing); e == nil {
		return c, fmt.Errorf("invalid config %q: unexpected trailing JSON", path)
	} else if !errors.Is(e, io.EOF) {
		return c, fmt.Errorf("invalid config %q: %w", path, e)
	}
	if err = validateAppConfig(c); err != nil {
		return c, fmt.Errorf("invalid config %q: %w", path, err)
	}
	return c, nil
}

func validateAppConfig(c appConfig) error {
	if c.Version != 2 {
		return fmt.Errorf("version must be 2 (got %d)", c.Version)
	}
	if c.Device.PM < 0 || c.Device.PM > 255 {
		return fmt.Errorf("device.pm must be 0..255")
	}
	if c.Device.PM > 0 {
		st, _ := styleFromPM(c.Device.PM, nil)
		if st == 0 {
			return fmt.Errorf("unknown device.pm %d", c.Device.PM)
		}
	}
	if !validCPUTempSource(c.Sensors.CPUTemperature) {
		return fmt.Errorf("sensors.cpuTemperature %q: expected tctl, ccd1, ccd2, ccd-max or ccd-avg", c.Sensors.CPUTemperature)
	}
	for _, x := range []struct {
		name, value string
		min         time.Duration
	}{
		{"display.interval", c.Display.Interval, 200 * time.Millisecond},
		{"display.phaseInterval", c.Display.PhaseInterval, 200 * time.Millisecond},
	} {
		d, e := time.ParseDuration(x.value)
		if e != nil || d < x.min {
			return fmt.Errorf("%s must be duration >=%s", x.name, x.min)
		}
	}
	if c.Display.Brightness < 0 || c.Display.Brightness > 100 {
		return errors.New("display.brightness must be 0..100")
	}
	if err := validateDisplaySlots(c.Display.Slots); err != nil {
		return err
	}
	switch c.Theme.Preset {
	case "minimal", "thermal", "night", "custom":
	default:
		return fmt.Errorf("theme.preset %q: use minimal, thermal, night or custom", c.Theme.Preset)
	}
	if _, err := parseColor(c.Theme.Color); err != nil {
		return fmt.Errorf("theme.color: %w", err)
	}
	if _, err := parseColor(c.Theme.PowerColor); err != nil {
		return fmt.Errorf("theme.powerColor: %w", err)
	}
	switch c.Theme.Link {
	case "static", "temp", "load", "gpu-temp", "gpu-load":
	default:
		return fmt.Errorf("theme.link %q invalid", c.Theme.Link)
	}
	for name, z := range c.Theme.Zones {
		found := false
		for _, valid := range zoneNames {
			if name == valid {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("theme.zones key %q: use cpu.temp/cpu.load/gpu.temp/gpu.load", name)
		}
		switch z.Mode {
		case "", "solid", "temp", "load":
		default:
			return fmt.Errorf("theme.zones.%s.mode %q invalid", name, z.Mode)
		}
		if z.Color != "" {
			if _, err := parseColor(z.Color); err != nil {
				return fmt.Errorf("theme.zones.%s.color: %w", name, err)
			}
		}
		if z.PowerColor != "" {
			if _, err := parseColor(z.PowerColor); err != nil {
				return fmt.Errorf("theme.zones.%s.powerColor: %w", name, err)
			}
		}
	}
	return nil
}

func createAppConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists at %q; edit it instead (will not overwrite)", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(defaultAppConfig(), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	return nil
}

func configCommand(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: trcc-mini config init|show|check|path [--config file]")
	}
	verb := strings.ToLower(args[0])
	if verb != "init" && verb != "show" && verb != "check" && verb != "path" {
		return fmt.Errorf("unknown config subcommand %q (init/show/check/path)", verb)
	}
	explicit, err := configArgPath(args[1:])
	if err != nil {
		return err
	}
	path := resolveSettingsPath(explicit)
	if verb == "path" {
		fmt.Println(path)
		return nil
	}
	if verb == "init" {
		if err := createAppConfig(path); err != nil {
			return err
		}
		fmt.Printf("Created %s\nEdit it, then run: trcc-mini run\n", path)
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("configuration %q unavailable (%w): run 'trcc-mini config init'", path, err)
	}
	c, err := readAppConfig(path)
	if err != nil {
		return err
	}
	if verb == "check" {
		fmt.Printf("Config valid: %s (theme=%s, gpu=%t, slots=%d)\n", path, c.Theme.Preset, c.Sensors.GPU, len(c.Display.Slots))
		return nil
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Effective config (%s):\n%s\n", path, data)
	return nil
}

func configArgPath(args []string) (string, error) {
	var path string
	for i := 0; i < len(args); i++ {
		s := args[i]
		switch {
		case s == "-c" || s == "--config":
			i++
			if i >= len(args) || args[i] == "" {
				return "", errors.New("--config requires a path")
			}
			path = args[i]
		case strings.HasPrefix(s, "--config="):
			path = strings.TrimPrefix(s, "--config=")
			if path == "" {
				return "", errors.New("--config requires a path")
			}
		case strings.HasPrefix(s, "-c="):
			path = strings.TrimPrefix(s, "-c=")
			if path == "" {
				return "", errors.New("-c requires a path")
			}
		default:
			return "", fmt.Errorf("unknown argument %q (use --config FILE or -c FILE)", s)
		}
	}
	return path, nil
}
