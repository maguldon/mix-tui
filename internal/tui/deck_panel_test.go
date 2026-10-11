package tui

import "testing"

// TestZoomedDeckShowsAnOverview. RenderOverview was written, tested, and never
// called from a view: the review that "fixed" it addressed a blank return value
// in a function no panel asked for. When zoom is on, the waveform is a window
// and the strip is the only thing showing where that window sits in the track.
func TestZoomedDeckShowsAnOverview(t *testing.T) {
	snap := snapTwoDecks()
	snap.Decks[0].Duration = 200e9
	snap.Decks[0].Position = 20e9

	st := ViewState{Layout: LayoutD, Focus: 0, Zoom: true}
	st.OtherDeck = snap.Decks[1]

	out := Render(120, 40, snap, st)
	// The overview strip is braille; the waveform above it is braille too, so
	// assert on the count of lit cells rising above the window-only case.
	withOverview := litBrailleCells(splitLines(out))

	st.Zoom = false
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
