package midi

import "testing"

// These run with no hardware. They assert the pure conversion between wire
// messages and our internal Message, which is the part that can be wrong.

func TestWireConversionForCC(t *testing.T) {
	m := fromWireCC(3, 20, 127)
	if m.Kind != KindCC {
		t.Errorf("kind = %v", m.Kind)
	}
	if m.Channel != 3 {
		t.Errorf("chan = %d", m.Channel)
	}
	if m.Num != 20 {
		t.Errorf("num = %d", m.Num)
	}
	if m.Value != 127 {
		t.Errorf("value = %d", m.Value)
	}
}

func TestWireConversionClampsChannel(t *testing.T) {
	if m := fromWireCC(200, 20, 64); m.Channel > 15 {
		t.Errorf("channel %d out of range", m.Channel)
	}
}

func TestWireConversionNoteOffIsZeroVelocity(t *testing.T) {
	m := fromWireNote(1, 40, 0)
	if m.Kind != KindNote {
		t.Error("kind")
	}
	if m.Velocity != 0 {
		t.Errorf("note-off velocity = %d", m.Velocity)
	}
}

func TestFromWireClassifiesClockAsOther(t *testing.T) {
	m := fromWireRaw(0xF8)
	if m.Kind != KindOther {
		t.Errorf("clock should be Other, got %v", m.Kind)
	}
	if m.Other != OtherClock {
		t.Errorf("category = %v", m.Other)
	}
}

func TestFromWireClassifiesSysex(t *testing.T) {
	if m := fromWireRaw(0xF0); m.Other != OtherSysex {
		t.Errorf("0xF0 should be sysex, got %v", m.Other)
	}
}

func TestOpenDevicesDoesNotPanicWithoutHardware(t *testing.T) {
	devs, err := OpenDevices()
	if err != nil && len(devs) != 0 {
		t.Errorf("returned %d devices and an error simultaneously", len(devs))
	}
}

// TestFromWireDispatchesChannelMessages checks the whole raw-byte path, not
// just the per-kind constructors: a real CC on the wire must land as a CC.
func TestFromWireDispatchesChannelMessages(t *testing.T) {
	cases := []struct {
		raw    []byte
		kind   MsgKind
		num    uint8
		reason string
	}{
		{[]byte{0xB0, 9, 127}, KindCC, 9, "control change"},
		{[]byte{0xB5, 20, 64}, KindCC, 20, "control change on channel 5"},
		{[]byte{0x90, 40, 100}, KindNote, 40, "note on"},
		{[]byte{0x80, 41, 0}, KindNote, 41, "note off"},
	}
	for _, c := range cases {
		m := fromWire(c.raw)
		if m.Kind != c.kind {
			t.Errorf("%s: kind = %v, want %v", c.reason, m.Kind, c.kind)
		}
		if m.Num != c.num {
			t.Errorf("%s: num = %d, want %d", c.reason, m.Num, c.num)
		}
	}
}

// TestFromWireNoteOnZeroVelocityIsNoteOff is a real-world trap: many
// controllers report note-off as note-on with velocity 0, and treating it as a
// press would make the control stick on.
func TestFromWireNoteOnZeroVelocityIsNoteOff(t *testing.T) {
	m := fromWire([]byte{0x90, 40, 0})
	if m.Kind != KindNote {
		t.Fatalf("kind = %v", m.Kind)
	}
	if m.Velocity != 0 {
		t.Errorf("velocity = %d, want 0 so the control releases", m.Velocity)
	}
}

// TestFromWireHandlesShortMessage ensures a truncated message cannot index out
// of bounds. A malformed byte from a flaky device must not crash the process
// mid-set.
func TestFromWireHandlesShortMessage(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, {0xB0}, {0xF8}} {
		m := fromWire(raw)
		if m.Kind == KindCC || m.Kind == KindNote {
			t.Errorf("short message %v classified as a channel message", raw)
		}
	}
}

// TestFromWireClassifiesOtherSystemBytes covers the remaining ignored
// categories so the debug overlay can attribute them (spec §7).
func TestFromWireClassifiesOtherSystemBytes(t *testing.T) {
	cases := map[byte]OtherCategory{
		0xF0: OtherSysex,
		0xF8: OtherClock,
		0xC0: OtherProgramChange,
		0xD0: OtherAftertouch,
		0xE0: OtherPitchBend,
	}
	for b, want := range cases {
		if got := fromWireRaw(b); got.Other != want {
			t.Errorf("0x%02X -> %v, want %v", b, got.Other, want)
		}
	}
}
