package engine

import (
	"math"
	"testing"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// TestAcceptanceFullMix renders a complete DJ mix and asserts the properties
// the spec promises. This is the milestone-1 exit criterion.
func TestAcceptanceFullMix(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
		RingFrames: 44100, DeckCount: 2, CuedIndex: 1})

	// Two tracks with known grids.
	d0, d1 := e.Deck(0), e.Deck(1)
	d0.Load(memTrack(dsp.Sine(220, 60)))
	d0.AttachAnalysisNow(testGrid(128.0, 120), nil)
	d1.Load(memTrack(dsp.Sine(330, 60)))
	d1.AttachAnalysisNow(testGrid(124.0, 120), nil)

	e.Apply(Action{Kind: DeckPlay, Target: 0})
	e.Apply(Action{Kind: DeckPlay, Target: 1})
	e.Apply(Action{Kind: MixerCrossfader, Value: 0})

	var collected dsp.Stereo
	main, cue := audio.NewRing(1<<20), audio.NewRing(1<<20)
	tmp := make(dsp.Stereo, 512*2)
	const blocks = 512 // ~6 seconds
	for i := 0; i < blocks; i++ {
		if err := e.RenderInto(main, cue); err != nil {
			t.Fatal(err)
		}
		for main.AvailableRead() >= 512 {
			n := main.Read(tmp)
			collected = append(collected, tmp[:n*2]...)
		}
		for cue.Read(tmp) > 0 {
		}
	}

	s := e.Snapshot()

	// 1. both decks played
	if !s.Decks[0].Playing || !s.Decks[1].Playing {
		t.Errorf("both decks should be playing: %+v", s.Decks)
	}
	// 2. audio was produced and never clipped
	if collected.RMS() < 0.01 {
		t.Errorf("output silent: %v", collected.RMS())
	}
	if collected.Peak() > 1.0 {
		t.Errorf("output clipped: %v", collected.Peak())
	}
	// 3. levels are reported
	if s.MasterLevel <= 0 {
		t.Errorf("master level = %v", s.MasterLevel)
	}
	// 4. both positions advanced
	if s.Decks[0].Position <= 0 || s.Decks[1].Position <= 0 {
		t.Errorf("positions did not advance: %+v", s.Decks)
	}
	// 5. no output was dropped on the way out
	if s.Overruns != 0 {
		t.Errorf("overruns = %d; the consumer should have kept up", s.Overruns)
	}
}

// TestAcceptanceLoopRollSyncSequence walks the whole performance path: play,
// beat loop, sync, roll, release.
func TestAcceptanceLoopRollSyncSequence(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
		RingFrames: 44100, DeckCount: 2})
	m, s := e.Deck(0), e.Deck(1)
	m.Load(memTrack(dsp.Sine(220, 60)))
	m.AttachAnalysisNow(testGrid(128, 120), nil)
	s.Load(memTrack(dsp.Sine(330, 60)))
	s.AttachAnalysisNow(testGrid(124, 120), nil)
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	e.Apply(Action{Kind: DeckPlay, Target: 1})

	// Render into a ring large enough that nothing is dropped, draining it the
	// way an audio callback would.
	main, cue := audio.NewRing(1<<16), audio.NewRing(1<<16)
	tmp := make(dsp.Stereo, 512*2)
	step := func(n int) {
		for i := 0; i < n; i++ {
			if err := e.RenderInto(main, cue); err != nil {
				t.Fatal(err)
			}
			for main.Read(tmp) > 0 {
			}
			for cue.Read(tmp) > 0 {
			}
		}
	}

	step(50)
	if err := s.SetLoopBeat(4); err != nil {
		t.Fatal(err)
	}
	step(100)
	if !s.Loop().Active {
		t.Fatal("beat loop did not engage")
	}
	loopBefore := s.Loop()

	if err := s.EngageSync(m); err != nil {
		t.Fatal(err)
	}
	step(100)
	if math.Abs(s.PitchRatio()-1.032) > 0.01 {
		t.Errorf("after sync ratio = %v, want ~1.032", s.PitchRatio())
	}
	if !s.Loop().Active {
		t.Error("sync deactivated the loop")
	}
	// Sync must not re-lay the loop (spec §5.6).
	if got := s.Loop(); got.Start != loopBefore.Start || got.End != loopBefore.End {
		t.Errorf("sync moved the loop: %v-%v -> %v-%v",
			loopBefore.Start, loopBefore.End, got.Start, got.End)
	}

	if err := s.PushRoll(1); err != nil {
		t.Fatal(err)
	}
	step(50)
	if err := s.PopRoll(); err != nil {
		t.Fatal(err)
	}
	if !s.Loop().Active {
		t.Error("roll release dropped the underlying loop")
	}

	s.ReleaseLoop()
	step(50)
	if s.Loop().Active {
		t.Error("loop still active after release")
	}
	if math.IsNaN(e.Snapshot().MasterLevel) {
		t.Fatal("NaN in final snapshot")
	}
}

// A loop must keep playing past the end of its source material. This is the
// property that separates a loop from a seek-and-stop.
func TestAcceptanceLoopOutlivesTrackEnd(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	d.AttachAnalysisNow(testGrid(120.0, 8), nil)
	d.Play()
	if err := d.SetLoopBeat(1); err != nil {
		t.Fatal(err)
	}
	tmp := make(dsp.Stereo, BlockFrames*2)
	for i := 0; i < 400; i++ {
		if n := d.Render(tmp); n == 0 {
			t.Fatalf("deck stopped at block %d with an active loop", i)
		}
	}
	if !d.Loop().Active {
		t.Error("loop deactivated")
	}
	if !d.IsPlaying() {
		t.Error("deck stopped while looping")
	}
}

// RenderTarget is the seam milestone 2 attaches a device to, so its two
// adapters must behave as the engine already assumes.
func TestRenderTargetAdapters(t *testing.T) {
	r := audio.NewRing(4096)
	src := dsp.Sine(440, 0.1)
	n := r.Write(src)
	if n == 0 {
		t.Fatal("ring rejected a write")
	}

	out := make(dsp.Stereo, 512*2)
	if !RingTarget(r).Consume(out) {
		t.Fatal("ring target reported no audio for a non-empty ring")
	}
	// Draining must eventually report empty rather than blocking.
	for r.AvailableRead() > 0 {
		RingTarget(r).Consume(out)
	}
	if RingTarget(r).Consume(out) {
		t.Error("ring target reported audio from an empty ring")
	}

	var got int
	FuncTarget(func(s dsp.Stereo) { got += s.Frames() }).Consume(out)
	if got == 0 {
		t.Error("func target did not deliver the block")
	}
}
