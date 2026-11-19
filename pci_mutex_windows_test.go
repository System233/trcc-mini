//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func testPCIMutex(t *testing.T) syscall.Handle {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(fmt.Sprintf("Local\\TRCCMiniPCITest_%d_%d", syscall.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := pCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		t.Fatalf("CreateMutexW: %v", err)
	}
	t.Cleanup(func() { syscall.CloseHandle(syscall.Handle(h)) })
	return syscall.Handle(h)
}

func TestPCIMutexThreadOwnershipAndContention(t *testing.T) {
	h := testPCIMutex(t)
	p := &pawnSensor{pciMutex: h}
	threadID := dllKernel.NewProc("GetCurrentThreadId")
	for i := 0; i < 25; i++ {
		err := p.withPCILock(func() error {
			before, _, _ := threadID.Call()
			runtime.Gosched()
			after, _, _ := threadID.Call()
			if before != after {
				return fmt.Errorf("OS thread migrated while holding mutex: %d -> %d", before, after)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("lock cycle %d: %v", i, err)
		}
	}

	locked := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- p.withPCILock(func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("mutex acquisition did not complete")
	}
	contender := &pawnSensor{pciMutex: h, pciMutexWait: 15}
	if err := contender.withPCILock(func() error {
		return errors.New("contender unexpectedly entered the critical section")
	}); !errors.Is(err, errPCIMutexTimeout) {
		close(release)
		t.Fatalf("contending lock: got %v, want timeout", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatalf("mutex release: %v", err)
	}
	if err := contender.withPCILock(func() error { return nil }); err != nil {
		t.Fatalf("mutex remained locked after owner exited: %v", err)
	}
}

func TestPCIMutexPreservesReadErrors(t *testing.T) {
	p := &pawnSensor{pciMutex: testPCIMutex(t)}
	want := errors.New("sample failed")
	if err := p.withPCILock(func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("read error lost: %v", err)
	}
	if err := p.withPCILock(func() error { return nil }); err != nil {
		t.Fatalf("mutex not released after read error: %v", err)
	}
}

func TestPCIMutexReportsInvalidHandle(t *testing.T) {
	// -1 is a valid GetCurrentProcess pseudo-handle and would merely time out.
	p := &pawnSensor{pciMutex: syscall.Handle(0xDEADBEEF)}
	if err := p.withPCILock(func() error { return nil }); err == nil || errors.Is(err, errPCIMutexTimeout) {
		t.Fatalf("invalid mutex handle must be a Windows API error, not a timeout: %v", err)
	}
}
