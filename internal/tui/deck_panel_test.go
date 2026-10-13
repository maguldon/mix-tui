package tui

import (
	"strings"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

// TestUnanalysedDeckShowsNoTempo. Spec 8: a track with no analysis shows the
// unknown marker, never a fabricated number.
//
// This used to be asserted at the acceptance level, by expecting a fixture's
// beat tracker to fail. It stopped failing once analysis was wired to load --
// correctly -- so the assertion was deleted along with the bad premise. The
// property it was reaching for is real, so it is pinned here instead, on a
// snapshot that genuinely carries no grid.
func TestUnanalysedDeckShowsNoTempo(t *testing.T) {
	d := engine.DeckState{Index: 0, Loaded: true, Title: "Unknown"}
	// No BPM and no HasBeatGrid.
	if d.BPM != 0 || d.HasBeatGrid {
		t.Fatal("fixture is not unanalysed")
	}

	p := renderDeckPanel(40, d, ViewState{}, true, 12)
	for _, row := range p.Rows {
		if strings.Contains(row.Text, "0.00") {
			t.Errorf("an unanalysed deck displayed a tempo: %q", row.Text)
		}
	}
}

// TestZoomedDeckShowsAnOverview. RenderOverview was written, tested, and never
// called from a view: the review that "fixed" it addressed a blank return value
// in a function no panel asked for. When zoom is on, the waveform is a window
// and the strip is the only thing showing where that window sits in the track.
func TestZoomedDeckShowsAnOverview(t *testing.T) {
	snap := snapTwoDecks()
	snap.Decks[0].Duration = 200e9
	snap.Decks[0].Position = 20e9

	st := ViewState{Layout: LayoutD, Focus: 0, Zoom: []bool{true}}
	st.OtherDeck = snap.Decks[1]

	out := Render(120, 40, snap, st)
	// The overview strip is braille; the waveform above it is braille too, so
	// assert on the count of lit cells rising above the window-only case.
	withOverview := litBrailleCells(splitLines(out))

	st.Zoom = []bool{false}
	without := litBrailleCells(splitLines(Render(120, 40, snap, st)))

	if withOverview <= without {
		t.Errorf("zooming added no waveform glyphs (%d vs %d); the overview strip is not rendering",
			withOverview, without)
	}
}

// TestOverviewIsSkippedWithoutZoom. With the whole track already visible, an
// overview of it is redundant.
func TestOverviewIsSkippedWithoutZoom(t *testing.T) {
	snap := snapTwoDecks()
	snap.Decks[0].Duration = 200e9
	st := ViewState{Layout: LayoutD, Focus: 0}
	st.OtherDeck = snap.Decks[1]

	if got := litBrailleCells(splitLines(Render(120, 40, snap, st))); got == 0 {
		t.Error("no waveform at all without zoom")
	}
}
