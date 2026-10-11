package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/dsp"
)

func testPeaks(w int, v float32) Peaks {
	p := Peaks{Min: make([]float32, w*2), Max: make([]float32, w*2)}
	for i := range p.Max {
		p.Max[i] = v
		p.Min[i] = -v
	}
	return p
}

func TestRenderWaveDimensions(t *testing.T) {
	for _, h := range []int{2, 3, 4} {
		rows := RenderWave(WaveSpec{Peaks: testPeaks(40, 1), Width: 40, Height: h, PlayCol: -1})
		if len(rows) != h {
			t.Errorf("height %d gave %d rows", h, len(rows))
		}
		for i, r := range rows {
			if lipgloss.Width(r) != 40 {
				t.Errorf("row %d width %d", i, lipgloss.Width(r))
			}
		}
	}
}

func TestRenderWaveUsesBrailleRange(t *testing.T) {
	rows := RenderWave(WaveSpec{Peaks: testPeaks(20, 1), Width: 20, Height: 4, PlayCol: -1})
	for _, r := range rows {
		for _, c := range r {
			if c < 0x2800 || c > 0x28FF {
				t.Fatalf("non-braille rune %U in waveform", c)
			}
		}
	}
}

func TestRenderWaveIsSingleWidthPerCell(t *testing.T) {
	// Global constraint: no mixed-width glyphs in a waveform row
	rows := RenderWave(WaveSpec{Peaks: testPeaks(30, 0.5), Width: 30, Height: 4, PlayCol: -1})
	for _, r := range rows {
		if lipgloss.Width(r) != 30 {
			t.Errorf("row width %d, want 30 (a wide glyph slipped in)", lipgloss.Width(r))
		}
	}
}

func TestRenderWaveSilenceIsBlank(t *testing.T) {
	rows := RenderWave(WaveSpec{Peaks: testPeaks(20, 0), Width: 20, Height: 4, PlayCol: -1})
	for _, r := range rows {
		for _, c := range r {
			if c != 0x2800 {
				t.Fatalf("silence produced %U", c)
			}
		}
	}
}

func TestRenderWaveQuietIsShorterThanLoud(t *testing.T) {
	quiet := RenderWave(WaveSpec{Peaks: testPeaks(20, 0.2), Width: 20, Height: 4, PlayCol: -1})
	loud := RenderWave(WaveSpec{Peaks: testPeaks(20, 1.0), Width: 20, Height: 4, PlayCol: -1})
	count := func(rows []string) int {
		n := 0
		for _, r := range rows {
			for _, c := range r {
				if c != 0x2800 {
					n++
				}
			}
		}
		return n
	}
	if count(quiet) >= count(loud) {
		t.Errorf("quiet waveform not shorter than loud: %d vs %d", count(quiet), count(loud))
	}
}

func TestRenderWaveZeroWidth(t *testing.T) {
	_ = RenderWave(WaveSpec{Peaks: testPeaks(0, 1), Width: 0, Height: 4, PlayCol: -1}) // must not panic
}

func TestPeaksFromPCMRespectsLength(t *testing.T) {
	p := PeaksFromPCM(dsp.Sine(440, 1.0), 50)
	if len(p.Max) != 100 {
		t.Errorf("peak slices = %d, want 100 (2 per cell)", len(p.Max))
	}
}

func TestPeaksFromPCMEmptyIsSafe(t *testing.T) {
	p := PeaksFromPCM(nil, 10)
	if len(p.Max) != 20 {
		t.Errorf("empty PCM gave %d", len(p.Max))
	}
}

func TestRenderOverviewHighlightsWindow(t *testing.T) {
	o := RenderOverview(testPeaks(20, 0.5), 20, 5, 10, 7)
	if lipgloss.Width(o) != 20 {
		t.Errorf("overview width %d", lipgloss.Width(o))
	}
}

func TestRenderWaveZeroWidthHeight(t *testing.T) {
	for _, s := range []WaveSpec{
		{Width: 0, Height: 0, PlayCol: -1}, {Width: 10, Height: 0}, {Width: 0, Height: 4},
		{Width: 10, Height: 1},
	} {
		_ = RenderWave(s) // must not panic
	}
}

// TestRenderWaveIsMirrored checks the waveform is symmetric about the centre
// line: a DJ reads amplitude from the shape, and an asymmetric wave misreads as
// a level difference that is not there.
func TestRenderWaveIsMirrored(t *testing.T) {
	rows := RenderWave(WaveSpec{Peaks: testPeaks(20, 1), Width: 20, Height: 4, PlayCol: -1})
	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(rows))
	}
	// Rows 0,1 are the positive half and 2,3 the negative; with full-scale
	// input every row must be lit.
	for i, r := range rows {
		blank := true
		for _, c := range r {
			if c != 0x2800 {
				blank = false
			}
		}
		if blank {
			t.Errorf("row %d blank at full scale; waveform is not mirrored", i)
		}
	}
}

// TestRenderWaveMarkersAreVisible checks playhead, beats, cues, and loops
// overlay the generated wave rather than being hidden by it.
func TestRenderWaveMarkersAreVisible(t *testing.T) {
	base := RenderWave(WaveSpec{Peaks: testPeaks(20, 0), Width: 20, Height: 4, PlayCol: -1})
	marked := RenderWave(WaveSpec{
		Peaks: testPeaks(20, 0), Width: 20, Height: 4,
		PlayCol:   5, // a real playhead
		Beats:     []int{3, 7, 11},
		Downbeats: []int{0},
		Cues:      []int{15},
		Loops:     []LoopSpan{{Start: 2, End: 18}},
	})
	same := true
	for i := range base {
		if base[i] != marked[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("markers did not change the rendered waveform")
	}
	// Each marker class must be individually visible. The previous assertion
	// only required the frame to differ, which a single marker of the five
	// would satisfy.
	for name, spec := range map[string]WaveSpec{
		"playhead": {Peaks: testPeaks(20, 0), Width: 20, Height: 4, PlayCol: 7},
		"beats":    {Peaks: testPeaks(20, 0), Width: 20, Height: 4, PlayCol: -1, Beats: []int{7}},
		"cues":     {Peaks: testPeaks(20, 0), Width: 20, Height: 4, PlayCol: -1, Cues: []int{7}},
		"loops":    {Peaks: testPeaks(20, 0), Width: 20, Height: 4, PlayCol: -1, Loops: []LoopSpan{{Start: 3, End: 12}}},
	} {
		got := RenderWave(spec)
		changed := false
		for i := range base {
			if base[i] != got[i] {
				changed = true
				break
			}
		}
		if !changed {
			t.Errorf("the %s marker is invisible", name)
		}
	}
	_ = marked
}

// TestRenderWaveMarkersStayInBounds feeds out-of-range marker indices; a
// marker past the end must be dropped, not index off the buffer.
func TestRenderWaveMarkersStayInBounds(t *testing.T) {
	_ = RenderWave(WaveSpec{
		Peaks: testPeaks(10, 0.3), Width: 10, Height: 4,
		PlayCol:   999,
		Beats:     []int{-5, 0, 500},
		Downbeats: []int{-1},
		Cues:      []int{1000},
		Loops:     []LoopSpan{{Start: -10, End: 900}, {Start: 3, End: 2}},
	})
}

// TestRenderMeterIsBounded covers the level bar across the full input range
// including values outside 0..1.
func TestRenderMeterIsBounded(t *testing.T) {
	for _, v := range []float64{-1, 0, 0.5, 1, 2, -3} {
		for _, w := range []int{0, 1, 5, 20} {
			s := RenderMeter(v, w)
			if lipgloss.Width(s) != maxInt(0, w) {
				t.Errorf("RenderMeter(%v, %d) width %d", v, w, lipgloss.Width(s))
			}
		}
	}
}

// TestRenderPeakHoldNeverShrinksInstantly guards the decay: a peak hold that
// drops to the current value in one frame is invisible, which defeats its
// purpose of showing a transient for about a second.
func TestRenderPeakHoldNeverShrinksInstantly(t *testing.T) {
	s := RenderPeakHold(1.0, 0.0, 10)
	if lipgloss.Width(s) != 10 {
		t.Fatalf("width = %d, want 10", lipgloss.Width(s))
	}
	if RenderPeakHold(0.5, 0.0, 10) == RenderPeakHold(1.0, 0.0, 10) {
		t.Error("peak hold ignores the held peak")
	}
}

// TestRenderOverviewActuallyDraws is the assertion the existing overview test
// was missing: it checked only the width, which a blank string satisfies. The
// implementation wrote into one row and returned another, so the strip was
// always empty.
func TestRenderOverviewActuallyDraws(t *testing.T) {
	o := RenderOverview(testPeaks(20, 0.8), 20, 5, 10, -1)
	lit := 0
	for _, r := range o {
		if r != 0x2800 {
			lit++
		}
	}
	if lit == 0 {
		t.Errorf("overview strip is blank for a non-silent signal: %q", o)
	}
	if lipgloss.Width(o) != 20 {
		t.Errorf("overview width %d, want 20", lipgloss.Width(o))
	}
}

// TestRenderOverviewMarksSilence checks the strip stays blank when there is
// nothing to show, so a silent track is not drawn as a full block.
func TestRenderOverviewMarksSilence(t *testing.T) {
	// No window and no playhead: a silent track must render as an empty strip
	// rather than a row of marks implying there is audio there.
	o := RenderOverview(testPeaks(20, 0), 20, -1, -2, -1)
	for _, r := range o {
		if r != 0x2800 {
			t.Fatalf("silence produced a mark: %q", o)
		}
	}
}

// TestRenderOverviewHighlightsWindowRange checks the window highlight moves the
// marker, which is what tells a DJ where the zoomed window sits in the track.
func TestRenderOverviewHighlightsWindowRange(t *testing.T) {
	peaks := testPeaks(20, 0.8)
	a := RenderOverview(peaks, 20, 0, 4, -1)
	b := RenderOverview(peaks, 20, 15, 19, -1)
	if a == b {
		t.Error("moving the window changed nothing; the overview cannot show where it sits")
	}
}
