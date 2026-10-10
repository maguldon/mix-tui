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

func TestDefaultRateIsSane(t *testing.T) {
	if !pipewire.Available() {
		t.Skip("no daemon")
	}
	if rate, ok := pipewire.DefaultRate(); ok {
		if rate < 8000 || rate > 192000 {
			t.Errorf("default rate %d is not a plausible sample rate", rate)
		}
	}
}
