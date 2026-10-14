package main

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// analyze.Progress and DebugStats.AnalysisState existed and were connected to
// nothing: spec §4.1's "analyzing -> ready" pipeline was written down and never
// observed, and the overlay's "scan:" row was never set.
//
// The user-visible consequence is the point: a full STFT over a long file leaves
// a deck showing "--" for its BPM, and there was nothing on screen to distinguish
// working from wedged.
func TestAnalysisProgressReachesTheDebugOverlay(t *testing.T) {
	a := NewAnalyzer(nil, newFakeTarget(2), nil)
	if got := a.State(); got != "" {
		t.Fatalf("State() = %q before anything runs, want empty", got)
	}

	a.Analyze(0, "/m/long.wav", dsp.Sine(220, 0.05))
	a.Wait(t.Context())

	st := views.DebugStats{AnalysisState: a.State()}
	if st.AnalysisState == "" {
		t.Error("the debug overlay's scan row would be blank after an analysis")
	}
	if st.AnalysisState != string(analyze.ProgressReady) {
		t.Errorf("state = %q, want %q after a successful pass", st.AnalysisState, analyze.ProgressReady)
	}
}
