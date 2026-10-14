package main

import (
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/tui/library"
)

// The cached grid was rebuilt from BPM and Beats alone.
//
// Downbeats and the meter were dropped on the way into the cache, so a track
// analysed this run drew bar lines in its waveform and a time signature next to
// its BPM -- and every load after that, coming from cache, drew neither. The
// waveform visibly changed shape between a track's first load and its second.
//
// Confidence was worse: it was never stored and beatGridFrom invented 1.0, so a
// grid the analyser had distrusted came back fully trusted and enabled sync,
// quantise and beat loops that the analysis had specifically ruled out.
func TestTheCachedGridIsTheWholeGrid(t *testing.T) {
	beats := make([]time.Duration, 24)
	for i := range beats {
		beats[i] = time.Duration(i) * 468 * time.Millisecond
	}
	res := &analyze.Result{HasGrid: true, Grid: &analyze.BeatGrid{
		BPM: 128, Beats: beats, Downbeats: []int{0, 3, 6, 9},
		Meter: 3, Confidence: 0.42,
	}}

	cached := analysisFrom(res)
	back := beatGridFrom(cached, nil)

	if len(back.Downbeats) != len(res.Grid.Downbeats) {
		t.Fatalf("downbeats %v, want %v", back.Downbeats, res.Grid.Downbeats)
	}
	for i := range back.Downbeats {
		if back.Downbeats[i] != res.Grid.Downbeats[i] {
			t.Errorf("downbeat %d = %d, want %d", i, back.Downbeats[i], res.Grid.Downbeats[i])
		}
	}
	if back.Meter != 3 {
		t.Errorf("meter = %d, want 3", back.Meter)
	}
	if back.Confidence != 0.42 {
		t.Errorf("confidence = %v, want the analyser's own 0.42", back.Confidence)
	}
}

// An entry written before these fields existed must still produce a usable grid,
// or every existing user's cache would silently lose its beat data.
func TestAnOlderCacheEntryStillLoads(t *testing.T) {
	old := library.Analysis{HasGrid: true, BPM: 128, Beats: []time.Duration{0, time.Second}}

	g := beatGridFrom(old, nil)

	if g == nil {
		t.Fatal("a pre-existing cache entry produced no grid at all")
	}
	if g.Meter != 4 {
		t.Errorf("meter = %d, want the 4/4 default for an entry with none", g.Meter)
	}
	if g.Confidence != 1 {
		t.Errorf("confidence = %v, want the old unconditional trust", g.Confidence)
	}
}
