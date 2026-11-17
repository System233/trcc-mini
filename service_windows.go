//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Native Win32 Service Control Manager integration, no external runtime.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const serviceName = "TRCCMini"
const serviceDisplayName = "TRCC Mini Display"

const (
	serviceWin32OwnProcess    = 0x00000010
	serviceAutoStart          = 0x00000002
	serviceErrorNormal        = 0x00000001
	serviceStopped            = 1
	serviceStartPending       = 2
	serviceStopPending        = 3
	serviceRunning            = 4
	serviceAcceptStop         = 0x00000001
	serviceAcceptShutdown     = 0x00000004
	serviceControlStop        = 1
	serviceControlInterrogate = 4
	serviceControlShutdown    = 5
	scmConnect                = 0x0001
	scmCreateService          = 0x0002
	serviceQueryStatus        = 0x0004
	serviceStartAccess        = 0x0010
	serviceStopAccess         = 0x0020
	serviceDeleteAccess       = 0x00010000
)

var (
	advapi                     = syscall.NewLazyDLL("advapi32.dll")
	procOpenSCManager          = advapi.NewProc("OpenSCManagerW")
	procCreateService          = advapi.NewProc("CreateServiceW")
	procOpenService            = advapi.NewProc("OpenServiceW")
	procCloseServiceHandle     = advapi.NewProc("CloseServiceHandle")
	procStartService           = advapi.NewProc("StartServiceW")
	procControlService         = advapi.NewProc("ControlService")
	procQueryServiceStatusEx   = advapi.NewProc("QueryServiceStatusEx")
	procDeleteService          = advapi.NewProc("DeleteService")
	procStartDispatcher        = advapi.NewProc("StartServiceCtrlDispatcherW")
	procRegisterServiceHandler = advapi.NewProc("RegisterServiceCtrlHandlerExW")
	procSetServiceStatus       = advapi.NewProc("SetServiceStatus")
)

type winServiceStatus struct {
	ServiceType             uint32
	CurrentState            uint32
	ControlsAccepted        uint32
	Win32ExitCode           uint32
	ServiceSpecificExitCode uint32
	CheckPoint              uint32
	WaitHint                uint32
}
type winServiceStatusProcess struct {
	winServiceStatus
	ProcessId    uint32
	ServiceFlags uint32
}
type serviceTableEntry struct {
	Name *uint16
	Proc uintptr
}

func utf16Value(text string) *uint16 {
	p, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		panic(err)
	} // all caller values are fixed or verified Windows paths
	return p
}

func openSCManager(access uintptr) (uintptr, error) {
	h, _, err := procOpenSCManager.Call(0, 0, access)
	if h == 0 {
		return 0, fmt.Errorf("OpenSCManager: %w", winError(err))
	}
	return h, nil
}
func openManagedService(manager, access uintptr) (uintptr, error) {
	h, _, err := procOpenService.Call(manager, uintptr(unsafe.Pointer(utf16Value(serviceName))), access)
	if h == 0 {
		return 0, fmt.Errorf("OpenService(%s): %w", serviceName, winError(err))
	}
	return h, nil
}
func closeSC(h uintptr) {
	if h != 0 {
		procCloseServiceHandle.Call(h)
	}
}

func querySCState(h uintptr) (uint32, error) {
	var stat winServiceStatusProcess
	var needed uint32
	ok, _, err := procQueryServiceStatusEx.Call(h, 0, uintptr(unsafe.Pointer(&stat)),
		uintptr(unsafe.Sizeof(stat)), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return 0, fmt.Errorf("QueryServiceStatusEx: %w", winError(err))
	}
	return stat.CurrentState, nil
}
func serviceStateName(state uint32) string {
	switch state {
	case serviceStopped:
		return "stopped"
	case serviceStartPending:
		return "start-pending"
	case serviceStopPending:
		return "stop-pending"
	case serviceRunning:
		return "running"
	case 5:
		return "continue-pending"
	case 6:
		return "pause-pending"
	case 7:
		return "paused"
	}
	return fmt.Sprintf("unknown(%d)", state)
}

func serviceInstallValidation(config appConfig) error {
	if config.Device.PM == 0 {
		return fmt.Errorf("service needs an explicit device.pm (the LocalSystem service cannot use the interactive user's device cache); set device.pm to the detected PM and rerun service install")
	}
	if style, _ := styleFromPM(config.Device.PM, nil); !supportsStyle(style) {
		return fmt.Errorf("device.pm %d is not a supported display style", config.Device.PM)
	}
	return nil
}

func serviceImageCommand(exe, configPath string) string {
	// Go's %q would double backslashes, corrupting Windows SCM ImagePath.
	return "\"" + exe + "\" service-run --config \"" + configPath + "\""
}

// LocalSystem must not execute its binary from a user-writable project
// directory: doing so would allow executable replacement/privilege escalation.
// The installed service runs a copy inside administrator-protected Program
// Files. The JSON remains external so it can be hot reloaded without a copy.
func copyServiceExecutable(source, installDir string) (string, error) {
	target := filepath.Join(installDir, "trcc-mini.exe")
	if strings.EqualFold(filepath.Clean(source), filepath.Clean(target)) {
		return target, nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("read service binary: %w", err)
	}
	if err = os.MkdirAll(installDir, 0755); err != nil {
		return "", fmt.Errorf("create protected service directory: %w", err)
	}
	f, err := os.CreateTemp(installDir, ".trcc-mini-install-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	// Installation refuses an existing service; any leftover old binary
	// belongs to a stopped/uninstalled instance and may be replaced.
	if err = os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err = os.Rename(tmp, target); err != nil {
		return "", err
	}
	return target, nil
}

// service install|start|stop|status|uninstall. Only install accepts -c.
func serviceCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: trcc-mini service install|start|stop|status|uninstall")
	}
	action := args[0]
	if action != "install" && len(args) != 1 {
		return fmt.Errorf("service %s: no extra arguments expected", action)
	}
	switch action {
	case "install":
		explicit, err := configArgPath(args[1:])
		if err != nil {
			return err
		}
		cfgPath, err := filepath.Abs(resolveSettingsPath(explicit))
		if err != nil {
			return err
		}
		if _, err = os.Stat(cfgPath); err != nil {
			return fmt.Errorf("service config missing: %w", err)
		}
		config, err := readAppConfig(cfgPath)
		if err != nil {
			return err
		}
		if err := serviceInstallValidation(config); err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, err = filepath.Abs(exe)
		if err != nil {
			return err
		}
		manager, err := openSCManager(scmConnect | scmCreateService)
		if err != nil {
			return fmt.Errorf("service install requires an elevated Windows terminal: %w", err)
		}
		defer closeSC(manager)
		if old, openErr := openManagedService(manager, serviceQueryStatus); openErr == nil {
			closeSC(old)
			return fmt.Errorf("service %s is already installed; stop and uninstall before reinstalling", serviceName)
		} else if !errors.Is(openErr, syscall.Errno(1060)) {
			return openErr
		}
		protectedBase := os.Getenv("ProgramFiles")
		if protectedBase == "" || !filepath.IsAbs(protectedBase) {
			return fmt.Errorf("ProgramFiles system installation directory unavailable")
		}
		installedExe, err := copyServiceExecutable(exe, filepath.Join(protectedBase, "TRCCMini"))
		if err != nil {
			return fmt.Errorf("install service executable: %w", err)
		}
		command := serviceImageCommand(installedExe, cfgPath)
		h, _, e := procCreateService.Call(manager,
			uintptr(unsafe.Pointer(utf16Value(serviceName))),
			uintptr(unsafe.Pointer(utf16Value(serviceDisplayName))),
			serviceQueryStatus|serviceStartAccess|serviceStopAccess|serviceDeleteAccess,
			serviceWin32OwnProcess, serviceAutoStart, serviceErrorNormal,
			uintptr(unsafe.Pointer(utf16Value(command))), 0, 0, 0, 0, 0)
		if h == 0 {
			return fmt.Errorf("CreateService: %w (run elevated to install)", winError(e))
		}
		closeSC(h)
		fmt.Printf("Installed Windows service %s (automatic startup).\nBinary: %s\nConfig: %s\nStart now with: trcc-mini service start\n", serviceName, installedExe, cfgPath)
		return nil
	case "start", "stop", "status", "uninstall":
		manager, err := openSCManager(scmConnect)
		if err != nil {
			return err
		}
		defer closeSC(manager)
		access := uintptr(serviceQueryStatus)
		switch action {
		case "start":
			access |= serviceStartAccess
		case "stop":
			access |= serviceStopAccess
		case "uninstall":
			access |= serviceDeleteAccess
		}
		h, err := openManagedService(manager, access)
		if err != nil {
			if action == "status" && errors.Is(err, syscall.Errno(1060)) {
				fmt.Printf("Service %s: not installed\n", serviceName)
				return nil
			}
			return err
		}
		defer closeSC(h)
		state, err := querySCState(h)
		if err != nil {
			return err
		}
		switch action {
		case "status":
			fmt.Printf("Service %s: %s\n", serviceName, serviceStateName(state))
			return nil
		case "start":
			if state == serviceRunning {
				fmt.Println("Service already running")
				return nil
			}
			if state != serviceStopped {
				return fmt.Errorf("cannot start service in %s state", serviceStateName(state))
			}
			ok, _, e := procStartService.Call(h, 0, 0)
			if ok == 0 {
				return fmt.Errorf("StartService: %w", winError(e))
			}
			fmt.Println("Service start requested")
			return nil
		case "stop":
			if state == serviceStopped {
				fmt.Println("Service already stopped")
				return nil
			}
			if state != serviceRunning {
				return fmt.Errorf("cannot stop service in %s state", serviceStateName(state))
			}
			var status winServiceStatus
			ok, _, e := procControlService.Call(h, serviceControlStop, uintptr(unsafe.Pointer(&status)))
			if ok == 0 {
				return fmt.Errorf("ControlService stop: %w", winError(e))
			}
			fmt.Println("Service stop requested")
			return nil
		case "uninstall":
			if state != serviceStopped {
				return fmt.Errorf("service is %s: stop it before uninstalling", serviceStateName(state))
			}
			ok, _, e := procDeleteService.Call(h)
			if ok == 0 {
				return fmt.Errorf("DeleteService: %w", winError(e))
			}
			fmt.Printf("Service %s removed (configuration and executable retained)\n", serviceName)
			return nil
		}
	}
	return fmt.Errorf("unknown service subcommand %q (install/start/stop/status/uninstall)", action)
}

type serviceRuntime struct {
	mu         sync.Mutex
	handle     uintptr
	cancel     context.CancelFunc
	configPath string
}

var scmRuntime *serviceRuntime

func (r *serviceRuntime) report(state, exitCode, checkpoint, waitHint uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handle == 0 {
		return
	}
	accepted := uint32(0)
	if state == serviceRunning {
		accepted = serviceAcceptStop | serviceAcceptShutdown
	}
	s := winServiceStatus{
		ServiceType:  serviceWin32OwnProcess,
		CurrentState: state, ControlsAccepted: accepted,
		Win32ExitCode: exitCode, CheckPoint: checkpoint, WaitHint: waitHint,
	}
	procSetServiceStatus.Call(r.handle, uintptr(unsafe.Pointer(&s)))
}

// Callback functions must have the exact WINAPI stack ABI and stay alive
// for the lifetime of the SCM dispatcher.
func serviceControlCallback(code, eventType, eventData, userContext uintptr) uintptr {
	r := scmRuntime
	if r == nil {
		return 0
	}
	if code == serviceControlStop || code == serviceControlShutdown {
		r.report(serviceStopPending, 0, 1, 10000)
		if r.cancel != nil {
			r.cancel()
		}
	} else if code == serviceControlInterrogate {
		// The SCM already has the last reported status.
	}
	return 0
}
func serviceMainCallback(argc, argv uintptr) uintptr {
	r := scmRuntime
	if r == nil {
		return 0
	}
	r.serviceMain()
	return 0
}

func (r *serviceRuntime) serviceMain() {
	h, _, e := procRegisterServiceHandler.Call(
		uintptr(unsafe.Pointer(utf16Value(serviceName))),
		syscall.NewCallback(serviceControlCallback), 0)
	if h == 0 {
		fmt.Fprintln(os.Stderr, "RegisterServiceCtrlHandlerEx:", winError(e))
		return
	}
	r.handle = h
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	defer cancel()
	r.report(serviceStartPending, 0, 1, 10000)

	logFile, err := openServiceLog()
	if err != nil {
		r.report(serviceStopped, 1, 0, 0)
		return
	}
	defer logFile.Close()
	originalOut, originalErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = logFile, logFile
	defer func() { os.Stdout, os.Stderr = originalOut, originalErr }()
	fmt.Println("Windows service starting")
	err = runDisplay("run", commandOptions{ConfigPath: r.configPath}, ctx, func() {
		r.report(serviceRunning, 0, 0, 0)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Windows service stopped with error:", err)
		r.report(serviceStopped, 1, 0, 0)
	} else {
		fmt.Println("Windows service stopped normally")
		r.report(serviceStopped, 0, 0, 0)
	}
}

func openServiceLog() (*os.File, error) {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "TRCCMini")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
}

func runWindowsService(args []string) error {
	path, err := configArgPath(args)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("service-run requires explicit --config path (use service install)")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	scmRuntime = &serviceRuntime{configPath: path}
	name := utf16Value(serviceName)
	// The service table must be null terminated.
	table := [2]serviceTableEntry{{Name: name, Proc: syscall.NewCallback(serviceMainCallback)}, {}}
	ok, _, e := procStartDispatcher.Call(uintptr(unsafe.Pointer(&table[0])))
	if ok == 0 {
		return fmt.Errorf("StartServiceCtrlDispatcher: %w (service-run must be launched by Windows SCM)", winError(e))
	}
	return nil
}
