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
