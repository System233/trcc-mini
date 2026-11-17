// SPDX-License-Identifier: GPL-3.0-or-later
// Safe in-process settings refresh. No filesystem watcher or background
// service is required: both console and SCM modes share this transaction.
package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"reflect"
)

type configReloader struct {
	path        string
	current     appConfig
	hash        [32]byte
	seen        bool
	lastFailure string
}

func newConfigReloader(path string, active appConfig) *configReloader {
	r := &configReloader{path: path, current: active}
	if b, err := os.ReadFile(path); err == nil {
		// If an editor replaced the file between initial startup loading
		// and watcher construction, the first poll must still apply it.
		if onDisk, e := parseAppConfig(b); e == nil && reflect.DeepEqual(onDisk, active) {
			r.hash = sha256.Sum256(b)
			r.seen = true
		}
	}
	return r
}

// check reads the complete file and validates every option before any
// runtime state is changed. prepare may stage external resources (e.g. NVML).
// On failure the previous config stays in effect. A rejected input hash is
// remembered so the same bad file is not parsed or logged on every poll.
func (r *configReloader) check(prepare func(old, next appConfig) error) (bool, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		reason := fmt.Sprintf("read %s: %v", r.path, err)
		if r.lastFailure == reason {
			return false, nil
		}
		r.lastFailure = reason
		return false, errors.New(reason)
	}
	checksum := sha256.Sum256(data)
	if r.seen && checksum == r.hash {
		r.lastFailure = ""
		return false, nil
	}
	candidate, err := parseAppConfig(data)
	if err == nil && candidate.Device.PM != r.current.Device.PM {
		err = fmt.Errorf("device.pm cannot change during hot reload (%d -> %d); restart required", r.current.Device.PM, candidate.Device.PM)
	}
	if err == nil && prepare != nil {
		err = prepare(r.current, candidate)
	}
	r.seen = true
	r.hash = checksum
	if err != nil {
		r.lastFailure = err.Error()
		return false, fmt.Errorf("rejected config reload: %w (keeping previous valid settings)", err)
	}
	r.current = candidate
	r.lastFailure = ""
	return true, nil
}
