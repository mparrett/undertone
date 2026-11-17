//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreMIDI -framework CoreFoundation
#include <stdlib.h>
#include <CoreMIDI/CoreMIDI.h>

static OSStatus utOpen(const char *name, MIDIClientRef *client, MIDIEndpointRef *src) {
	CFStringRef n = CFStringCreateWithCString(NULL, name, kCFStringEncodingUTF8);
	OSStatus err = MIDIClientCreate(n, NULL, NULL, client);
	if (err == noErr) {
		// MIDISourceCreate is deprecated for MIDISourceCreateWithProtocol,
		// which speaks MIDI 2.0 packets; MIDI 1.0 bytes are all this sends.
		err = MIDISourceCreate(*client, n, src);
	}
	CFRelease(n);
	return err;
}

// utSend sends one message, stamped now: see midiSink.
static OSStatus utSend(MIDIEndpointRef src, Byte b0, Byte b1, Byte b2) {
	Byte buf[64];
	Byte data[3] = {b0, b1, b2};
	MIDIPacketList *pl = (MIDIPacketList *)buf;
	MIDIPacket *p = MIDIPacketListInit(pl);
	p = MIDIPacketListAdd(pl, sizeof buf, p, 0, 3, data);
	if (p == NULL) {
		return -1;
	}
	return MIDIReceived(src, pl);
}

static void utClose(MIDIClientRef client, MIDIEndpointRef src) {
	MIDIEndpointDispose(src);
	MIDIClientDispose(client);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// coreMIDISource is a CoreMIDI virtual source: other apps list it as an
// input device, as they would a keyboard plugged in.
type coreMIDISource struct {
	client C.MIDIClientRef
	src    C.MIDIEndpointRef
}

func openVirtualSource(name string) (midiSink, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var s coreMIDISource
	if err := C.utOpen(cname, &s.client, &s.src); err != 0 {
		return nil, fmt.Errorf("coremidi: creating source %q: OSStatus %d", name, int(err))
	}
	return &s, nil
}

func (s *coreMIDISource) send(d [3]byte) error {
	if err := C.utSend(s.src, C.Byte(d[0]), C.Byte(d[1]), C.Byte(d[2])); err != 0 {
		return fmt.Errorf("coremidi: send: OSStatus %d", int(err))
	}
	return nil
}

func (s *coreMIDISource) close() { C.utClose(s.client, s.src) }
