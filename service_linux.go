//go:build linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Linux systemd adapter; the display loop and hot reload stay platform neutral.
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

const linuxServiceName = "trcc-mini.service"
const linuxServiceUnit = "/etc/systemd/system/trcc-mini.service"
const linuxServiceExecutable = "/usr/local/libexec/trcc-mini/trcc-mini"

func serviceSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }

func serviceCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: trcc-mini service install|start|stop|status|uninstall")
	}
	action := args[0]
	if action != "install" && len(args) != 1 {
		return fmt.Errorf("unexpected arguments for service %s", action)
	}
	runSystemctl := func(sub ...string) error {
		command := exec.Command("systemctl", sub...)
		output, e := command.CombinedOutput()
		if e != nil {
			return fmt.Errorf("systemctl %s: %w: %s", strings.Join(sub, " "), e, strings.TrimSpace(string(output)))
		}
		if len(output) != 0 {
			fmt.Print(string(output))
		}
		return nil
	}
	switch action {
	case "install":
		path, err := configArgPath(args[1:])
		if err != nil {
			return err
		}
		if os.Geteuid() != 0 {
			return errors.New("service installation requires root (sudo)")
		}
		cfgPath, err := filepath.Abs(resolveSettingsPath(path))
		if err != nil {
			return err
		}
		cfg, err := readAppConfig(cfgPath)
		if err != nil {
			return err
		}
		if cfg.Device.PM == 0 {
			return errors.New("service installation requires explicit device.pm; user HID cache is not used by root service")
		}
		if _, err = os.Stat(cfgPath); err != nil {
			return fmt.Errorf("missing config: %w", err)
		}
		if strings.ContainsAny(cfgPath, "\n\r\"%") {
			return errors.New("unsupported characters in systemd configuration path")
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if _, err = os.Stat(linuxServiceUnit); err == nil {
			return errors.New("trcc-mini systemd service already installed")
		}
		content := linuxServiceUnitText(linuxServiceExecutable, cfgPath)
		data, err := os.ReadFile(exe)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(linuxServiceExecutable), 0755); err != nil {
			return err
		}
		// Only copy into a root-owned installation directory.
		if err = os.WriteFile(linuxServiceExecutable, data, 0755); err != nil {
			return err
		}
		if err = os.Chmod(linuxServiceExecutable, 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(linuxServiceUnit, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(content)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err = runSystemctl("daemon-reload"); err != nil {
			return err
		}
		if err = runSystemctl("enable", linuxServiceName); err != nil {
			return err
		}
		fmt.Printf("Installed auto-start %s with config %s. Run service start to start now.\n", linuxServiceName, cfgPath)
		return nil
	case "start":
		return runSystemctl("start", linuxServiceName)
	case "stop":
		return runSystemctl("stop", linuxServiceName)
	case "status":
		output, err := exec.Command("systemctl", "is-active", linuxServiceName).CombinedOutput()
		state := strings.TrimSpace(string(output))
		if err != nil && state == "" {
			return fmt.Errorf("systemctl status: %w", err)
		}
		fmt.Printf("Service %s: %s\n", linuxServiceName, state)
		return nil
	case "uninstall":
		if os.Geteuid() != 0 {
			return errors.New("service removal requires root (sudo)")
		}
		if err := runSystemctl("disable", "--now", linuxServiceName); err != nil {
			return err
		}
		if err := os.Remove(linuxServiceUnit); err != nil {
			return err
		}
		if err := runSystemctl("daemon-reload"); err != nil {
			return err
		}
		fmt.Println("Service uninstalled; config and installed executable retained")
		return nil
	default:
		return fmt.Errorf("unknown service command %q", action)
	}
}

func linuxServiceUnitText(exe, cfg string) string {
	return "[Unit]\nDescription=TRCC Mini HID Display\nAfter=local-fs.target\n\n" +
		"[Service]\nType=simple\nExecStart=\"" + exe + "\" service-run --config \"" + cfg + "\"\n" +
		"Restart=on-failure\nRestartSec=2\nNoNewPrivileges=true\n" +
		"ProtectSystem=strict\nProtectHome=read-only\nReadWritePaths=/dev\n" +
		"\n[Install]\nWantedBy=multi-user.target\n"
}

// Under systemd the regular run command is the foreground service. Keep
// the internal command name consistent across build targets.
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
