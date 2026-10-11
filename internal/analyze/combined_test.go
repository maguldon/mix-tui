package analyze

import (
	"context"
	"testing"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// TestAnalyzeRunsBothReaders proves the combined entry point produces both
// results. It is the function the application calls; AnalyzeBeatGrid and
// AnalyzeKey separately are now the fallback path.
func TestAnalyzeRunsBothReaders(t *testing.T) {
	r, err := Analyze(context.Background(), clickTrack(128, 20))
	if err != nil {
		t.Fatal(err)
	}
	if r.Grid == nil {
		t.Error("no grid returned")
	}
	if r.Key == nil {
		t.Error("no key returned")
	}
	if !r.HasGrid {
		t.Errorf("a click track has an unambiguous tempo; got %+v", r.Grid)
	}
}

// TestAnalyzeMatchesTheSeparateCalls is the equivalence guard for the shared
// STFT: the combined path must return exactly what the two standalone readers
// return from the same spectrogram. If they drift, the FFT refactor has
// silently changed analysis results for every track.
func TestAnalyzeMatchesTheSeparateCalls(t *testing.T) {
	pcm := clickTrack(120, 15)
	spec := ComputeSpectrogram(pcm, windowLen, hopSize)

	wantGrid, err := BeatGridFromSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	wantKey, err := KeyFromSpec(spec)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Analyze(context.Background(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	if got.Grid.BPM != wantGrid.BPM {
		t.Errorf("BPM %v, want %v", got.Grid.BPM, wantGrid.BPM)
	}
	if got.Key.Camelot != wantKey.Camelot {
		t.Errorf("Camelot %q, want %q", got.Key.Camelot, wantKey.Camelot)
	}
}

// TestAnalyzeRejectsEmptyAudio. A track with no decodable samples has nothing
// to analyse; that is not an analysis failure and must not look like one.
func TestAnalyzeRejectsEmptyAudio(t *testing.T) {
	if _, err := Analyze(context.Background(), nil); err != ErrNoAudio {
		t.Errorf("got %v, want ErrNoAudio", err)
	}
}

// TestAnalyzeIsCancellable. Analysis runs in the background and a user may load
// several tracks in a row; the abandoned ones must stop rather than compete
// for the CPU with the one being waited on.
func TestAnalyzeIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Analyze(ctx, clickTrack(128, 60)); err != context.Canceled {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// TestAnalyzeReportsUsability pins HasGrid and HasKey, which is what separates
// "analysed and found nothing" from "never analysed". Both readers always
// return a non-nil result, so a nil check would report silence as analysed.
func TestAnalyzeReportsUsability(t *testing.T) {
	r, err := Analyze(context.Background(), dsp.DC(0, 20))
	if err != nil {
		t.Fatal(err)
	}
	if r.HasKey {
		t.Errorf("silence has no key; got %+v", r.Key)
	}
	if r.Grid == nil || r.Key == nil {
		t.Error("readers must always return a non-nil result, even for silence")
	}
}
