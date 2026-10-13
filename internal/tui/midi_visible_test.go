package tui

import (
	"os"
	"testing"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/midi"
)

// Every control in the shipped Xone K2 mapping must move something on screen.
//
// The question is the same one the keyboard sweep asks, asked of a different
// input path: a physical knob that changes the audio but shows nothing is worse
// than a dead key, because the user is holding hardware in their hand.
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
		name string
		msg  midi.Message
	}
	probes := []probe{
		{"CC9 pitch A", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 9, Value: 100}},
		{"CC10 pitch B", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 10, Value: 100}},
		{"CC20 EQ low A", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 20, Value: 100}},
		{"CC21 EQ mid A", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 21, Value: 100}},
		{"CC22 EQ high A", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 22, Value: 100}},
		{"CC23 EQ low B", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 23, Value: 100}},
		{"CC24 EQ mid B", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 24, Value: 100}},
		{"CC25 EQ high B", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 25, Value: 100}},
		{"CC30 filter A", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 30, Value: 100}},
		{"CC31 filter B", midi.Message{Kind: midi.KindCC, Channel: 1, Num: 31, Value: 100}},
		{"CC ch4/9 crossfader", midi.Message{Kind: midi.KindCC, Channel: 4, Num: 9, Value: 100}},
		{"note40 hotcue jump 1", midi.Message{Kind: midi.KindNote, Channel: 1, Num: 40, Value: 127}},
		{"note41 hotcue jump 2", midi.Message{Kind: midi.KindNote, Channel: 1, Num: 41, Value: 127}},
	}

	for _, p := range probes {
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
		if err := h.Handle(p.msg); err != nil {
			t.Errorf("%s: handler rejected the message: %v", p.name, err)
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
