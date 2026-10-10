//go:build linux

package pipewire

import (
	"runtime/cgo"
	"sync"
	"unsafe"
)

// This file holds the state behind the C-visible callbacks.
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

// rateProbe collects the rate from a metadata listener.
//
// It is written on a libpipewire thread and read by the goroutine waiting in
// DefaultRate, so the two fields are guarded.
type rateProbe struct {
	mu    sync.Mutex
	found bool
	rate  int
}

func (p *rateProbe) set(rate int) {
	p.mu.Lock()
	p.rate = rate
	p.found = true
	p.mu.Unlock()
}

// result returns the rate once it has arrived, and (0, false) until then.
func (p *rateProbe) result() (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rate, p.found
}

func probeFromHandle(h cgo.Handle) (*rateProbe, bool) {
	if h == 0 {
		return nil, false
	}
	v := cgo.Handle(h).Value()
	p, ok := v.(*rateProbe)
	return p, ok
}

// coreID is PipeWire's well-known core object id.
const coreID = 0
