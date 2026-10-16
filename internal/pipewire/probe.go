//go:build linux

package pipewire

import (
	"unsafe"
)

// This file holds the C-string readers used by the callbacks in callbacks.go.
//
// The callbacks receive a `void *` that libpipewire RETAINS for as long as the
// listener is installed. That rules out passing a Go pointer directly: cgo
// forbids handing Go memory to C when C may keep it, and the collector is free
// to move or reclaim it. runtime/cgo.Handle is the supported answer — it
// stores the value in a table and hands C an opaque integer token.

// cstr reads a NUL-terminated C string handed to an exported callback.
// Exported functions cannot take *C.char, so the trampoline passes it as an
// opaque pointer and the read happens here.
func cstr(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	return goString((*byte)(p))
}

// goString copies a NUL-terminated C string into a Go string.
func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	src := unsafe.Slice((*byte)(unsafe.Pointer(p)), n)
	copy(buf, src)
	return string(buf)
}
