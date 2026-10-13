package tui

import (
	"fmt"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

// ZoomWindow is the sliding time window shown when Zoom is on (spec §6.3).
type ZoomWindow struct {
	Start time.Duration
	// Length is how much time the window spans.
	Length time.Duration
	// Playhead is where the playhead sits within the window, 0..1.
	Playhead float64
}

// DefaultZoomBars is bars visible when zoom is first enabled.
const DefaultZoomBars = 4

// ZoomBarOptions are the selectable bar counts, coarse to fine.
var ZoomBarOptions = []int{2, 4, 8, 16, 32}

// PlayX keeps the playhead left of centre so upcoming bars are visible. At 0.5
// the user cannot see what is coming, which is the whole point of the view.
const PlayX = 0.30

// WindowFor returns the zoom window for a position, bar count, and tempo.
//
// A missing or nonsensical tempo falls back to 128 rather than dividing by zero:
// a deck without a beatgrid must still show a usable window, not a panic.
func WindowFor(pos time.Duration, bars int, bpm float64) ZoomWindow {
	return WindowAt(pos, ZoomLength(bars, bpm))
}

// ZoomLength is how much time a window of the given bar count spans at a tempo.
//
// It is split out because both decks must share one value. WindowFor derived the
// length from each deck's own BPM, so at 128 and 124 the two windows spanned
// different durations and the spec's "both decks display the same time scale"
// could not hold however they were drawn: every column meant a different
// instant on each deck, and beat markers could never line up.
func ZoomLength(bars int, bpm float64) time.Duration {
	if bpm <= 0 {
		bpm = 128
	}
	if bars <= 0 {
		bars = DefaultZoomBars
	}
	// A beat is 60/bpm SECONDS, not 1/bpm. Dividing a second by the tempo gives
	// a 128th-of-a-second at 128 BPM, so every window was 60x too short: two bars
	// showed 62ms of audio rather than 3.75 seconds. The engine's own
	// beatPeriodFrames gets this right (rate*60/bpm), which is how the two
	// disagreed without either test noticing.
	beat := time.Duration(float64(time.Minute) / bpm)
	return time.Duration(bars*4) * beat
}

// WindowAt places a window of a given length on a position.
//
// Both zoomed decks use this with the SAME length and the same PlayX, so a
// column is the same offset from each playhead. When the tempos match, the beat
// markers share columns; as the decks drift, the columns slide -- which spec
// 6.3 calls the core beatmatching affordance.
func WindowAt(pos time.Duration, length time.Duration) ZoomWindow {
	if length <= 0 {
		length = ZoomLength(DefaultZoomBars, 0)
	}
	// The window starts PlayX of its length before the playhead so what is
	// coming is visible.
	start := pos - time.Duration(float64(length)*PlayX)
	if start < 0 {
		start = 0
	}
	return ZoomWindow{Start: start, Length: length, Playhead: PlayX}
}

// ZoomIndex advances a zoom index to the next option, wrapping.
func ZoomIndex(i int) int { return (i + 1) % len(ZoomBarOptions) }

// RenderZoomed draws the waveform for a window, with beat and cue markers that
// fall inside it.
//
// Markers outside [Start, Start+Length] are filtered out: a cue twenty minutes
// away must not appear in the middle of the view.
func RenderZoomed(pcm dsp.Stereo, w ZoomWindow, width, height int, m ZoomMarkers, sampleRate int) []string {

	if width <= 0 || height <= 0 {
		return nil
	}
	// The sample rate is a parameter, not the dsp.SampleRate constant: a device
	// may grant 48k, and converting with the 44.1k constant puts every window
	// 8.8% out -- the zoomed wave is drawn from the wrong region of the audio
	// and the playhead does not match what is drawn (spec 4.2, rate-agnostic).
	if sampleRate <= 0 {
		sampleRate = dsp.SampleRate
	}
	rate := float64(sampleRate)

	// Slice the PCM to the window, tolerating a window that runs past the end.
	startFrame := int(w.Start.Seconds() * rate)
	if startFrame < 0 {
		startFrame = 0
	}
	endFrame := startFrame + int(w.Length.Seconds()*rate)
	var slice dsp.Stereo
	if startFrame*2 < len(pcm) {
		if endFrame*2 > len(pcm) {
			endFrame = len(pcm) / 2
		}
		if endFrame > startFrame {
			slice = pcm[startFrame*2 : endFrame*2]
		}
	}

	// Map marker times to cell indices within the window.
	toCell := func(t time.Duration) int {
		if t < w.Start {
			return -1
		}
		rel := float64(t-w.Start) / float64(w.Length)
		if rel > 1 || w.Length <= 0 {
			return -1
		}
		return int(rel * float64(width))
	}
	var cellBeats, cellDownbeats, cellCues []int
	for _, b := range m.Beats {
		if c := toCell(b); c >= 0 {
			cellBeats = append(cellBeats, c)
		}
	}
	for _, b := range m.Downbeats {
		if c := toCell(b); c >= 0 {
			cellDownbeats = append(cellDownbeats, c)
		}
	}
	for _, c := range m.Cues {
		if i := toCell(c); i >= 0 {
			cellCues = append(cellCues, i)
		}
	}
	cellLoops := make([]LoopSpan, 0, len(m.Loops))
	for _, lp := range m.Loops {
		a, b := toCell(lp.Start), toCell(lp.End)
		if a < 0 || b < 0 || b < a {
			continue
		}
		cellLoops = append(cellLoops, LoopSpan{Start: a, End: b})
	}

	playCell := int(w.Playhead * float64(width))
	if playCell < 0 {
		playCell = 0
	}
	if playCell >= width {
		playCell = width - 1
	}

	return RenderWave(WaveSpec{
		Peaks:     PeaksFromPCM(slice, width),
		Width:     width,
		Height:    height,
		PlayCol:   playCell,
		Beats:     cellBeats,
		Cues:      cellCues,
		Downbeats: cellDownbeats,
		Loops:     cellLoops,
	})
}

// ZoomMarkers are the times a zoomed waveform draws markers at.
type ZoomMarkers struct {
	Beats     []time.Duration
	Downbeats []time.Duration
	Cues      []time.Duration
	// Loops are the active loop bounds in track time. The zoomed view passed nil
	// here, so an active loop was invisible on exactly the view a DJ uses to
	// judge whether a loop is landing on the beat.
	Loops []TimeSpan
}

// TimeSpan is a half-open range in track time. LoopSpan is the same idea in cell
// indices; this is the input form, before mapping through a window.
type TimeSpan struct{ Start, End time.Duration }

// absDur is the absolute value of a duration.
func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// DeltaBeat reports a deck's beat-phase offset from the master.
//
// The value comes from the engine, which computes it against both real
// beatgrids. This package previously re-derived it as position-modulo-beat,
// which assumes a beat lands at t == 0; a detected grid essentially never starts
// there, so that number was wrong by the grid's first-beat offset. It also
// ignored BeatTimes, which the Snapshot already carried.
func DeltaBeat(d, master engine.DeckState) (time.Duration, bool) {
	if !d.HasDelta || !master.HasDelta {
		return 0, false
	}
	return time.Duration(d.DeltaMS * float64(time.Millisecond)), true
}

// RenderDeltaBeat renders the ΔBEAT readout in ms.
func RenderDeltaBeat(d, master engine.DeckState) string {
	delta, ok := DeltaBeat(d, master)
	if !ok {
		return dimStyle.Render("dBEAT --")
	}
	sign := "+"
	if delta < 0 {
		sign = "-"
	}
	ms := float64(absDur(delta)) / float64(time.Millisecond)
	style := labelStyle
	if ms > 5 {
		style = warnStyle // audibly off; worth flagging
	}
	return style.Render(fmt.Sprintf("dBEAT %s%.0fms", sign, ms))
}
