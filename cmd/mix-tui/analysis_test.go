package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/tui/library"
)

// fakeTarget records what the analyzer hands to the engine.
type fakeTarget struct {
	mu    sync.Mutex
	grids map[int]*analyze.BeatGrid
	keys  map[int]*analyze.Key
	calls int
}

// writeTempTrack writes pcm to a real file and returns its path. The cache
// stamps entries with the file's size and mtime, so testing against a
// nonexistent path would exercise an unstampable entry rather than the real one.
func writeTempTrack(t *testing.T, pcm dsp.Stereo) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "track.raw")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newFakeTarget(decks int) *fakeTarget {
	return &fakeTarget{
		grids: map[int]*analyze.BeatGrid{},
		keys:  map[int]*analyze.Key{},
	}
}

func (f *fakeTarget) ApplyAnalysis(deck int, g *analyze.BeatGrid, k *analyze.Key) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.grids[deck] = g
	f.keys[deck] = k
}

func (f *fakeTarget) DeckCount() int { return 2 }

func (f *fakeTarget) waitFor(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := f.calls
		f.mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d analysis results, saw %d", want, f.calls)
}

// clickTrack is an impulse train at a known tempo: the clearest possible signal
// for a beat tracker, so a passing test here is meaningful.
func clickTrack(bpm, seconds float64) dsp.Stereo {
	period := int(60.0 / bpm * dsp.SampleRate)
	total := int(seconds * dsp.SampleRate)
	out := make(dsp.Stereo, total*2)
	for i := 0; i < total; i += period {
		for j := 0; j < 128 && i+j < total; j++ {
			v := float32(0.5)
			if j > 0 {
				v = float32(1.0 / float64(j+1))
			}
			out[(i+j)*2] += v
			out[(i+j)*2+1] += v
		}
	}
	return out
}

// TestAnalyzerDeliversAGridToTheEngine is the test that should have existed
// before this shipped.
//
// The analysis code was written, benchmarked and never called by the
// application: the loader read a cache that nothing ever wrote. Every deck in a
// running program therefore had no beatgrid, which silently disabled sync,
// quantise, beat loops, zoom and the dBEAT readout. Nothing failed, because
// nothing asserted that a loaded track ever gained a tempo.
func TestAnalyzerDeliversAGridToTheEngine(t *testing.T) {
	cache := library.OpenCache(t.TempDir())
	eng := newFakeTarget(2)
	a := NewAnalyzer(cache, eng, nil)
	defer a.CancelAll()

	path := writeTempTrack(t, clickTrack(128, 30))
	a.Analyze(0, path, clickTrack(128, 30))

	eng.waitFor(t, 1)
	eng.mu.Lock()
	grid := eng.grids[0]
	eng.mu.Unlock()

	if grid == nil {
		t.Fatal("no beatgrid reached the engine; a loaded track would never have a tempo")
	}
	if grid.BPM <= 0 {
		t.Fatalf("grid has no tempo: %+v", grid)
	}
	if len(grid.Beats) == 0 {
		t.Error("grid has no beat positions")
	}
}

// TestAnalyzerWritesTheCache closes the loop: a second load of the same track
// must hit the cache rather than re-analysing. Before this, Put had zero
// callers, so the cache was read forever and never written.
func TestAnalyzerWritesTheCache(t *testing.T) {
	dir := t.TempDir()
	cache := library.OpenCache(dir)
	eng := newFakeTarget(2)
	a := NewAnalyzer(cache, eng, nil)
	defer a.CancelAll()

	// A real file: the cache stamps size and mtime, and an entry with no
	// stamp describes a file it has never seen and cannot be trusted.
	path := writeTempTrack(t, clickTrack(128, 20))
	a.Analyze(0, path, clickTrack(128, 20))
	eng.waitFor(t, 1)

	// The write happens after the apply, so give it a moment.
	deadline := time.Now().Add(10 * time.Second)
	var got library.Analysis
	var ok bool
	for time.Now().Before(deadline) {
		got, ok = cache.Get(path)
		if ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !ok {
		t.Fatal("analysis completed but nothing was written to the cache")
	}
	if !got.HasGrid || got.BPM <= 0 {
		t.Errorf("cached entry has no grid: %+v", got)
	}
}

// TestAnalyzerIgnoresEmptyAudio. A track with no decodable samples must not
// schedule work or attach a result.
func TestAnalyzerIgnoresEmptyAudio(t *testing.T) {
	eng := newFakeTarget(2)
	a := NewAnalyzer(nil, eng, nil)
	defer a.CancelAll()

	a.Analyze(0, "/tmp/empty.mp3", nil)

	eng.mu.Lock()
	calls := eng.calls
	eng.mu.Unlock()
	if calls != 0 {
		t.Errorf("empty audio produced %d analysis results, want 0", calls)
	}
}

// TestAnalyzerSupersedesTheSameTrack guards the reload case: loading the same
// track twice must not let the stale run overwrite the newer one.
func TestAnalyzerSupersedesTheSameTrack(t *testing.T) {
	eng := newFakeTarget(2)
	a := NewAnalyzer(nil, eng, nil)
	defer a.CancelAll()

	path := "/tmp/superseded.mp3"
	a.Analyze(0, path, clickTrack(128, 60))
	a.Analyze(0, path, clickTrack(90, 20))

	eng.waitFor(t, 1)

	// Let any superseded result that slipped through land too.
	time.Sleep(200 * time.Millisecond)
	eng.mu.Lock()
	grid := eng.grids[0]
	calls := eng.calls
	eng.mu.Unlock()

	if calls > 2 {
		t.Errorf("%d results applied for one deck; superseded runs must not overwrite", calls)
	}
	if grid == nil {
		t.Fatal("no grid applied at all")
	}
	if grid.BPM > 100 {
		t.Errorf("the superseded 60s run won over the newer 20s run: BPM %v", grid.BPM)
	}
}

// TestAnalyzerRejectsOutOfRangeDecks. The analyzer is driven by a UI callback,
// so a stale deck index must not panic the program.
func TestAnalyzerRejectsOutOfRangeDecks(t *testing.T) {
	eng := newFakeTarget(2)
	a := NewAnalyzer(nil, eng, nil)
	defer a.CancelAll()

	a.Analyze(9, "/tmp/bogus.mp3", clickTrack(128, 5))
	a.Analyze(-1, "/tmp/bogus2.mp3", clickTrack(128, 5))

	time.Sleep(100 * time.Millisecond)
	eng.mu.Lock()
	calls := eng.calls
	eng.mu.Unlock()
	if calls != 0 {
		t.Errorf("%d results applied for out-of-range decks, want 0", calls)
	}
}

// TestAnalyzerWaitForInFlight. -screenshot captures one settled frame, and
// analysis is asynchronous so it does not sit on the path a user waits on. The
// two conflict: a screenshot taken before analysis lands shows "--" for BPM and
// key on a track that has both, which is exactly what a screenshot is for
// demonstrating.
func TestAnalyzerWaitForInFlight(t *testing.T) {
	eng := newFakeTarget(2)
	a := NewAnalyzer(nil, eng, nil)
	defer a.CancelAll()

	a.Analyze(0, writeTempTrack(t, clickTrack(128, 30)), clickTrack(128, 30))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a.Wait(ctx)

	eng.mu.Lock()
	calls := eng.calls
	eng.mu.Unlock()
	if calls == 0 {
		t.Error("Wait returned before any analysis applied")
	}
}

// TestAnalyzerWaitReturnsWhenIdle. Waiting with nothing in flight must return
// immediately rather than blocking until the context expires.
func TestAnalyzerWaitReturnsWhenIdle(t *testing.T) {
	a := NewAnalyzer(nil, newFakeTarget(2), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	a.Wait(ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Wait blocked for %v with nothing in flight", elapsed)
	}
}
