package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/tui/library"
)

// caretField is how every text field in this interface is drawn, so these are
// its tests rather than each field's.
func TestCaretFieldPlacesTheCaretWhereItIs(t *testing.T) {
	cases := []struct {
		name string
		text string
		at   int
		want string
	}{
		{name: "empty", text: "", at: 0, want: caretGlyph},
		{name: "at the end", text: "beat", at: 4, want: "beat" + caretGlyph},
		{name: "in the middle", text: "beat", at: 2, want: "be" + caretGlyph + "at"},
		{name: "at the start", text: "beat", at: 0, want: caretGlyph + "beat"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := caretField("> ", c.text, c.at)
			want := "> " + c.want
			if got != want {
				t.Errorf("caretField(%q, %d) = %q, want %q", c.text, c.at, got, want)
			}
		})
	}
}

// TestCaretFieldClampsAnOutOfRangeCaret. The model clamps on every edit, so a
// caret that arrives out of range means some path missed the setter -- and a
// caret past the end would either panic or draw nothing at all.
func TestCaretFieldClampsAnOutOfRangeCaret(t *testing.T) {
	for _, at := range []int{4, 400} {
		if got := caretField("", "beat", at); got != "beat"+caretGlyph {
			t.Errorf("caretField(%d) = %q, want the caret clamped to the end", at, got)
		}
	}
	for _, at := range []int{-1, -99} {
		if got := caretField("", "beat", at); got != caretGlyph+"beat" {
			t.Errorf("caretField(%d) = %q, want the caret clamped to the start", at, got)
		}
	}
}

// TestCaretFieldMeasuresAsOneColumn. The caret is drawn, not styled, precisely
// so it can be measured; a caret that did not count as one column would make
// the field a column too wide whenever it is drawn and one too narrow when it
// is not, and every pane line has to be exactly the pane's width.
func TestCaretFieldMeasuresAsOneColumn(t *testing.T) {
	for _, at := range []int{0, 2, 4} {
		got := caretField("> ", "beat", at)
		if w := lipgloss.Width(got); w != 7 {
			t.Errorf("caretField at %d measures %d columns, want 7 (%q)", at, w, got)
		}
	}
}

// TestCaretDoesNotEraseTheCharacterUnderIt. A caret drawn over the character it
// sits on hides that character exactly when the user is deciding whether to
// delete it, so it is drawn beside the text instead.
func TestCaretDoesNotEraseTheCharacterUnderIt(t *testing.T) {
	got := caretField("", "beat", 2)
	if n := strings.Count(got, "e"); n != 1 {
		t.Errorf("caretField at 2 = %q, with %d 'e'; the character under the caret "+
			"must still be drawn", got, n)
	}
}

// TestOverlayDrawsTheCaretWhereItIs. The overlay is the one field that was
// already drawn, with a trailing underscore standing in for a caret that could
// never move. It is now the real thing.
func TestOverlayDrawsTheCaretWhereItIs(t *testing.T) {
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/1.wav", Title: "Inner Speaker"})

	at := strings.Index(RenderOverlay(120, 40, ix, "inner", 2, 0, "esc cancel"), "in"+caretGlyph+"ner")
	if at < 0 {
		t.Errorf("the overlay did not draw the caret at 2:\n%s", RenderOverlay(120, 40, ix, "inner", 2, 0, "esc cancel"))
	}
}

// TestSettingsDrawsTheCaretWhereItIs. Same for the library-root field, which
// had the same trailing underscore.
func TestSettingsDrawsTheCaretWhereItIs(t *testing.T) {
	out := RenderSettings(80, 24, SettingsState{Input: "/srv/dj", Cursor: 5})
	if want := "/srv/" + caretGlyph + "dj"; !strings.Contains(out, want) {
		t.Errorf("the settings field did not draw the caret at 5; want %q:\n%s", want, out)
	}
}

// TestFieldsStillFitTheirWidth. The caret is one more glyph than there used to
// be, and a line that overflows its pane makes a full-screen app repaint noise.
func TestFieldsStillFitTheirWidth(t *testing.T) {
	ix := library.NewIndex()
	ix.Add(library.Track{Path: "/1.wav", Title: "Inner Speaker"})
	long := strings.Repeat("verylongdirectoryname", 12)
	for _, w := range []int{200, 120, 90, 80, 40, 20} {
		for _, out := range []string{
			RenderOverlay(w, 30, ix, long, 0, 0, ""),
			RenderOverlay(w, 30, ix, "", 0, 0, ""),
			RenderSettings(w, 30, SettingsState{Input: long, Cursor: 3}),
			RenderSettings(w, 30, SettingsState{Input: "", Cursor: 0}),
		} {
			for i, line := range strings.Split(out, "\n") {
				if got := lipgloss.Width(line); got > w {
					t.Errorf("w=%d line %d is %d columns", w, i, got)
				}
			}
		}
	}
}
