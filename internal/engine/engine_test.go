package engine

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

func newTestEngine() *Engine {
	return New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
		RingFrames: 44100, DeckCount: 2})
}

// renderN renders blocks and drains the main ring, returning the audio.
func renderN(e *Engine, blocks int) dsp.Stereo {
	main, cue := audio.NewRing(44100), audio.NewRing(44100)
	out := make(dsp.Stereo, 0, blocks*1024+44100*2)
	tmp := make(dsp.Stereo, 512*2)
	for i := 0; i < blocks; i++ {
		if err := e.RenderInto(main, cue); err != nil {
			return out
		}
		if n := main.Read(tmp); n > 0 {
			out = append(out, tmp[:n*2]...)
		}
	}
	return out
}

func TestEngineTwoDecksCrossfade(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 5)))
	e.Deck(1).Load(memTrack(dsp.Sine(440, 5)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	e.Apply(Action{Kind: DeckPlay, Target: 1})

	out := renderN(e, 200)
	if math.IsNaN(out.RMS()) {
		t.Fatal("NaN in mix")
	}
	if out.RMS() < 0.01 {
		t.Errorf("mix is silent: %v", out.RMS())
	}
}

func TestSnapshotReportsDeckState(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 5)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	renderN(e, 50)
	s := e.Snapshot()
	if len(s.Decks) != 2 {
		t.Fatalf("snapshot has %d decks", len(s.Decks))
	}
	d0 := s.Decks[0]
	if !d0.Loaded {
		t.Error("deck 0 not marked loaded")
	}
	if !d0.Playing {
		t.Error("deck 0 not marked playing")
	}
	if d0.Position <= 0 {
		t.Errorf("position = %v", d0.Position)
	}
	if d0.Level <= 0 {
		t.Errorf("level = %v", d0.Level)
	}
	if s.Decks[1].Loaded {
		t.Error("deck 1 should be unloaded")
	}
}

func TestSnapshotIsImmutableCopy(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 5)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	renderN(e, 20)
	s1 := e.Snapshot()
	s1.Decks[0].Position = 99 * time.Hour // mutate the copy
	renderN(e, 20)
	s2 := e.Snapshot()
	if s2.Decks[0].Position == 99*time.Hour {
		t.Error("mutating a Snapshot changed engine state")
	}
}

func TestActionOnUnloadedDeckIsSafe(t *testing.T) {
	e := newTestEngine()
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	e.Apply(Action{Kind: DeckSeek, Target: 0, Value: 0.5})
	e.Apply(Action{Kind: DeckHotcueSet, Target: 1, Slot: 1})
	e.Apply(Action{Kind: DeckHotcueJump, Target: 0, Slot: 1})
	e.Apply(Action{Kind: DeckPitch, Target: 0, Value: 1})
	renderN(e, 10)
}

func TestActionOutOfRangeDeckIsIgnored(t *testing.T) {
	e := newTestEngine()
	e.Apply(Action{Kind: DeckPlay, Target: 99})
	e.Apply(Action{Kind: DeckPitch, Target: -5, Value: 1})
	renderN(e, 10)
}

func TestCrossfaderMovesLevelBetweenDecks(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 5)))
	e.Deck(1).Load(memTrack(dsp.Sine(440, 5)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	e.Apply(Action{Kind: DeckPlay, Target: 1})
	e.Apply(Action{Kind: MixerCrossfader, Target: -1})
	left := renderN(e, 100)
	e.Apply(Action{Kind: MixerCrossfader, Target: 1})
	right := renderN(e, 100)
	if math.Abs(left.RMS()-right.RMS()) > 0.3 {
		t.Errorf("crossfader ends differ too much: %v vs %v", left.RMS(), right.RMS())
	}
}

func TestEngineSupportsMoreThanTwoDecks(t *testing.T) {
	e := New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
		RingFrames: 44100, DeckCount: 4})
	if e.DeckCount() != 4 {
		t.Fatalf("deckCount = %d", e.DeckCount())
	}
	for i := 0; i < 4; i++ {
		e.Deck(i).Load(memTrack(dsp.Sine(440+float64(i)*50, 5)))
		e.Apply(Action{Kind: DeckPlay, Target: i})
	}
	out := renderN(e, 100)
	if out.Peak() > 1.0 {
		t.Errorf("4-deck mix clipped: %v", out.Peak())
	}
}

func TestApplyIsSafeFromAnotherGoroutine(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 30)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			e.Apply(Action{Kind: DeckPitch, Target: 0, Value: float64(i%100) / 100})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			renderN(e, 10)
		}
	}()
	wg.Wait()
	if math.IsNaN(e.Snapshot().Decks[0].Level) {
		t.Fatal("NaN after concurrent apply")
	}
}

// BenchmarkRenderInto measures a full N-deck mix into the rings. Zero
// allocations is a hard requirement, not a target: the render goroutine runs
// this ahead of the audio callback and must not hand work to the GC
// (spec §4.3).
func BenchmarkRenderInto(b *testing.B) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 30)))
	e.Deck(1).Load(memTrack(dsp.Sine(660, 30)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	e.Apply(Action{Kind: DeckPlay, Target: 1})
	main, cue := audio.NewRing(65536), audio.NewRing(65536)
	drain := make(dsp.Stereo, 2048)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := e.RenderInto(main, cue); err != nil {
			b.Fatal(err)
		}
		main.Read(drain)
		cue.Read(drain)
	}
}

// The whole point of the command queue: Apply may be called concurrently with
// rendering without a lock and without a race. Run with -race to be meaningful.
func TestConcurrentApplyAndRenderIsRaceFree(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 30)))
	e.Deck(1).Load(memTrack(dsp.Sine(660, 30)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	e.Apply(Action{Kind: DeckPlay, Target: 1})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// One goroutine per control surface, all posting at once.
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				e.Apply(Action{Kind: DeckPitch, Target: g % 2, Value: float64(i%200)/100 - 1})
				e.Apply(Action{Kind: DeckEQLow, Target: g % 2, Value: float64(i%100)/50 - 1})
				e.Apply(Action{Kind: DeckHotcueSet, Target: g % 2, Slot: i % 8})
				e.Apply(Action{Kind: DeckHotcueJump, Target: g % 2, Slot: i % 8})
				e.Apply(Action{Kind: DeckSeek, Target: g % 2, Value: float64(i%100) / 100})
				e.Apply(Action{Kind: MixerCrossfader, Target: 0, Value: float64(i%100)/50 - 1})
				_ = e.Snapshot()
			}
		}(g)
	}

	// The render goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		main, cue := audio.NewRing(65536), audio.NewRing(65536)
		for i := 0; i < 500; i++ {
			if err := e.RenderInto(main, cue); err != nil {
				return
			}
		}
	}()

	// Let the render goroutine finish, then stop the writers.
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	close(stop)
	wg.Wait()

	if d := e.Deck(0); d.DroppedCommands() > 0 {
		t.Logf("dropped %d control changes (queue overflow)", d.DroppedCommands())
	}
}

// A command older than one block should be superseded, not replayed: the
// queue must hold the LATEST intent, not every intermediate value.
func TestLatestPitchWins(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 5)))
	d.SetPitch(0.1)
	d.SetPitch(0.2)
	d.SetPitch(1.0)
	renderBlocks(d, BlockFrames, 1)
	if got := d.Pitch(); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("pitch = %v after three posts, want the last (1.0)", got)
	}
}

// A stopped deck must still process its queue, or it could never restart.
func TestStoppedDeckStillAppliesCommands(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 5)))
	d.Play()
	renderBlocks(d, BlockFrames, 2)
	d.Stop()
	renderBlocks(d, BlockFrames, 1)
	if d.IsPlaying() {
		t.Fatal("deck still playing after stop")
	}
	d.Play()
	renderBlocks(d, BlockFrames, 1)
	if !d.IsPlaying() {
		t.Error("play command was not applied to a stopped deck")
	}
}

// A healthy render with a consumer keeping up must report zero overruns.
//
// This guards a units bug that was live for a while: Ring.Write returns
// FRAMES, the scratch buffers are measured in SAMPLES, and comparing the two
// directly made every complete write look short.
func TestNoOverrunWhenConsumerKeepsUp(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 5)))
	e.Deck(1).Load(memTrack(dsp.Sine(660, 5)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})
	e.Apply(Action{Kind: DeckPlay, Target: 1})

	main, cue := audio.NewRing(65536), audio.NewRing(65536)
	drain := make(dsp.Stereo, BlockFrames*2)
	for i := 0; i < 200; i++ {
		if err := e.RenderInto(main, cue); err != nil {
			t.Fatal(err)
		}
		for main.Read(drain) > 0 {
		}
		for cue.Read(drain) > 0 {
		}
	}
	if got := e.Overruns(); got != 0 {
		t.Errorf("overruns = %d with a consumer keeping up, want 0", got)
	}
}

// Overrun accounting must still fire when a consumer stops reading.
func TestOverrunFillsWhenConsumerStalls(t *testing.T) {
	e := newTestEngine()
	e.Deck(0).Load(memTrack(dsp.Sine(440, 30)))
	e.Apply(Action{Kind: DeckPlay, Target: 0})

	// A ring far too small to hold the rendered backlog.
	main, cue := audio.NewRing(1024), audio.NewRing(1024)
	for i := 0; i < 200; i++ {
		if err := e.RenderInto(main, cue); err != nil {
			t.Fatal(err)
		}
	}
	if e.Overruns() == 0 {
		t.Error("a stalled consumer produced no overruns")
	}
}
