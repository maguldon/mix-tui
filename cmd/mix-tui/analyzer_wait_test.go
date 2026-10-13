package main

import (
	"context"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// blockedAnalyzer returns an analyzer whose run is parked just before it would
// hand its result over, so Wait's behaviour can be observed at a known point
// rather than wherever the scheduler happens to put it.
//
// The old code de-registered the run immediately after the analysis finished and
// then did the cache write, the library callback and the engine apply outside the
// window Wait watches. Parking here -- after the analysis, before the apply --
// is exactly that window, made deterministic.
func blockedAnalyzer(t *testing.T) (*Analyzer, chan struct{}, chan struct{}) {
	t.Helper()
	a := NewAnalyzer(nil, newFakeTarget(2), nil)

	blocked := make(chan struct{})
	release := make(chan struct{})
	a.afterAnalyze = func() {
		close(blocked)
		<-release
	}
	return a, blocked, release
}

// TestWaitDoesNotReturnBeforeTheResultLands is the regression test for #8.
//
// A run de-registered itself straight after analyze.Analyze returned, so the
// cache write, the library callback and the engine apply all happened while
// nothing was registered. Wait polls the map, so it could report "nothing in
// flight" -- and return -- while the result was still being written to disk and
// pushed to the deck.
//
// -screenshot depends on exactly this: it calls Wait for a settled frame, and
// one taken early shows "--" for BPM and key on a track that has both, which is
// the only thing a screenshot exists to demonstrate. The gap is widest on a cold
// cache, where there is most to write.
func TestWaitDoesNotReturnBeforeTheResultLands(t *testing.T) {
	a, blocked, release := blockedAnalyzer(t)

	a.Analyze(0, "/m/a.mp3", dsp.Sine(440, 4))

	// Wait for the run to reach the parked point: the analysis is done, the
	// result is not applied, and the run must still be registered.
	select {
	case <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("the analysis never reached the hand-off point")
	}

	if a.InFlight() != 1 {
		t.Errorf("with a finished analysis not yet applied, %d runs are in flight; want 1",
			a.InFlight())
	}

	returned := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.Wait(ctx)
		close(returned)
	}()

	select {
	case <-returned:
		t.Fatal("Wait returned while the result was still in flight")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)

	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return once the run finished")
	}
	if a.InFlight() != 0 {
		t.Errorf("%d runs still registered after Wait returned", a.InFlight())
	}
}

// TestSupersededRunDoesNotClearANewerOne covers the hazard the fix introduces.
//
// The map is keyed by path, and a newer load of the same track replaces the
// entry. A run that finished late and deleted by path would remove the NEWER
// run's registration, under-reporting the work in flight -- the same bug one
// level down, which is why runs clear only their own entry.
func TestSupersededRunDoesNotClearANewerOne(t *testing.T) {
	a := NewAnalyzer(nil, newFakeTarget(2), nil)
	defer a.CancelAll()

	pcm := dsp.Sine(440, 4)
	a.Analyze(0, "/m/a.mp3", pcm)
	a.Analyze(0, "/m/a.mp3", pcm) // supersedes the first

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a.Wait(ctx)

	if got := a.InFlight(); got != 0 {
		t.Errorf("%d runs still registered after Wait", got)
	}
}

// TestCancelClearsOnlyItsOwnPath keeps a cancel from stranding another track.
func TestCancelClearsOnlyItsOwnPath(t *testing.T) {
	a := NewAnalyzer(nil, newFakeTarget(2), nil)
	defer a.CancelAll()

	a.Analyze(0, "/m/a.mp3", dsp.Sine(440, 4))
	a.Analyze(1, "/m/b.mp3", dsp.Sine(440, 4))
	if a.InFlight() != 2 {
		t.Fatalf("%d runs registered, want 2", a.InFlight())
	}

	a.Cancel("/m/a.mp3")
	if a.InFlight() != 1 {
		t.Errorf("after cancelling one path, %d runs remain; want 1", a.InFlight())
	}
}

// TestCancelAllClearsEverything keeps shutdown from waiting on work it has just
// cancelled.
func TestCancelAllClearsEverything(t *testing.T) {
	a := NewAnalyzer(nil, newFakeTarget(2), nil)
	a.Analyze(0, "/m/a.mp3", dsp.Sine(440, 4))
	a.Analyze(1, "/m/b.mp3", dsp.Sine(440, 4))
	a.Analyze(0, "/m/c.mp3", dsp.Sine(440, 4))

	a.CancelAll()
	if got := a.InFlight(); got != 0 {
		t.Errorf("%d runs survived CancelAll", got)
	}
}

// TestWaitReturnsImmediatelyWhenIdle keeps the common case cheap.
func TestWaitReturnsImmediatelyWhenIdle(t *testing.T) {
	a := NewAnalyzer(nil, newFakeTarget(2), nil)
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.Wait(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait blocked with nothing in flight")
	}
}

// TestCancelledAnalysisStillClearsItsEntry covers the early-return path. A
// superseded run has no result to hand anywhere, but it must still stop counting
// or Wait would block forever on work that will never finish.
func TestCancelledAnalysisStillClearsItsEntry(t *testing.T) {
	a := NewAnalyzer(nil, newFakeTarget(2), nil)
	defer a.CancelAll()

	a.Analyze(0, "/m/a.mp3", dsp.Sine(440, 4))
	a.Analyze(0, "/m/a.mp3", dsp.Sine(440, 4))
	a.Cancel("/m/a.mp3")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	a.Wait(ctx)
	if time.Since(start) > 5*time.Second {
		t.Error("Wait blocked on a cancelled analysis")
	}
	if a.InFlight() != 0 {
		t.Errorf("%d runs still registered", a.InFlight())
	}
}

// TestWaitReturnsAfterAFailedAnalysis keeps a broken track from blocking a
// screenshot forever.
func TestWaitReturnsAfterAFailedAnalysis(t *testing.T) {
	a := NewAnalyzer(nil, newFakeTarget(2), nil)
	// Empty audio is rejected before anything is registered, so a real failure
	// has to come from a cancel; either way Wait must return.
	a.Analyze(0, "/m/a.mp3", nil)
	if a.InFlight() != 0 {
		t.Errorf("empty audio registered a run: %d", a.InFlight())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.Wait(ctx)
}
