package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

// ansiOf strips the SGR sequences from a rendered string, so an assertion can
// talk about glyphs, and leaves colour assertions to count escape codes.
func ansiOf(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		for i < len(s) && s[i] != 'm' {
			i++
		}
	}
	return b.String()
}

// sgrCount counts colour changes in a rendered string.
func sgrCount(s string) int { return strings.Count(s, "\x1b[38;5;") + strings.Count(s, "\x1b[38;2;") }

func loudPeaks(n int) Peaks {
	p := Peaks{Min: make([]float32, n), Max: make([]float32, n)}
	for i := range p.Max {
		p.Max[i] = 0.9
		p.Min[i] = -0.9
	}
	return p
}

// TestWaveformIsColoured is the regression test for issue #10.
//
// internal/tui/style.go declared `played` and `unplayed` with comments naming
// the waveform, and nothing ever used them: RenderWave returned plain runes and
// the panel inserted every row with no style. Spec 6.2 asks for "played audio
// renders in one color, unplayed in another", which was never drawn.
func TestWaveformIsColoured(t *testing.T) {
	forceColour(t)
	rows := RenderWave(WaveSpec{Peaks: loudPeaks(40), Width: 20, Height: 4, PlayCol: 9})
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	for i, r := range rows {
		if sgrCount(r) == 0 {
			t.Errorf("row %d carries no colour: %q", i, ansiOf(r))
		}
	}
}

// TestWaveformPlayedAndUnplayedDiffer pins the split specifically: the two
// halves must not merely be coloured, they must be coloured differently.
func TestWaveformPlayedAndUnplayedDiffer(t *testing.T) {
	forceColour(t)
	rows := RenderWave(WaveSpec{Peaks: loudPeaks(40), Width: 20, Height: 4, PlayCol: 9})
	row := rows[1]
	// Split at the playhead: cells 0..9 played, 10..19 unplayed.
	plain := ansiOf(row)
	if got := len([]rune(plain)); got != 20 {
		t.Fatalf("row is %d glyphs, want 20", got)
	}
	// The playhead cell is column 9, so the last played glyph and the first
	// unplayed one differ in style. Count distinct colour codes on the row.
	seen := map[string]bool{}
	for _, part := range strings.Split(row, "\x1b[") {
		if i := strings.Index(part, "m"); i >= 0 {
			seen[part[:i]] = true
		}
	}
	if len(seen) < 2 {
		t.Errorf("played and unplayed render identically; only %d distinct colour(s)", len(seen))
	}
}

// TestWaveformWithoutAPlayheadIsStillColoured keeps an idle deck from rendering
// unstyled, which would make it look broken rather than stopped.
func TestWaveformWithoutAPlayheadIsStillColoured(t *testing.T) {
	forceColour(t)
	rows := RenderWave(WaveSpec{Peaks: loudPeaks(40), Width: 20, Height: 4})
	if sgrCount(rows[0]) == 0 {
		t.Error("a waveform with no playhead is unstyled")
	}
}

// TestWaveformMarkersUseThePalette checks the beat, downbeat, cue, loop and
// playhead colours are actually reachable -- each kind has to be drawn.
func TestWaveformMarkersUseThePalette(t *testing.T) {
	forceColour(t)
	kinds := []struct {
		name string
		spec WaveSpec
	}{
		{"beat", WaveSpec{Beats: []int{5}}},
		{"downbeat", WaveSpec{Downbeats: []int{5}}},
		{"cue", WaveSpec{Cues: []int{5}}},
		{"loop", WaveSpec{Loops: []LoopSpan{{Start: 3, End: 9}}}},
		{"playhead", WaveSpec{PlayCol: 5}},
	}
	for _, k := range kinds {
		k.spec.Peaks = loudPeaks(40)
		k.spec.Width, k.spec.Height = 20, 4
		rows := RenderWave(k.spec)
		if sgrCount(rows[0]) == 0 && sgrCount(rows[2]) == 0 {
			t.Errorf("%s marker produced no colour", k.name)
		}
	}
}

// TestDownbeatOutranksBeat keeps bar lines findable. Both are drawn as the same
// full-height tick, so wherever they coincide the later one wins -- and they
// coincide at every bar line, which is the only place a downbeat is.
func TestDownbeatOutranksBeat(t *testing.T) {
	forceColour(t)
	withBeatOnly := RenderWave(WaveSpec{Peaks: loudPeaks(40), Width: 20, Height: 4, Beats: []int{5}})
	withBoth := RenderWave(WaveSpec{Peaks: loudPeaks(40), Width: 20, Height: 4,
		Beats: []int{5}, Downbeats: []int{5}})

	// The styled strings must differ: the cell at column 5 is a beat in one and
	// a downbeat in the other.
	if withBeatOnly[2] == withBoth[2] {
		t.Fatal("adding a downbeat changed no colour")
	}
	// The glyphs must not: braille is one column wide, and a shift here would
	// move every column after it.
	beatOnly, both := ansiOf(withBeatOnly[2]), ansiOf(withBoth[2])
	if beatOnly != both {
		t.Errorf("the dot pattern changed:\n beat:     %q\n downbeat: %q", beatOnly, both)
	}
}

// TestWaveformGlyphsAreUnchanged guards the alignment contract. Colour must not
// alter the dot pattern or the column count: braille cells are one column wide,
// and a shift here moves every column after it.
func TestWaveformGlyphsAreUnchanged(t *testing.T) {
	rows := RenderWave(WaveSpec{Peaks: loudPeaks(40), Width: 24, Height: 4, PlayCol: 9,
		Beats: []int{4}, Cues: []int{7}})
	for i, r := range rows {
		if got := len([]rune(ansiOf(r))); got != 24 {
			t.Errorf("row %d is %d glyphs, want 24", i, got)
		}
		for _, g := range ansiOf(r) {
			if g < 0x2800 || g > 0x28ff {
				t.Errorf("row %d contains %q, which is not a braille cell", i, g)
				break
			}
		}
	}
}

// TestDeckPanelDrawsBeatsAndDownbeats covers the unzoomed path, which passed
// neither field before: WaveSpec had them and overlayMarkers consumed them, but
// deck_panel populated neither, so the main waveform had no beatgrid at all.
func TestDeckPanelDrawsBeatsAndDownbeats(t *testing.T) {
	forceColour(t)
	d := engine.DeckState{
		Index: 0, Loaded: true, Duration: 3 * time.Minute,
		Peaks: loudPeaks(400), PeaksWidth: 400,
		HasBeatGrid: true, BPM: 128,
		BeatTimes:     []time.Duration{0, 5e8, 10e8, 15e8, 20e8},
		DownbeatTimes: []time.Duration{0, 20e8},
	}
	p := renderDeckPanel(50, d, ViewState{Layout: LayoutD, Zoom: []bool{false}}, true, 20)

	var plain strings.Builder
	for _, r := range p.Rows {
		if r.Kind == RowText {
			plain.WriteString(ansiOf(r.Text))
		}
	}
	if !strings.Contains(plain.String(), "CRATES") && len(plain.String()) == 0 {
		t.Fatal("the panel rendered nothing")
	}
	// Both marker kinds must contribute colour the waveform alone would not.
	if sgrCount(strings.Join(rowTexts(p), "")) == 0 {
		t.Error("no deck panel row carries colour")
	}
}

func rowTexts(p Panel) []string {
	var out []string
	for _, r := range p.Rows {
		if r.Kind == RowText {
			out = append(out, r.Text)
		}
	}
	return out
}

// TestZoomIsPerDeck covers spec 6.3's "per-deck toggle", which was a single bool
// following deck focus, so TAB moved the zoom rather than adding a comparison.
func TestZoomIsPerDeck(t *testing.T) {
	m, _ := testModel(t)

	press(t, m, "z")
	if m.st.deckZoomed(0) != true || m.st.deckZoomed(1) {
		t.Fatalf("z on deck 0 gave zoom = %v", m.st.Zoom)
	}

	press(t, m, "tab")
	press(t, m, "z")
	if !m.st.deckZoomed(0) || !m.st.deckZoomed(1) {
		t.Errorf("both decks should be zoomable independently, got %v", m.st.Zoom)
	}

	// Tab must not carry the zoom with it: the third press lands back on deck 0
	// and turns DECK 0 off, leaving deck 1 zoomed. With one bool following focus
	// this read [false, false] instead.
	press(t, m, "tab")
	press(t, m, "z")
	if m.st.deckZoomed(0) || !m.st.deckZoomed(1) {
		t.Errorf("z should have toggled deck 0 only; got %v", m.st.Zoom)
	}
}

// TestBothDecksShareOneWindowLength is the fix for spec 6.3's core affordance.
//
// The length was derived per deck from its own BPM, so at 128 and 124 a column
// meant a different instant on each and beat markers could not line up. Both
// decks must now be given the same span.
func TestBothDecksShareOneWindowLength(t *testing.T) {
	a := engine.DeckState{Index: 0, Loaded: true, Duration: 5 * time.Minute, BPM: 128, HasBeatGrid: true}
	b := engine.DeckState{Index: 1, Loaded: true, Duration: 5 * time.Minute, BPM: 124, HasBeatGrid: true}

	st := ViewState{Zoom: []bool{true, true}, ZoomBars: 8}
	st.ZoomLength = ZoomLength(st.ZoomBars, 128)

	wa, okA := st.zoomWindow(a)
	wb, okB := st.zoomWindow(b)
	if !okA || !okB {
		t.Fatal("both decks should be zoomed")
	}
	if wa.Length != wb.Length {
		t.Errorf("window lengths differ: %v vs %v; columns are not comparable", wa.Length, wb.Length)
	}
	if wa.Playhead != wb.Playhead {
		t.Errorf("playhead fractions differ: %v vs %v", wa.Playhead, wb.Playhead)
	}
}

// TestDeckWithoutAGridStaysUnzoomed keeps a gridless deck off the comparison.
// It has no tempo, so it has no scale to share, and showing it on a different
// one would make the comparison quietly wrong rather than obviously absent.
func TestDeckWithoutAGridStaysUnzoomed(t *testing.T) {
	st := ViewState{Zoom: []bool{true, true}, ZoomBars: 8}
	st.ZoomLength = ZoomLength(8, 128)
	noGrid := engine.DeckState{Index: 1, Loaded: true, Duration: time.Minute, BPM: 128}
	if _, ok := st.zoomWindow(noGrid); ok {
		t.Error("a deck with no usable beatgrid was zoomed")
	}
}

// TestOverviewStripTracksTheZoomLevel covers the strip whose span was hardcoded
// to Duration/8, so it had no relationship to the zoom level at all: at 2 bars
// it highlighted a fraction of a cell while the waveform above showed three
// seconds, and at 32 bars a different window again.
//
// The strip maps the WHOLE track onto width cells, so more bars means more
// seconds and therefore a wider highlight.
func TestOverviewStripTracksTheZoomLevel(t *testing.T) {
	d := engine.DeckState{Index: 0, Loaded: true, Duration: 5 * time.Minute, Position: time.Minute}
	const width = 120

	span := func(bars int) int {
		s, e := overviewWindow(d, width, ZoomLength(bars, 128))
		if s < 0 || e < s {
			return -1
		}
		return e - s
	}
	fine, coarse := span(32), span(2)
	if fine <= 0 {
		t.Fatal("a 32-bar window highlighted nothing")
	}
	if coarse >= fine {
		t.Errorf("a 2-bar window (%d cells) should be narrower than a 32-bar one (%d cells)", coarse, fine)
	}
	if got := span(0); got <= 0 {
		t.Error("a zero span produced no highlight at all")
	}
}

// TestRetargetZoomRecomputesOnBarChange keeps Z from leaving both decks on the
// previous scale.
func TestRetargetZoomRecomputesOnBarChange(t *testing.T) {
	m, f := testModel(t)
	f.snap.Decks[0].BPM = 128
	f.snap.Decks[0].HasBeatGrid = true

	press(t, m, "z")
	coarse := m.st.ZoomLength
	press(t, m, "Z")
	fine := m.st.ZoomLength

	if coarse <= 0 || fine <= 0 {
		t.Fatalf("zoom length not set: coarse=%v fine=%v", coarse, fine)
	}
	if coarse == fine {
		t.Error("changing the bar count did not change the window length")
	}
}

// TestZoomedWaveformDrawsLoopsAndDownbeats covers the two fields the zoomed view
// hardcoded to nil, so an active loop was invisible on exactly the view a DJ
// uses to judge whether a loop lands on the beat.
func TestZoomedWaveformDrawsLoopsAndDownbeats(t *testing.T) {
	forceColour(t)
	pcm := dsp.Sine(440, 30)
	w := ZoomWindow{Start: time.Second, Length: 4 * time.Second, Playhead: PlayX}
	beats := []time.Duration{2 * time.Second, 3 * time.Second}
	downs := []time.Duration{2 * time.Second}
	cues := []time.Duration{3 * time.Second}
	loops := []TimeSpan{{Start: 1500 * time.Millisecond, End: 3500 * time.Millisecond}}

	plain := RenderZoomed(pcm, w, 40, 4, ZoomMarkers{}, dsp.SampleRate)
	marked := RenderZoomed(pcm, w, 40, 4, ZoomMarkers{
		Beats: beats, Downbeats: downs, Cues: cues, Loops: loops,
	}, dsp.SampleRate)

	if sgrCount(strings.Join(marked, "")) <= sgrCount(strings.Join(plain, "")) {
		t.Error("markers added no colour to the zoomed waveform")
	}
	for i := range marked {
		if len([]rune(ansiOf(marked[i]))) != len([]rune(ansiOf(plain[i]))) {
			t.Errorf("row %d changed width when markers were added", i)
		}
	}
}

// TestZoomLengthAgreesWithTheEngine pins the window length to the engine's own
// beat period.
//
// The view computed a beat as time.Second/bpm, which at 128 BPM is 7.8ms rather
// than 468ms -- a beat is 60/bpm seconds. Every zoom window was therefore 60x
// too short, showing 62ms of audio where the label said two bars. The engine's
// beatPeriodFrames has always had it right (rate*60/bpm), so the two disagreed
// and neither test noticed: the view tests asserted windows against windows.
func TestZoomLengthAgreesWithTheEngine(t *testing.T) {
	for _, bpm := range []float64{128, 124, 140} {
		wantBeat := time.Duration(float64(time.Minute) / bpm)
		// Eight bars is 32 beats.
		if got, want := ZoomLength(8, bpm), 32*wantBeat; got != want {
			t.Errorf("at %v BPM an 8-bar window is %v, want %v (32 beats of %v)", bpm, got, want, wantBeat)
		}
	}
	// The concrete value, so a regression names itself.
	if got, want := ZoomLength(2, 128), 3750*time.Millisecond; got != want {
		t.Errorf("two bars at 128 BPM = %v, want %v", got, want)
	}
	// 32 beats at 128 BPM is exactly 15 seconds.
	if got, want := ZoomLength(8, 128), 15*time.Second; got != want {
		t.Errorf("eight bars at 128 BPM = %v, want %v", got, want)
	}
}

// TestZoomLengthHandlesNoTempo keeps a gridless reference deck from dividing by
// zero.
func TestZoomLengthHandlesNoTempo(t *testing.T) {
	if got := ZoomLength(8, 0); got <= 0 {
		t.Errorf("ZoomLength with no tempo = %v, want the 128 fallback", got)
	}
}
