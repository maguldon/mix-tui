package engine

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/analyze"
)

// A deck with no grid must still be fully usable. Beat-dependent features
// report analyze.ErrNoBeatGrid; nothing else may break (spec §8).
func TestDeckWithoutBeatGridIsPlayable(t *testing.T) {
	d := NewDeck(0)
	if d.HasBeatGrid() {
		t.Fatal("empty deck reported a beatgrid")
	}
	if d.BeatGrid() != nil {
		t.Fatal("BeatGrid returned non-nil on an empty deck")
	}
	if d.IsPlaying() {
		t.Fatal("empty deck reported playing")
	}
	d.Play()
	if d.IsPlaying() {
		t.Error("deck could not play without a beatgrid")
	}
}

func TestDeckAttachBeatGrid(t *testing.T) {
	d := NewDeck(0)
	if d.HasBeatGrid() {
		t.Fatal("fresh deck already had a grid")
	}

	grid := &analyze.BeatGrid{BPM: 128, Confidence: 0.9}
	d.AttachAnalysisNow(grid, nil)
	if !d.HasBeatGrid() {
		t.Error("attached grid not reported")
	}
	if got := d.BeatGrid(); got != grid {
		t.Errorf("BeatGrid() = %p, want %p", got, grid)
	}

	// A nil grid returns the deck to its playable-but-ungridded state.
	d.AttachAnalysisNow(nil, nil)
	if d.HasBeatGrid() {
		t.Error("nil grid still reported as available")
	}
}

// A grid the analyser could not trust must not enable beat-dependent features.
func TestDeckLowConfidenceGridIsNotAvailable(t *testing.T) {
	d := NewDeck(0)
	d.AttachAnalysisNow(&analyze.BeatGrid{BPM: 128, Confidence: 0}, nil)
	if d.HasBeatGrid() {
		t.Error("zero-confidence grid enabled beat features")
	}
	// The grid is still attached and inspectable; it just is not usable.
	if d.BeatGrid() == nil {
		t.Error("low-confidence grid was discarded rather than flagged")
	}
}
