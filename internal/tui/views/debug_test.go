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

// TestTheOverlayDoesNotEchoTheDeclaredRate is the other half of the pair above,
// and the reason the field is allowed to be 0 at all.
//
// The stream seeded Stats.NegotiatedRate with the declared rate at construction,
// so the value could never be 0 and the guard that builds the "--" placeholder
// was always true. The row read "sample rate 44100 (device 44100)", with both
// halves the same stored integer: the number we asked for, echoed back as
// though the device had confirmed it. For the window before the grant that is a
// confirmation that never happened, on the one row whose whole job is to say
// whether the device is doing what was asked.
func TestTheOverlayDoesNotEchoTheDeclaredRate(t *testing.T) {
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
		AudioExpected: true, DeclaredRate: 44100, NegotiatedRate: 0,
	}))
	if !strings.Contains(out, "44100 (device --)") {
		t.Errorf("the row claims a granted rate the daemon never stated:\n%s", out)
	}
	// An unknown rate is not a disagreement.
	if strings.Contains(out, "right speed") {
		t.Errorf("an unknown granted rate raises a mismatch warning:\n%s", out)
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

// TestDebugShowsShortCallbacks is the regression test for #21. DebugStats.Shorts
// was populated by the audio session and read by nothing, so a ring that came up
// short on every callback was invisible in the app and visible only in
// `mixplay -stats`.
func TestDebugShowsShortCallbacks(t *testing.T) {
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
		MainCallbacks: 100, CueCallbacks: 100, Shorts: 40,
	}))
	if !strings.Contains(out, "short callbacks") {
		t.Errorf("the short-callback count is not shown:\n%s", out)
	}
	if !strings.Contains(out, "40 of 200") {
		t.Errorf("the short-callback count is not shown against its total:\n%s", out)
	}
}

// TestDebugDoesNotWarnAboutTheLearningCallback is the other half, and the reason
// the row is not simply a warning.
//
// The pump converges from below: it starts at the configured target, sees the
// device's buffer size on the first callback, and raises. Exactly one short
// callback is the price of learning it, and it is the steady state on any host
// whose driver batches. Keying the warning on "any short at all" fires forever,
// about something that is working.
func TestDebugDoesNotWarnAboutTheLearningCallback(t *testing.T) {
	// Measured on this machine, stable across runs.
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
		MainCallbacks: 53, CueCallbacks: 0, Shorts: 1,
	}))
	if strings.Contains(out, "!") {
		t.Errorf("one short callback out of 53 raised a warning:\n%s", out)
	}
	if !strings.Contains(out, "1 of 53") {
		t.Errorf("the single short callback is not shown either:\n%s", out)
	}
}

// TestDebugDoesNotWarnBeforeThereIsACallbackHistory guards the other false
// alarm: during the first second of playback one short out of two is 50%, which
// says nothing at all.
func TestDebugDoesNotWarnBeforeThereIsACallbackHistory(t *testing.T) {
	for _, total := range []uint64{0, 1, 2, 5, 19} {
		out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
			MainCallbacks: total, Shorts: 1,
		}))
		if strings.Contains(out, "!") {
			t.Errorf("%d callbacks with 1 short raised a warning:\n%s", total, out)
		}
	}
}

// TestDebugWarnsOnSustainedShorts is the fault itself: every callback short,
// which is what a ring that cannot hold the device's buffers looks like.
func TestDebugWarnsOnSustainedShorts(t *testing.T) {
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{
		MainCallbacks: 48, Shorts: 48, MaxBufferFrames: 12200,
	}))
	if !strings.Contains(out, "48 of 48") {
		t.Errorf("sustained shorts not shown:\n%s", out)
	}
	if !strings.Contains(out, "audio.ring_frames") {
		t.Errorf("no advice given for sustained shorts:\n%s", out)
	}
	// The advice must name the number that decides it. The pump floors its
	// target at the device's buffer size, so that is the value the setting has
	// to be above, and nothing else on this screen carries it.
	if !strings.Contains(out, "12200") {
		t.Errorf("advice does not name the device buffer size:\n%s", out)
	}
}

// TestShortWarningNeedsNoDeviceBuffer checks the fallback wording when the
// device has not reported a buffer size yet. It must still say something
// actionable rather than going quiet.
func TestShortWarningNeedsNoDeviceBuffer(t *testing.T) {
	msg, ok := shortWarning(DebugStats{MainCallbacks: 50, Shorts: 50})
	if !ok {
		t.Fatal("sustained shorts with no known buffer size produced no warning")
	}
	if !strings.Contains(msg, "audio.ring_frames") {
		t.Errorf("fallback message is not actionable: %q", msg)
	}
	if strings.Contains(msg, "must exceed") {
		t.Errorf("fallback message invents a threshold it does not have: %q", msg)
	}
}

// TestShortsWithNoAudioReadsAsNotMeasured: "0 of 0" would be a ratio with no
// denominator, and an empty overlay row reads as broken rather than as "no audio
// has happened yet".
func TestShortsWithNoAudioReadsAsNotMeasured(t *testing.T) {
	out := stripANSI(RenderDebug(120, 40, emptySnap(), DebugStats{}))
	if !strings.Contains(out, "short callbacks") {
		t.Errorf("the row is missing entirely when there is no audio:\n%s", out)
	}
	if !strings.Contains(out, "--") {
		t.Errorf("a session with no callbacks should read as not measured:\n%s", out)
	}
	if strings.Contains(out, "of 0") {
		t.Errorf("divided by a zero callback count:\n%s", out)
	}
}

// TestShortWarningFitsTheOverlay: pad() does not wrap, so a warning longer than
// the overlay is silently cut off -- and the tail of this one is where the
// advice is. The debug overlay is most needed at 80 columns, which is also the
// smallest layout it is ever shown in.
func TestShortWarningFitsTheOverlay(t *testing.T) {
	msg, ok := shortWarning(DebugStats{
		MainCallbacks: 48, Shorts: 48, MaxBufferFrames: 12200,
	})
	if !ok {
		t.Fatal("sustained shorts produced no warning")
	}
	for _, w := range []int{100, 80, 70} {
		// 70 is the narrowest the app renders at (layout B; A is 100, C is 80,
		// D is 90), so that is the real floor rather than an arbitrary one.
		// +2 for the leading indent the warning is padded with.
		if len(msg)+2 > w {
			t.Errorf("warning is %d chars and is cut off at w=%d: %q", len(msg)+2, w, msg)
		}
	}

	// And it must survive an actual render at the narrowest width, not just
	// the arithmetic above.
	out := stripANSI(RenderDebug(80, 30, emptySnap(), DebugStats{
		MainCallbacks: 48, Shorts: 48, MaxBufferFrames: 12200, AudioExpected: true,
	}))
	if !strings.Contains(out, "audio.ring_frames") {
		t.Errorf("advice was truncated in a real render at 80 columns:\n%s", out)
	}
	if !strings.Contains(out, "12200") {
		t.Errorf("the threshold was truncated in a real render at 80 columns:\n%s", out)
	}
}

// TestDebugShowsDeviceLatency covers the row #26 needed. The overlay used to
// show callbacks and short-callback counts with nothing that said how long the
// audio actually took to reach the device, so the one number a user comparing
// two setups wants could not be read off the screen.
func TestDebugShowsDeviceLatency(t *testing.T) {
	out := RenderDebug(120, 40, emptySnap(), DebugStats{
		DeviceDelayFrames: 2048,
		NegotiatedRate:    48000,
	})
	if !strings.Contains(out, "device latency") {
		t.Errorf("device latency row missing:\n%s", out)
	}
	// 2048 frames at 48000Hz is 42.7ms. Anything but a millisecond figure
	// leaves the reader dividing by hand, which is the thing this row exists to
	// stop them doing.
	if !strings.Contains(out, "42.7ms") {
		t.Errorf("latency not rendered in milliseconds:\n%s", out)
	}
}

// TestDeviceLatencyRendersDashWhenUnmeasured pins the absent-measurement
// rendering. Zero means either "no latency" or "nothing measured yet", and
// printing "0.0ms" for the second claims an instant device that was never
// observed.
func TestDeviceLatencyRendersDashWhenUnmeasured(t *testing.T) {
	cases := []struct {
		name string
		s    DebugStats
	}{
		{"no delay reported", DebugStats{NegotiatedRate: 48000}},
		{"no rate yet", DebugStats{DeviceDelayFrames: 2048}},
		{"nothing at all", DebugStats{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatDeviceLatency(tc.s); got != "--" {
				t.Errorf("formatDeviceLatency() = %q, want %q", got, "--")
			}
		})
	}
}
