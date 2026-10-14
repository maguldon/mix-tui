package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/engine"
)

func deckWithSavedLoops(slots map[int]engine.TimeRange) engine.DeckState {
	return engine.DeckState{
		Index: 0, Loaded: true, Duration: 6 * time.Minute, HasBeatGrid: true, BPM: 128,
		SavedLoopSlots: slots,
	}
}

// TestDeckPanelNumbersSavedLoopSlots covers spec 6.2.1: "saved loop slots appear
// as small numbered markers".
//
// They are a text row rather than braille markers because a braille cell is a
// 2x4 dot grid and cannot be a digit -- no glyph is both. The numbers are the
// point, so they get a row.
//
// Deck.Loop() has always returned the slots; the Snapshot did not carry them, so
// no view could have drawn this.
func TestDeckPanelNumbersSavedLoopSlots(t *testing.T) {
	d := deckWithSavedLoops(map[int]engine.TimeRange{
		1: {Start: 10 * time.Second, End: 14 * time.Second},
		2: {Start: 30 * time.Second, End: 38 * time.Second},
	})
	p := renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)

	text := strings.Join(rowTexts(p), "\n")
	if !strings.Contains(text, "LOOPS") {
		t.Fatalf("no loop row in the panel:\n%s", text)
	}
	// Real numbers, in slot order, with the length of each.
	if !strings.Contains(text, "1: 0:04") {
		t.Errorf("slot 1 length missing or wrong:\n%s", text)
	}
	if !strings.Contains(text, "2: 0:08") {
		t.Errorf("slot 2 length missing or wrong:\n%s", text)
	}
	if strings.Index(text, "1: 0:04") > strings.Index(text, "2: 0:08") {
		t.Error("slots are listed out of order")
	}
}

// TestDeckPanelOmitsTheLoopRowWhenEmpty keeps a permanent "LOOPS" heading off
// every deck. The absence is itself the information that nothing is saved.
func TestDeckPanelOmitsTheLoopRowWhenEmpty(t *testing.T) {
	d := deckWithSavedLoops(nil)
	p := renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)
	if strings.Contains(strings.Join(rowTexts(p), "\n"), "LOOPS") {
		t.Error("the loop row is shown with no saved loops")
	}
}

// TestDeckPanelShowsFaults covers the diagnostic that did not exist. A deck whose
// file has been deleted still renders a title, a BPM and a waveform, so nothing
// about it says it will make no sound.
func TestDeckPanelShowsFaults(t *testing.T) {
	d := engine.DeckState{
		Index: 0, Loaded: true, Duration: time.Minute, Title: "Gone",
		Faulted: true, FaultReason: "track finished",
	}
	p := renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)
	if !strings.Contains(strings.Join(rowTexts(p), "\n"), "FAULT") {
		t.Errorf("a faulted deck shows no fault line:\n%s", strings.Join(rowTexts(p), "\n"))
	}
}

// TestDeckPanelShowsNoFaultWhenHealthy guards against the banner appearing on
// every deck, which would be worse than not having it.
func TestDeckPanelShowsNoFaultWhenHealthy(t *testing.T) {
	d := engine.DeckState{Index: 0, Loaded: true, Duration: time.Minute, Title: "Fine"}
	p := renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)
	if strings.Contains(strings.Join(rowTexts(p), "\n"), "FAULT") {
		t.Error("a healthy deck reports a fault")
	}
}

// TestNoAudioBannerIsShownAndClears covers spec 8's "never start a TUI that
// silently produces no sound".
//
// A session with no device used to be exactly that: a working interface, no
// audio, and nothing saying so -- and the debug overlay's own warning was
// suppressed because SetStatsFunc was only wired when a device had opened.
func TestNoAudioBannerIsShownAndClears(t *testing.T) {
	m, _ := testModel(t)
	active := false
	m.SetAudioStatus(func() (bool, string) { return active, "cannot open alsa" })

	m.Update(keyMsg("z")) // any tick-equivalent: drive the sampler directly
	m.refreshAudio()

	frame := m.RenderAt(120, 24)
	if !strings.Contains(frame, "NO AUDIO") {
		t.Fatalf("no banner while audio is down:\n%s", lastLines(frame, 3))
	}
	if !strings.Contains(frame, "cannot open alsa") {
		t.Errorf("the banner does not say why:\n%s", lastLines(frame, 3))
	}

	// The device appears -- the background retry opens it -- and the banner goes.
	active = true
	m.refreshAudio()
	if frame := m.RenderAt(120, 24); strings.Contains(frame, "NO AUDIO") {
		t.Errorf("the banner survived audio coming up:\n%s", lastLines(frame, 3))
	}
}

// TestNoAudioBannerNeedsNoAudioFunc keeps -no-audio silent: there is nothing to
// retry for, so a permanent warning would be noise.
func TestNoAudioBannerNeedsNoAudioFunc(t *testing.T) {
	m, _ := testModel(t)
	m.refreshAudio()
	if m.st.AudioDown {
		t.Error("audio is reported as down with no status source")
	}
	if strings.Contains(m.RenderAt(120, 24), "NO AUDIO") {
		t.Error("the banner is shown with no status source")
	}
}

// TestNoAudioBannerCoexistsWithErrors keeps the two lines from overwriting each
// other: a load failure and a missing device are independent facts.
func TestNoAudioBannerCoexistsWithErrors(t *testing.T) {
	m, _ := testModel(t)
	m.SetAudioStatus(func() (bool, string) { return false, "no device" })
	m.refreshAudio()
	m.setError("could not decode that file")

	frame := m.RenderAt(120, 24)
	if !strings.Contains(frame, "NO AUDIO") || !strings.Contains(frame, "could not decode") {
		t.Errorf("the banner and the error must both be visible:\n%s", lastLines(frame, 4))
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// TestTheLoopSizeStaysOnScreenBetweenLoops is spec 6.2.2: the beat-loop encoder's
// position must always be visible.
//
// The readout was inside `if d.LoopActive`, and ReleaseLoop zeroed the armed
// length, so in between loops the deck showed no number at all. The user could
// turn the encoder and watch nothing happen -- the failure the section names.
func TestTheLoopSizeStaysOnScreenBetweenLoops(t *testing.T) {
	d := engine.DeckState{
		Index: 0, Loaded: true, Duration: 6 * time.Minute,
		HasBeatGrid: true, BPM: 128, Playing: true, LoopBeatCount: 8,
	}

	if got := strings.Join(rowTexts(renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)), "\n"); !strings.Contains(got, "8") {
		t.Errorf("with 8 beats armed and no loop engaged, the panel shows no loop size:\n%s", got)
	}

	d.LoopActive = true
	active := strings.Join(rowTexts(renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)), "\n")
	if !strings.Contains(active, "LOOP 8") {
		t.Errorf("an engaged loop does not read as engaged:\n%s", active)
	}
}

// TestADeckWithNoArmedLengthClaimsNone keeps the readout from inventing a size
// the user never chose.
func TestADeckWithNoArmedLengthClaimsNone(t *testing.T) {
	d := engine.DeckState{Index: 0, Loaded: true, Duration: 6 * time.Minute, Playing: true}
	got := strings.Join(rowTexts(renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)), "\n")
	if strings.Contains(got, "BEATS") {
		t.Errorf("a deck with no armed length shows one:\n%s", got)
	}
}

// TestThePlayingAccentTracksLevel is spec 6.4: "small VU-style art accents in the
// deck footer".
//
// "VU-style" was a fixed ">>>" drawn whenever no loop was active. It moved only
// on play and stop, so it reported *that* a deck was playing and nothing about
// what it was doing -- and a stopped deck got one too, since the condition was
// the absence of a loop rather than the presence of playback.
func TestThePlayingAccentTracksLevel(t *testing.T) {
	d := engine.DeckState{
		Index: 0, Loaded: true, Duration: 6 * time.Minute, Playing: true,
	}
	seen := map[string]bool{}
	for _, lvl := range []float64{0, 0.02, 0.1, 0.5} {
		d.Level = lvl
		rows := rowTexts(renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20))
		seen[strings.Join(rows, "\n")] = true
	}
	if len(seen) != 4 {
		t.Errorf("four levels produced %d distinct decks, want 4: the accent does not track level", len(seen))
	}
}

// TestAStoppedDeckHasNoAccent keeps the accent meaning "this deck is making
// sound". It used to appear on every deck with no loop engaged, including idle
// ones, implying something was happening.
func TestAStoppedDeckHasNoAccent(t *testing.T) {
	d := engine.DeckState{Index: 0, Loaded: true, Duration: 6 * time.Minute}
	got := strings.Join(rowTexts(renderDeckPanel(60, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)), "\n")
	if strings.Contains(got, ">>") {
		t.Errorf("a stopped deck shows a playing accent:\n%s", got)
	}
}

// TestANonFourMeterIsSaidOut loud is the half of meter detection the user sees.
// Correct bar lines are no use if the grid looks arbitrary; naming the meter
// explains them.
func TestANonFourMeterIsSaidOutLoud(t *testing.T) {
	d := engine.DeckState{
		Index: 0, Loaded: true, Duration: 6 * time.Minute,
		HasBeatGrid: true, BPM: 128, Meter: 7,
	}
	if got := metaRow(d, 60); !strings.Contains(got, "7/8") {
		t.Errorf("a 7/8 track does not say so: %q", got)
	}

	// 4/4 is assumed and shown as plain BPM: naming it on every track would be
	// noise.
	d.Meter = 4
	if got := metaRow(d, 60); strings.Contains(got, "4/4") {
		t.Errorf("4/4 is spelled out where plain BPM is the norm: %q", got)
	}
}
