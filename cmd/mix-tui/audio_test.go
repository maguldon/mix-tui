package main

import (
	"context"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/pipewire"
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

// TestStartDoesNotDeadlockOnItsOwnLock is the bug that stopped the application
// from starting.
//
// start() held s.mu and then called Active(), which locks s.mu again.
// sync.Mutex is not reentrant, so the second Lock blocks forever and the process
// hangs at startup -- before the interface is drawn, with no output, no banner
// and no error, since the alt screen is never entered. It hit every run with
// audio enabled and no device present yet, which is precisely the case the
// background retry exists to handle.
//
// No test caught it because none of them called start(): they constructed an
// audioSession and poked its accessors, all of which lock and unlock correctly.
// The only caller is startupAudio, which needs a real device.
//
// So this drives start() directly and fails on the hang rather than on its
// result: start returns either an error or nil, and a third outcome is the bug.
func TestStartDoesNotDeadlockOnItsOwnLock(t *testing.T) {
	s := &audioSession{}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- nil // a panic still means it did not hang
			}
		}()
		// With no PipeWire daemon reachable this fails fast; on a machine with
		// audio it succeeds. Either is fine. Blocking is not.
		done <- s.start(t.Context())
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("start() deadlocked on audioSession.mu: it holds the lock and calls " +
			"Active(), which locks it again. sync.Mutex is not reentrant, so the " +
			"application hangs at startup with no output and no error.")
	}
}

// The retry loop calls Active() on every tick, and adopt calls startPump, which
// takes the same lock. A regression that reintroduced reentrancy anywhere on
// that path would hang a background goroutine rather than the foreground, which
// is quieter and just as fatal.
func TestTheRetryPathDoesNotReenterTheLock(t *testing.T) {
	s := &audioSession{}
	s.onChange = func(bool, string) {}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The body of retry's tick, which is what runs every 3 seconds forever.
		if s.Active() {
			return
		}
		_ = s.start(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the retry path reentered audioSession.mu")
	}
}

// TestNegotiatedRateWithinReturnsWithoutADevice keeps the startup wait bounded
// on the runs where nothing can ever answer it.
//
// run calls it on every start with audio, before the interface is drawn, to
// decide whether to warn about a granted rate that disagrees with the config. On
// a machine with no sound card — or with a config asking for nothing — no rate is
// ever coming, so the call has to come back on its deadline rather than sit
// there. The wait bounds startup; it must never be the thing that stalls it.
func TestNegotiatedRateWithinReturnsWithoutADevice(t *testing.T) {
	s := &audioSession{}
	start := time.Now()
	if got := s.negotiatedRateWithin(50 * time.Millisecond); got != 0 {
		t.Errorf("negotiatedRateWithin with no device = %d, want 0", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("negotiatedRateWithin took %s on a session that can never be granted a rate", elapsed)
	}
}

// TestTheStartupWaitIsLongEnoughToSeeAGrant keeps rateNegotiationWait honest.
//
// The wait exists because the daemon's grant arrives asynchronously — measured at
// well under 50ms after the stream connects, and not yet present the instant
// Start returns. A window shorter than the real arrival time would put the
// startup warning back in the state #27 describes: never able to fire, because
// the only value it can read is the zero that means "not known yet".
//
// Skipped without a device, since there is nothing to measure against.
func TestTheStartupWaitIsLongEnoughToSeeAGrant(t *testing.T) {
	s := &audioSession{cfg: pipewire.OutputConfig{SampleRate: 44100}}
	if err := s.start(t.Context()); err != nil {
		t.Skipf("no audio device: %v", err)
	}
	defer s.Close()

	if got := s.negotiatedRateWithin(rateNegotiationWait); got == 0 {
		t.Errorf("a started stream produced no granted rate within %s, so the "+
			"startup warning can never fire", rateNegotiationWait)
	}
}
