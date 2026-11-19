package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppConfigLifecycle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "subdir", "trcc-mini.json")
	cfg, err := readAppConfig(p)
	if err != nil || cfg.Version != 1 || !cfg.Sensors.GPU || len(cfg.Display.Slots) != 4 || len(cfg.Display.Slots[cpuPrimary].Metrics) != 2 {
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
	if !strings.Contains(string(bytes), `"gpu": true`) || !strings.Contains(string(bytes), `"cpu.primary"`) {
		t.Fatalf("incomplete config: %s", bytes)
	}
}

func TestMetricConfigFileRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.json")
	c := defaultAppConfig()
	c.Display.RowLabels = map[string]string{"cpu": "off", "gpu": "on"}
	c.Indicators = map[string]indicatorConfig{
		"gpu.power": {Symbols: []string{"C", "F"}, Color: &colorRule{
			Mode: "threshold", Stops: []colorStop{
				{At: 0, Color: "#00FF00"}, {At: 100, Color: "#FFA500"}, {At: 200, Color: "#FF0000"},
			}}},
		"cpu.temperature": {Symbols: []string{}, Unit: "F"},
	}
	c.Derived = map[string]derivedConfig{
		"system.power.estimated": {Operation: "sum", Sources: []string{"cpu.power", "gpu.power"}, Offset: 25},
	}
	c.Display.Slots[gpuPrimary] = slotConfig{Metrics: []string{"gpu.power"}}
	c.Display.Slots[cpuPrimary] = slotConfig{Metrics: []string{"cpu.temperature", "system.power.estimated"}, Interval: "4s"}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readAppConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Indicators["gpu.power"].Symbols[0] != "C" || loaded.Indicators["gpu.power"].Color.Stops[1].At != 100 {
		t.Fatalf("power color/symbol config lost: %+v", loaded.Indicators["gpu.power"])
	}
	if got := loaded.Indicators["cpu.temperature"].Symbols; got == nil || len(got) != 0 {
		t.Fatalf("explicitly blank symbols must survive JSON roundtrip: %#v", got)
	}
	if loaded.Derived["system.power.estimated"].Offset != 25 {
		t.Fatal("derived estimate offset lost")
	}
	if loaded.Display.Slots[cpuPrimary].Metrics[1] != "system.power.estimated" {
		t.Fatal("derived slot reference lost")
	}
	if loaded.Version != 1 {
		t.Fatal("wrong schema version")
	}
	if loaded.Display.RowLabels["cpu"] != "off" || loaded.Display.RowLabels["gpu"] != "on" {
		t.Fatalf("row label overrides lost: %v", loaded.Display.RowLabels)
	}
}

func TestMetricConfigValidationFromJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "invalid.json")
	for _, extra := range []string{
		`"indicators":{"gpu.power":{"color":{"mode":"gradient","stops":[{"at":100,"color":"#FFFFFF"}]}}}`,
		`"indicators":{"gpu.power":{"symbols":["W"]}}`,
		`"indicators":{"gpu.power":{"unit":"GiB"}}`,
		`"indicators":{"cpu.temperature":{"unit":"Kelvin"}}`,
		`"indicators":{"cpu.power":{"color":{}}}`,
		`"indicators":{"unknown.key":{"color":{"mode":"solid","value":"#FFFFFF"}}}`,
		`"derived":{"system.power.estimated":{"operation":"sum","sources":["cpu.temperature","gpu.power"]}}`,
		`"display":{"rowLabels":{"cpu":"hide"}}`,
		`"display":{"rowLabels":{"cpu":"off","gpu":"show"}}`,
		`"display":{"rowLabels":{"unrecognized":"on"}}`,
		`"indicators":{"cpu.power":{"symbols":["GPU","C","F"]}}`,
		`"indicators":{"gpu.power":{"symbols":["CPU","C","F"]}}`,
	} {
		b := []byte(`{"version":1,` + extra + `}`)
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readAppConfig(p); err == nil {
			t.Errorf("accepted invalid JSON indicators/derived: %s", b)
		}
	}
}

func TestAppConfigStrictValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.json")
	for _, v := range []string{
		`{"version":999}`,
		`{"version":2}`,
		`{"version":3}`,
		`{"version":1,"display":{"power":"rotate"}}`,
		`{"version":1,"unknown":1}`,
		`{"version":1,"display":{"brightness":150}}`,
		`{"version":1,"display":{"power":"rotate"}}`,
		`{"version":1,"display":{"powerInterval":"4s"}}`,
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
	if err := os.WriteFile(p, []byte(`{"version":1,"sensors":{"cpuTemperature":"ccd-max"},"display":{"slots":{"cpu.primary":{"metrics":["cpu.power"]}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := readAppConfig(p)
	if err != nil || !c.Sensors.GPU || c.Sensors.CPUTemperature != "ccd-max" || c.Display.Slots[cpuPrimary].Metrics[0] != "cpu.power" {
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
