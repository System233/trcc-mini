// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type commandOptions struct {
	ConfigPath string
	Verbose    bool
	DryRun     bool
	ForceProbe bool
	Help       bool
}

// Only operational controls are flags. Sensor, display and theme choices are
// JSON settings; no aliases for historical -long options or data overrides.
func parseCommandOptions(verb string, args []string) (commandOptions, error) {
	var opt commandOptions
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if verb == "run" || verb == "once" {
		fs.StringVar(&opt.ConfigPath, "config", "", "settings file")
		fs.StringVar(&opt.ConfigPath, "c", "", "settings file")
		fs.BoolVar(&opt.Verbose, "verbose", false, "print metrics")
		fs.BoolVar(&opt.Verbose, "v", false, "print metrics")
		if verb == "run" {
			fs.BoolVar(&opt.DryRun, "dry-run", false, "disable USB writes")
			fs.BoolVar(&opt.DryRun, "n", false, "disable USB writes")
		}
	}
	if verb == "probe" {
		fs.BoolVar(&opt.ForceProbe, "force-probe", false, "repeat hardware probe")
	}
	fs.BoolVar(&opt.Help, "help", false, "print help")
	fs.BoolVar(&opt.Help, "h", false, "print help")
	// Go's flag package normally accepts both -long and --long. Our CLI
	// contract deliberately has only one-character -x short flags.
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			continue
		}
		name := strings.SplitN(strings.TrimPrefix(a, "-"), "=", 2)[0]
		if len(name) > 1 {
			return opt, fmt.Errorf("unsupported option %q: only -c/-v/-n/-h are CLI flags; configure sensors and display in JSON", a)
		}
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			opt.Help = true
			return opt, nil
		}
		return opt, fmt.Errorf("%s: %w", verb, err)
	}
	if fs.NArg() != 0 {
		return opt, fmt.Errorf("%s: unexpected arguments %v", verb, fs.Args())
	}
	return opt, nil
}

func printDevices() error {
	paths, err := enumerateHID()
	if err != nil {
		return err
	}
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
