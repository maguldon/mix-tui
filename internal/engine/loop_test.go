package engine

import (
	"math"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// testGrid builds a synthetic beatgrid of n beats at bpm.
func testGrid(bpm float64, beats int) *analyze.BeatGrid {
	g := &analyze.BeatGrid{BPM: bpm, Confidence: 0.9}
	per := time.Duration(float64(time.Second) / bpm)
	for i := 0; i < beats; i++ {
		g.Beats = append(g.Beats, time.Duration(i)*per)
	}
	return g
}

func TestBeatLoopWrapsAndIsClickFree(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 10.0)))
	d.AttachBeatGrid(testGrid(128.0, 4))
	d.Play()
	if err := d.SetLoopBeat(4); err != nil {
		t.Fatal(err)
	}
	ls := d.Loop()
	if !ls.Active {
		t.Fatal("loop not active")
	}
	if math.Abs(ls.BeatCount-4) > 1e-9 {
		t.Errorf("beatCount = %v", ls.BeatCount)
	}

	// Render past several wraps.
	tmp := make(dsp.Stereo, 512*2)
	for i := 0; i < 900; i++ {
		d.Render(tmp)
	}

	// The position must never leave the loop region.
	if d.Pos() < ls.Start || d.Pos() > ls.End+50*time.Millisecond {
		t.Errorf("position %v escaped loop [%v, %v]", d.Pos(), ls.Start, ls.End)
	}
}

func TestBeatLoopAtEndOfTrackWrapsNotStops(t *testing.T) {
	// A loop starting near the track end must WRAP, not stop at the track end
	// or go silent.
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	d.AttachBeatGrid(testGrid(120.0, 8))
	d.Play()
	d.seek(1900 * time.Millisecond)
	if err := d.SetLoopBeat(1); err != nil {
		t.Fatal(err)
	}
	tmp := make(dsp.Stereo, 512*2)
	for i := 0; i < 600; i++ {
		d.Render(tmp)
	}
	if tmp.Peak() == 0 {
		t.Error("deck went silent at track end with an active loop")
	}
	if !d.Loop().Active {
		t.Error("loop deactivated at track end")
	}
}

func TestBeatLoopLengthTracksPitch(t *testing.T) {
	// A 4-beat loop must contain 4 beats at ANY pitch. This is the test that
	// catches a loop whose length was measured in output samples rather than
	// source samples, which would stretch the loop as the pitch fader moves.
	for _, pitch := range []float64{-1.0, -0.5, 0.0, 0.5, 1.0} {
		d := NewDeck(0)
		d.Load(memTrack(dsp.Sine(440, 20.0)))
		d.AttachBeatGrid(testGrid(128.0, 40))
		d.SetPitch(pitch)
		d.Play()
		renderBlocks(d, BlockFrames, 1) // apply the posted pitch
		if err := d.SetLoopBeat(4); err != nil {
			t.Fatal(err)
		}
		ls := d.Loop()
		// 128 BPM means 128 beats per MINUTE, so 4 beats take 4*60/128.
		want := 4 * 60 * time.Second / 128
		if diff := (ls.End - ls.Start) - want; diff > 10*time.Millisecond || diff < -10*time.Millisecond {
			t.Errorf("pitch %v: loop = %v, want %v", pitch, ls.End-ls.Start, want)
		}
	}
}

func TestBeatLoopWithoutGridErrors(t *testing.T) {
	// No grid means the feature is unavailable, not wrong.
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 5.0)))
	if err := d.SetLoopBeat(4); err == nil {
		t.Error("beat loop on a deck with no beatgrid should error")
	}
	if d.Loop().Active {
		t.Error("loop activated without a beatgrid")
	}
}

func TestFreeLoopAndSaveRecall(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 10.0)))
	d.Play()
	d.seek(2 * time.Second)
	if err := d.SetLoopIn(); err != nil {
		t.Fatal(err)
	}
	d.seek(4 * time.Second)
	if err := d.SetLoopOut(); err != nil {
		t.Fatal(err)
	}
	ls := d.Loop()
	if ls.BeatCount != 0 {
		t.Errorf("free loop has BeatCount %v, want 0", ls.BeatCount)
	}
	if err := d.SaveLoop(3); err != nil {
		t.Fatal(err)
	}
	d.ReleaseLoop()
	if d.Loop().Active {
		t.Fatal("still active after release")
	}
	if err := d.RecallLoop(3); err != nil {
		t.Fatal(err)
	}
	if !d.Loop().Active {
		t.Fatal("recall did not activate")
	}
}

func TestLoopOutBeforeInErrors(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 10.0)))
	d.seek(4 * time.Second)
	d.SetLoopIn()
	d.seek(2 * time.Second)
	if err := d.SetLoopOut(); err == nil {
		t.Error("loop out before loop in should error")
	}
}

func TestLoopOutAtTrackEndIsClamped(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 3.0)))
	d.Play()
	d.seek(1 * time.Second)
	d.SetLoopIn()
	d.seek(10 * time.Second) // past the end
	if err := d.SetLoopOut(); err != nil {
		t.Fatal(err)
	}
	if d.Loop().End > d.Duration() {
		t.Errorf("loop end %v past duration %v", d.Loop().End, d.Duration())
	}
}

func TestLoopReleaseContinuesFromLoopEnd(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 10.0)))
	d.AttachBeatGrid(testGrid(128.0, 20))
	d.Play()
	d.SetLoopBeat(4)
	tmp := make(dsp.Stereo, 512*2)
	for i := 0; i < 200; i++ {
		d.Render(tmp)
	}
	d.ReleaseLoop()
	if d.Loop().Active {
		t.Fatal("still looping")
	}
	p1 := d.Pos()
	for i := 0; i < 100; i++ {
		d.Render(tmp)
	}
	if d.Pos() <= p1 {
		t.Error("position did not advance after release")
	}
}

// --- roll ---

func TestRollRestoresPositionOnRelease(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 20.0)))
	d.AttachBeatGrid(testGrid(128.0, 40))
	d.Play()
	tmp := make(dsp.Stereo, 512*2)
	for i := 0; i < 100; i++ {
		d.Render(tmp)
	}
	before := d.Pos()
	if err := d.PushRoll(1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		d.Render(tmp)
	}
	if d.Pos() == before {
		t.Error("roll did not move the playhead")
	}
	if err := d.PopRoll(); err != nil {
		t.Fatal(err)
	}
	if d.Pos() != before {
		t.Errorf("after release pos = %v, want %v", d.Pos(), before)
	}
}

func TestRollReleaseWithEmptyStackIsNoOp(t *testing.T) {
	// A stray release must NEVER move the playhead.
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 10.0)))
	d.Play()
	tmp := make(dsp.Stereo, 512*2)
	for i := 0; i < 50; i++ {
		d.Render(tmp)
	}
	before := d.Pos()
	if err := d.PopRoll(); err == nil {
		t.Error("PopRoll on an empty stack should error")
	}
	if d.Pos() != before {
		t.Errorf("stray release moved playhead %v -> %v", before, d.Pos())
	}
}

func TestRollFromInsideLoopRestoresLoop(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 20.0)))
	d.AttachBeatGrid(testGrid(128.0, 40))
	d.Play()
	d.SetLoopBeat(8)
	tmp := make(dsp.Stereo, 512*2)
	for i := 0; i < 100; i++ {
		d.Render(tmp)
	}
	beforePos, beforeActive := d.Pos(), d.Loop().Active
	d.PushRoll(1)
	for i := 0; i < 60; i++ {
		d.Render(tmp)
	}
	d.PopRoll()
	if d.Pos() != beforePos {
		t.Errorf("pos = %v, want %v", d.Pos(), beforePos)
	}
	if d.Loop().Active != beforeActive {
		t.Errorf("loop active = %v, want %v", d.Loop().Active, beforeActive)
	}
}

func TestRollWithoutGridErrors(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 5)))
	if err := d.PushRoll(1); err == nil {
		t.Error("roll on a deck with no beatgrid should error")
	}
}

// The wrap must be performed by the seek primitive, not by writing the
// position field. A raw position write skips resampler priming and produces an
// audible seam at every loop point, which is the defect this whole feature
// exists to avoid (spec §4.4.1).
func TestLoopWrapIsClickFree(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 10.0)))
	d.AttachBeatGrid(testGrid(128.0, 8))
	d.Play()
	if err := d.SetLoopBeat(4); err != nil {
		t.Fatal(err)
	}
	tmp := make(dsp.Stereo, BlockFrames*2)
	out := make(dsp.Stereo, 0, 900*BlockFrames*2)
	for i := 0; i < 900; i++ {
		// Honour the frame count: the block is shortened at the loop end, and
		// appending the whole buffer would splice the unwritten tail into the
		// signal as a discontinuity that does not exist in the audio.
		n := d.Render(tmp)
		out = append(out, tmp[:n*2]...)
	}
	// A 440Hz sine's maximum legitimate frame-to-frame step is its slope.
	// A raw position write would show a step near full scale instead.
	if jump := worstStep(out); jump > maxSlope(460) {
		t.Errorf("loop wrap discontinuity %v exceeds tone slope %v", jump, maxSlope(460))
	}
}

// worstStep returns the largest frame-to-frame jump in a stereo buffer.
func worstStep(s dsp.Stereo) float64 {
	worst := 0.0
	for i := 2; i < len(s); i += 2 {
		d := math.Abs(float64(s[i] - s[i-2]))
		if d > worst {
			worst = d
		}
	}
	return worst
}

// A loop must not leak: releasing restores ordinary forward playback.
func TestLoopDoesNotAllocateOnWrap(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 10.0)))
	d.AttachBeatGrid(testGrid(128.0, 20))
	d.Play()
	if err := d.SetLoopBeat(4); err != nil {
		t.Fatal(err)
	}
	tmp := make(dsp.Stereo, BlockFrames*2)
	allocs := testing.AllocsPerRun(200, func() { d.Render(tmp) })
	if allocs != 0 {
		t.Errorf("looping render allocates %v times per block, want 0", allocs)
	}
}
