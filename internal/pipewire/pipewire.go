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
	"runtime"
	"sync"
	"sync/atomic"
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

// releasePkg drops a reference.
//
// It deliberately does NOT call pw_deinit.
//
// pw_deinit tears down libpipewire's GLOBAL state: the spa system registry,
// the default event loop, and the daemon glue. Calling it whenever our own
// reference count reaches zero means every sequential context teardown cycles
// the entire global stack down and back up. That is not merely wasteful -- it
// deadlocks. pw_thread_loop_stop joins the loop thread, and that thread can
// still be inside a global structure pw_deinit has just torn down, so the join
// never returns. Observed as an intermittent hang in whichever test happened to
// be running: 5 of 8 runs of this package, always stuck in either
// pw_thread_loop_stop or the lock immediately before it, and never in the
// same test twice.
//
// pw_deinit is documented as a once-at-exit call. The process exiting reclaims
// everything it would release, so not calling it costs nothing and removes the
// only global teardown from the process's lifetime.
func releasePkg() {
	pkgInit.mu.Lock()
	defer pkgInit.mu.Unlock()
	if pkgInit.refs > 0 {
		pkgInit.refs--
	}
}

// Context owns a libpipewire thread loop and the context on it.
type Context struct {
	// loop is immutable once constructed. Close stops and destroys it but does
	// NOT nil the field: nil-ing it while other goroutines read it to decide
	// whether the loop is safe to touch was a data race, and it made every
	// read a three-way check. closed is the atomic that says whether it is
	// still usable, so a late reader observes a stale-but-valid pointer
	// instead of a torn one.
	loop   *C.struct_pw_thread_loop
	closed atomic.Bool
	ctx    *C.struct_pw_context
	// core is the connected pipewire core. A stream is created from the core,
	// not the context, so NewContext connects eagerly: a caller that only
	// wants the loop would otherwise be told there is a daemon and then
	// discover otherwise at stream creation.
	core *C.struct_pw_core
	mu   sync.Mutex
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
	// error anywhere. The goroutine is pinned so that this lock, the connect,
	// and the unlock all happen on one thread; see lockOSThread.
	lockOSThread()
	C.pw_thread_loop_lock(loop)
	core := C.pw_context_connect(ctx, nil, 0)
	C.pw_thread_loop_unlock(loop)
	unlockOSThread()
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
// alive reports whether the loop is still usable.
func (c *Context) alive() bool { return c != nil && c.loop != nil && !c.closed.Load() }

// lock takes the loop lock when the context is still usable, and reports
// whether it actually took it.
//
// The boolean matters. lock and unlock MUST be symmetric: lock declines to
// take the lock once the context is closing, so an unconditional unlock would
// release a lock this caller does not hold -- and if the loop has already been
// destroyed, it would unlock freed memory. Either way the loop's mutex is left
// in a state no later caller can reason about, which showed up as an
// intermittent hang in whichever test happened to run next.
//
// The goroutine is also PINNED TO ITS OS THREAD for the duration of the lock.
// This is not an optimisation; without it the whole package deadlocks. See the
// long note on LockOSThread below.
func (c *Context) lock() bool {
	if c == nil || c.loop == nil || c.closed.Load() {
		return false
	}
	lockOSThread()
	C.pw_thread_loop_lock(c.loop)
	return true
}

// unlock releases the loop lock, but only if lock reported that it took it.
//
// The UnlockOSThread MUST come after the C unlock: until the loop lock is
// dropped, this goroutine is still inside libpipewire's world.
func (c *Context) unlock(held bool) {
	if !held || c == nil || c.loop == nil {
		return
	}
	C.pw_thread_loop_unlock(c.loop)
	unlockOSThread()
}

// lockOSThread pins the calling goroutine to its current OS thread.
//
// WHY THIS IS REQUIRED, since it is the single most important line in the
// package and the bug it fixes looked like a random hang in unrelated code.
//
// libpipewire's loop lock is THREAD-AFFINE and RECURSIVE. The loop remembers,
// in thread-local storage, which thread currently holds it, so that a nested
// lock from the same thread is a no-op rather than a self-deadlock, and so that
// the library can tell whether a caller has paid for the lock it is about to
// demand. Both properties are keyed on the OS thread, and Go does not promise
// that two cgo calls from one goroutine land on the same OS thread. A cgo call
// is a scheduling point: the goroutine can be resumed by any idle M.
//
// With no pinning, `lock` and the `unlock` a few lines later routinely run on
// DIFFERENT threads, and the bookkeeping is corrupted in two ways at once:
//
//   - The unlock, on a thread whose TLS says "I do not hold this", decrements
//     or releases a lock that the acquiring thread still believes it owns. The
//     loop is now left unlocked while the graph is mid-mutation, and any
//     concurrent caller walks straight in.
//   - libpipewire's own guard notices that the marshalling call did not come
//     from a thread holding the lock and refuses it, logging
//     "pw_stream_connect called from wrong context, check thread and locking".
//     That is not cosmetic: the object is left half-built.
//
// The observed symptom was an unreproducible-looking hang in whichever test
// happened to be running, always parked in pthread_mutex_lock inside
// pw_thread_loop_lock, roughly 15-20% of runs, never twice in the same test.
// The loop's own thread ends up blocked on the same mutex, so nothing can ever
// service the loop again. Pinning makes every cgo call between lock and unlock
// run on one thread, which is exactly the contract libpipewire expects and the
// one Go was silently breaking.
func lockOSThread() { runtime.LockOSThread() }

// unlockOSThread releases the pin taken by lockOSThread. It pairs strictly
// with it: calling it without a matching lockOSThread detaches the goroutine
// from a thread it still needs.
func unlockOSThread() { runtime.UnlockOSThread() }

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
	if !c.alive() {
		return nil
	}
	return unsafe.Pointer(c.loop)
}

// Close stops the loop and releases everything it owns. It is idempotent.
//
// ORDER MATTERS, and the order is the opposite of what it looks like it should
// be. The core must be disconnected BEFORE the loop is stopped, because
// pw_core_disconnect marshals a request through the loop and then blocks until
// the loop thread replies. Stopping the loop first joins that thread, and the
// reply never comes -- Close waits forever on a loop that can no longer answer.
//
// The sequence is: disconnect the core, destroy the context (which stops the
// spa data-loop thread that shares this loop), then stop and destroy the thread
// loop. Destroying the context underneath a live loop is a use-after-free, and
// leaving the data loop running while freeing the loop it runs on is the same
// hazard one step later.
func (c *Context) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return nil
	}
	c.closed.Store(true)

	// The core first, with the loop still running and under its lock.
	if c.core != nil {
		if c.loop != nil {
			lockOSThread()
			C.pw_thread_loop_lock(c.loop)
			C.pw_core_disconnect(c.core)
			C.pw_thread_loop_unlock(c.loop)
			unlockOSThread()
		}
		c.core = nil
	}
	if c.ctx != nil {
		C.pw_context_destroy(c.ctx)
		c.ctx = nil
	}
	if c.loop != nil {
		C.pw_thread_loop_stop(c.loop)
		C.pw_thread_loop_destroy(c.loop)
		// The field is left set on purpose: a concurrent reader must see a
		// valid pointer (and rely on `closed`) rather than a race with nil.
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

	// The loop thread must be RUNNING before connecting. pw_context_connect
	// marshals a handshake through the loop and waits for the reply; with no
	// loop thread to service it, the call blocks forever. This is the same
	// ordering NewContext uses, and the reason Available() used to hang the UI
	// thread on a wedged daemon (spec §8 makes it the gate for the idle-art
	// diagnostic screen).
	if rc := C.pw_thread_loop_start(loop); rc != 0 {
		C.pw_context_destroy(ctx)
		C.pw_thread_loop_destroy(loop)
		return false
	}

	// pw_context_connect returns a pw_core*, NULL on failure. Comparing it to
	// an int would treat a non-NULL pointer as an error code.
	lockOSThread()
	C.pw_thread_loop_lock(loop)
	core := C.pw_context_connect(ctx, nil, 0)
	if core == nil {
		C.pw_thread_loop_unlock(loop)
		unlockOSThread()
		C.pw_context_destroy(ctx)
		C.pw_thread_loop_stop(loop)
		C.pw_thread_loop_destroy(loop)
		return false
	}
	C.pw_core_disconnect(core)
	C.pw_thread_loop_unlock(loop)
	unlockOSThread()
	// Teardown order matches Context.Close, and must stay matched: destroy the
	// context BEFORE stopping the thread loop.
	//
	// This used to be the other way round, and it hung — but only under
	// concurrency, which is what made it so hard to see. Stopping the loop first
	// joins the loop thread while the context is still attached to that loop;
	// the thread stays parked in epoll_wait and never reaches its stop check, so
	// the join in pw_thread_loop_stop never returns. One goroutine's serial
	// probes sailed through; two goroutines wedged the process inside a call
	// that is supposed to be a cheap, side-effect-free "is there a daemon?"
	// question.
	C.pw_context_destroy(ctx)
	C.pw_thread_loop_stop(loop)
	C.pw_thread_loop_destroy(loop)
	return true
}
