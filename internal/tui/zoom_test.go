package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

// TestWindowLengthIsFourBars pins the window to real seconds.
//
// It used to compute the expectation as time.Duration(16) * time.Second / 128,
// which is 16/128 of a second rather than 16 beats. The implementation had the
// same error -- a beat taken as time.Second/bpm instead of time.Minute/bpm -- so
// the two agreed, the test passed, and every zoom window was 60x too short. A
// test written from the same mistaken formula as the code confirms nothing.
//
// Four bars is 16 beats; at 128 BPM a beat is 60/128 = 468.75ms, so 7.5s.
func TestWindowLengthIsFourBars(t *testing.T) {
	w := WindowFor(30*time.Second, 4, 128)
	beat := time.Duration(float64(time.Minute) / 128)
	want := 16 * beat
	if absDur(w.Length-want) > time.Millisecond {
		t.Errorf("window = %v, want 4 bars = 16 beats = %v", w.Length, want)
	}
}

func TestWindowTracksPosition(t *testing.T) {
	a := WindowFor(30*time.Second, 4, 128)
	b := WindowFor(90*time.Second, 4, 128)
	if b.Start-a.Start != 60*time.Second {
		t.Errorf("window did not track: %v vs %v", a.Start, b.Start)
	}
}

func TestWindowScalesWithTempo(t *testing.T) {
	slow := WindowFor(0, 4, 100)
	fast := WindowFor(0, 4, 140)
	if fast.Length >= slow.Length {
		t.Errorf("faster tempo should give a shorter window: %v vs %v", fast.Length, slow.Length)
	}
}

func TestWindowHandlesZeroBPM(t *testing.T) {
	w := WindowFor(30*time.Second, 4, 0)
	if w.Length <= 0 {
		t.Errorf("zero BPM must fall back, got length %v", w.Length)
	}
}

func TestZoomIndexCycles(t *testing.T) {
	i := 0
	seen := map[int]bool{}
	for n := 0; n < len(ZoomBarOptions); n++ {
		seen[ZoomBarOptions[i]] = true
		i = ZoomIndex(i)
	}
	if len(seen) != len(ZoomBarOptions) {
		t.Errorf("zoom cycle covered %d of %d", len(seen), len(ZoomBarOptions))
	}
}

func TestRenderZoomedDimensions(t *testing.T) {
	w := WindowFor(30*time.Second, 4, 128)
	rows := RenderZoomed(dsp.Sine(440, 60), w, 60, 4, ZoomMarkers{}, dsp.SampleRate)
	if len(rows) != 4 {
		t.Errorf("got %d rows", len(rows))
	}
}

func TestRenderZoomedShowsBeatsInWindow(t *testing.T) {
	w := WindowFor(30*time.Second, 4, 128)
	// Beats must fall inside the window to be visible; spread them across it.
	beats := make([]time.Duration, 200)
	for i := range beats {
		beats[i] = w.Start + time.Duration(i)*w.Length/200
	}
	withBeats := RenderZoomed(dsp.Sine(440, 60), w, 60, 2, ZoomMarkers{Beats: beats}, dsp.SampleRate)
	without := RenderZoomed(dsp.Sine(440, 60), w, 60, 2, ZoomMarkers{}, dsp.SampleRate)
	if strings.Join(withBeats, "\n") == strings.Join(without, "\n") {
		t.Error("beat markers changed nothing inside the window")
	}
}

func TestDeltaBeatReportsOffset(t *testing.T) {
	// A beat at 128bpm is 7.8125ms, so half a beat is 3.9ms. 62500us is
	// exactly 8 beats; 2ms past it is a real phase offset that must survive the
	// wrap into (-beat/2, beat/2].
	// The engine computes phase against both real beatgrids and reports it in
	// the Snapshot, so the view reads a value rather than re-deriving one.
	const twoMs = 2 * time.Millisecond
	master := engine.DeckState{HasDelta: true, DeltaMS: 0}
	slave := engine.DeckState{HasDelta: true, DeltaMS: float64(twoMs) / float64(time.Millisecond)}
	d, ok := DeltaBeat(slave, master)
	if !ok {
		t.Fatal("delta unavailable despite grids")
	}
	if absDur(d-twoMs) > 500*time.Microsecond {
		t.Errorf("delta = %v, want ~%v", d, twoMs)
	}
}

func TestDeltaBeatNeedsBothGrids(t *testing.T) {
	// Either deck lacking a beatgrid means no delta: showing a confident number
	// computed from one deck's grid alone would be worse than showing none.
	_, ok := DeltaBeat(engine.DeckState{}, engine.DeckState{HasDelta: true})
	if ok {
		t.Error("delta must be unavailable when a grid is missing")
	}
}

func TestRenderDeltaBeatShowsPhases(t *testing.T) {
	s := strings.TrimSpace(RenderDeltaBeat(
		engine.DeckState{HasDelta: true, DeltaMS: 0},
		engine.DeckState{HasDelta: true, DeltaMS: 2}))
	if !strings.Contains(s, "ms") {
		t.Errorf("expected a ms readout, got %q", s)
	}
}

// TestRenderZoomedIsBounded sweeps the renderer over sizes, since a zoomed wave
// that overruns its box corrupts the whole layout around it.
func TestRenderZoomedIsBounded(t *testing.T) {
	pcm := dsp.Sine(440, 60)
	w := WindowFor(30*time.Second, 4, 128)
	for _, width := range []int{0, 1, 2, 10, 40, 120} {
		for _, height := range []int{0, 1, 2, 4, 8} {
			rows := RenderZoomed(pcm, w, width, height, ZoomMarkers{}, dsp.SampleRate)
			// A zero width has nothing to draw into, so no rows is correct.
			if width > 0 && height > 0 && len(rows) != height {
				t.Errorf("width %d height %d gave %d rows", width, height, len(rows))
			}
			for _, r := range rows {
				if displayWidth(r) > maxInt(0, width) {
					t.Errorf("width %d row %q", width, r)
				}
			}
		}
	}
}

// TestRenderZoomedIgnoresMarkersOutsideWindow guards the window filter: a cue
// twenty minutes away must not be drawn in the middle of the view.
func TestRenderZoomedIgnoresMarkersOutsideWindow(t *testing.T) {
	pcm := dsp.Sine(440, 60)
	w := WindowFor(30*time.Second, 4, 128)
	inWindow := make([]time.Duration, 64)
	for i := range inWindow {
		inWindow[i] = w.Start + time.Duration(i)*time.Second/128
	}
	withIn := RenderZoomed(pcm, w, 60, 2, ZoomMarkers{Beats: inWindow}, dsp.SampleRate)
	none := RenderZoomed(pcm, w, 60, 2, ZoomMarkers{}, dsp.SampleRate)
	if strings.Join(withIn, "\n") == strings.Join(none, "\n") {
		t.Error("in-window beats changed nothing")
	}
	outside := []time.Duration{5 * time.Hour, -2 * time.Hour}
	same := RenderZoomed(pcm, w, 60, 2, ZoomMarkers{Beats: outside}, dsp.SampleRate)
	if strings.Join(same, "\n") != strings.Join(none, "\n") {
		t.Error("markers outside the window were drawn inside it")
	}
}

// TestRenderZoomedHandlesShortPCM feeds a window past the end of the audio,
// which happens when the user zooms near the end of a track.
func TestRenderZoomedHandlesShortPCM(t *testing.T) {
	w := WindowFor(50*time.Second, 4, 128)
	rows := RenderZoomed(dsp.Sine(440, 2), w, 60, 4, ZoomMarkers{}, dsp.SampleRate)
	if len(rows) != 4 {
		t.Errorf("short PCM gave %d rows", len(rows))
	}
}

// TestRenderZoomedRespectsSampleRate pins spec 4.2's rate-agnostic requirement.
// Converting window times with the dsp.SampleRate constant put every window
// 8.8% out at 48kHz, so the zoomed waveform was drawn from the wrong region of
// the audio and the playhead did not match what was drawn.
//
// The previous version of this test computed the expected start frame from
// w.Start and its own rate, but w.Start comes from WindowFor, which takes no
// rate -- so the assertion never touched RenderZoomed's rate handling and
// passed unchanged against the bug it was written for.
//
// This puts a single click at a known second and asserts it lands in the
// column the window geometry puts it in. That assertion only holds if
// RenderZoomed actually converted the window with the rate it was given.
func TestRenderZoomedRespectsSampleRate(t *testing.T) {
	// One click per second, so a click marks every whole second of the PCM.
	const rate = 48000
	const seconds = 40
	pcm := make(dsp.Stereo, seconds*rate*2)
	for i := 0; i < seconds*rate; i++ {
		v := float32(0)
		if i%rate == 0 {
			v = 1
		}
		pcm[i*2], pcm[i*2+1] = v, v
	}

	const width = 60
	w := WindowFor(30*time.Second, 2, 128)
	rows := RenderZoomed(pcm, w, width, 4, ZoomMarkers{}, rate)
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}

	// The window starts at 30s and is 2s long, so the clicks at 30s and 31s
	// are inside it, at columns 0 and width/2. Under an 8.8% error the slice
	// would begin at 27.4s and neither click would be where it belongs.
	lit := litColumns(rows, width)
	for _, want := range []float64{0, 0.5} {
		col := int(want * float64(width-1))
		if lit[col] == 0 {
			t.Errorf("no waveform at column %d (expected a click at %.0f%% of the window); lit=%v",
				col, want*100, lit)
		}
	}

	// The strongest form of the check: rendering the same window with the wrong
	// rate must produce a different picture. If it produced the same one, the
	// rate argument would not be reaching the slice at all.
	wrong := RenderZoomed(pcm, w, width, 4, ZoomMarkers{}, 44100)
	if sameAs(t, rows, wrong) {
		t.Error("rendering at 44.1k and 48k produced identical output; the rate argument is not being used")
	}

	// A non-positive rate falls back to the default rather than dividing by zero.
	if got := RenderZoomed(pcm, w, width, 4, ZoomMarkers{}, 0); len(got) != 4 {
		t.Error("a zero sample rate must fall back, not fail")
	}
}

// litColumns reports, per column, how many of the rows have a glyph there.
func litColumns(rows []string, width int) []int {
	lit := make([]int, width)
	for _, row := range rows {
		r := []rune(row)
		for i := 0; i < width && i < len(r); i++ {
			if r[i] != ' ' {
				lit[i]++
			}
		}
	}
	return lit
}

func sameAs(t *testing.T, a, b []string) bool {
	t.Helper()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
