package main

import (
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/midi"
	"github.com/maguldon/mix-tui/internal/tui"
)

// seedMapping is a synthetic performance driven through the real input path.
//
// It exists so the whole chain -- Message to Action to engine state to rendered
// frame -- is exercisable in CI with no controller attached. The messages are
// literal so the resulting frame is deterministic, which is what makes the
// acceptance screenshot stable.
const seedMapping = `
device: "Seed"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPlay, target: 0}
  - midi: {kind: cc, chan: 1, num: 10}
    action: {kind: DeckPlay, target: 1}
  - midi: {kind: cc, chan: 1, num: 30}
    action: {kind: LoopBeat, target: 0}
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckHotcueSet, target: 0}
    slot: 1
`

// seedPerformance returns the fixed message sequence.
func seedPerformance() []midi.Message {
	return []midi.Message{
		{Kind: midi.KindCC, Channel: 1, Num: 9, Value: 127},  // play A
		{Kind: midi.KindCC, Channel: 1, Num: 10, Value: 127}, // play B
		{Kind: midi.KindCC, Channel: 1, Num: 30, Value: 90},  // 4-beat loop
		{Kind: midi.KindCC, Channel: 1, Num: 40, Value: 127}, // hotcue 1
	}
}

// seedMIDI drives a scripted performance through the real MIDI handler.
//
// It returns the handler so the model can show its ignored-message counts in
// the debug overlay exactly as it would with a real device.
func seedMIDI(m *tui.Model) *midi.Handler {
	table, err := midi.ParseMapping("seed", []byte(seedMapping))
	if err != nil {
		// The seed mapping is a constant; failing here means the constant is
		// broken, which is a programming error, not a user condition.
		panic("seed mapping is invalid: " + err.Error())
	}
	compiled, err := table.Compile()
	if err != nil {
		panic("seed mapping does not compile: " + err.Error())
	}
	h := midi.NewHandler(compiled, m.Engine().Apply)
	for _, msg := range seedPerformance() {
		// An unmapped or ignored message here would mean the seed table and the
		// message list disagree, which is a bug in this file.
		if err := h.Handle(msg); err != nil {
			panic("seed performance hit an unmapped message: " + err.Error())
		}
	}
	return h
}

// applyCrossfader sets the crossfader from the command line.
func applyCrossfader(m *tui.Model, pos float64) {
	if pos == 0 {
		return
	}
	m.Engine().Apply(engine.Action{Kind: engine.MixerCrossfader, Value: pos})
}
