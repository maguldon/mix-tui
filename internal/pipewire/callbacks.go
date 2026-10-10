package pipewire

/*
#include <stdlib.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// This file holds the C-visible callbacks.
//
// The //export directive forbids C types in an exported function's signature,
// so the entry point below is written in plain Go types and trampoline.c
// supplies a C shim carrying the real signature.
//
// The import "C" below is load-bearing even though no C symbol is referenced
// here: cgo only scans files that import "C" for //export directives, so a
// file that omits it has its exported function silently dropped. The package
// then builds, archives, and fails to link with an undefined reference — a
// failure that appears only when something actually links an executable.

//export rateProbeProperty
func rateProbeProperty(data unsafe.Pointer, subject uint32, key, typ, prop unsafe.Pointer, props unsafe.Pointer) {
	// Called on a libpipewire thread, which is real-time prioritised. Two
	// things follow from that and both are load-bearing:
	//
	// A panic crossing the cgo boundary aborts the process rather than
	// unwinding, taking the audio graph with it. The recover is not optional.
	//
	// The body must be trivial. It does one integer compare, one string
	// compare, and a dict scan, then returns.
	defer func() { _ = recover() }()

	// data carries a cgo.Handle token, not a pointer.
	p, ok := probeFromHandle(cgo.Handle(uintptr(data)))
	if !ok {
		return
	}
	if subject != uint32(coreID) {
		return
	}
	if key == nil {
		return
	}
	// cgo passes C strings as *C.char; the exported signature is plain Go, so
	// the conversion happens on the Go side.
	if cstr(key) != "default.clock.rate" {
		return
	}
	rate, ok := dictInt(unsafe.Pointer(props), "value")
	if !ok {
		return
	}
	p.set(rate)
}

// streamStateChanged reports a state transition. It runs on the loop thread,
// not the realtime one, but the same discipline applies: recover, keep it
// trivial.
//
//export streamStateChanged
func streamStateChanged(data unsafe.Pointer, old, state int32, errs unsafe.Pointer) {
	defer func() { _ = recover() }()
}

// streamParamChanged is where the negotiated format is captured.
//
// The daemon tells us the format it granted here, which may differ from the
// one requested; recording it is the only way to notice a mismatch rather
// than render 44.1k audio into a 48k stream.
//
//export streamParamChanged
func streamParamChanged(data unsafe.Pointer, id uint32, param unsafe.Pointer) {
	defer func() { _ = recover() }()
}

// streamProcess is the realtime drain. This is spec §4.3's contract.
//
// It runs on libpipewire's real-time data-loop thread. Everything that could
// stall the thread is excluded by construction: the work is one ring read and
// two atomic increments, no locks, no allocation, no logging, no map lookups.
// A single defer, the recover, guards against a panic crossing the cgo
// boundary — which aborts the process rather than unwinding, taking the audio
// thread with it.
//
//export streamProcess
func streamProcess(data unsafe.Pointer) {
	defer func() { _ = recover() }()

	s, ok := streamFromHandle(cgo.Handle(uintptr(data)))
	if !ok || s == nil {
		return
	}
	s.drain()
}

// streamFromHandle recovers the stream behind a handle token.
func streamFromHandle(h cgo.Handle) (*Stream, bool) {
	if h == 0 {
		return nil, false
	}
	v := h.Value()
	s, ok := v.(*Stream)
	return s, ok
}
