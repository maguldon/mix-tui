//go:build linux

package pipewire

import (
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
