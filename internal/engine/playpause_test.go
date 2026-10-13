package engine

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/dsp"
)

// playEngine returns an engine with a track on both decks and the queue drained.
func playEngine(t *testing.T) *Engine {
	t.Helper()
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	tr := NewTrackFromPCM(dsp.Sine(220, 1))
	e.Deck(0).LoadNow(tr, nil, nil)
	e.Deck(1).LoadNow(tr, nil, nil)
	return e
}

// TestPlayPauseToggles is the regression test for issue #9. The transport button
// did `playing = true` unconditionally, so pressing it on a rolling deck
// re-asserted the same state and nothing happened: there was no pause, while
// the help screen called the key "play/pause".
func TestPlayPauseToggles(t *testing.T) {
	e := playEngine(t)
	d := e.Deck(0)

	if d.IsPlaying() {
		t.Fatal("a freshly loaded deck is playing")
	}

	e.Apply(Action{Kind: DeckPlayPause, Target: 0})
	drain(e)
	if !d.IsPlaying() {
		t.Fatal("the first press did not start the deck")
	}

	e.Apply(Action{Kind: DeckPlayPause, Target: 0})
	drain(e)
	if d.IsPlaying() {
		t.Fatal("the second press did not pause the deck")
	}

	e.Apply(Action{Kind: DeckPlayPause, Target: 0})
	drain(e)
	if !d.IsPlaying() {
		t.Fatal("the third press did not resume the deck")
	}
}

// TestPlayPauseKeepsThePlayhead is what makes it a pause rather than a stop: the
// deck resumes from where it stopped, not from the top.
func TestPlayPauseKeepsThePlayhead(t *testing.T) {
	e := playEngine(t)
	d := e.Deck(0)

	d.Play()
	for i := 0; i < 40; i++ {
		drain(e)
	}
	playing := e.Snapshot().Decks[0].Position
	if playing == 0 {
		t.Fatal("the deck did not advance")
	}

	e.Apply(Action{Kind: DeckPlayPause, Target: 0})
	drain(e)
	paused := e.Snapshot().Decks[0].Position
	if paused == 0 {
		t.Fatal("pausing threw away the playhead")
	}

	e.Apply(Action{Kind: DeckPlayPause, Target: 0})
	drain(e)
	if got := e.Snapshot().Decks[0].Position; got < paused {
		t.Errorf("resuming rewound to %v, from %v", got, paused)
	}
}

// TestPlayStaysIdempotent guards the reason DeckPlayPause is a separate kind.
// mixplay and mixrender start a deck with DeckPlay and need that to mean "make
// this play"; under a toggle their second call would render silence.
func TestPlayStaysIdempotent(t *testing.T) {
	e := playEngine(t)
	d := e.Deck(0)

	for i := 0; i < 3; i++ {
		e.Apply(Action{Kind: DeckPlay, Target: 0})
		drain(e)
		if !d.IsPlaying() {
			t.Fatalf("DeckPlay call %d left the deck stopped", i+1)
		}
	}
}

// TestPlayPauseOnAnEmptyDeckDoesNothing keeps the button from "playing" a deck
// with no audio, which would report a rolling transport over silence.
func TestPlayPauseOnAnEmptyDeckDoesNothing(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})

	e.Apply(Action{Kind: DeckPlayPause, Target: 1})
	drain(e)

	if e.Deck(1).IsPlaying() {
		t.Error("an empty deck reported playing")
	}
}

// TestPlayPauseIsTakesDeck keeps the focus substitution reaching it. It sits at
// the end of the deck-action range, so a bound that drifted would silently stop
// substituting the focused deck -- the bug fixed in the previous commit.
func TestPlayPauseIsTakesDeck(t *testing.T) {
	if !DeckPlayPause.TakesDeck() {
		t.Error("DeckPlayPause must be treated as a deck action")
	}
	if !DeckPlay.TakesDeck() {
		t.Error("DeckPlay must be treated as a deck action")
	}
	if MixerMasterGain.TakesDeck() {
		t.Error("MixerMasterGain must not be treated as a deck action")
	}
}

// TestPlayPauseRoundTripsThroughItsName keeps it reachable from a config file
// and a mapping, which is the only way a user can bind it.
func TestPlayPauseRoundTripsThroughItsName(t *testing.T) {
	k, err := ParseActionKind("DeckPlayPause")
	if err != nil {
		t.Fatalf("DeckPlayPause is not parseable: %v", err)
	}
	if k != DeckPlayPause {
		t.Errorf("parsed to %v, want DeckPlayPause", k)
	}
	if got := FormatActionKind(DeckPlayPause); got != "DeckPlayPause" {
		t.Errorf("formats as %q", got)
	}
}
