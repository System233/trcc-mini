package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeReloadConfig(t *testing.T, path string, c appConfig) {
	t.Helper()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionalReloadRejectsInvalidKeepsLastGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	first := defaultAppConfig()
	first.Device.PM = 16
	writeReloadConfig(t, path, first)
	reloader := newConfigReloader(path, first)
	if ok, err := reloader.check(nil); ok || err != nil {
		t.Fatalf("initial unchanged config %v %v", ok, err)
	}
	next := first
	next.Display.Brightness = 23
	next.Display.Interval = "300ms"
	writeReloadConfig(t, path, next)
	if ok, err := reloader.check(nil); !ok || err != nil {
		t.Fatalf("valid reload %v %v", ok, err)
	}
	if reloader.current.Display.Brightness != 23 || reloader.current.Display.Interval != "300ms" {
		t.Fatal("new configuration not committed")
	}

	// File truncated by an editor: stay on the last valid snapshot.
	if err := os.WriteFile(path, []byte(`{"version": 1,"display":`), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := reloader.check(nil); ok || err == nil {
		t.Fatalf("truncated config should reject %v %v", ok, err)
	}
	if reloader.current.Display.Brightness != 23 {
		t.Fatal("invalid reload changed brightness")
	}
	if ok, err := reloader.check(nil); ok || err != nil {
		t.Fatal("identical invalid contents should be silent")
	}

	// Unknown fields and semantic errors reject without partial application.
	for _, bad := range []string{
		`{"version":1,"display":{"brightness":101}}`,
		`{"version":1,"display":{"bad":1}}`,
		`{"version":1,"display":{"interval":"0s"}}`,
		`null`,
	} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if ok, err := reloader.check(nil); ok || err == nil {
			t.Fatalf("accepted invalid %s: %v %v", bad, ok, err)
		}
		if reloader.current.Display.Brightness != 23 {
			t.Fatal("rejected data partially applied")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if ok, err := reloader.check(nil); ok || err == nil {
		t.Fatalf("missing configuration must keep active settings: %v %v", ok, err)
	}
	if reloader.current.Display.Brightness != 23 {
		t.Fatal("missing file reset live config to defaults")
	}
	if ok, err := reloader.check(nil); ok || err != nil {
		t.Fatal("same missing file must not spam reload errors")
	}

	next.Display.Brightness = 91
	writeReloadConfig(t, path, next)
	if ok, err := reloader.check(nil); !ok || err != nil {
		t.Fatalf("recovery after errors failed: %v %v", ok, err)
	}
	if reloader.current.Display.Brightness != 91 {
		t.Fatal("config not recovered after invalid edits")
	}
}

func TestIndexedSensorSwitchHotReloadDoesNotRequireHardware(t *testing.T) {
	path := filepath.Join(t.TempDir(), "indexed.json")
	first := defaultAppConfig()
	first.Device.PM = 16
	writeReloadConfig(t, path, first)
	reloader := newConfigReloader(path, first)
	next := first
	// Deliberately choose an indexed GPU that may be missing. Syntax is
	// valid; at runtime the unavailable sensor is N/A, not a reload error.
	next.Display.Slots = make(map[string]slotConfig)
	for key, val := range first.Display.Slots {
		next.Display.Slots[key] = val
	}
	next.Display.Slots[gpuPrimary] = slotConfig{Metrics: []string{"gpu0.power", "gpu1.power"}, Interval: "2s"}
	next.Display.Slots[gpuSecondary] = slotConfig{Metrics: []string{"gpu1.load"}}
	writeReloadConfig(t, path, next)
	if updated, err := reloader.check(nil); !updated || err != nil {
		t.Fatalf("indexed GPU reload rejected: updated=%t err=%v", updated, err)
	}
	if got := reloader.current.Display.Slots[gpuPrimary].Metrics[1]; got != "gpu1.power" {
		t.Fatalf("indexed sensor changed during reload: %s", got)
	}
	if updated, err := reloader.check(nil); updated || err != nil {
		t.Fatalf("unchanged indexed config should be silent: %t %v", updated, err)
	}
}

func TestReloadRejectsRestartOnlyOrFailedResourcePreparation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	first := defaultAppConfig()
	first.Device.PM = 16
	writeReloadConfig(t, path, first)
	watcher := newConfigReloader(path, first)
	changed := first
	changed.Device.PM = 17
	changed.Display.Brightness = 11
	writeReloadConfig(t, path, changed)
	if ok, err := watcher.check(nil); ok || err == nil || !strings.Contains(err.Error(), "device.pm") {
		t.Fatalf("device identifier should require restart: %v %v", ok, err)
	}
	if watcher.current.Device.PM != 16 || watcher.current.Display.Brightness != 75 {
		t.Fatal("device change committed despite rejection")
	}

	changed.Device.PM = 16
	changed.Sensors.GPU = false
	writeReloadConfig(t, path, changed)
	called := false
	ok, err := watcher.check(func(old, next appConfig) error {
		called = true
		if old.Device.PM != 16 || next.Display.Brightness != 11 {
			t.Error("prepare received unexpected configuration")
		}
		return os.ErrPermission
	})
	if ok || err == nil || !called {
		t.Fatalf("failed resource setup must refuse reload: %v %v", ok, err)
	}
	if watcher.current.Sensors.GPU != true || watcher.current.Display.Brightness != 75 {
		t.Fatal("resource failure changed active settings")
	}
	changed.Display.Brightness = 12
	writeReloadConfig(t, path, changed)
	if ok, err := watcher.check(nil); !ok || err != nil {
		t.Fatalf("retry after external resource failure: %v %v", ok, err)
	}
}

func TestReloadDetectsContentNotJustModificationTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	first := defaultAppConfig()
	writeReloadConfig(t, path, first)
	watcher := newConfigReloader(path, first)
	fileStat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	next := first
	next.Display.Brightness = 44
	writeReloadConfig(t, path, next)
	os.Chtimes(path, fileStat.ModTime(), fileStat.ModTime())
	if ok, err := watcher.check(nil); !ok || err != nil {
		t.Fatalf("same mtime should not mask config update: %v %v", ok, err)
	}
	if watcher.current.Display.Brightness != 44 {
		t.Fatal("same mtime update lost")
	}
}
