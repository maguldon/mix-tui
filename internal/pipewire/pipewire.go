//go:build linux

// Package pipewire wraps libpipewire just far enough to play the engine's two
// output streams to a device.
//
// This is the ONLY package in the repo that may import "C". Everything else —
// engine, dsp, analyze, audio — stays pure Go and testable with no audio
// device present, which is what lets the whole audio core be verified on a
// machine with no sound card (spec §3.2).
package pipewire

/*
// The include and link flags are named explicitly rather than requested via
// #cgo pkg-config, because Debian and Ubuntu ship a libspa-0.2.pc that adds
// -fno-strict-overflow. Go's cgo rejects any pkg-config flag it does not
// recognise, so the build fails before compiling a single line and every
// developer and CI job would have to set CGO_CFLAGS_ALLOW by hand. Naming the
// directories costs portability to an unusual install prefix, which is a far
// smaller price than a build that does not run as written.
#cgo CFLAGS: -I/usr/include/pipewire-0.3 -I/usr/include/spa-0.2 -D_REENTRANT
#cgo LDFLAGS: -lpipewire-0.3
#include <pipewire/pipewire.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// pkgInit guards pw_init and pw_deinit.
//
// These are process-global. libpipewire reference-counts them internally, but
// that is not a contract worth depending on across versions: pairing them
// exactly once from our own side is cheaper to reason about than being right
// about theirs.
var pkgInit struct {
	once sync.Once
	mu   sync.Mutex
	refs int
}

// ensureInit calls pw_init once for the process.
func ensureInit() {
	pkgInit.once.Do(func() {
		C.pw_init(nil, nil)
	})
}

func acquirePkg() {
	pkgInit.mu.Lock()
	pkgInit.refs++
	pkgInit.mu.Unlock()
}

func releasePkg() {
	pkgInit.mu.Lock()
	pkgInit.refs--
	atZero := pkgInit.refs <= 0
	pkgInit.mu.Unlock()
	if atZero {
		C.pw_deinit()
	}
}

// Context owns a libpipewire thread loop and the context on it.
type Context struct {
	loop   *C.struct_pw_thread_loop
	ctx    *C.struct_pw_context
	mu     sync.Mutex
	closed bool
}

// NewContext initialises libpipewire, creates a named thread loop, and starts
// it. The returned Context must be closed to release the loop.
//
// Errors are returned rather than logged-and-ignored: if the daemon cannot be
// reached the caller decides what to show the user, which per spec §8 is the
// idle art screen with a diagnostic — never a silent TUI.
func NewContext(name string) (*Context, error) {
	ensureInit()
	acquirePkg()

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	// pw_thread_loop_new takes a props dict as its second argument; NULL is
	// the documented way to say "none".
	loop := C.pw_thread_loop_new(cname, nil)
	if loop == nil {
		releasePkg()
		return nil, fmt.Errorf("pipewire: pw_thread_loop_new(%q) returned NULL", name)
	}

	// pw_context_new takes a pw_loop*, not a pw_thread_loop*. The two are
	// related but distinct: the thread loop owns a pw_loop, and passing the
	// outer type where the inner one is expected would not compile against the
	// headers, which is the cheapest possible way to find this out.
	ctx := C.pw_context_new(C.pw_thread_loop_get_loop(loop), nil, 0)
	if ctx == nil {
		C.pw_thread_loop_destroy(loop)
		releasePkg()
		return nil, fmt.Errorf("pipewire: pw_context_new returned NULL (no daemon reachable?)")
	}

	// pw_thread_loop_start returns an int error code, not NULL. Reading it as
	// a pointer would compile and silently compare a status against nothing.
	if rc := C.pw_thread_loop_start(loop); rc != 0 {
		C.pw_context_destroy(ctx)
		C.pw_thread_loop_destroy(loop)
		releasePkg()
		return nil, fmt.Errorf("pipewire: pw_thread_loop_start failed with code %d", int(rc))
	}

	return &Context{loop: loop, ctx: ctx}, nil
}

// Loop returns the opaque pw_loop* for stream construction.
//
// It is an unsafe.Pointer rather than a typed C pointer so that no libpipewire
// type leaks into this package's exported API — the rest of the package would
// then have to carry a cgo import of its own.
func (c *Context) Loop() unsafe.Pointer {
	if c == nil || c.loop == nil {
		return nil
	}
	return unsafe.Pointer(c.loop)
}

// Close stops the loop and releases everything it owns. It is idempotent.
//
// ORDER MATTERS. The loop must be stopped before the context is destroyed:
// a running loop dereferences the context, and destroying it underneath a live
// loop is a use-after-free that takes the process with it.
func (c *Context) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true

	if c.loop != nil {
		C.pw_thread_loop_stop(c.loop)
	}
	if c.ctx != nil {
		C.pw_context_destroy(c.ctx)
		c.ctx = nil
	}
	if c.loop != nil {
		C.pw_thread_loop_destroy(c.loop)
		c.loop = nil
	}
	releasePkg()
	return nil
}

// Available reports whether a PipeWire daemon is reachable.
//
// The result is never cached: a daemon can appear or vanish between calls, and
// a stale answer would turn a recoverable situation into a hard failure.
func Available() bool {
	ensureInit()
	acquirePkg()
	defer releasePkg()

	loop := C.pw_thread_loop_new(nil, nil)
	if loop == nil {
		return false
	}
	// pw_context_new takes a pw_loop*, not a pw_thread_loop*. The two are
	// related but distinct: the thread loop owns a pw_loop.
	ctx := C.pw_context_new(C.pw_thread_loop_get_loop(loop), nil, 0)
	if ctx == nil {
		C.pw_thread_loop_destroy(loop)
		return false
	}

	// pw_context_connect returns a pw_core*, NULL on failure. Comparing it to
	// an int would treat a non-NULL pointer as an error code.
	core := C.pw_context_connect(ctx, nil, 0)
	if core == nil {
		C.pw_context_destroy(ctx)
		C.pw_thread_loop_destroy(loop)
		return false
	}

	C.pw_core_disconnect(core)
	C.pw_context_destroy(ctx)
	C.pw_thread_loop_destroy(loop)
	return true
}
