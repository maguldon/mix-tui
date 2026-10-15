package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/maguldon/mix-tui/internal/engine"
)

// sgrPattern matches one SGR (Select Graphic Rendition) sequence: the escapes
// lipgloss uses to set and reset colour.
var sgrPattern = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// colourRun is a stretch of visible text together with the SGR sequence that was
// in effect when it was emitted.
//
// The sequence is empty when nothing had set a foreground, which means the
// glyphs took whatever the terminal already had. That distinction is the whole
// point of this file: on this code path an empty sequence is a bug, not a
// stylistic choice.
type colourRun struct {
	text string
	sgr  string
}

// splitByColour walks a rendered line and reports what colour each visible
// character was drawn in.
//
// A reset (\x1b[0m or a bare \x1b[m) clears the current colour rather than
// setting one, so text after a reset is reported with an empty sgr. That is not
// a limitation of the helper: it is exactly how a terminal reads it, and it is
// what makes a row that wraps a pre-styled string in an outer style lose colour
// for its tail.
func splitByColour(s string) []colourRun {
	var out []colourRun
	cur := ""
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, colourRun{text: b.String(), sgr: cur})
			b.Reset()
		}
	}
	for i := 0; i < len(s); {
		m := sgrPattern.FindStringIndex(s[i:])
		if m == nil {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+m[0]])
		code := s[i+m[0] : i+m[1]]
		flush()
		if code == "\x1b[0m" || code == "\x1b[m" {
			cur = ""
		} else {
			cur = code
		}
		i += m[1]
	}
	flush()
	return out
}

// inheritedGlyphs returns the visible non-space characters that were emitted
// with no foreground of their own.
func inheritedGlyphs(s string) []string {
	var bad []string
	for _, r := range splitByColour(s) {
		if r.sgr != "" {
			continue
		}
		for _, ch := range r.text {
			if ch != ' ' {
				bad = append(bad, string(ch))
			}
		}
	}
	return bad
}

// colourOf returns the SGR sequence in effect for the run containing needle.
func colourOf(s, needle string) string {
	for _, r := range splitByColour(s) {
		if strings.Contains(r.text, needle) {
			return r.sgr
		}
	}
	return ""
}

// firstSGR returns the first SGR sequence in s, or "".
func firstSGR(s string) string {
	if m := sgrPattern.FindString(s); m != "" {
		return m
	}
	return ""
}

// TestPanelRowStyleIsApplied is the regression test for the styling fix.
//
// RenderPanel used to read PanelRow.Style and discard it, so every colour a
// panel builder set was a no-op and the whole palette reduced to the borders.
// The fix applied the style -- and came with no test, so reverting it left the
// suite green.
func TestPanelRowStyleIsApplied(t *testing.T) {
	forceColour(t)
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

	if got, want := colourOf(lines[1], "styled"), firstSGR(red.Render("x")); got != want {
		t.Errorf("styled row colour = %q, want %q: Style was discarded", got, want)
	}

	// The second row sets no style, so it must land on the default body colour
	// -- and specifically not on red, and not on nothing at all.
	if got, want := colourOf(lines[2], "plain"), firstSGR(labelStyle.Render("x")); got != want {
		t.Errorf("unstyled row colour = %q, want the body colour %q", got, want)
	}
}

// TestPanelRowStyleKeepsWidth. lipgloss measures visible width, so styling must
// not change how wide a row is -- otherwise every coloured panel is a different
// width from every plain one, and the border is ragged.
func TestPanelRowStyleKeepsWidth(t *testing.T) {
	forceColour(t)
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

		with := RenderPanel(withStyle, 4)
		plain := RenderPanel(without, 4)
		for i := range with {
			if got, want := lipgloss.Width(with[i]), lipgloss.Width(plain[i]); got != want {
				t.Errorf("%s: styled line %d is %d wide, unstyled is %d", name, i, got, want)
			}
		}
	}
}

// TestPanelRowNilStyleGetsTheBodyColour pins the contract for nil Style.
//
// nil used to mean "emit no SGR at all", so the row fell through to the
// terminal's foreground -- normally the brightest thing on screen. The mixer's
// MASTER, GAIN and CUE/P rows and the deck's BPM/key row all came out that way,
// louder than the titles above them, purely because their builders did not
// assign a Style. nil now means "nothing special", i.e. body text.
//
// This test forces the colour profile. Without it lipgloss degrades to Ascii
// headless and asserts nothing -- which is how the original bug survived, and is
// why the previous version of this test passed against the broken code.
func TestPanelRowNilStyleGetsTheBodyColour(t *testing.T) {
	forceColour(t)
	p := Panel{Title: "T", Width: 12, Rows: []PanelRow{
		{Kind: RowText, Text: "plain", Style: nil},
	}}
	lines := RenderPanel(p, 4)

	if got, want := ansiOf(lines[1]), "│plain     │"; got != want {
		t.Errorf("visible text = %q, want %q", got, want)
	}
	want := firstSGR(labelStyle.Render("x"))
	if got := colourOf(lines[1], "plain"); got != want {
		t.Errorf("nil-style row colour = %q, want the body colour %q", got, want)
	}
}

// TestPanelPreStyledRowsAreNotRewrapped guards the exception to the rule above.
//
// Waveform, transport and overview rows carry their own per-run colours. They
// must not be wrapped in the default body style: lipgloss does not re-apply an
// outer style across the reset those helpers embed, so wrapping would recolour
// nothing and would only add two escapes per row at 30fps.
func TestPanelPreStyledRowsAreNotRewrapped(t *testing.T) {
	forceColour(t)
	body := okStyle.Render("PLAY") + " " + dimStyle.Render("CUE")
	p := Panel{Title: "T", Width: 20, Rows: []PanelRow{
		{Kind: RowText, Text: body, Style: nil},
	}}
	line := RenderPanel(p, 4)[1]

	if got, want := colourOf(line, "PLAY"), firstSGR(okStyle.Render("x")); got != want {
		t.Errorf("PLAY colour = %q, want %q: the row's own style was overridden", got, want)
	}
	if got, want := colourOf(line, "CUE"), firstSGR(dimStyle.Render("x")); got != want {
		t.Errorf("CUE colour = %q, want %q: the row's own style was overridden", got, want)
	}
	if strings.Contains(line, firstSGR(labelStyle.Render("x"))) {
		t.Errorf("a pre-styled row was wrapped in the default body style: %q", line)
	}
}

// TestPanelFrameIsDrawnInOneColour is the regression test for the half-drawn box.
//
// RenderPanel wrapped the top and bottom borders in borderBox but concatenated
// the side borders raw, so a panel's horizontal edges were #30363d and its
// vertical edges were the terminal's default foreground. The box read as two
// unrelated lines rather than one frame.
func TestPanelFrameIsDrawnInOneColour(t *testing.T) {
	forceColour(t)
	const frame = "┌┐└┘─│"
	p := Panel{Title: "T", Width: 24, Rows: []PanelRow{
		{Kind: RowText, Text: "hello", Style: &titleStyle},
		{Kind: RowText, Text: "world"},
	}}
	lines := RenderPanel(p, 5)

	byColour := map[string]int{}
	for _, l := range lines {
		for _, r := range splitByColour(l) {
			for _, ch := range r.text {
				if strings.ContainsRune(frame, ch) {
					byColour[r.sgr]++
				}
			}
		}
	}
	if len(byColour) != 1 {
		t.Errorf("frame drawn in %d colours %v, want exactly one", len(byColour), byColour)
	}
	want := firstSGR(borderBox.Render("x"))
	if _, ok := byColour[want]; !ok {
		t.Errorf("frame colour %v, want the border colour %q", byColour, want)
	}
}

// TestNoGlyphInheritsTheTerminalForeground is the top-level statement of the
// reported bug: some characters were grey and some were not, because a glyph
// either had a foreground or fell through to the terminal's.
//
// Rather than assert on the particular rows that happened to be affected --
// which is how the bug kept moving to rows nobody had checked -- this renders
// the real panels and requires that every visible, non-space character carries
// a foreground of its own.
func TestNoGlyphInheritsTheTerminalForeground(t *testing.T) {
	forceColour(t)

	peaks := Peaks{Min: make([]float32, 64), Max: make([]float32, 64)}
	for i := range peaks.Max {
		v := float32(0.3 + 0.6*float32((i*7)%11)/11.0)
		peaks.Min[i], peaks.Max[i] = -v, v
	}
	deck := engine.DeckState{
		Index: 0, Loaded: true, Title: "Warehouse Bloom", Artist: "Nadia Sirota",
		Duration: 6*time.Minute + 12*time.Second, Position: 92 * time.Second,
		Playing: true, Level: 0.5, Pitch: 0.02,
		HasBeatGrid: true, BPM: 128, Meter: 4, KeyCamelot: "8A", KeyConfidence: 0.9,
		BeatTimes:     []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
		DownbeatTimes: []time.Duration{0, 4 * time.Second},
		Peaks:         peaks,
	}
	deck.CueTimes[0], deck.CueTimes[1] = 30*time.Second, 120*time.Second
	deck.CueCount = 2
	deck.LoopActive, deck.LoopBeatCount = true, 4
	deck.LoopStart, deck.LoopEnd = 120*time.Second, 140*time.Second

	snap := engine.Snapshot{
		Crossfader: 0.2, MasterGain: 0.9, CueMix: 0.1,
		MasterLevel: 0.4, MasterPeak: 0.7,
		Decks: []engine.DeckState{{Level: 0.55, Peak: 0.8}, {Level: 0.1, Peak: 0.2}},
	}

	panels := map[string][]string{
		"deck A":      RenderPanel(renderDeckPanel(52, deck, ViewState{}, true, 16), 16),
		"deck A idle": RenderPanel(renderDeckPanel(52, engine.DeckState{Index: 1}, ViewState{}, false, 8), 8),
		"mixer":       RenderPanel(renderMixerPanel(30, snap, ViewState{}), 8),
		"deck strip":  RenderPanel(renderDeckStrip(30, deck), 3),
	}

	for name, lines := range panels {
		for i, l := range lines {
			if bad := inheritedGlyphs(l); len(bad) > 0 {
				t.Errorf("%s line %d: %d glyph(s) took the terminal's foreground: %q\n  %q",
					name, i, len(bad), bad, ansiOf(l))
			}
		}
	}
}
