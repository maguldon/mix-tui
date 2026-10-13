package main

import (
	"context"
	"testing"
	"time"
)

// TestAudioSessionStatsSurvivesNoDevice is the regression test for a panic that
// only appeared in the running application.
//
// The device is opened before the engine exists -- that is how the engine gets
// built at the rate the device actually granted -- so for a few milliseconds the
// session holds an output and no pump. Stats dereferenced the pump
// unconditionally, and the first tick after startup took the whole interface
// down with a nil dereference inside bubbletea's update loop.
//
// It reached no test because every Stats call in the suite went through a
// session that had already been adopted.
func TestAudioSessionStatsSurvivesNoDevice(t *testing.T) {
	for _, s := range []*audioSession{
		{},                 // nothing opened
		{err: "no device"}, // failed
	} {
		got := s.Stats() // must not panic
		if got.AudioExpected {
			t.Errorf("a session with no device reported audio as expected: %+v", got)
		}
	}
}

// TestAudioSessionIsInactiveWithoutAPump keeps the banner honest during the
// window between opening the device and adopting the engine.
//
// An open device with no pump renders silence. Reporting that as "active" would
// claim sound that is not happening and hide the banner exactly when it is
// needed.
func TestAudioSessionIsInactiveWithoutAPump(t *testing.T) {
	s := &audioSession{}
	if s.Active() {
		t.Error("an empty session reports active")
	}
}

// TestAudioSessionReasonAndCloseAreSafeOnNothing keeps shutdown and the reason
// accessor from needing a device, which is the path taken when audio never came
// up at all.
func TestAudioSessionReasonAndCloseAreSafeOnNothing(t *testing.T) {
	s := &audioSession{err: "cannot open alsa"}
	if s.Reason() != "cannot open alsa" {
		t.Errorf("Reason = %q", s.Reason())
	}
	s.Close()
	s.Close() // idempotent
}

// TestAudioSessionCloseStopsRetry keeps shutdown from leaving a goroutine
// poking at a device that has been released.
func TestAudioSessionCloseStopsRetry(t *testing.T) {
	s := &audioSession{}
	s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// With the context already done, retry must return rather than open a device.
	done := make(chan struct{})
	go func() { s.retry(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not return after its context was cancelled")
	}
}
