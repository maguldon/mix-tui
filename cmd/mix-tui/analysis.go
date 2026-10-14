package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

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
	// The whole grid, not just its tempo. Downbeats and the meter were dropped
	// on the way into the cache, so a track analysed this run drew bar lines and
	// a time signature and every load after drew neither -- the waveform changed
	// shape between a track's first load and its second.
	conf := a.GridConfidence
	if conf == 0 {
		// An entry written before confidence was cached. It was trusted
		// unconditionally then and there is no way to recover what the analyser
		// actually thought, so keep the old behaviour for it rather than
		// silently discarding every grid in an existing cache.
		conf = 1
	}
	meter := a.Meter
	if meter <= 0 {
		meter = 4
	}
	return &analyze.BeatGrid{
		BPM:        a.BPM,
		Beats:      a.Beats,
		Downbeats:  a.Downbeats,
		Meter:      meter,
		Confidence: conf,
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
		a.Downbeats = r.Grid.Downbeats
		a.Meter = r.Grid.Meter
		a.GridConfidence = r.Grid.Confidence
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
	pending map[string]*analysisRun
	// state is the current pipeline position, for the debug overlay.
	//
	// analyze.Progress and its three constants existed and were referenced
	// nowhere: spec §4.1's "analyzing -> ready" pipeline was written down and
	// never observed. DebugStats.AnalysisState was the other half -- a rendered
	// row nothing ever set. Together they meant a long analysis pass over a
	// large file was invisible: the user watched a deck sit on "--" for its BPM
	// with nothing to say whether it was working or wedged.
	state analyze.Progress
	// afterAnalyze is a test hook, parked between a finished analysis and the
	// hand-off of its result. The bug this fixes lived exactly in that gap, and
	// a timing test could only catch it when the scheduler happened to line up;
	// parking there makes the window observable. Never set in production.
	afterAnalyze func()
	// onResult is called once per completed analysis, on the analysis goroutine.
	// It exists so a finished result can reach the library index: without it the
	// analyzer produced BPM and key for the cache and the engine and nothing
	// else, so the browser's columns never showed a value for any track.
	onResult func(path string, r *analyze.Result)
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
		pending: map[string]*analysisRun{},
		log:     log,
	}
}

// OnResult registers a callback for completed analyses. Only the last one is
// kept: there is one consumer, the library index, and a chain would be a way to
// leak a reference to a closed-over index long after a rescan replaced it.
func (a *Analyzer) OnResult(f func(path string, r *analyze.Result)) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.onResult = f
	a.mu.Unlock()
}

// Analyze schedules analysis for a track loaded into a deck.
//
// A track already being analysed for the same deck has its old run cancelled:
// the user loaded something else, and the superseded result would overwrite
// the newer one.
// State is the pipeline position, for the debug overlay. Empty means nothing is
// running.
func (a *Analyzer) State() string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return string(a.state)
}

func (a *Analyzer) setState(p analyze.Progress) {
	a.mu.Lock()
	a.state = p
	a.mu.Unlock()
}

func (a *Analyzer) Analyze(deck int, path string, pcm dsp.Stereo) {
	if a == nil || len(pcm) == 0 {
		return
	}

	a.mu.Lock()
	if run, ok := a.pending[path]; ok {
		run.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &analysisRun{cancel: cancel, path: path}
	a.pending[path] = run
	a.mu.Unlock()

	a.setState(analyze.ProgressAnalyzing)

	go func() {
		res, err := analyze.Analyze(ctx, pcm)

		// A superseded or cancelled run is done immediately: there is no result
		// to hand anywhere. It must still clear its own entry.
		if err != nil {
			a.finishRun(run)
			if err == context.Canceled {
				return // superseded; the newer load owns the deck now
			}
			a.log.Debug("analysis failed", "path", path, "err", err)
			a.setState(analyze.ProgressFailed)
			return
		}

		// The run stays registered until the result has actually landed. It used
		// to de-register here, immediately after the analysis, which left the
		// cache write, the library callback and the engine apply outside the
		// window Wait watches -- so Wait could report "nothing in flight" while
		// the result was still being written to disk and pushed to the deck. The
		// cache write is unbounded I/O, so on a cold cache the gap is widest
		// exactly when there is most to write.
		//
		// -screenshot depends on this: it calls Wait for a settled frame, and
		// taking the shot early yields "--" for BPM and key on a track that has
		// both, which is the one thing a screenshot exists to demonstrate.
		if a.afterAnalyze != nil {
			a.afterAnalyze()
		}

		// Store it, so the next load of this track is instant. A cache write
		// failure is not fatal: the result still applies to this session.
		if a.cache != nil {
			if err := a.cache.Put(path, analysisFrom(res)); err != nil {
				a.log.Debug("cache write failed", "path", path, "err", err)
			}
		}

		a.mu.Lock()
		onResult := a.onResult
		a.mu.Unlock()
		if onResult != nil {
			onResult(path, res)
		}

		a.apply(deck, res)

		a.finishRun(run)
		a.setState(analyze.ProgressReady)
	}()
}

// analysisRun is one in-flight analysis.
//
// It exists so a run clears only its OWN entry. The map is keyed by path and a
// newer load of the same track replaces the entry, so a run that finished late
// and deleted by path would remove the newer run's registration and let Wait
// return early -- the same bug, one level down.
// analysisRun is one in-flight or finished analysis pass.
type analysisRun struct {
	cancel context.CancelFunc
	path   string
}

// finishRun de-registers a run, releasing its resources.
func (a *Analyzer) finishRun(run *analysisRun) {
	if a == nil || run == nil {
		return
	}
	run.cancel()
	a.mu.Lock()
	// Only if this run is still the registered one. A newer load of the same path
	// has its own entry, and removing that would under-report the work in flight.
	if a.pending[run.path] == run {
		delete(a.pending, run.path)
	}
	a.mu.Unlock()
}

// InFlight reports how many analyses are running. Tests use it; Wait uses it too.
func (a *Analyzer) InFlight() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
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

// Wait blocks until no analysis is in flight, or ctx is done.
//
// Analysis is asynchronous on purpose -- it must not sit on the path a user
// waits on. But a caller that needs a settled frame rather than a live one
// needs the other behaviour: -screenshot captures a single frame, and one
// taken before analysis lands shows "--" for BPM and key on a track that
// does have them.
//
// There is no correctness reason for a screenshot to wait; there is only the
// question of whether it is the picture anyone wants to look at.
//
// The contract is "no result is still being handed over". A run stays
// registered from the moment it is scheduled until its result has been cached,
// published to the library and applied to the deck, so returning from here means
// every started analysis has fully landed. It used to mean only that every
// analysis had finished computing, which left the writes outside the window --
// see the run's de-registration.
func (a *Analyzer) Wait(ctx context.Context) {
	if a == nil {
		return
	}
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		a.mu.Lock()
		n := len(a.pending)
		a.mu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Cancel stops an in-flight analysis for a path.
func (a *Analyzer) Cancel(path string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	run := a.pending[path]
	delete(a.pending, path)
	a.mu.Unlock()
	if run != nil {
		run.cancel()
	}
}

// CancelAll stops every in-flight analysis. Used on shutdown so a pending pass
// does not outlive the process and race its teardown.
func (a *Analyzer) CancelAll() {
	if a == nil {
		return
	}
	a.mu.Lock()
	runs := make([]*analysisRun, 0, len(a.pending))
	for path, run := range a.pending {
		runs = append(runs, run)
		delete(a.pending, path)
	}
	a.mu.Unlock()
	for _, run := range runs {
		run.cancel()
	}
}
