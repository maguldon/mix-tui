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
	// Shorts counts blocks where the ring held audio but less than the device
	// asked for. A device asking for more than the ring can ever hold underruns
	// this way on EVERY callback while Underruns stays at zero, so the two are
	// reported apart rather than summed.
	Shorts uint64
	// MaxBufferFrames is the largest single buffer the device has asked for.
	//
	// Shown because it decides whether a sustained Shorts count is the user's
	// configuration to change: the pump's target floors at this number, so a
	// ring that cannot hold it underruns on every callback regardless of how
	// much headroom it appears to have.
	MaxBufferFrames int
	// QuantumFrames is the per-quantum sample count the graph negotiated, and
	// DeviceDelayFrames is its reported delay to the device in frames. Both are
	// 0 until the daemon has reported a quantum.
	//
	// These, not any ratio of frames to callbacks, are the output latency. The
	// frames a drain moves are bounded by MaxBufferFrames, so frames/callbacks
	// measures how much room the driver allocated rather than how long the audio
	// takes to come out — which read as a 250ms device on a host that was
	// serving 10ms quanta.
	QuantumFrames     uint64
	DeviceDelayFrames int64
	// MIDIIgnored counts ignored messages per category. A nil map is fine.
	MIDIIgnored map[string]uint64
	// MIDIMods is the set of modifier latches currently held. Spec §5.3 says
	// the TUI displays them, and without it a shift-modified action is
	// indistinguishable from an unmodified one that simply did the wrong thing.
	// A nil or empty map means nothing is held.
	MIDIMods map[string]bool
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
	// AudioExpected reports that an audio device was actually opened. The
	// "no callbacks" warning is only meaningful when it was.
	AudioExpected bool
	// DeclaredRate is the rate the stream asked for and NegotiatedRate the one
	// the daemon granted, or 0 when it has not said.
	//
	// These are the only two numbers that can say whether a track is playing at
	// the right speed. mix-tui had neither: it compared its configured rate
	// against the rate it declared, which is the same number by construction, so
	// the startup warning it prints could not fire for the disagreement it was
	// written to report.
	DeclaredRate   int
	NegotiatedRate int
	// RateMismatch is true when the daemon granted something other than the
	// declared rate. Whether that resamples or plays everything at the wrong
	// speed depends on PipeWire's converter, which is not something the app can
	// assume -- so it is surfaced rather than judged.
	RateMismatch bool
}

// knownMIDICategories are always listed, even at zero.
//
// A category that is absent reads as "not measured" rather than "measured,
// nothing seen", and the whole point of this overlay is to distinguish the two.
var knownMIDICategories = []string{
	"sysex", "program-change", "pitch-bend", "aftertouch", "clock", "transport", "unmapped",
}

// shortWarnPercent is the share of callbacks that must come up short before the
// overlay calls it a fault rather than a startup artifact.
const shortWarnPercent = 10

// shortWarnMinCallbacks is how many callbacks must have happened before the
// share means anything.
//
// Without this floor the overlay would fire during the first second of
// playback, when one short callback out of two is 50% and says nothing.
const shortWarnMinCallbacks = 20

// formatShorts renders the short-callback count against the total, because the
// count alone cannot be read. One short callback is the pump learning the
// device's buffer size and is expected; one short callback out of one hundred
// is a fault.
func formatShorts(s DebugStats) string {
	total := s.MainCallbacks + s.CueCallbacks
	if total == 0 {
		// No audio has happened yet, so there is no denominator. "0 of 0"
		// would be a division waiting to happen and "--" reads as "not yet
		// measured", which is what it is.
		return "--"
	}
	pct := s.Shorts * 100 / total
	return fmt.Sprintf("%d of %d (%d%%)", s.Shorts, total, pct)
}

// formatDeviceLatency renders the graph's delay to the device in milliseconds.
//
// "--" rather than "0.0ms" when the daemon has reported no quantum. A stream
// that has not started yet has no latency, and a zero would read as an instant
// device — a measurement where there is none, which is worse than an absence.
func formatDeviceLatency(s DebugStats) string {
	if s.DeviceDelayFrames <= 0 || s.NegotiatedRate <= 0 {
		return "--"
	}
	return fmt.Sprintf("%.1fms", float64(s.DeviceDelayFrames)*1000/float64(s.NegotiatedRate))
}

// shortWarning reports whether the short-callback rate is a fault, and what to
// do about it.
//
// A sustained rate is the bug #20 was written for: a ring that cannot hold the
// buffers the device asks for, underrunning on every callback. It is the ONE
// short callback at the start that is normal -- the pump converges from below,
// starting at the configured target and raising to the device's buffer size
// once it has seen one, so exactly one short is the price of learning it.
//
// Keying on "any short at all" would therefore warn permanently, and about
// something that is working, on every host whose driver batches. That is the
// failure mode this function exists to avoid.
func shortWarning(s DebugStats) (string, bool) {
	total := s.MainCallbacks + s.CueCallbacks
	if total < shortWarnMinCallbacks {
		return "", false
	}
	if s.Shorts*100/total < shortWarnPercent {
		return "", false
	}

	// Name the number when it is known. The pump floors its target at the
	// device's buffer size, so that is the value audio.ring_frames has to be
	// above, and it is not derivable from anything else on this screen.
	//
	// Kept short deliberately. pad() does not wrap, so anything past the
	// overlay's width is simply lost, and the tail of a warning is the part
	// carrying the advice. The counts are already in the row above.
	if s.MaxBufferFrames > 0 {
		return fmt.Sprintf("! shorts are sustained: raise audio.ring_frames above %d",
			s.MaxBufferFrames), true
	}
	return "! shorts are sustained: raise audio.ring_frames", true
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
	lines = append(lines, kv("ring overruns", fmt.Sprintf("%d", s.Overruns), w)...)
	lines = append(lines, kv("MAIN callbacks", fmt.Sprintf("%d", stats.MainCallbacks), w)...)
	lines = append(lines, kv("CUE callbacks", fmt.Sprintf("%d", stats.CueCallbacks), w)...)
	lines = append(lines, kv("short callbacks", formatShorts(stats), w)...)
	// The output latency, which is the one number that cannot be worked out
	// from any other row here. Placed with the other audio-health figures
	// because it is the one a user comparing two setups actually wants.
	lines = append(lines, kv("device latency", formatDeviceLatency(stats), w)...)

	// The single most useful diagnostic: both streams idle means nothing is
	// draining. It is only meaningful once playback was expected, so a headless
	// or -no-audio run does not raise a permanent false alarm.
	if stats.AudioExpected && stats.MainCallbacks == 0 && stats.CueCallbacks == 0 {
		lines = append(lines, pad(warnStyle.Render("! no audio callbacks: check the device"), w))
	}
	if stats.Underruns > 0 {
		lines = append(lines, pad(warnStyle.Render(fmt.Sprintf("! %d underrun(s)", stats.Underruns)), w))
	}
	if msg, ok := shortWarning(stats); ok {
		lines = append(lines, pad(warnStyle.Render(msg), w))
	}

	// What the device is actually running at, next to what was asked for.
	if stats.DeclaredRate > 0 {
		got := "--"
		if stats.NegotiatedRate > 0 {
			got = fmt.Sprintf("%d", stats.NegotiatedRate)
		}
		lines = append(lines, kv("sample rate", fmt.Sprintf("%d (device %s)",
			stats.DeclaredRate, got), w)...)
	}
	if stats.RateMismatch {
		// Not phrased as a verdict. Whether PipeWire's converter absorbs the
		// difference or every track plays at the wrong speed is not something
		// this process can observe -- it only knows the two numbers differ.
		lines = append(lines, pad(warnStyle.Render(
			"! device granted a different rate than declared; check the mix is at the right speed"), w))
	}

	// MIDI health.
	lines = append(lines, "")
	lines = append(lines, section("MIDI (ignored)", w)...)
	lines = append(lines, midiCounts(stats.MIDIIgnored, w)...)
	lines = append(lines, heldMods(stats.MIDIMods, w)...)

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

// heldMods renders the modifier latches currently held.
//
// Shown only when something is held, because an empty row on every screen in
// normal use teaches a reader to skip past the place where this information
// lives. When a modifier IS held and the resulting action is not the one the
// user expected, this is the line that explains it.
func heldMods(mods map[string]bool, w int) []string {
	held := make([]string, 0, len(mods))
	for name, on := range mods {
		if on {
			held = append(held, name)
		}
	}
	if len(held) == 0 {
		return nil
	}
	sort.Strings(held)
	return kv("mods held", strings.Join(held, " "), w)
}
