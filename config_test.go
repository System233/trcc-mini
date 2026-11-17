package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppConfigLifecycle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "subdir", "trcc-mini.json")
	cfg, err := readAppConfig(p)
	if err != nil || cfg.Version != 1 || !cfg.Sensors.GPU || cfg.Display.Power != "rotate" {
		t.Fatalf("built-in defaults missing: %+v %v", cfg, err)
	}
	if err = createAppConfig(p); err != nil {
		t.Fatal(err)
	}
	if err = createAppConfig(p); err == nil {
		t.Fatal("config init must not overwrite")
	}
	cfg, err = readAppConfig(p)
	if err != nil || cfg.Display.Brightness != 75 || cfg.Theme.Preset != "minimal" {
		t.Fatalf("generated config: %+v %v", cfg, err)
	}
	bytes, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), `"gpu": true`) || !strings.Contains(string(bytes), `"power": "rotate"`) {
		t.Fatalf("incomplete config: %s", bytes)
	}
}

func TestAppConfigStrictValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.json")
	for _, v := range []string{
		`{"version":999}`,
		`{"version":1,"unknown":1}`,
		`{"version":1,"display":{"brightness":150}}`,
		`{"version":1,"display":{"power":"watt"}}`,
		`{"version":1,"sensors":{"cpuTemperature":"diode"}}`,
		`{"version":1,"theme":{"preset":"nonexistent"}}`,
		`{"version":1,"theme":{"zones":{"cpu.typo":{"color":"#ffffff"}}}}`,
		`{"version":1,"theme":{"zones":{"cpu.load":{"mode":"RGB"}}}}`,
		`{"version":1,"theme":{"zones":{"gpu.temp":{"powerColor":"red"}}}}`,
		`{"version":1,"theme":{"bogus":true}}`,
		`{"version":1}{"version":1}`,
		`null`,
		`{`,
	} {
		if err := os.WriteFile(p, []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readAppConfig(p); err == nil {
			t.Errorf("accepted invalid config: %s", v)
		}
	}
	// A partial config inherits supported defaults, including the GPU row.
	if err := os.WriteFile(p, []byte(`{"version":1,"sensors":{"cpuTemperature":"ccd-max"},"display":{"power":"replace"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := readAppConfig(p)
	if err != nil || !c.Sensors.GPU || c.Sensors.CPUTemperature != "ccd-max" || c.Display.Power != "replace" {
		t.Fatalf("partial configuration: %+v %v", c, err)
	}
}

func TestCLIFlagSpelling(t *testing.T) {
	for _, tc := range []struct {
		verb string
		args []string
	}{
		{"run", []string{"-c", "config.json", "-n", "-v"}},
		{"run", []string{"--config=config.json", "--verbose", "--dry-run"}},
		{"once", []string{"-c=config.json", "-v"}},
		{"probe", []string{"--force-probe"}},
		{"detect", []string{"-h"}},
	} {
		if _, err := parseCommandOptions(tc.verb, tc.args); err != nil {
			t.Errorf("reject %s %v: %v", tc.verb, tc.args, err)
		}
	}
	for _, tc := range []struct {
		verb string
		args []string
	}{
		{"run", []string{"-gpu"}},
		{"run", []string{"--gpu"}},
		{"run", []string{"-dry-run"}},
		{"run", []string{"--power", "replace"}},
		{"run", []string{"--cpu-temp-source", "ccd-avg"}},
		{"run", []string{"--brightness=50"}},
		{"run", []string{"--config"}},
		{"once", []string{"-n"}},
		{"probe", []string{"--config", "file.json"}},
		{"detect", []string{"-v"}},
	} {
		if _, err := parseCommandOptions(tc.verb, tc.args); err == nil {
			t.Errorf("accepted unsupported %s %v", tc.verb, tc.args)
		}
	}
	path, err := configArgPath([]string{"-c=portable.json"})
	if err != nil || path != "portable.json" {
		t.Fatalf("config subcommand: %q %v", path, err)
	}
	if _, err = configArgPath([]string{"--config"}); err == nil {
		t.Fatal("missing argument accepted")
	}
}

func TestConfigCommandNoHID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "my.json")
	if err := configCommand([]string{"show", "--config", p}); err == nil {
		t.Fatal("explicitly missing config silently fell back to defaults")
	}
	for _, cmd := range [][]string{
		{"init", "-c", p},
		{"check", "--config", p},
		{"show", "--config=" + p},
		{"path", "-c=" + p},
	} {
		if err := configCommand(cmd); err != nil {
			t.Fatalf("%v: %v", cmd, err)
		}
	}
	if err := configCommand([]string{"init", "--config", p}); err == nil {
		t.Fatal("refused to protect existing user edits")
	}
}
