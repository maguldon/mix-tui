package midi

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

// valueForMapping drives one message through a compiled control and returns the
// value the handler emitted.
func valueForMapping(t *testing.T, mode string, kind MsgKind, intensity uint8) float64 {
	t.Helper()
	tab := tableFrom(t, `
device: "T"
controls:
  - midi: {kind: note, chan: 1, num: 60}
    action: {kind: LoopBeat, target: 0}
    mode: `+mode+`
  - midi: {kind: cc, chan: 1, num: 20}
    action: {kind: LoopBeat, target: 0}
    mode: `+mode+`
`)
	var last engine.Action
	h := NewHandler(tab, func(a engine.Action) { last = a })

	// A note must arrive as fromWireNote builds it: velocity set, Value left
	// zero. Setting Value too would hide the very bug this test exists for.
	msg := Message{Kind: kind, Channel: 1, Num: 60}
	if kind == KindNote {
		msg.Velocity = intensity
	} else {
		msg.Num = 20
		msg.Value = intensity
	}
	if err := h.Handle(msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return last.Value
}

// TestNoteAbsoluteUsesVelocity is the regression test for issue #10.
//
// A note carries its intensity in Velocity and leaves Value zero. Absolute mode
// read Value, so every note produced Absolute(0) == -1.0 and pinned its control
// to full negative on each press -- a mapping that compiled cleanly and did the
// exact opposite of what the user wrote.
func TestNoteAbsoluteUsesVelocity(t *testing.T) {
	cases := []struct {
		velocity uint8
		want     float64
	}{
		{127, 1.0},
		{64, 64.0 / 127.0},
		{1, 1.0 / 127.0},
	}
	for _, tc := range cases {
		got := valueForMapping(t, "absolute", KindNote, tc.velocity)
		if got < tc.want-0.01 || got > tc.want+0.01 {
			t.Errorf("note velocity %d produced %v, want %v", tc.velocity, got, tc.want)
		}
	}
}

// TestNoteAbsoluteIsNeverNegative pins the symptom rather than the cause: the
// old code produced exactly -1.0 for every note.
func TestNoteAbsoluteIsNeverNegative(t *testing.T) {
	for vel := uint8(1); vel <= 127; vel += 7 {
		if v := valueForMapping(t, "absolute", KindNote, vel); v < 0 {
			t.Errorf("note velocity %d produced %v; notes are unipolar", vel, v)
		}
	}
}

// TestCCAbsoluteStaysBipolar keeps the CC path unchanged. A CC knob is centred
// at 64, so it must still reach -1.0 at the bottom and +1.0 at the top.
func TestCCAbsoluteStaysBipolar(t *testing.T) {
	if v := valueForMapping(t, "absolute", KindCC, 0); v > -0.99 {
		t.Errorf("CC 0 produced %v, want -1.0", v)
	}
	if v := valueForMapping(t, "absolute", KindCC, 127); v < 0.99 {
		t.Errorf("CC 127 produced %v, want +1.0", v)
	}
	if v := valueForMapping(t, "absolute", KindCC, 64); v > 0.05 || v < -0.05 {
		t.Errorf("CC 64 produced %v, want ~0 (centre)", v)
	}
}

// TestNoteMomentaryStillToggles guards the mode that already worked.
func TestNoteMomentaryStillToggles(t *testing.T) {
	tab := tableFrom(t, `
device: "T"
controls:
  - midi: {kind: note, chan: 1, num: 60}
    action: {kind: LoopBeat, target: 0}
    mode: momentary
`)
	var last engine.Action
	h := NewHandler(tab, func(a engine.Action) { last = a })

	_ = h.Handle(Message{Kind: KindNote, Channel: 1, Num: 60, Velocity: 100})
	if last.Value != 1 {
		t.Errorf("note-on momentary produced %v, want 1", last.Value)
	}
	_ = h.Handle(Message{Kind: KindNote, Channel: 1, Num: 60, Velocity: 0})
	if last.Value != 0 {
		t.Errorf("note-off momentary produced %v, want 0", last.Value)
	}
}

// TestControlValue covers the accessor directly.
func TestControlValue(t *testing.T) {
	if got := (Message{Kind: KindNote, Velocity: 99}).ControlValue(); got != 99 {
		t.Errorf("note ControlValue = %d, want 99", got)
	}
	if got := (Message{Kind: KindCC, Value: 42}).ControlValue(); got != 42 {
		t.Errorf("CC ControlValue = %d, want 42", got)
	}
}
