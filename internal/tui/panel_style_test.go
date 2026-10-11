package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestPanelRowStyleIsApplied is the regression test for the styling fix.
//
// RenderPanel used to read PanelRow.Style and discard it, so every colour a
// panel builder set was a no-op and the whole palette reduced to the borders.
// The fix applied the style -- and came with no test, so reverting it left the
// suite green.
//
// lipgloss suppresses colour when it does not think it is on a TTY, which is
// the case under `go test`. The style is therefore built on a renderer whose
// profile is forced, so the escape codes are genuinely produced and the
// assertion is about RenderPanel, not about the environment.
func TestPanelRowStyleIsApplied(t *testing.T) {
	r := lipgloss.NewRenderer(nil)
	r.SetColorProfile(termenv.ANSI)
	red := r.NewStyle().Foreground(lipgloss.Color("1"))

	p := Panel{
		Title: "T",
		Width: 20,
		Rows: []PanelRow{
			{Kind: RowText, Text: "styled", Style: &red},
			{Kind: RowText, Text: "plain"},
		},
	}
	lines := RenderPanel(p, 4)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	body := lines[1]
	if !strings.Contains(body, "styled") {
		t.Fatalf("row text missing: %q", body)
	}
	if !strings.Contains(body, "\x1b[") {
		t.Errorf("the styled row contains no escape sequence, so Style was discarded: %q", body)
	}

	// The unstyled row must NOT be coloured, otherwise "styled" proves nothing.
	if plain := lines[2]; strings.Contains(plain, "\x1b[") {
		t.Errorf("the unstyled row picked up colour: %q", plain)
	}
}

// TestPanelRowStyleKeepsWidth. lipgloss measures visible width, so styling must
// not change how wide a row is -- otherwise every coloured panel is a different
// width from every plain one, and the border is ragged.
func TestPanelRowStyleKeepsWidth(t *testing.T) {
	r := lipgloss.NewRenderer(nil)
	r.SetColorProfile(termenv.ANSI)
	styles := map[string]lipgloss.Style{
		"bold":  r.NewStyle().Bold(true),
		"red":   r.NewStyle().Foreground(lipgloss.Color("1")),
		"dim":   r.NewStyle().Faint(true),
		"multi": r.NewStyle().Bold(true).Foreground(lipgloss.Color("3")).Faint(true),
	}

	for name, st := range styles {
		style := st
		withStyle := Panel{Title: "T", Width: 24, Rows: []PanelRow{
			{Kind: RowText, Text: "hello", Style: &style},
		}}
		without := Panel{Title: "T", Width: 24, Rows: []PanelRow{
			{Kind: RowText, Text: "hello"},
		}}

		for i := range RenderPanel(withStyle, 4) {
			got := lipgloss.Width(RenderPanel(withStyle, 4)[i])
			want := lipgloss.Width(RenderPanel(without, 4)[i])
			if got != want {
				t.Errorf("%s: styled line %d is %d wide, unstyled is %d", name, i, got, want)
			}
		}
	}
}

// TestPanelRowNilStyleRendersPlainText covers the nil case explicitly: it must
// produce the bare text, not a literal escape sequence from a nil dereference.
func TestPanelRowNilStyleRendersPlainText(t *testing.T) {
	p := Panel{Title: "T", Width: 12, Rows: []PanelRow{
		{Kind: RowText, Text: "plain", Style: nil},
	}}
	for i, line := range RenderPanel(p, 4) {
		if strings.Contains(line, "\x1b") && !strings.Contains(line, "hello") && i == 1 {
			t.Errorf("line %d has stray escapes with a nil style: %q", i, line)
		}
	}
	body := RenderPanel(p, 4)[1]
	if body != "│"+padRight("plain", 10)+"│" {
		t.Errorf("nil style row = %q, want plain padded text with no escapes", body)
	}
}
