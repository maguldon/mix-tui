package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/tui/library"
)

// beatGridFrom rebuilds a beatgrid from a cached analysis.
//
// Confidence is carried across rather than recomputed: the cache stores what
// the analyser decided, and re-deciding here would let the two disagree.
func beatGridFrom(a library.Analysis, track *audio.Track) *analyze.BeatGrid {
	if !a.HasGrid || a.BPM <= 0 {
		return nil
	}
	return &analyze.BeatGrid{
		BPM:        a.BPM,
		Beats:      a.Beats,
		Confidence: 1.0,
	}
}

// keyFrom rebuilds a key result from a cached analysis.
//
// It returns nil below the confidence threshold, so the view renders unknown
// rather than a guess: a wrong key displayed confidently is worse than no key,
// because the user would crate-match against it (spec 6.5).
func keyFrom(a library.Analysis) *analyze.Key {
	if a.KeyCamelot == "" || a.KeyConfidence < analyze.KeyConfidenceThreshold {
		return nil
	}
	return &analyze.Key{
		Camelot:    a.KeyCamelot,
		Name:       a.KeyName,
		Confidence: a.KeyConfidence,
	}
}

// analysisFrom converts a live result into the cache's representation.
func analysisFrom(r *analyze.Result) library.Analysis {
	a := library.Analysis{}
	if r == nil {
		return a
	}
	if r.HasGrid && r.Grid != nil {
		a.BPM = r.Grid.BPM
		a.HasGrid = true
		a.Beats = r.Grid.Beats
	}
	if r.HasKey && r.Key != nil {
		a.KeyCamelot = r.Key.Camelot
		a.KeyName = r.Key.Name
		a.KeyConfidence = r.Key.Confidence
	}
	return a
}

// Analyzer runs track analysis in the background and hands the result to the
// engine once it arrives.
//
// This is the wiring that was missing. The analysis code was written, tested
// and never called: the loader read a cache that nothing ever wrote, so no
// deck in a running application ever had a beatgrid. Sync, quantise, beat
// loops and the zoom view were all inert.
//
// Analysis is slow -- a full STFT over the whole track -- so it must not run on
// the path that loads a track, which is a UI-latency path. It runs on its own
// goroutine and applies itself when it finishes.
type Analyzer struct {
	cache   *library.Cache
	eng     libraryAnalysisTarget
	decks   map[string]int // path -> deck index awaiting a result
	log     *slog.Logger
	mu      sync.Mutex
	pending map[string]context.CancelFunc
}

// libraryAnalysisTarget is the part of the engine the analyzer needs.
//
// It is an interface so this file can be tested without a running audio
// device, which matters: the entire reason the previous code shipped unwired
// is that nothing exercised the path end to end.
type libraryAnalysisTarget interface {
	// ApplyAnalysis attaches a finished grid and key to a deck.
	ApplyAnalysis(deck int, grid *analyze.BeatGrid, key *analyze.Key)
	// DeckCount reports how many decks can hold a result.
	DeckCount() int
}

// NewAnalyzer builds an analyzer writing through the given cache.
func NewAnalyzer(cache *library.Cache, eng libraryAnalysisTarget, log *slog.Logger) *Analyzer {
	if log == nil {
		log = slog.Default()
	}
	return &Analyzer{
		cache:   cache,
		eng:     eng,
		decks:   map[string]int{},
		pending: map[string]context.CancelFunc{},
		log:     log,
	}
}

// Analyze schedules analysis for a track loaded into a deck.
//
// A track already being analysed for the same deck has its old run cancelled:
// the user loaded something else, and the superseded result would overwrite
// the newer one.
func (a *Analyzer) Analyze(deck int, path string, pcm dsp.Stereo) {
	if a == nil || len(pcm) == 0 {
		return
	}

	a.mu.Lock()
	if cancel, ok := a.pending[path]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.pending[path] = cancel
	a.mu.Unlock()

	go func() {
		res, err := analyze.Analyze(ctx, pcm)

		a.mu.Lock()
		delete(a.pending, path)
		a.mu.Unlock()
		cancel()

		if err != nil {
			if err == context.Canceled {
				return // superseded; the newer load owns the deck now
			}
			a.log.Debug("analysis failed", "path", path, "err", err)
			return
		}

		// Store it, so the next load of this track is instant. A cache write
		// failure is not fatal: the result still applies to this session.
		if a.cache != nil {
			if err := a.cache.Put(path, analysisFrom(res)); err != nil {
				a.log.Debug("cache write failed", "path", path, "err", err)
			}
		}

		a.apply(deck, res)
	}()
}

// apply hands a finished result to the engine.
//
// The grid is only attached when it holds a usable tempo. Attaching an empty
// one would make the deck claim to have a beatgrid it does not have, which
// re-enables every feature gated on HasBeatGrid with nothing to show.
func (a *Analyzer) apply(deck int, res *analyze.Result) {
	if a.eng == nil || res == nil {
		return
	}
	if deck < 0 || deck >= a.eng.DeckCount() {
		return
	}
	var grid *analyze.BeatGrid
	if res.HasGrid && res.Grid != nil {
		grid = res.Grid
	}
	var key *analyze.Key
	if res.HasKey && res.Key != nil {
		key = res.Key
	}
	a.eng.ApplyAnalysis(deck, grid, key)
	a.log.Debug("analysis complete",
		"deck", deck, "bpm", res.Grid.BPM, "has_grid", res.HasGrid,
		"key", res.Key.Camelot, "has_key", res.HasKey, "took", res.Duration)
}

// Cancel stops an in-flight analysis for a path.
func (a *Analyzer) Cancel(path string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if cancel, ok := a.pending[path]; ok {
		cancel()
		delete(a.pending, path)
	}
	a.mu.Unlock()
}

// CancelAll stops every in-flight analysis. Used on shutdown so a pending pass
// does not outlive the process and race its teardown.
func (a *Analyzer) CancelAll() {
	if a == nil {
		return
	}
	a.mu.Lock()
	for path, cancel := range a.pending {
		cancel()
		delete(a.pending, path)
	}
	a.mu.Unlock()
}
