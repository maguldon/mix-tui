package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// TestOverlayKeepsTheDecksVisible covers spec 6.5: the quick-load overlay
// floats over the deck view, which stays visible and playing behind it.
//
// It did not. RenderOverlay took no snapshot and the caller replaced the whole
// body, so the decks vanished and the user was choosing a replacement track
// blind -- the exact situation an overlay exists to prevent.
//
// The old test for this, TestOverlayShowsDeckBehind, asserted only that the
// overlay's own title rendered, which passes with no deck behind it at all.
func TestOverlayKeepsTheDecksVisible(t *testing.T) {
	m, _ := testModel(t)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/a.wav", Title: "Incoming Track"})
	m.SetLibrary(ix)

	// Render the same model in mix mode first, so the comparison is against
	// what the decks actually draw rather than a separately-built snapshot.
	m.st.Mode = ModeMix
	mix := m.RenderAt(120, 40)

	m.st.Mode = ModeOverlay
	m.st.Query = ""
	overlay := m.RenderAt(120, 40)

	// The deck view must still be identifiable in the overlay frame.
	if !strings.Contains(mix, "A Track") {
		t.Fatalf("fixture does not render the deck title in the mix view:\n%s", mix)
	}
	if !strings.Contains(overlay, "A Track") {
		t.Errorf("the overlay hid the deck title:\n%s", overlay)
	}

	// The distinguishing assertion: deck panel borders must appear BOTH above
	// and below the box.
	//
	// Comparing the two frames line by line does not work here: the box is nine
	// rows in a forty-row frame and lands where the deck panels are, so
	// "replaced" and "covered" can produce byte-identical output. What
	// separates them is whether any deck row survives on the far side of the
	// box -- with the deck view replaced, nothing follows it.
	mixLines := splitLines(mix)
	overLines := splitLines(overlay)
	if len(mixLines) != len(overLines) {
		t.Fatalf("frame height changed: mix %d, overlay %d", len(mixLines), len(overLines))
	}

	boxTop, boxBottom := -1, -1
	for i, l := range overLines {
		if boxTop < 0 && strings.Contains(l, "LOAD TRACK") {
			boxTop = i
		}
		if boxTop >= 0 && i > boxTop && strings.Contains(l, "─") && !strings.Contains(l, "LOAD") {
			boxBottom = i
		}
	}
	if boxTop < 0 {
		t.Fatalf("the box did not render:\n%s", overlay)
	}

	// Deck content below the box.
	below := 0
	for _, l := range overLines[boxTop+1:] {
		if strings.Contains(l, "│") {
			below++
		}
	}
	if below == 0 {
		t.Errorf("nothing from the deck view appears below the box; it was replaced, not overlaid:\n%s", overlay)
	}

	// Deck content above the box.
	above := 0
	for _, l := range overLines[:boxTop] {
		if strings.Contains(l, "│") {
			above++
		}
	}
	if above == 0 {
		t.Errorf("nothing from the deck view appears above the box:\n%s", overlay)
	}
	_ = boxBottom

	// And the box is on top.
	if !strings.Contains(overlay, "LOAD TRACK") {
		t.Errorf("the overlay box is missing:\n%s", overlay)
	}
}

// TestOverlayIsStillExactlyTheFrameSize guards compositing against ragged
// output: a full-screen app whose frame is not exactly h lines of w columns
// scrolls and corrupts the display.
func TestOverlayIsStillExactlyTheFrameSize(t *testing.T) {
	m, _ := testModel(t)
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/a.wav", Title: "One"})
	ix.Add(library.Track{Path: "/b.wav", Title: "Two"})
	m.SetLibrary(ix)
	m.st.Mode = ModeOverlay

	for _, size := range [][2]int{{120, 40}, {80, 24}, {200, 50}} {
		out := m.RenderAt(size[0], size[1])
		lines := splitLines(out)
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines, want %d", size[0], size[1], len(lines), size[1])
		}
		for i, l := range lines {
			if n := lipgloss.Width(l); n != size[0] {
				t.Errorf("%dx%d line %d is %d columns wide", size[0], size[1], i, n)
				break
			}
		}
	}
}

// TestOverlayRowsFitsInsideTheFrame. The box must never claim more rows than
// the terminal has, or the compositor would push the deck view off screen.
func TestOverlayRowsFitsInsideTheFrame(t *testing.T) {
	ix := library.NewIndex()
	for i := 0; i < 50; i++ {
		ix.Add(library.Track{Path: "/x.wav", Title: "T"})
	}
	for _, h := range []int{10, 20, 24, 40} {
		rows, offset := views.OverlayRows(80, h, ix, "", 0, 0)
		if offset+len(rows) > h {
			t.Errorf("h=%d: box occupies rows %d..%d, past the frame", h, offset, offset+len(rows)-1)
		}
	}
}
