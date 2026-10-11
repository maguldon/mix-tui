package midi_test

import (
	"testing"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/midi"
)

func testGrid(bpm float64) *analyze.BeatGrid {
	return &analyze.BeatGrid{BPM: bpm, Confidence: 0.9}
}

// pump renders enough blocks for posted commands to be applied. Deck.Apply
// posts to the command queue rather than mutating deck state directly — that is
// the realtime-safety design from milestone 1 — so a control is only observable
// in the Snapshot after the next render block drains the queue. Asserting
// without rendering tests nothing.
func pump(t *testing.T, eng *engine.Engine, blocks int) {
	t.Helper()
	main := audio.NewRing(1 << 16)
	cue := audio.NewRing(1 << 16)
	for i := 0; i < blocks; i++ {
		if err := eng.RenderInto(main, cue); err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
	}
}

// TestSyntheticControllerDrivesEngine plays a "performance" with no hardware:
// a table of literal MIDI messages produces the same Actions a real controller
// would, and the engine ends in the expected state.
func TestSyntheticControllerDrivesEngine(t *testing.T) {
	eng := engine.New(engine.Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
		RingFrames: 8820, DeckCount: 2})
	track := engine.NewTrackFromPCM(dsp.Sine(440, 60))
	eng.Deck(0).Load(track)
	eng.Deck(1).Load(track)
	eng.Deck(0).AttachBeatGrid(testGrid(128))
	eng.Deck(1).AttachBeatGrid(testGrid(128))

	table, err := midi.ParseMapping("test", []byte(performanceMapping))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := table.Compile()
	if err != nil {
		t.Fatal(err)
	}

	h := midi.NewHandler(compiled, eng.Apply)
	step := func(m midi.Message) {
		if err := h.Handle(m); err != nil {
			t.Fatalf("handle %v: %v", m, err)
		}
	}

	// load both decks, play A
	step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 9, Value: 127})  // A play
	step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 10, Value: 127}) // B play
	// push A's pitch up
	for i := 0; i < 20; i++ {
		step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 20, Value: midi.RelativeNull + 2})
	}
	// 4-beat loop on A
	step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 30, Value: 90})
	// shift+hotcue1 sets nothing but deletes
	step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 49, Value: 127}) // shift down
	step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 40, Value: 127}) // shift+hc1
	step(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 49, Value: 0})   // shift up

	pump(t, eng, 4)
	s := eng.Snapshot()
	if !s.Decks[0].Playing {
		t.Error("A should be playing")
	}
	if !s.Decks[1].Playing {
		t.Error("B should be playing")
	}
	if s.Decks[0].Pitch <= 0 {
		t.Errorf("A pitch = %v, want > 0", s.Decks[0].Pitch)
	}
	// 20 detents accumulate to a real nudge but must not slam pitch to the
	// rail — that is the jog-wheel bug the relative encoder exists to prevent.
	if s.Decks[0].Pitch > 0.8 {
		t.Errorf("A pitch = %v, want a moderate nudge, not near the rail", s.Decks[0].Pitch)
	}
	if !s.Decks[0].LoopActive {
		t.Error("A loop should be active")
	}
}

func TestIgnoredMessagesDoNotStopTheHandler(t *testing.T) {
	// A stream of sysex between real notes must not break the mapping.
	var got int
	table, _ := midi.ParseMapping("t", []byte(performanceMapping))
	compiled, _ := table.Compile()
	h := midi.NewHandler(compiled, func(a engine.Action) { got++ })
	for i := 0; i < 100; i++ {
		h.Handle(midi.Message{Kind: midi.KindOther, Other: midi.OtherSysex})
	}
	h.Handle(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 9, Value: 127})
	if got == 0 {
		t.Error("handler stopped working after sysex")
	}
	c := h.Counts()
	if c["sysex"] != 100 {
		t.Errorf("sysex counted %d, want 100", c["sysex"])
	}
}

// TestShiftChordOverridesBaseInLiveEngine proves modifier resolution works
// through the whole stack into real engine state, not just in isolation: the
// same CC must set a hotcue bare and delete it under shift.
func TestShiftChordOverridesBaseInLiveEngine(t *testing.T) {
	eng := engine.New(engine.Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	eng.Deck(0).Load(engine.NewTrackFromPCM(dsp.Sine(440, 30)))
	eng.Deck(0).AttachBeatGrid(testGrid(128))

	table, err := midi.ParseMapping("t", []byte(performanceMapping))
	if err != nil {
		t.Fatal(err)
	}
	compiled, _ := table.Compile()
	h := midi.NewHandler(compiled, eng.Apply)

	// Bare press sets hotcue 1.
	h.Handle(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 40, Value: 127})
	pump(t, eng, 2)
	if _, ok := eng.Deck(0).Hotcue(1); !ok {
		t.Fatal("bare press did not set hotcue 1")
	}

	// Shift+press deletes it.
	h.Handle(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 49, Value: 127})
	h.Handle(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 40, Value: 127})
	pump(t, eng, 2)
	if _, ok := eng.Deck(0).Hotcue(1); ok {
		t.Error("shift+press did not delete hotcue 1")
	}
}

// TestKeysAndMIDIConvergeOnTheSameAction checks spec 5.1: three producers, one
// consumer. A keybinding and a controller button resolving to the same action
// must leave the engine in identical state.
func TestKeysAndMIDIConvergeOnTheSameAction(t *testing.T) {
	eng := engine.New(engine.Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	eng.Deck(0).Load(engine.NewTrackFromPCM(dsp.Sine(440, 30)))

	table, _ := midi.ParseMapping("t", []byte(performanceMapping))
	compiled, _ := table.Compile()
	h := midi.NewHandler(compiled, eng.Apply)
	h.Handle(midi.Message{Kind: midi.KindCC, Channel: 1, Num: 9, Value: 127}) // play via CC
	pump(t, eng, 2)

	eng2 := engine.New(engine.Config{SampleRate: dsp.SampleRate, DeckCount: 2})
	eng2.Deck(0).Load(engine.NewTrackFromPCM(dsp.Sine(440, 30)))
	eng2.Apply(engine.Action{Kind: engine.DeckPlay, Target: 0, Value: 1}) // same action by hand
	pump(t, eng2, 2)

	a := eng.Snapshot().Decks[0].Playing
	b := eng2.Snapshot().Decks[0].Playing
	if a != b {
		t.Errorf("MIDI path playing=%v but direct action playing=%v", a, b)
	}
	if !a {
		t.Error("neither path started playback")
	}
}

const performanceMapping = `
device: "Synthetic"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPlay, target: 0}
  - midi: {kind: cc, chan: 1, num: 10}
    action: {kind: DeckPlay, target: 1}
  - midi: {kind: cc, chan: 1, num: 20}
    mode: relative
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: cc, chan: 1, num: 30}
    mode: absolute
    action: {kind: LoopBeat, target: 0}
  - midi: {kind: cc, chan: 1, num: 40}
    action: {kind: DeckHotcueSet, target: 0}
    slot: 1
  - midi: {kind: cc, chan: 1, num: 40}
    mods: [shift]
    action: {kind: DeckHotcueDelete, target: 0}
    slot: 1
modifiers:
  shift:
    kind: cc
    chan: 1
    num: 49
    pressed: 127
    released: 0
    mode: momentary
`
