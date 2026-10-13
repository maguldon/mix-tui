//go:build linux

package pipewire

import (
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
)

// Stopping a stream AFTER its context is closed must be safe.
//
// This is the path that corrupted the loop mutex. lock() declines to take the
// lock once the context is closing, so an unconditional unlock released a lock
// the caller never held -- and, after the loop was destroyed, released freed
// memory. The mutex was left in a state the next context could not recover
// from, which is why the hang moved between tests and never repeated the same
// one.
func TestStopAfterContextCloseDoesNotCorruptTheLoop(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	ctx, err := NewContext("mix-tui-close-order")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewStream(ctx, StreamConfig{
		NodeName: "mix-tui: order", SampleRate: 44100, Channels: 2,
	}, audio.NewRing(44100))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Start()

	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_ = s.Stop()
		_ = s.Destroy()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop/Destroy after Close did not return; the loop lock is wedged")
	}
}

// Close must disconnect the core before stopping the loop.
//
// pw_core_disconnect marshals through the loop and blocks until the loop
// thread replies. Stopping the loop first joins that thread, so the reply
// never arrives and Close waits forever.
func TestCloseDisconnectsBeforeStoppingTheLoop(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	ctx, err := NewContext("mix-tui-core-order")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ctx.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close hung: the core was disconnected after the loop was stopped")
	}
}

// lock() must pin the goroutine to its OS thread for the whole critical
// section.
//
// libpipewire's loop lock is thread-affine and recursive: the loop records in
// thread-local storage which thread holds it. Go makes no promise that two cgo
// calls from one goroutine land on the same OS thread — a cgo call is a
// scheduling point — so an unpinned lock/unlock pair splits across threads and
// corrupts that bookkeeping. The visible result is a hang in
// pw_thread_loop_lock in whichever test happened to be running.
func TestLoopLockPinsTheGoroutineToItsThread(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	ctx, err := NewContext("mix-tui-affinity")
	if err != nil {
		t.Fatal(err)
	}

	const sections = 200
	const yieldsEach = 8

	migrated := make(chan int, 1)
	go func() {
		n := 0
		for i := 0; i < sections; i++ {
			held := ctx.lock()
			before := syscall.Gettid()
			for j := 0; j < yieldsEach; j++ {
				// Yield hard: without the pin this is exactly how a goroutine
				// gets rescheduled onto a different thread mid-critical-section.
				runtime.Gosched()
			}
			if syscall.Gettid() != before {
				n++
			}
			ctx.unlock(held)
		}
		migrated <- n
	}()

	select {
	case n := <-migrated:
		// Deliberately not deferred: reaching here means nothing is wedged, so
		// the context is safe to tear down.
		ctx.Close()
		if n != 0 {
			t.Fatalf("goroutine migrated across %d of %d loop-lock critical sections; "+
				"libpipewire's lock is thread-affine and this corrupts it", n, sections)
		}
	case <-time.After(30 * time.Second):
		// Close is deliberately not called: a wedged loop lock would make
		// teardown block too, turning a clear failure into a hang.
		t.Fatalf("loop lock wedged after %d critical sections; a thread released a "+
			"lock it never took and the loop can never make progress again", sections)
	}
}

// Concurrent loop-lock traffic must not wedge.
//
// This is the deadlock, directly. Without the pin it reproduced on essentially
// every run: goroutines that entered the critical section handed the lock back
// from a thread that had never taken it, the loop's own thread ended up blocked
// on the same mutex, and nothing could service the loop again. It is worth
// having as a test rather than as a comment because the failure is silent until
// it is total.
func TestConcurrentLoopLockTrafficDoesNotWedge(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}
	ctx, err := NewContext("mix-tui-contended")
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 8
	const iterations = 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				held := ctx.lock()
				runtime.Gosched()
				ctx.unlock(held)
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		ctx.Close()
	case <-time.After(30 * time.Second):
		// No Close here either: if the loop lock is wedged, teardown would
		// block on the same mutex and bury the real failure under a timeout.
		t.Fatalf("%d goroutines could not share the loop lock; the loop is wedged",
			goroutines)
	}
}

// Available() must tear its probe down in the same order Context.Close does,
// and must survive being called from several goroutines at once.
//
// Available builds a throwaway loop/context/core on every call, so it is the
// one entry point in the package that a caller can legitimately hit
// concurrently — every test here calls it as a skip guard. It used to stop the
// thread loop BEFORE destroying the context, the reverse of the order
// Context.Close documents as mandatory. Doing that left the loop thread parked
// in epoll_wait while pw_thread_loop_stop waited in pthread_join for it, so the
// call never returned and took the calling goroutine with it.
//
// Serial calls were fine; it took two goroutines to surface, which is why it
// survived so long.
func TestAvailableIsConcurrentAndTearsDownInOrder(t *testing.T) {
	if !Available() {
		t.Skip("no daemon")
	}

	const goroutines = 4
	const iterations = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if !Available() {
					t.Error("daemon went away mid-test")
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("%d goroutines could not each run %d Available() probes; "+
			"a probe tore its loop down in the wrong order", goroutines, iterations)
	}
}
