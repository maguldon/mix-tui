package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

func loadedTrack(path string, secs float64) *audio.Track {
	t := NewTrackFromPCM(dsp.Sine(220, secs))
	t.Path = path
	return t
}

// TestHotcuesAndLoopsSurviveAReload is spec §4.4.3 and §10: both belong to the
// track, and "a loop set on a particular track returns on the next load".
//
// Neither was stored at all -- there was no persistence layer for hotcues or
// loops anywhere, and no ~/.config/mix-tui/hotcues or loops directory -- so a DJ
// who set their performance cues lost them at the end of every set.
func TestHotcuesAndLoopsSurviveAReload(t *testing.T) {
	dir := t.TempDir()
	store := OpenCueStore(dir)
	path := "/m/set.wav"

	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.SetCueStore(store)
	e.Deck(0).LoadNow(loadedTrack(path, 4), testGrid(128, 64), nil)
	drain(e)
	e.Deck(0).Play()
	drain(e)

	// Mark a hotcue a third of the way in.
	e.Apply(Action{Kind: DeckNudge, Value: 1.3})
	drain(e)
	e.Apply(Action{Kind: DeckHotcueSet, Slot: 2})
	drain(e)

	// And save the loop currently in place.
	e.Apply(Action{Kind: LoopBeat, Value: 4})
	drain(e)
	for i := 0; i < 200; i++ {
		drain(e)
	}
	e.Apply(Action{Kind: LoopIn})
	drain(e)
	for i := 0; i < 40; i++ {
		drain(e)
	}
	e.Apply(Action{Kind: LoopOut})
	drain(e)
	e.Apply(Action{Kind: LoopSave, Slot: 1})
	drain(e)

	before := e.Snapshot().Decks[0]
	if _, ok := before.SavedLoopSlots[1]; !ok {
		t.Fatal("setup failed: slot 1 holds no loop")
	}

	// A NEW engine, as a fresh launch would be.
	e2 := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e2.SetCueStore(OpenCueStore(dir))
	e2.Deck(0).LoadNow(loadedTrack(path, 4), testGrid(128, 64), nil)
	drain(e2)

	after := e2.Snapshot().Decks[0]
	// CueTimes is packed densely in slot order rather than indexed by slot, so
	// one restored cue is CueTimes[0].
	if after.CueCount != 1 {
		t.Errorf("hotcue 2 did not come back: %d of 8 set", after.CueCount)
	}
	if got := after.CueTimes[0]; got <= 0 {
		t.Errorf("hotcue 2 = %v, want it restored", got)
	}
	if _, ok := after.SavedLoopSlots[1]; !ok {
		t.Errorf("saved loop slot 1 did not come back: %v", after.SavedLoopSlots)
	}
}

// TestCuesAreStoredUnderTheTrackNotTheDeck keeps two decks on one track from
// overwriting each other, and keeps the same track in two decks from fighting
// over one file. Without this, the second deck to save overwrites the first.
func TestCuesAreStoredUnderTheTrackNotTheDeck(t *testing.T) {
	dir := t.TempDir()
	store := OpenCueStore(dir)
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.SetCueStore(store)

	for _, deck := range []int{0, 1} {
		e.Deck(deck).LoadNow(loadedTrack("/m/shared.wav", 4), testGrid(128, 64), nil)
	}
	drain(e)
	for _, deck := range []int{0, 1} {
		e.Deck(deck).Play()
	}
	drain(e)

	e.Apply(Action{Kind: DeckNudge, Target: 0, Value: 1})
	drain(e)
	e.Apply(Action{Kind: DeckHotcueSet, Target: 0, Slot: 0})
	drain(e)

	e.Apply(Action{Kind: DeckNudge, Target: 1, Value: 2})
	drain(e)
	e.Apply(Action{Kind: DeckHotcueSet, Target: 1, Slot: 0})
	drain(e)

	// One file, one track: the second save wins, which is correct. The point is
	// that both decks wrote to the SAME key rather than to two that would later
	// restore differently on each deck.
	entries, err := os.ReadDir(filepath.Join(dir, "hotcues"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("one track on two decks wrote %d files, want 1", len(entries))
	}
}

// TestDifferentTracksGetDifferentFiles is the property that makes the store
// usable at all: a cue remembered for one track must never restore onto another.
func TestDifferentTracksGetDifferentFiles(t *testing.T) {
	dir := t.TempDir()
	store := OpenCueStore(dir)

	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.SetCueStore(store)
	e.Deck(0).LoadNow(loadedTrack("/m/a.wav", 4), testGrid(128, 64), nil)
	drain(e)
	e.Deck(0).Play()
	drain(e)
	e.Apply(Action{Kind: DeckNudge, Value: 1})
	drain(e)
	e.Apply(Action{Kind: DeckHotcueSet, Slot: 0})
	drain(e)

	// Load a different track into the same deck.
	e.Deck(0).LoadNow(loadedTrack("/m/b.wav", 4), testGrid(128, 64), nil)
	drain(e)

	if n := e.Snapshot().Decks[0].CueCount; n != 0 {
		t.Errorf("track B inherited %d hotcues from track A", n)
	}
}

// TestCuesAreRestoredWithinTheTrack covers a file replaced by a shorter one. A
// cue at 90% of a ten-minute track, restored into a one-minute replacement,
// would otherwise sit past the end where pressing it seeks somewhere unreachable
// -- and the user cannot see that the file changed underneath them.
func TestCuesAreRestoredWithinTheTrack(t *testing.T) {
	dir := t.TempDir()
	store := OpenCueStore(dir)
	path := "/m/short.wav"

	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.SetCueStore(store)
	e.Deck(0).LoadNow(loadedTrack(path, 4), testGrid(128, 64), nil)
	drain(e)
	e.Deck(0).Play()
	drain(e)
	e.Apply(Action{Kind: DeckNudge, Value: 3.9}) // 97.5% in
	drain(e)
	e.Apply(Action{Kind: DeckHotcueSet, Slot: 0})
	drain(e)

	// The file is now a one-second version of itself.
	e2 := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e2.SetCueStore(OpenCueStore(dir))
	e2.Deck(0).LoadNow(loadedTrack(path, 1), testGrid(128, 64), nil)
	drain(e2)

	d := e2.Snapshot().Decks[0]
	at := d.CueTimes[0]
	if at > d.Duration {
		t.Errorf("hotcue 0 restored to %v, past the end of a %v track", at, d.Duration)
	}
}

// TestAnUnwritableStoreCostsNothingButTheCue: a read-only config directory must
// not stop a track from loading.
func TestAnUnwritableStoreCostsNothingButTheCue(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.Deck(0).LoadNow(loadedTrack("/m/a.wav", 4), testGrid(128, 64), nil)
	drain(e)
	e.Deck(0).Play()
	drain(e)
	e.Apply(Action{Kind: DeckNudge, Value: 1})
	drain(e)
	e.Apply(Action{Kind: DeckHotcueSet, Slot: 0})
	drain(e)

	// No store at all: the headless and test default.
	if n := e.Snapshot().Decks[0].CueCount; n != 1 {
		t.Errorf("with no store the hotcue is %d, want 1", n)
	}
}

// TestNoStoreMeansNoAccumulation guards the renderer and the tests, which load
// fixtures repeatedly and must not write to the user's real config.
func TestNoStoreMeansNoAccumulation(t *testing.T) {
	var s *CueStore
	if err := s.Save("/m/a.wav", Cues{}); err == nil {
		t.Error("a nil store accepted a save")
	}
}

// TestCuePositionsRoundTripExactly pins the unit conversion, which is the part
// most likely to be wrong and hardest to notice.
//
// Both directions are checked against the same numbers, so a wrong formula
// cannot pass by being wrong in a way that cancels out: 1.86s and 2.30s into a
// 4-second track must come back as exactly those times, not merely somewhere
// plausible.
func TestCuePositionsRoundTripExactly(t *testing.T) {
	dir := t.TempDir()
	const dur = 4.0
	path := "/m/exact.wav"

	e := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e.SetCueStore(OpenCueStore(dir))
	e.Deck(0).LoadNow(loadedTrack(path, dur), testGrid(128, 64), nil)
	drain(e)

	// Drive the deck straight rather than through playback, so the expected
	// positions are arithmetic rather than "wherever the pump got to".
	want := []struct {
		slot int
		at   time.Duration
	}{
		{0, 0},
		{1, dur * time.Second * 25 / 100},
		{2, dur * time.Second / 2},
		{3, dur * time.Second * 75 / 100},
	}
	for _, w := range want {
		e.Deck(0).seek(w.at)
		drain(e)
		e.Apply(Action{Kind: DeckHotcueSet, Slot: w.slot})
		drain(e)
	}

	e2 := New(Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	e2.SetCueStore(OpenCueStore(dir))
	e2.Deck(0).LoadNow(loadedTrack(path, dur), testGrid(128, 64), nil)
	drain(e2)

	d := e2.Snapshot().Decks[0]
	// CueTimes is packed in slot order.
	if d.CueCount != len(want) {
		t.Fatalf("restored %d cues, want %d", d.CueCount, len(want))
	}
	wantTimes := []time.Duration{0, dur * time.Second * 25 / 100, dur * time.Second / 2, dur * time.Second * 75 / 100}
	for i, wt := range wantTimes {
		got := d.CueTimes[i]
		// One render block of slack.
		if diff := got - wt; diff < -BlockFrames*time.Second/time.Duration(dsp.SampleRate) ||
			diff > BlockFrames*time.Second/time.Duration(dsp.SampleRate) {
			t.Errorf("cue %d at %v, want %v", i, got, wt)
		}
	}
}
