package engine

import (
	"math"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

func TestSyncMatchesTempos(t *testing.T) {
	master := NewDeck(0)
	master.Load(memTrack(dsp.Sine(440, 30)))
	master.AttachBeatGrid(testGrid(128.0, 60))
	slave := NewDeck(1)
	slave.Load(memTrack(dsp.Sine(440, 30)))
	slave.AttachBeatGrid(testGrid(124.0, 60))
	if err := slave.EngageSync(master); err != nil {
		t.Fatal(err)
	}
	// Pitch is applied through the command queue, so it lands on the next
	// block. Reading it before then would see the pre-sync value.
	renderBlocks(slave, BlockFrames, 1)
	ratio := slave.PitchRatio()
	if ratio < 1.02 || ratio > 1.05 {
		t.Errorf("sync pitch ratio = %v, want ~1.032", ratio)
	}
}

func TestSyncRequiresBothGrids(t *testing.T) {
	master := NewDeck(0)
	master.Load(memTrack(dsp.Sine(440, 10)))
	master.AttachBeatGrid(testGrid(128, 20))
	slave := NewDeck(1)
	slave.Load(memTrack(dsp.Sine(440, 10)))
	// no grid on the slave
	if err := slave.EngageSync(master); err == nil {
		t.Error("sync without a slave beatgrid should error")
	}
	if slave.SyncActive() {
		t.Error("sync engaged despite error")
	}
}

func TestSyncWithoutMasterGridErrors(t *testing.T) {
	m := NewDeck(0)
	m.Load(memTrack(dsp.Sine(440, 10)))
	s := NewDeck(1)
	s.Load(memTrack(dsp.Sine(440, 10)))
	s.AttachBeatGrid(testGrid(128, 20))
	if err := s.EngageSync(m); err == nil {
		t.Error("expected error")
	}
}

func TestQuantizeSnapsToNearestBeat(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 20)))
	d.AttachBeatGrid(testGrid(120.0, 40))
	d.seek(1050 * time.Millisecond) // beat is at 1000ms; 50ms late
	if err := d.QuantizeToNearestBeat(); err != nil {
		t.Fatal(err)
	}
	if math.Abs(d.Pos().Seconds()-1.0) > 0.01 {
		t.Errorf("after quantize pos = %v, want 1.0s", d.Pos().Seconds())
	}
}

func TestQuantizeIsClickFree(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 20)))
	d.AttachBeatGrid(testGrid(120.0, 40))
	d.Play()
	tmp := make(dsp.Stereo, 512*2)
	for i := 0; i < 100; i++ {
		d.Render(tmp)
	}
	if err := d.QuantizeToNearestBeat(); err != nil {
		t.Fatal(err)
	}
	d.Render(tmp)
	if math.IsNaN(tmp.RMS()) {
		t.Fatal("quantize produced NaN")
	}
	if tmp.Peak() == 0 {
		t.Error("quantize silenced the deck")
	}
}

func TestQuantizeWithoutGridErrors(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 10)))
	if err := d.QuantizeToNearestBeat(); err == nil {
		t.Error("quantize without a beatgrid should error")
	}
}

func TestDeltaBeatReportsOffset(t *testing.T) {
	master := NewDeck(0)
	master.Load(memTrack(dsp.Sine(440, 30)))
	master.AttachBeatGrid(testGrid(128.0, 60))
	master.seek(4 * time.Second) // exactly on a beat
	slave := NewDeck(1)
	slave.Load(memTrack(dsp.Sine(440, 30)))
	slave.AttachBeatGrid(testGrid(128.0, 60))
	slave.seek(4*time.Second + 40*time.Millisecond)
	delta, err := slave.DeltaBeat(master)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(delta.Seconds()-0.040) > 0.005 {
		t.Errorf("delta = %v, want ~40ms", delta)
	}
}

func TestSyncDoesNotRelayLoops(t *testing.T) {
	// Sync retunes the deck but must NOT move the loop (spec §5.6).
	master := NewDeck(0)
	master.Load(memTrack(dsp.Sine(440, 40)))
	master.AttachBeatGrid(testGrid(128.0, 80))
	slave := NewDeck(1)
	slave.Load(memTrack(dsp.Sine(440, 40)))
	slave.AttachBeatGrid(testGrid(128.0, 80))
	slave.Play()
	if err := slave.SetLoopBeat(4); err != nil {
		t.Fatal(err)
	}
	ls := slave.Loop()
	if err := slave.EngageSync(master); err != nil {
		t.Fatal(err)
	}
	got := slave.Loop()
	if got.Start != ls.Start || got.End != ls.End {
		t.Errorf("sync moved the loop: %v-%v -> %v-%v", ls.Start, ls.End, got.Start, got.End)
	}
}

func TestNudgeBeats(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 20)))
	d.AttachBeatGrid(testGrid(128.0, 40))
	d.seek(4 * time.Second)
	if err := d.NudgeBeats(-1); err != nil {
		t.Fatal(err)
	}
	want := 4*time.Second - time.Second*60/128
	if math.Abs(d.Pos().Seconds()-want.Seconds()) > 0.01 {
		t.Errorf("after -1 beat pos = %v, want %v", d.Pos(), want)
	}
}

// An extreme tempo difference must clamp to the pitch range rather than
// producing an absurd speed-up.
func TestSyncClampsToPitchRange(t *testing.T) {
	master := NewDeck(0)
	master.Load(memTrack(dsp.Sine(440, 30)))
	master.AttachBeatGrid(testGrid(200.0, 100))
	slave := NewDeck(1)
	slave.Load(memTrack(dsp.Sine(440, 30)))
	slave.AttachBeatGrid(testGrid(60.0, 200))
	if err := slave.EngageSync(master); err != nil {
		t.Fatal(err)
	}
	renderBlocks(slave, BlockFrames, 1)
	if got := slave.PitchRatio(); got > 1+PitchRange+1e-9 {
		t.Errorf("pitch ratio %v exceeds the fader range %v", got, 1+PitchRange)
	}
	if got := slave.Pitch(); got < -1 || got > 1 {
		t.Errorf("normalized pitch %v out of range", got)
	}
}

func TestDisableSyncStopsTracking(t *testing.T) {
	master := NewDeck(0)
	master.Load(memTrack(dsp.Sine(440, 30)))
	master.AttachBeatGrid(testGrid(128.0, 60))
	slave := NewDeck(1)
	slave.Load(memTrack(dsp.Sine(440, 30)))
	slave.AttachBeatGrid(testGrid(124.0, 60))
	if err := slave.EngageSync(master); err != nil {
		t.Fatal(err)
	}
	if !slave.SyncActive() {
		t.Fatal("sync not active after engage")
	}
	slave.DisableSync()
	if slave.SyncActive() {
		t.Error("sync still active after disable")
	}
}

// SyncPair is the programmatic entry point the TUI and mixrender both use.
func TestSyncPairLocksTempos(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 30)))
	e.Deck(1).Load(memTrack(dsp.Sine(440, 30)))
	e.Deck(0).AttachBeatGrid(testGrid(128.0, 60))
	e.Deck(1).AttachBeatGrid(testGrid(124.0, 60))
	if err := e.SyncPair(1, 0); err != nil {
		t.Fatal(err)
	}
	renderBlocks(e.Deck(1), BlockFrames, 1)
	if got := e.Deck(1).PitchRatio(); got < 1.02 || got > 1.05 {
		t.Errorf("slave pitch ratio = %v, want ~1.032", got)
	}
	if !e.Deck(1).SyncActive() {
		t.Error("slave not marked synced")
	}
}

// Sync driven through Apply must reach the right decks and report no error
// when both carry grids.
func TestSyncActionEngagesSync(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 30)))
	e.Deck(1).Load(memTrack(dsp.Sine(440, 30)))
	e.Deck(0).AttachBeatGrid(testGrid(128.0, 60))
	e.Deck(1).AttachBeatGrid(testGrid(124.0, 60))
	e.Apply(Action{Kind: Sync, Target: 1})
	renderBlocks(e.Deck(1), BlockFrames, 1)
	if !e.Deck(1).SyncActive() {
		t.Error("Sync action did not engage sync")
	}
}

// Quantize through Apply must snap the deck to the beat.
func TestQuantizeActionSnapsToBeat(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 20)))
	e.Deck(0).AttachBeatGrid(testGrid(120.0, 40))
	e.Deck(0).seek(1050 * time.Millisecond)
	e.Apply(Action{Kind: Quantize, Target: 0})
	if math.Abs(e.Deck(0).Pos().Seconds()-1.0) > 0.01 {
		t.Errorf("after Quantize action pos = %v, want 1.0s", e.Deck(0).Pos())
	}
}
