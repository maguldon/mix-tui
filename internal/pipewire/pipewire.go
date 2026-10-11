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
	mu   sync.Mutex
	refs int
}

// acquirePkg calls pw_init on the first reference and returns.
//
// pw_init and pw_deinit are paired by REFERENCE COUNT, not by a sync.Once.
// An earlier version initialised once and deinitialised whenever the count
// reached zero, which left the library deinitialised but never
// reinitialised: every call after the first context closed got a dead
// libpipewire, and pw_thread_loop_new began returning NULL. That only
// surfaced once a daemon was actually reachable.
func acquirePkg() {
	pkgInit.mu.Lock()
	defer pkgInit.mu.Unlock()
	if pkgInit.refs == 0 {
		C.pw_init(nil, nil)
	}
	pkgInit.refs++
}

// releasePkg drops a reference, calling pw_deinit when the last one goes.
func releasePkg() {
	pkgInit.mu.Lock()
	defer pkgInit.mu.Unlock()
	pkgInit.refs--
	if pkgInit.refs <= 0 {
		pkgInit.refs = 0
		C.pw_deinit()
	}
}

// Context owns a libpipewire thread loop and the context on it.
type Context struct {
	loop *C.struct_pw_thread_loop
	ctx  *C.struct_pw_context
	// core is the connected pipewire core. A stream is created from the core,
	// not the context, so NewContext connects eagerly: a caller that only
	// wants the loop would otherwise be told there is a daemon and then
	// discover otherwise at stream creation.
	core   *C.struct_pw_core
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

	// The loop thread must be RUNNING before the core is created.
	// pw_context_connect marshals a handshake through the loop and waits for
	// the reply, so with no loop thread to service it the call blocks
	// forever. pw_thread_loop_start returns an int status, not NULL.
	if rc := C.pw_thread_loop_start(loop); rc != 0 {
		C.pw_context_destroy(ctx)
		C.pw_thread_loop_destroy(loop)
		releasePkg()
		return nil, fmt.Errorf("pipewire: pw_thread_loop_start failed with code %d", int(rc))
	}

	// The core is a PROXY. Creating it marshals through the loop, so it must
	// run with the loop lock held; without it libpipewire reports
	// "impl_ext_end_proxy called from wrong context" and returns a core that
	// never completes its handshake, leaving every stream stuck PAUSED with no
	// error anywhere.
	C.pw_thread_loop_lock(loop)
	core := C.pw_context_connect(ctx, nil, 0)
	C.pw_thread_loop_unlock(loop)
	if core == nil {
		C.pw_context_destroy(ctx)
		C.pw_thread_loop_destroy(loop)
		releasePkg()
		return nil, fmt.Errorf("pipewire: pw_context_connect returned NULL (no daemon reachable?)")
	}

	return &Context{loop: loop, ctx: ctx, core: core}, nil
}

// pwctx exposes the context pointer for stream construction inside this
// package, without leaking a C type into the exported API.
// lock and unlock guard object construction and state changes.
//
// libpipewire requires pw_stream_new, pw_stream_connect and their kin to run
// with the thread loop's lock held. Calling them from an arbitrary goroutine
// is rejected with "called from wrong context", and the resulting half-built
// object segfaults on first use. Since our loop is driven by a thread
// PipeWire owns, locking from Go is how we enter that thread's world
// synchronously.
func (c *Context) lock() {
	if c == nil || c.loop == nil {
		return
	}
	C.pw_thread_loop_lock(c.loop)
}

func (c *Context) unlock() {
	if c == nil || c.loop == nil {
		return
	}
	C.pw_thread_loop_unlock(c.loop)
}

func (c *Context) pwcore() *C.struct_pw_core {
	if c == nil {
		return nil
	}
	return c.core
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
	// Disconnect the core before destroying its context, mirroring the loop
	// ordering above: teardown runs strictly outermost-first.
	if c.core != nil {
		C.pw_thread_loop_lock(c.loop)
		C.pw_core_disconnect(c.core)
		C.pw_thread_loop_unlock(c.loop)
		c.core = nil
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
	C.pw_thread_loop_lock(loop)
	core := C.pw_context_connect(ctx, nil, 0)
	if core == nil {
		C.pw_thread_loop_unlock(loop)
		C.pw_context_destroy(ctx)
		C.pw_thread_loop_destroy(loop)
		return false
	}
	C.pw_core_disconnect(core)
	C.pw_thread_loop_unlock(loop)
	C.pw_context_destroy(ctx)
	C.pw_thread_loop_destroy(loop)
	return true
}
