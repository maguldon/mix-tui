package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

// brailleDot maps (column, row) to the bit for U+2800+n. Column 0 is the left
// dot column, column 1 the right; rows 0-3 run top to bottom.
//
// Braille cells are single-width by construction: U+2800..U+28FF occupy exactly
// one terminal column. That is why waveforms use them — a box-drawing or block
// glyph would be two columns wide and silently shift every column after it
// (global constraint).
var brailleDot = [2][4]rune{
	{0x01, 0x02, 0x04, 0x40}, // left column, rows 0-3
	{0x08, 0x10, 0x20, 0x80}, // right column, rows 0-3
}

// brailleBase is the empty braille cell.
const brailleBase = 0x2800

// Peaks is an alias for the engine's envelope type, so a view and the engine
// share one type rather than two structurally identical ones that could drift.
type Peaks = engine.Peaks

// cellKind is what a single braille cell is showing, which decides its colour.
//
// A braille cell is one glyph and takes one colour, so a cell that carries both
// waveform and a beat tick can only be one of them. Markers win: the existing
// comment on overlayMarkers says a marker buried under the waveform is a marker
// nobody sees, and colouring is a second way to bury it.
type cellKind uint8

const (
	kindNone cellKind = iota
	// kindUnplayed is waveform ahead of the playhead.
	kindUnplayed
	// kindPlayed is waveform behind the playhead.
	kindPlayed
	kindCue
	kindBeat
	kindDownbeat
	kindLoop
	kindPlayhead
)

// cellStyle resolves a kind to its colour.
//
// The palette entries for the waveform existed at internal/tui/style.go with
// comments naming exactly these jobs and were used nowhere: RenderWave returned
// plain runes, so the spec's "played audio renders in one color, unplayed in
// another" was never drawn at all.
func cellStyle(k cellKind) lipgloss.Style {
	switch k {
	case kindUnplayed:
		return lipgloss.NewStyle().Foreground(unplayed)
	case kindPlayed:
		return lipgloss.NewStyle().Foreground(played)
	case kindCue:
		return lipgloss.NewStyle().Foreground(cue)
	case kindBeat:
		// A plain beat is the quieter of the two: a grey tick against an orange
		// downbeat, so a bar line is findable without every beat shouting.
		return dimStyle
	case kindDownbeat:
		return lipgloss.NewStyle().Foreground(beat)
	case kindLoop:
		return lipgloss.NewStyle().Foreground(loopColor)
	case kindPlayhead:
		return lipgloss.NewStyle().Foreground(playhead)
	default:
		return lipgloss.NewStyle()
	}
}

// paintKinds fills a run of cells that share a kind, returning the styled text.
//
// Runs rather than per-cell styling matter: braille cells are one column wide
// but a full escape sequence is ~19 bytes, so styling every cell individually
// quadruples the frame's byte count at 30fps for no visual difference.
func paintRow(cells [][]int, kinds [][]cellKind, row, w int) string {
	var b strings.Builder
	start := 0
	for start < w {
		k := kinds[row][start]
		end := start + 1
		for end < w && kinds[row][end] == k {
			end++
		}
		var plain strings.Builder
		for c := start; c < end; c++ {
			plain.WriteRune(rune(brailleBase + cells[row][c]))
		}
		if k == kindNone {
			b.WriteString(plain.String())
		} else {
			b.WriteString(cellStyle(k).Render(plain.String()))
		}
		start = end
	}
	return b.String()
}

// LoopSpan marks a loop region in cell indices.
type LoopSpan struct{ Start, End int }

// WaveSpec describes one waveform to draw.
type WaveSpec struct {
	Peaks  Peaks
	Width  int // cells
	Height int // braille rows; 4 gives 16 amplitude levels per column pair
	// PlayCol is the cell index of the playhead, or -1 for none.
	PlayCol   int
	Beats     []int
	Downbeats []int
	Cues      []int
	Loops     []LoopSpan
}

// PeaksFromPCM reduces PCM to a min/max envelope of width*2 columns.
//
// Each cell takes two columns (stereo), and within a cell the wider of the two
// channels' peaks is used for that column so a hard-panned bass note still shows
// at full height rather than being halved.
func PeaksFromPCM(pcm dsp.Stereo, width int) engine.Peaks {
	n := width * 2
	p := engine.Peaks{Min: make([]float32, n), Max: make([]float32, n)}
	if width <= 0 || len(pcm) == 0 {
		return p
	}
	frames := len(pcm) / 2
	if frames == 0 {
		return p
	}
	for c := 0; c < n; c++ {
		start := c * frames / n
		end := (c + 1) * frames / n
		if end <= start {
			end = start + 1
		}
		if end > frames {
			end = frames
		}
		var mn, mx float32
		for f := start; f < end; f++ {
			l := pcm[f*2]
			r := pcm[f*2+1]
			if l < mn {
				mn = l
			}
			if l > mx {
				mx = l
			}
			if r < mn {
				mn = r
			}
			if r > mx {
				mx = r
			}
		}
		p.Min[c] = mn
		p.Max[c] = mx
	}
	return p
}

// RenderWave returns exactly Height strings of Width braille cells.
//
// The waveform is mirrored about the centre line: the top half is the positive
// half and the bottom half the negative. A DJ reads amplitude from the shape,
// so a half that grows from the centre outward (rather than from the top down)
// is what makes the display readable at a glance.
//
// Every width and height is clamped: zero or negative values produce no output
// rather than a panic, because the terminal can be resized to anything.
func RenderWave(spec WaveSpec) []string {
	w, h := spec.Width, spec.Height
	if w <= 0 || h <= 0 {
		return nil
	}
	half := h / 2
	if half < 1 {
		// One row cannot be mirrored; render a flat centre line instead of
		// dividing by a zero half.
		return []string{strings.Repeat(brailleRune(0), w)}
	}

	// cells[row][col] accumulates dot bits, and kinds[row][col] says what that
	// cell is showing so it can be coloured.
	cells := make([][]int, h)
	kinds := make([][]cellKind, h)
	for i := range cells {
		cells[i] = make([]int, w)
		kinds[i] = make([]cellKind, w)
	}

	// Past the playhead is played; ahead of it is not. A deck with no playhead
	// (PlayCol < 0) is entirely unplayed, which is what an idle deck shows.
	for r := 0; r < h; r++ {
		for c := 0; c < w; c++ {
			if spec.PlayCol >= 0 && c <= spec.PlayCol {
				kinds[r][c] = kindPlayed
			} else {
				kinds[r][c] = kindUnplayed
			}
		}
	}

	maxLvl := float64(half*4 - 1)
	cols := len(spec.Peaks.Max)
	for c := 0; c < cols && c/2 < w; c++ {
		cell := c / 2
		col := c % 2
		amp := amplitude(spec.Peaks.Min[c], spec.Peaks.Max[c])
		levels := int(amp*maxLvl + 0.5)

		// Positive half grows up from the centre; negative grows down.
		for l := 0; l < levels; l++ {
			ring := l / 4
			sub := l % 4
			// Upper: centre row is half-1, so step outward upward.
			ur := half - 1 - ring
			if ur >= 0 && ur < h {
				cells[ur][cell] |= int(brailleDot[col][3-sub])
			}
			lr := half + ring
			if lr >= 0 && lr < h {
				cells[lr][cell] |= int(brailleDot[col][3-sub])
			}
		}
	}

	// Overlay markers after the wave so they are always visible: a marker
	// buried under waveform dots is a marker nobody sees.
	overlayMarkers(cells, kinds, spec, w, h)

	rows := make([]string, h)
	for r := 0; r < h; r++ {
		rows[r] = paintRow(cells, kinds, r, w)
	}
	return rows
}

// amplitude is the magnitude of one column: the larger of its absolute peaks.
func amplitude(mn, mx float32) float64 {
	a, b := math.Abs(float64(mn)), math.Abs(float64(mx))
	if b > a {
		a = b
	}
	return clampFloat(a, 0, 1)
}

func brailleRune(bits int) string {
	return string(rune(brailleBase + bits))
}

// overlayMarkers paints the playhead, beatgrid, cues, and loop spans onto the
// bit matrix. Indices are bounds-checked: a marker past the end is dropped
// rather than indexing off the buffer.
//
// It records a kind alongside each dot it sets. Where two markers share a cell
// the later one wins, and the order below is the priority: a downbeat is more
// useful than the beat it coincides with, and the playhead more useful than
// either.
func overlayMarkers(cells [][]int, kinds [][]cellKind, spec WaveSpec, w, h int) {
	setCol := func(c, row, bits int, kind cellKind) {
		if c < 0 || c >= w || row < 0 || row >= h {
			return
		}
		cells[row][c] |= bits
		kinds[row][c] = kind
	}

	// Loop spans: mark only the two edges. Filling the whole span would bury
	// the waveform the user is trying to read inside the loop they are looking
	// at — the point of showing both at once is that neither hides the other.
	for _, lp := range spec.Loops {
		start, end := lp.Start, lp.End
		if start < 0 {
			start = 0
		}
		if end >= w {
			end = w - 1
		}
		for r := 0; r < h; r++ {
			setCol(start, r, int(brailleDot[0][2]), kindLoop)
			setCol(end, r, int(brailleDot[0][2]), kindLoop)
		}
	}

	// Beats: a full-height tick. A centre-line dot is invisible against a dense
	// waveform, which is exactly when beats matter most.
	//
	// Downbeats are painted AFTER beats because setCol's kind assignment is a
	// last-write-wins, and the two are drawn with the same glyph. Painted the
	// other way round, every bar line took the ordinary-beat colour and the
	// distinction the spec draws between them was invisible -- at exactly the
	// column a DJ looks for.
	for _, b := range spec.Beats {
		for r := 0; r < h; r++ {
			setCol(b, r, int(brailleDot[0][2]), kindBeat)
		}
	}
	for _, b := range spec.Downbeats {
		for r := 0; r < h; r++ {
			setCol(b, r, int(brailleDot[0][2]), kindDownbeat)
		}
	}
	// Cues: a top marker.
	for _, c := range spec.Cues {
		setCol(c, 0, int(brailleDot[0][0]), kindCue)
		setCol(c, 1, int(brailleDot[0][1]), kindCue)
	}
	// Playhead: a solid full-height column, drawn last so it wins over
	// whatever waveform or marker already occupies those cells.
	if spec.PlayCol >= 0 {
		for r := 0; r < h; r++ {
			setCol(spec.PlayCol, r, int(brailleDot[0][2])|int(brailleDot[1][2]), kindPlayhead)
		}
	}
}

// RenderOverview draws the full-track strip with the visible window highlighted.
// It returns exactly width columns.
func RenderOverview(peaks Peaks, width, winStart, winEnd, playCell int) string {
	if width <= 0 {
		return ""
	}
	// A one-row strip: with half == 1 the amplitude ladder only reaches 4
	// levels, all of which land on row 0. The previous two-row version wrote
	// only row 0 and returned row 1, which was therefore always blank.
	h := 1
	cells := make([][]int, h)
	kinds := make([][]cellKind, h)
	for i := range cells {
		cells[i] = make([]int, width)
		kinds[i] = make([]cellKind, width)
	}
	for c := 0; c < width; c++ {
		if playCell >= 0 && c <= playCell {
			kinds[0][c] = kindPlayed
		} else {
			kinds[0][c] = kindUnplayed
		}
	}
	const maxLvl = 3
	cols := len(peaks.Max)
	for c := 0; c < cols && c/2 < width; c++ {
		amp := amplitude(peaks.Min[c], peaks.Max[c])
		levels := int(amp*maxLvl + 0.5)
		if levels < 1 && amp > 0.01 {
			levels = 1 // a quiet but present signal still shows a mark
		}
		cell := c / 2
		col := c % 2
		for l := 0; l < levels; l++ {
			cells[0][cell] |= int(brailleDot[col][3-l%4])
		}
	}
	// The window is bracketed rather than filled: filling the window range would
	// bury the very audio it is meant to locate, and on a silent track it would
	// draw marks for a track that has none.
	// The window edges are marked by overwriting the cell with a distinctive
	// top-dot glyph rather than OR-ing another dot. OR-ing cannot work: at full
	// amplitude every amplitude dot is already lit, so the marker is invisible
	// exactly when the track is loud.
	if winEnd >= winStart {
		for _, c := range []int{winStart, winEnd} {
			if c >= 0 && c < width {
				cells[0][c] = int(brailleDot[0][0]) | int(brailleDot[1][0])
				kinds[0][c] = kindLoop
			}
		}
	}
	if playCell >= 0 && playCell < width {
		for r := 0; r < h; r++ {
			cells[r][playCell] |= int(brailleDot[0][2]) | int(brailleDot[1][2])
			kinds[r][playCell] = kindPlayhead
		}
	}
	return paintRow(cells, kinds, 0, width)
}
