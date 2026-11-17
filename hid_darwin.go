//go:build darwin && cgo

// SPDX-License-Identifier: GPL-3.0-or-later
// macOS HID adapter using the native IOKit API.
package main

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/hid/IOHIDLib.h>
#include <IOKit/hid/IOHIDKeys.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <stdint.h>

typedef struct {
  IOHIDManagerRef manager;
  IOHIDDeviceRef device;
} trcc_hid;

static int trcc_number(IOHIDDeviceRef device, CFStringRef key) {
  CFTypeRef value=IOHIDDeviceGetProperty(device,key);
  int n=0;
  if (value && CFGetTypeID(value)==CFNumberGetTypeID()) {
    CFNumberGetValue((CFNumberRef)value,kCFNumberIntType,&n);
  }
  return n;
}
static int trcc_is_device(IOHIDDeviceRef device) {
  return trcc_number(device,CFSTR(kIOHIDVendorIDKey))==0x0416 &&
         trcc_number(device,CFSTR(kIOHIDProductIDKey))==0x8001;
}
static uint32_t trcc_location(IOHIDDeviceRef device) {
  return (uint32_t)trcc_number(device,CFSTR(kIOHIDLocationIDKey));
}
static IOHIDManagerRef trcc_manager(void) {
  IOHIDManagerRef manager=IOHIDManagerCreate(kCFAllocatorDefault,kIOHIDOptionsTypeNone);
  if (!manager) return NULL;
  IOHIDManagerSetDeviceMatching(manager,NULL);
  if (IOHIDManagerOpen(manager,kIOHIDOptionsTypeNone)!=kIOReturnSuccess) {
    CFRelease(manager);
    return NULL;
  }
  return manager;
}
// Fill up to max device locations. A location of zero is accepted if the
// device has no location property; the Go side refuses ambiguous results.
static int trcc_locations(uint32_t *out, int max) {
  IOHIDManagerRef manager=trcc_manager();
  if (!manager) return -1;
  CFSetRef set=IOHIDManagerCopyDevices(manager);
  if (!set) { IOHIDManagerClose(manager,0); CFRelease(manager); return 0; }
  CFIndex count=CFSetGetCount(set);
  IOHIDDeviceRef *all=calloc((size_t)count,sizeof(IOHIDDeviceRef));
  if (!all) { CFRelease(set); IOHIDManagerClose(manager,0); CFRelease(manager); return -1; }
  CFSetGetValues(set,(const void **)all);
  int found=0;
  for(CFIndex i=0;i<count;i++) {
    if (!trcc_is_device(all[i])) continue;
    if (found<max) out[found]=trcc_location(all[i]);
    found++;
  }
  free(all); CFRelease(set); IOHIDManagerClose(manager,0); CFRelease(manager);
  return found;
}
static int trcc_open(uint32_t location,trcc_hid *out) {
  out->manager=trcc_manager();
  if (!out->manager) return -1;
  CFSetRef set=IOHIDManagerCopyDevices(out->manager);
  if (!set) {IOHIDManagerClose(out->manager,0);CFRelease(out->manager);out->manager=NULL;return -2;}
  CFIndex count=CFSetGetCount(set);
  IOHIDDeviceRef *all=calloc((size_t)count,sizeof(IOHIDDeviceRef));
  if (!all) {CFRelease(set);IOHIDManagerClose(out->manager,0);CFRelease(out->manager);out->manager=NULL;return -3;}
  CFSetGetValues(set,(const void **)all);
  for(CFIndex i=0;i<count;i++){
    if(trcc_is_device(all[i])&&trcc_location(all[i])==location){
      out->device=(IOHIDDeviceRef)CFRetain(all[i]);break;
    }
  }
  free(all);CFRelease(set);
  if (!out->device) {IOHIDManagerClose(out->manager,0);CFRelease(out->manager);out->manager=NULL;return -4;}
  IOReturn result=IOHIDDeviceOpen(out->device,kIOHIDOptionsTypeNone);
  if (result!=kIOReturnSuccess) {
    CFRelease(out->device);out->device=NULL;
    IOHIDManagerClose(out->manager,0);CFRelease(out->manager);out->manager=NULL;
    return (int)result;
  }
  return 0;
}
static void trcc_close(trcc_hid *d){
  if(d->device){IOHIDDeviceClose(d->device,0);CFRelease(d->device);d->device=NULL;}
  if(d->manager){IOHIDManagerClose(d->manager,0);CFRelease(d->manager);d->manager=NULL;}
}
static int trcc_send(trcc_hid *d,const unsigned char *bytes){
  return (int)IOHIDDeviceSetReport(d->device,kIOHIDReportTypeOutput,0,bytes,64);
}
static int trcc_receive(trcc_hid *d,unsigned char *bytes,CFIndex *length){
  return (int)IOHIDDeviceGetReport(d->device,kIOHIDReportTypeInput,0,bytes,length);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

type hidDevice struct{ native C.trcc_hid }

func enumerateHID() ([]string, error) {
	var locations [64]C.uint32_t
	count := int(C.trcc_locations((*C.uint32_t)(unsafe.Pointer(&locations[0])), 64))
	if count < 0 {
		return nil, errors.New("IOKit HID manager unavailable")
	}
	if count > len(locations) {
		return nil, fmt.Errorf("too many HID devices (%d)", count)
	}
	result := make([]string, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, fmt.Sprintf("iohid:%08x", uint32(locations[i])))
	}
	sort.Strings(result)
	return result, nil
}
func openHID(path string) (*hidDevice, error) {
	raw := strings.TrimPrefix(path, "iohid:")
	if raw == path {
		return nil, fmt.Errorf("invalid IOKit HID path %q", path)
	}
	loc, e := strconv.ParseUint(raw, 16, 32)
	if e != nil {
		return nil, e
	}
	d := &hidDevice{}
	if code := C.trcc_open(C.uint32_t(loc), &d.native); code != 0 {
		return nil, fmt.Errorf("IOHIDDeviceOpen failed: IOReturn %d", int32(code))
	}
	return d, nil
}
func (d *hidDevice) Close() {
	if d != nil {
		C.trcc_close(&d.native)
	}
}
func (d *hidDevice) Send(payload []byte, timeout time.Duration) error {
	if len(payload) != 64 {
		return fmt.Errorf("expected 64-byte HID payload, got %d", len(payload))
	}
	if d == nil || d.native.device == nil {
		return errors.New("HID closed")
	}
	// IOKit report ID is provided separately; no leading zero in data.
	code := C.trcc_send(&d.native, (*C.uchar)(unsafe.Pointer(&payload[0])))
	if code != 0 {
		return fmt.Errorf("IOHIDDeviceSetReport: IOReturn %d", int32(code))
	}
	return nil
}
func (d *hidDevice) Receive(timeout time.Duration) ([]byte, error) {
	if d == nil || d.native.device == nil {
		return nil, errors.New("HID closed")
	}
	var payload [64]byte
	size := C.CFIndex(len(payload))
	code := C.trcc_receive(&d.native, (*C.uchar)(unsafe.Pointer(&payload[0])), &size)
	if code != 0 {
		return nil, fmt.Errorf("IOHIDDeviceGetReport: IOReturn %d (input-report support varies by HID device)", int32(code))
	}
	if size != 64 {
		return nil, fmt.Errorf("short HID report %d/64", int(size))
	}
	return payload[:], nil
}
func (d *hidDevice) SendFrame(packet []byte, timeout time.Duration) error {
	for offset := 0; offset < len(packet); offset += 64 {
		var chunk [64]byte
		copy(chunk[:], packet[offset:minInt(offset+64, len(packet))])
		if err := d.Send(chunk[:], timeout); err != nil {
			return err
		}
	}
	time.Sleep(30 * time.Millisecond)
	return nil
}
