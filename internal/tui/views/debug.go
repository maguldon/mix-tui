package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maguldon/mix-tui/internal/engine"
)

// DebugStats is the diagnostic surface the debug overlay shows.
//
// It exists because this overlay is the only place a user can find out WHY a
// controller is not doing what they expect. spec §7 requires ignored MIDI
// categories to be counted rather than dropped silently, and this is where
// those counts surface.
type DebugStats struct {
	// Underruns counts blocks where the drain found the ring empty: audible
	// dropouts.
	Underruns uint64
	// MIDIIgnored counts ignored messages per category. A nil map is fine.
	MIDIIgnored map[string]uint64
	// BlocksRendered and RenderWallMax describe the render goroutine's health.
	// RenderWallMax must stay far below one block period (~11.6ms at 44.1kHz);
	// if it approaches that, the renderer is at risk of underrunning.
	BlocksRendered uint64
	RenderWallMax  time.Duration
	// MainCallbacks and CueCallbacks count drain callbacks per stream. Zero on
	// both means nothing is draining at all.
	MainCallbacks uint64
	CueCallbacks  uint64
	// AnalysisState is whatever the library scanner is doing.
	AnalysisState string
}

// knownMIDICategories are always listed, even at zero.
//
// A category that is absent reads as "not measured" rather than "measured,
// nothing seen", and the whole point of this overlay is to distinguish the two.
var knownMIDICategories = []string{
	"sysex", "program-change", "pitch-bend", "aftertouch", "clock", "transport", "unmapped",
}

// RenderDebug draws the diagnostic overlay.
func RenderDebug(w, h int, s engine.Snapshot, stats DebugStats) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := make([]string, 0, h)

	lines = append(lines, pad(titleStyle.Render("DEBUG"), w))
	lines = append(lines, pad(dimStyle.Render(strings.Repeat("-", minInt(w, 40))), w))

	// Audio health.
	lines = append(lines, section("AUDIO", w)...)
	lines = append(lines, kv("underrun", fmt.Sprintf("%d", stats.Underruns), w)...)
	lines = append(lines, kv("blocks rendered", fmt.Sprintf("%d", stats.BlocksRendered), w)...)
	lines = append(lines, kv("worst render", fmt.Sprintf("%v", stats.RenderWallMax.Round(time.Microsecond)), w)...)
	lines = append(lines, kv("MAIN callbacks", fmt.Sprintf("%d", stats.MainCallbacks), w)...)
	lines = append(lines, kv("CUE callbacks", fmt.Sprintf("%d", stats.CueCallbacks), w)...)

	// The single most useful diagnostic: both streams idle means nothing is
	// draining, which looks identical to "no device" from the outside.
	if stats.MainCallbacks == 0 && stats.CueCallbacks == 0 {
		lines = append(lines, pad(warnStyle.Render("! no audio callbacks: check the device"), w))
	}
	if stats.Underruns > 0 {
		lines = append(lines, pad(warnStyle.Render(fmt.Sprintf("! %d underrun(s)", stats.Underruns)), w))
	}

	// MIDI health.
	lines = append(lines, "")
	lines = append(lines, section("MIDI (ignored)", w)...)
	lines = append(lines, midiCounts(stats.MIDIIgnored, w)...)

	// Decks and analysis.
	lines = append(lines, "")
	lines = append(lines, section("DECKS", w)...)
	for i, d := range s.Decks {
		lines = append(lines, pad(fmt.Sprintf("  %d %-18s %s", i, d.Title, playingStr(d)), w))
	}
	if stats.AnalysisState != "" {
		lines = append(lines, pad(dimStyle.Render("scan: "+stats.AnalysisState), w))
	}

	return frame(lines, w, h)
}

func section(name string, w int) []string {
	return []string{pad(labelStyle.Render(name), w)}
}

func kv(k, v string, w int) []string {
	return []string{pad(fmt.Sprintf("  %-18s %s", k, v), w)}
}

func playingStr(d engine.DeckState) string {
	switch {
	case !d.Loaded:
		return "empty"
	case d.Playing:
		return "PLAY"
	default:
		return "stop"
	}
}

// midiCounts lists every known category, including the zero ones, sorted so the
// rows do not jump around between frames.
func midiCounts(m map[string]uint64, w int) []string {
	seen := make(map[string]bool, len(m))
	for _, k := range knownMIDICategories {
		seen[k] = true
	}
	extra := make([]string, 0, len(m))
	for k := range m {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	names := append(append([]string{}, knownMIDICategories...), extra...)

	out := make([]string, 0, len(names))
	for _, n := range names {
		v := m[n] // a nil map reads as zero, which is what we want
		out = append(out, pad(fmt.Sprintf("  %-16s %d", n, v), w))
	}
	return out
}
