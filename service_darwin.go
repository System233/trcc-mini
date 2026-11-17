//go:build darwin

// SPDX-License-Identifier: GPL-3.0-or-later
// macOS launchd LaunchDaemon adapter, no GUI or third-party runtime.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	macServiceLabel      = "com.trcc-mini.display"
	macServicePlist      = "/Library/LaunchDaemons/com.trcc-mini.display.plist"
	macServiceExecutable = "/usr/local/libexec/trcc-mini/trcc-mini"
)

func serviceSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
func macPlistValue(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
func macServicePlistText(exe, config string) string {
	return "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" +
		"<plist version=\"1.0\"><dict>\n" +
		"<key>Label</key><string>" + macServiceLabel + "</string>\n" +
		"<key>ProgramArguments</key><array>\n" +
		"<string>" + macPlistValue(exe) + "</string><string>service-run</string><string>--config</string><string>" + macPlistValue(config) + "</string>\n" +
		"</array>\n<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><true/>\n" +
		"<key>StandardOutPath</key><string>/var/log/trcc-mini.log</string>\n" +
		"<key>StandardErrorPath</key><string>/var/log/trcc-mini.log</string>\n" +
		"</dict></plist>\n"
}
func macLaunchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
func serviceCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: trcc-mini service install|start|stop|status|uninstall")
	}
	action := args[0]
	if action != "install" && len(args) != 1 {
		return fmt.Errorf("service %s takes no extra args", action)
	}
	switch action {
	case "install":
		if os.Geteuid() != 0 {
			return errors.New("launchd service installation requires sudo")
		}
		configPath, err := configArgPath(args[1:])
		if err != nil {
			return err
		}
		configPath, err = filepath.Abs(resolveSettingsPath(configPath))
		if err != nil {
			return err
		}
		if _, err = os.Stat(configPath); err != nil {
			return err
		}
		cfg, err := readAppConfig(configPath)
		if err != nil {
			return err
		}
		if cfg.Device.PM == 0 {
			return errors.New("service requires explicit device.pm because user HID cache is not shared with launchd")
		}
		if _, err = os.Stat(macServicePlist); err == nil {
			return errors.New("launchd service already installed")
		}
		source, err := os.Executable()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(macServiceExecutable), 0755); err != nil {
			return err
		}
		if err = os.WriteFile(macServiceExecutable, data, 0755); err != nil {
			return err
		}
		if err = os.Chmod(macServiceExecutable, 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(macServicePlist, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(macServicePlistText(macServiceExecutable, configPath))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Printf("Installed launchd service %s; start with 'service start'.\n", macServiceLabel)
		return nil
	case "start":
		if os.Geteuid() != 0 {
			return errors.New("launchd service start requires sudo")
		}
		return macLaunchctl("bootstrap", "system", macServicePlist)
	case "stop":
		if os.Geteuid() != 0 {
			return errors.New("launchd service stop requires sudo")
		}
		return macLaunchctl("bootout", "system/"+macServiceLabel)
	case "status":
		err := macLaunchctl("print", "system/"+macServiceLabel)
		if err != nil {
			fmt.Println("Service not loaded or stopped")
			return nil
		}
		fmt.Println("Service loaded (launchctl)")
		return nil
	case "uninstall":
		if os.Geteuid() != 0 {
			return errors.New("launchd service uninstall requires sudo")
		}
		_ = macLaunchctl("bootout", "system/"+macServiceLabel)
		if err := os.Remove(macServicePlist); err != nil {
			return err
		}
		fmt.Println("launchd service uninstalled; configuration retained")
		return nil
	default:
		return fmt.Errorf("unknown service command %q", action)
	}
}
func runManagedService(args []string) error {
	path, err := configArgPath(args)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("service-run requires --config")
	}
	return runDisplay("run", commandOptions{ConfigPath: path}, context.Background(), func() {})
}
