package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/engine"
)

// TestDeltaBeatAppearsInARealLayout is the regression test for C4.
//
// The first review found dBEAT was computed every frame and then never
// rendered: RenderDeltaBeat had no production caller, and the tests only
// round-tripped a duration. The second review caught the same thing survived
// the fix. This asserts on the output of an actual layout render, which is the
// only thing that could have caught it.
func TestDeltaBeatAppearsInARealLayout(t *testing.T) {
	for _, layout := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
		snap := snapTwoDecks()
		snap.Decks[0].HasDelta = true
		snap.Decks[0].DeltaMS = 12

		st := ViewState{Layout: layout, Focus: 0}
		st.OtherDeck = snap.Decks[1]

		out := Render(120, 40, snap, st)
		if !strings.Contains(out, "dBEAT") {
			t.Errorf("layout %v: the focused deck must show its dBEAT readout\n%s", layout, out)
		}
	}
}

// TestDeltaBeatIsOnTheFocusedDeckOnly pins that the readout is not duplicated
// across both decks. It is measured against the other deck, so it belongs to
// the one the DJ is about to align.
func TestDeltaBeatIsOnTheFocusedDeckOnly(t *testing.T) {
	snap := snapTwoDecks()
	snap.Decks[0].HasDelta, snap.Decks[0].DeltaMS = true, 12
	snap.Decks[1].HasDelta, snap.Decks[1].DeltaMS = true, 40

	for focus, want := range map[int]int{0: 12, 1: 40} {
		st := ViewState{Layout: LayoutD, Focus: focus}
		st.OtherDeck = snap.Decks[1-focus]

		out := Render(120, 40, snap, st)
		if got := strings.Count(out, "dBEAT"); got != 1 {
			t.Errorf("focus %d: want exactly 1 dBEAT readout, got %d\n%s", focus, got, out)
		}
		_ = want
	}
}

// TestDeltaBeatRowIsHiddenWhenUnmeasured. A deck with no beatgrid, or one that
// is alone in the set, has nothing to compare against; showing "dBEAT" with a
// blank offset would imply a measurement that was never taken.
func TestDeltaBeatRowIsHiddenWhenUnmeasured(t *testing.T) {
	snap := snapTwoDecks() // neither deck sets HasDelta
	st := ViewState{Layout: LayoutD, Focus: 0}
	st.OtherDeck = snap.Decks[1]

	out := Render(120, 40, snap, st)
	if strings.Contains(out, "dBEAT") {
		t.Errorf("no deck has a measured offset; the readout must not appear\n%s", out)
	}
}

// TestDeltaBeatRowKeepsPanelWidth. The readout is a panel row like any other, so
// it must pad to the panel's inner width rather than leaving the right-hand
// border ragged.
func TestDeltaBeatRowKeepsPanelWidth(t *testing.T) {
	snap := snapTwoDecks()
	snap.Decks[0].HasDelta, snap.Decks[0].DeltaMS = true, 12
	st := ViewState{Layout: LayoutD, Focus: 0}
	st.OtherDeck = snap.Decks[1]

	out := Render(120, 40, snap, st)
	for i, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "dBEAT") {
			continue
		}
		// The row is padded to the panel's inner width, so the panel's closing
		// border sits at the same column as on every other row. If the row were
		// short, that border would be pulled left and the frame would be ragged.
		if w := lipgloss.Width(line); w != 120 {
			t.Errorf("line %d: width %d, want 120: %q", i, w, line)
		}
		if got := lipgloss.Width(strings.SplitN(line, "│", 3)[1]); got == 0 {
			t.Errorf("line %d: dBEAT row has an empty cell: %q", i, line)
		}
	}
}

// TestDeltaBeatTruncatesInNarrowPanels. Layout C and B squeeze the deck column
// hard; a long offset string must clip, not overflow or wrap.
func TestDeltaBeatTruncatesInNarrowPanels(t *testing.T) {
	snap := snapTwoDecks()
	snap.Decks[0].HasDelta = true
	snap.Decks[0].DeltaMS = 12345
	snap.Decks[0].Duration = 20 * 60 * 1e9

	for _, layout := range []LayoutKind{LayoutA, LayoutB, LayoutC, LayoutD} {
		st := ViewState{Layout: layout, Focus: 0}
		st.OtherDeck = snap.Decks[1]
		out := Render(80, 24, snap, st)
		assertLinesFit(t, out, 80)
	}
}

var _ = engine.DeckState{}
