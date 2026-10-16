package tui

import (
	"os"
	"slices"
	"testing"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/midi"
)

// Every control in the shipped Xone K2 mapping must move something on screen.
//
// The question is the same one the keyboard sweep asks, asked of a different
// input path: a physical knob that changes the audio but shows nothing is worse
// than a dead key, because the user is holding hardware in their hand.
//
// The probes name the action they expect to reach rather than the channel and
// CC number that produce it, and the message is built from the parsed mapping.
// A previous version hardcoded "cc ch1 n9" and friends; when the K2 fixture
// was reconciled against real hardware and its numbers changed, this test
// failed on thirteen unmapped messages rather than on anything to do with what
// it exists to check. Looking the control up by action keeps the assertion
// about the behaviour and not about the wiring.
func TestShippedMIDIMappingMovesTheScreen(t *testing.T) {
	raw, err := os.ReadFile("../testdata/mappings/xone-k2.yaml")
	if err != nil {
		t.Skipf("no shipped mapping: %v", err)
	}
	table, err := midi.ParseMapping("xone-k2.yaml", raw)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := table.Compile()
	if err != nil {
		t.Fatal(err)
	}

	type probe struct {
		name   string
		action engine.ActionKind
		target int
		slot   int
		mods   []string
	}
	probes := []probe{
		{"pitch deck A", engine.DeckPitch, 0, 0, nil},
		{"pitch deck B", engine.DeckPitch, 1, 0, nil},
		{"EQ low deck A", engine.DeckEQLow, 0, 0, nil},
		{"EQ mid deck A", engine.DeckEQMid, 0, 0, nil},
		{"EQ high deck A", engine.DeckEQHigh, 0, 0, nil},
		{"EQ low deck B", engine.DeckEQLow, 1, 0, nil},
		{"EQ mid deck B", engine.DeckEQMid, 1, 0, nil},
		{"EQ high deck B", engine.DeckEQHigh, 1, 0, nil},
		{"filter deck A", engine.DeckFilterCutoff, 0, 0, nil},
		{"filter deck B", engine.DeckFilterCutoff, 1, 0, nil},
		{"crossfader", engine.MixerCrossfader, 0, 0, nil},
		{"hotcue jump 1 deck A", engine.DeckHotcueJump, 0, 1, nil},
		{"hotcue jump 2 deck A", engine.DeckHotcueJump, 0, 2, nil},
	}

	for _, p := range probes {
		ctl, ok := findControl(table, p.action, p.target, p.slot, p.mods)
		if !ok {
			t.Errorf("%s: the shipped mapping binds no control for this action", p.name)
			continue
		}

		m, eng, _ := overlayRig(t)
		// Give deck A hotcues so the pads have something to hit.
		eng.Deck(0).SetHotcue(1)
		eng.Deck(0).SetHotcue(2)
		for i := 0; i < 4; i++ {
			_ = eng.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16))
		}
		eng.Deck(0).Play()
		for i := 0; i < 40; i++ {
			_ = eng.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16))
		}

		h := midi.NewHandler(compiled, m.Engine().Apply)
		before := m.RenderAt(120, 40)
		if err := h.Handle(pressMessage(ctl)); err != nil {
			t.Errorf("%s: handler rejected %s: %v", p.name, pressMessage(ctl), err)
			continue
		}
		for i := 0; i < 4; i++ {
			_ = eng.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16))
		}
		if m.RenderAt(120, 40) == before {
			t.Errorf("%s: the message was handled but nothing on screen changed", p.name)
		}
	}
}

// findControl locates a binding by what it does rather than by the numbers
// that reach it.
func findControl(m *midi.DeviceMapping, action engine.ActionKind, target, slot int, mods []string) (midi.Control, bool) {
	for _, c := range m.Controls {
		if c.Action != action || c.Target != target || c.Slot != slot {
			continue
		}
		if !slices.Equal(c.Mods, mods) {
			continue
		}
		return c, true
	}
	return midi.Control{}, false
}

// pressMessage builds the message a surface sends when the control is moved
// well off its rest position. A note carries its intensity in Velocity, not
// Value, so a note built with only Value set reads as a release.
//
// The value is 100 rather than 127 on purpose. Some controls already sit at
// their extreme when a deck starts -- dsp.NewFilter opens the band at Nyquist,
// so CC 127 asks the filter for the cutoff it is already at and the screen
// correctly does not move. Driving every control to its maximum therefore
// tests less than it looks like it tests.
func pressMessage(c midi.Control) midi.Message {
	if c.Kinds == midi.KindNote {
		return midi.Message{Kind: midi.KindNote, Channel: c.Chan, Num: c.Num, Velocity: 100}
	}
	return midi.Message{Kind: midi.KindCC, Channel: c.Chan, Num: c.Num, Value: 100}
}
