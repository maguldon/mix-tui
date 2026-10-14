package views

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/maguldon/mix-tui/internal/engine"
)

func emptySnap() engine.Snapshot { return engine.Snapshot{} }

func TestDebugShowsUnderruns(t *testing.T) {
	out := RenderDebug(120, 40, emptySnap(), DebugStats{Underruns: 7})
	if !strings.Contains(out, "underrun") {
		t.Errorf("underruns not shown:\n%s", out)
	}
	if !strings.Contains(out, "7") {
		t.Error("underrun count missing")
	}
}

func TestDebugShowsMIDIIgnoredCounts(t *testing.T) {
	out := RenderDebug(120, 40, emptySnap(),
		DebugStats{MIDIIgnored: map[string]uint64{"sysex": 12, "clock": 3}})
	if !strings.Contains(out, "sysex") {
		t.Error("sysex count missing")
	}
}

func TestDebugHandlesNilMap(t *testing.T) {
	_ = RenderDebug(120, 40, emptySnap(), DebugStats{})
}

func TestDebugFits(t *testing.T) {
	out := RenderDebug(120, 40, emptySnap(), DebugStats{
		MIDIIgnored: map[string]uint64{"a": 1, "b": 2, "c": 3, "d": 4}})
	for i, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 120 {
			t.Errorf("line %d too wide", i)
		}
	}
}

func TestHelpListsBindings(t *testing.T) {
	out := RenderHelp(100, 40, []string{"space  play deck A", "c  cue"})
	if !strings.Contains(out, "space") || !strings.Contains(out, "cue") {
		t.Error("bindings missing")
	}
}

// TestDebugShowsZeroCounts is the spec §7 requirement: this overlay is the only
// place a user can see WHY a controller is not behaving, so a category that has
// seen nothing must still be listed. A missing row reads as "not measured"
// rather than "measured, zero".
func TestDebugShowsZeroCounts(t *testing.T) {
	out := RenderDebug(120, 40, emptySnap(), DebugStats{})
	for _, want := range []string{"sysex", "clock", "underrun", "callback"} {
		if !strings.Contains(out, want) {
			t.Errorf("zero-valued category %q not shown:\n%s", want, out)
		}
	}
}

// TestDebugFlagsAnUnfedDevice catches the case that matters most: both streams
// reporting zero callbacks means nothing is draining, which is the symptom of a
// device the session manager would not configure.
func TestDebugFlagsAnUnfedDevice(t *testing.T) {
	// Only when a device was actually opened: a headless run must not raise a
	// permanent false alarm telling a user with working audio that it is broken.
	out := RenderDebug(120, 40, emptySnap(), DebugStats{AudioExpected: true})
	if !strings.Contains(out, "no audio") && !strings.Contains(out, "check") {
		t.Errorf("zero callbacks on both streams was not flagged:\n%s", out)
	}

	quiet := RenderDebug(120, 40, emptySnap(), DebugStats{AudioExpected: false})
	if strings.Contains(quiet, "no audio callbacks") {
		t.Errorf("a run with no audio device raised a device warning:\n%s", quiet)
	}
}

// TestDebugFlagsStarvingStreams catches underruns, which mean audible dropouts.
func TestDebugFlagsStarvingStreams(t *testing.T) {
	out := RenderDebug(120, 40, emptySnap(), DebugStats{MainCallbacks: 100, Underruns: 3})
	if !strings.Contains(out, "underrun") {
		t.Errorf("underruns not surfaced:\n%s", out)
	}
}

// TestDebugIsRectangular keeps the overlay a clean frame.
func TestDebugIsRectangular(t *testing.T) {
	for _, w := range []int{200, 140, 120, 100, 80} {
		out := RenderDebug(w, 20, emptySnap(), DebugStats{MIDIIgnored: map[string]uint64{"sysex": 1}})
		for i, l := range strings.Split(out, "\n") {
			if got := lipgloss.Width(l); got != w {
				t.Errorf("w=%d line %d width %d", w, i, got)
			}
		}
	}
}

// TestDebugNeverPanicsAcrossSizes sweeps the overlay over sizes.
func TestDebugNeverPanicsAcrossSizes(t *testing.T) {
	st := DebugStats{MIDIIgnored: map[string]uint64{"sysex": 5, "clock": 2}}
	for w := 1; w <= 170; w += 11 {
		for h := 1; h <= 30; h += 7 {
			_ = RenderDebug(w, h, emptySnap(), st)
			_ = RenderHelp(w, h, []string{"space play", "c cue"})
		}
	}
}

// TestHelpHandlesEmptyBindings keeps an empty keymap from rendering nothing.
func TestHelpHandlesEmptyBindings(t *testing.T) {
	if out := RenderHelp(100, 40, nil); out == "" {
		t.Error("empty binding list rendered nothing")
	}
}

// TestHelpShowsWorstRender guards that the slowest render time is visible, since
// it is the number that tells a user whether the render goroutine is keeping up.
func TestHelpShowsWorstRender(t *testing.T) {
	out := RenderDebug(120, 40, emptySnap(), DebugStats{
		MainCallbacks: 10, RenderWallMax: 180 * time.Microsecond})
	if !strings.Contains(out, "render") || !strings.Contains(out, "µs") && !strings.Contains(out, "us") {
		t.Errorf("worst render time not shown:\n%s", out)
	}
}

// TestTheDebugOverlayReportsTheGrantedSampleRate covers the one thing that can
// say whether a track is playing at the right speed.
//
// mix-tui had no way to see the granted rate at all. It printed a startup
// warning comparing its configured rate against the rate it declared -- the same
// number by construction -- so the warning could not fire for the disagreement it
// was written to report. The stream tracked both figures all along; only mixplay
// read them.
func TestTheDebugOverlayReportsTheGrantedSampleRate(t *testing.T) {
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
		AudioExpected: true, DeclaredRate: 48000, NegotiatedRate: 44100,
		RateMismatch: true, MainCallbacks: 10, CueCallbacks: 10,
	}))
	if !strings.Contains(out, "48000") || !strings.Contains(out, "44100") {
		t.Errorf("the overlay does not show declared vs granted:\n%s", out)
	}
	if !strings.Contains(out, "right speed") {
		t.Errorf("a rate mismatch raises no warning:\n%s", out)
	}
}

// TestNoRateWarningWithoutAMismatch keeps a matching rate quiet.
func TestNoRateWarningWithoutAMismatch(t *testing.T) {
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
		AudioExpected: true, DeclaredRate: 44100, NegotiatedRate: 44100,
		MainCallbacks: 10, CueCallbacks: 10,
	}))
	if strings.Contains(out, "right speed") {
		t.Errorf("a matching rate warns anyway:\n%s", out)
	}
}

// TestHeldModifiersAreShown covers spec §5.3: "held on the engine ... and the TUI
// displays it".
//
// midi.Handler.ActiveMods existed, was documented as being "for display", and had
// zero callers. So a shift-modified hotcue delete was indistinguishable from an
// unmodified one that did the wrong thing -- the mapping was resolving correctly
// and the user had no way to see why.
func TestHeldModifiersAreShown(t *testing.T) {
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
		MIDIMods: map[string]bool{"shift": true, "ctrl": false, "alt": true},
	}))
	if !strings.Contains(out, "mods held") {
		t.Errorf("held modifiers are not shown:\n%s", out)
	}
	for _, want := range []string{"shift", "alt"} {
		if !strings.Contains(out, want) {
			t.Errorf("held modifier %q is not listed:\n%s", want, out)
		}
	}
	// A latch that is NOT held must not appear.
	if strings.Contains(out, "ctrl") {
		t.Errorf("an unheld modifier is listed as held:\n%s", out)
	}
}

// TestNoModRowWhenNothingIsHeld: the row is shown only when something is held, so
// a reader does not learn to skip past the place this information lives.
func TestNoModRowWhenNothingIsHeld(t *testing.T) {
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
		MIDIMods: map[string]bool{"shift": false},
	}))
	if strings.Contains(out, "mods held") {
		t.Errorf("nothing is held but the row is shown:\n%s", out)
	}
}
