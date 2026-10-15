//go:build linux

package pipewire_test

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/pipewire"
)

func TestAvailableDoesNotPanic(t *testing.T) {
	_ = pipewire.Available() // true or false; must not crash either way
}

func TestNewContextSucceedsOrFailsCleanly(t *testing.T) {
	ctx, err := pipewire.NewContext("mix-tui-test")
	if err != nil {
		if !pipewire.Available() {
			t.Skipf("no pipewire daemon: %v", err)
		}
		t.Fatalf("daemon available but NewContext failed: %v", err)
	}
	defer ctx.Close()
	if ctx.Loop() == nil {
		t.Error("Loop() returned nil")
	}
}

// TestContextIsReusable proves Close/NewContext cycles do not leak global state.
func TestContextIsReusable(t *testing.T) {
	if !pipewire.Available() {
		t.Skip("no daemon")
	}
	for i := 0; i < 3; i++ {
		ctx, err := pipewire.NewContext("mix-tui-cycle")
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		ctx.Close()
	}
}

// TestDefaultRateIsSane pins the contract DefaultRate has while it is a stub:
// it must not return a rate, and in particular must not return a plausible one.
//
// An assertion of the shape "if ok, check the range" passes against ANY return
// value, including a fabricated 44100, which is the failure this guards against
// — the function once reported "unknown" only by accident of being unimplemented,
// and nothing here would have noticed if it started guessing.
func TestDefaultRateIsSane(t *testing.T) {
	if !pipewire.Available() {
		t.Skip("no daemon")
	}
	if rate, ok := pipewire.DefaultRate(); ok {
		t.Errorf("DefaultRate() = %d, ok; it is unimplemented and must report (0, false)", rate)
	}
}
