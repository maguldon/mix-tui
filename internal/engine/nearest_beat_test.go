package engine

import (
	"math"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// bruteForceNearestBeat is the original linear scan, kept as a reference for
// the equivalence test below.
func bruteForceNearestBeat(d *Deck, frame float64) (float64, bool) {
	if d.beatgrid == nil || d.beatgrid.BPM <= 0 {
		return 0, false
	}
	if beats := d.beatgrid.Beats; len(beats) > 0 {
		best := math.Inf(1)
		var bestFrame float64
		for _, b := range beats {
			f := d.durationToFrames(b)
			if diff := math.Abs(f - frame); diff < best {
				best, bestFrame = diff, f
			}
		}
		return bestFrame, true
	}
	per := d.beatPeriodFrames()
	if per <= 0 {
		return 0, false
	}
	return math.Round(frame/per) * per, true
}

func deckWithBeats(n int, bpm float64) *Deck {
	beats := make([]time.Duration, n)
	per := time.Duration(float64(time.Minute) / bpm)
	for i := range beats {
		beats[i] = time.Duration(i) * per
	}
	d := NewDeckAtRate(0, dsp.SampleRate)
	d.beatgrid = &analyze.BeatGrid{BPM: bpm, Beats: beats}
	return d
}

// TestNearestBeatMatchesALinearScan is the correctness guard for the binary
// search. It must return exactly the same beat as a scan over every beat, at
// every position including the edges -- a search that is merely close is not
// good enough, because the whole point of the grid is that both decks agree on
// which beat they are on.
func TestNearestBeatMatchesALinearScan(t *testing.T) {
	d := deckWithBeats(1000, 128)
	per := d.beatPeriodFrames()

	frames := []float64{
		0, 1, per - 1, per, per + 1, // the edges and either side of the first beat
		per * 999, per*1000 - 1, per * 1000, // the end
		per*1000 + 1, // past the last beat
	}
	// Plus a sweep across the whole track at sub-beat resolution.
	for i := 0.0; i < per*1000; i += per / 7 {
		frames = append(frames, i)
	}

	for _, f := range frames {
		wantF, wantOK := bruteForceNearestBeat(d, f)
		gotF, gotOK := d.nearestBeat(f)
		if wantOK != gotOK {
			t.Fatalf("frame %v: ok mismatch, want %v got %v", f, wantOK, gotOK)
		}
		if wantOK && gotF != wantF {
			t.Errorf("frame %v: got beat at %v frames, want %v", f, gotF, wantF)
		}
	}
}

// TestNearestBeatIsCorrectAtGridEdges covers the two cases a binary search is
// most likely to get wrong: before the first beat and after the last.
func TestNearestBeatIsCorrectAtGridEdges(t *testing.T) {
	d := deckWithBeats(4, 120)

	if got, _ := d.nearestBeat(-1000); got != 0 {
		t.Errorf("before the first beat: got frame %v, want 0", got)
	}

	lastFrame := d.durationToFrames(d.beatgrid.Beats[3])
	got, _ := d.nearestBeat(lastFrame + 10000)
	if got != lastFrame {
		t.Errorf("after the last beat: got %v, want the last beat %v", got, lastFrame)
	}
}

// TestNearestBeatDoesNotScaleWithTrackLength is the performance guard. The
// linear scan put work proportional to track length behind the engine mutex,
// thirty times a second, on the render path. Binary search makes it logarithmic,
// so a seven-hour set costs no more than a five-minute one.
func TestNearestBeatDoesNotScaleWithTrackLength(t *testing.T) {
	// 50,000 beats at 120BPM is about seven hours.
	const beats = 50000
	d := deckWithBeats(beats, 120)

	start := time.Now()
	for i := 0; i < 1000; i++ {
		_, _ = d.nearestBeat(float64(i) * 1000)
	}
	per := time.Since(start) / 1000

	// A linear scan of 50,000 entries takes roughly 500us. 10us leaves two
	// orders of magnitude of headroom while still failing on a regression to
	// the scan.
	if per > 10*time.Microsecond {
		t.Errorf("nearestBeat takes %v on a %d-beat grid; it must not scan", per, beats)
	}
	t.Logf("nearestBeat on %d beats: %v", beats, per)
}

// TestNearestBeatWithoutAListStillUsesTempo guards the branch the binary search
// did not touch.
func TestNearestBeatWithoutAListStillUsesTempo(t *testing.T) {
	d := NewDeckAtRate(0, dsp.SampleRate)
	d.beatgrid = &analyze.BeatGrid{BPM: 128}

	per := d.beatPeriodFrames()
	for _, mult := range []float64{0, 1, 1.5, 2, 10.4} {
		f := per * mult
		got, ok := d.nearestBeat(f)
		if !ok {
			t.Fatal("a tempo-only grid should still resolve a beat")
		}
		if want := math.Round(f/per) * per; got != want {
			t.Errorf("frame %v: got %v, want %v", f, got, want)
		}
	}
}
