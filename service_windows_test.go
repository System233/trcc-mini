//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

func TestServiceExecutableCopyAndReplace(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "candidate.exe")
	destinationDir := filepath.Join(root, "protected-install")
	for _, payload := range [][]byte{[]byte("first-build"), []byte("second-build-with-updates")} {
		if err := os.WriteFile(source, payload, 0600); err != nil {
			t.Fatal(err)
		}
		dest, err := copyServiceExecutable(source, destinationDir)
		if err != nil {
			t.Fatal(err)
		}
		if dest != filepath.Join(destinationDir, "trcc-mini.exe") {
			t.Fatalf("wrong install destination: %s", dest)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("installed executable corrupted: got=%q, want=%q", got, payload)
		}
	}
	file := filepath.Join(destinationDir, "trcc-mini.exe")
	if dest, err := copyServiceExecutable(file, destinationDir); err != nil || dest != file {
		t.Fatalf("self-copy must not corrupt executable: %s %v", dest, err)
	}
}

func TestSharedServiceDisplayLoopCancellationWithoutUSBWrites(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "trcc-mini.json")
	config := defaultAppConfig()
	config.Device.PM = 16
	config.Sensors.GPU = false
	b, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyCount := 0
	err = runDisplay("run", commandOptions{ConfigPath: configPath, DryRun: true}, ctx, func() {
		readyCount++
		cancel()
	})
	if err != nil {
		t.Fatal(err)
	}
	if readyCount != 1 {
		t.Fatalf("service should report ready exactly once; got %d", readyCount)
	}
}

func TestServiceInstallRequiresAccountIndependentModel(t *testing.T) {
	c := defaultAppConfig()
	if err := serviceInstallValidation(c); err == nil || !strings.Contains(err.Error(), "device.pm") {
		t.Fatalf("service must refuse per-user HID cache dependency: %v", err)
	}
	c.Device.PM = 16
	if err := serviceInstallValidation(c); err != nil {
		t.Fatal(err)
	}
	c.Device.PM = 128
	if err := serviceInstallValidation(c); err == nil {
		t.Fatal("unsupported display style must be rejected")
	}
}

func TestServiceImageCommandQuotesExecutableAndConfig(t *testing.T) {
	exe := filepath.FromSlash("C:/Program Files/TRCC Mini/trcc-mini.exe")
	config := filepath.FromSlash("D:/Work Files/Screen/trcc-mini.json")
	got := serviceImageCommand(exe, config)
	want := "\"" + exe + "\" service-run --config \"" + config + "\""
	if got != want {
		t.Fatalf("service ImagePath:\n got %s\nwant %s", got, want)
	}
}

func TestServiceStatusAndWindowsStructureLayout(t *testing.T) {
	expected := map[uint32]string{
		serviceStopped: "stopped", serviceStartPending: "start-pending",
		serviceStopPending: "stop-pending", serviceRunning: "running",
	}
	for state, want := range expected {
		if got := serviceStateName(state); got != want {
			t.Fatalf("state %d: got %q", state, got)
		}
	}
	if unsafe.Sizeof(winServiceStatus{}) != 28 {
		t.Fatalf("SERVICE_STATUS ABI changed: %d bytes", unsafe.Sizeof(winServiceStatus{}))
	}
	if unsafe.Sizeof(winServiceStatusProcess{}) != 36 {
		t.Fatalf("SERVICE_STATUS_PROCESS ABI changed: %d bytes", unsafe.Sizeof(winServiceStatusProcess{}))
	}
	if unsafe.Sizeof(serviceTableEntry{}) != 16 {
		t.Fatalf("SCM service entry table ABI changed: %d bytes", unsafe.Sizeof(serviceTableEntry{}))
	}
}
