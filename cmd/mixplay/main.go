// Command mixplay plays a two-deck mix to a real PipeWire device.
//
// It is milestone 2's deliverable and the audible counterpart to mixrender: the
// same engine, the same DSP, the same mixer, the same loops and sync, with the
// rings and drains in place. Everything a TUI would eventually drive is here as
// a flag, which is what lets milestone 4 substitute a terminal for flags and
// change nothing else.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/pipewire"
)

// flags is the parsed command line.
//
// Parsing is separated from execution so every rule about what is a valid
// invocation can be tested without a device, a daemon, or a sound card.
type flags struct {
	a, b         string
	play         []int
	seconds      float64
	xf           float64
	loopA, loopB float64
	roll         float64
	sync         bool
	quantize     bool
	rate         int
	bpmA, bpmB   float64
	mainTarget   string
	cueTarget    string
	stats        bool
	dump         string
	ringFrames   int
}

// configFor is the seam the tests use: parse an argv and hand back the flags,
// ignoring parse errors so validation can be tested independently.
func configFor(args []string) flags {
	fs, _ := parseFlags(args)
	return fs
}

// parseFlags builds a FlagSet over args and parses it.
func parseFlags(args []string) (flags, error) {
	var fs flags
	set := flag.NewFlagSet("mixplay", flag.ContinueOnError)

	set.StringVar(&fs.a, "a", "", "track for deck 0")
	set.StringVar(&fs.b, "b", "", "track for deck 1")
	set.Var((*intSlice)(&fs.play), "play", "deck index to play (repeatable)")
	set.Float64Var(&fs.seconds, "seconds", 0, "duration to play")
	set.Float64Var(&fs.xf, "xf", 0, "crossfader position, -1..1")
	set.Float64Var(&fs.loopA, "loop-a", 0, "beat loop on deck 0")
	set.Float64Var(&fs.loopB, "loop-b", 0, "beat loop on deck 1")
	set.Float64Var(&fs.roll, "roll", 0, "roll both decks by N beats")
	set.BoolVar(&fs.sync, "sync", false, "lock deck 1's tempo to deck 0")
	set.BoolVar(&fs.quantize, "quantize", false, "quantize both decks to a beat")
	set.IntVar(&fs.rate, "rate", 0, "sample rate (0 = default 44100)")
	set.Float64Var(&fs.bpmA, "bpm-a", 0, "deck 0 tempo, enabling beat features")
	set.Float64Var(&fs.bpmB, "bpm-b", 0, "deck 1 tempo")
	set.StringVar(&fs.mainTarget, "main-target", "", "route MAIN to this node")
	set.StringVar(&fs.cueTarget, "cue-target", "", "route CUE to this node")
	set.BoolVar(&fs.stats, "stats", false, "print final engine and device state")
	set.StringVar(&fs.dump, "dump", "", "also write the mix to this WAV")
	set.IntVar(&fs.ringFrames, "ring-frames", 0, "ring depth (0 = ~200ms)")

	return fs, set.Parse(args)
}

// intSlice collects a repeatable int flag.
type intSlice []int

func (s *intSlice) String() string { return fmt.Sprint([]int(*s)) }

func (s *intSlice) Set(v string) error {
	n := 0
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fmt.Errorf("not an integer: %q", v)
	}
	*s = append(*s, n)
	return nil
}

// deckCount is the MVP's deck count. Two is what the UI shows; the engine
// supports N and nothing here hardcodes it into the DSP.
const deckCount = 2

// validate rejects an invocation that cannot do what it was asked to do.
//
// Every rule here exists to turn a silent no-op into an explanation. A loop
// button that quietly does nothing because no beatgrid was supplied is worse
// than a command that refuses and says why.
func validate(f flags) error {
	if f.a == "" && f.b == "" {
		return errors.New("no tracks: pass -a and/or -b")
	}
	if f.seconds <= 0 {
		return fmt.Errorf("-seconds must be positive, got %v", f.seconds)
	}
	if f.xf < -1 || f.xf > 1 {
		return fmt.Errorf("-xf must be within [-1,1], got %v", f.xf)
	}
	for _, d := range f.play {
		if d < 0 || d >= deckCount {
			return fmt.Errorf("-play %d is out of range for %d decks", d, deckCount)
		}
	}
	// A deck with no track cannot be played, and saying so beats ignoring it.
	if f.a == "" && contains(f.play, 0) {
		return errors.New("-play 0 but no -a track given")
	}
	if f.b == "" && contains(f.play, 1) {
		return errors.New("-play 1 but no -b track given")
	}

	// Beat-dependent controls are unavailable without a grid, by design. Saying
	// so up front turns a mystery into a message.
	if f.loopA > 0 && f.bpmA <= 0 {
		return errors.New("-loop-a needs -bpm-a")
	}
	if f.loopB > 0 && f.bpmB <= 0 {
		return errors.New("-loop-b needs -bpm-b")
	}
	if f.roll > 0 && (f.bpmA <= 0 || f.bpmB <= 0) {
		return errors.New("-roll needs -bpm-a and -bpm-b")
	}
	if f.sync && (f.bpmA <= 0 || f.bpmB <= 0) {
		return errors.New("-sync needs -bpm-a and -bpm-b")
	}
	if f.quantize && (f.bpmA <= 0 || f.bpmB <= 0) {
		return errors.New("-quantize needs -bpm-a and -bpm-b")
	}
	if f.rate < 0 {
		return fmt.Errorf("-rate must be non-negative, got %v", f.rate)
	}
	if f.loopA < 0 || f.loopB < 0 || f.roll < 0 {
		return errors.New("loop and roll beat counts must be positive")
	}
	return nil
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mixplay:", err)
		os.Exit(1)
	}
}

func run() error {
	f, err := parseFlags(os.Args[1:])
	if err != nil {
		return err
	}
	if err := validate(f); err != nil {
		return err
	}

	// Ctrl-C tears down in order: cancel the pump, stop the streams, destroy
	// them, then close the context. A signal handler that skipped straight to
	// Close would leave a drain callback able to fire into a freed stream.
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runPlayback(ctx, f)
}

// runPlayback wires engine, output and pump together and runs for -seconds.
func runPlayback(ctx context.Context, f flags) error {
	rate := f.rate
	if rate <= 0 {
		rate = 44100
	}

	eng := engine.New(engine.Config{
		SampleRate:  rate,
		BlockFrames: engine.BlockFrames,
		RingFrames:  f.ringFrames,
		DeckCount:   deckCount,
		CuedIndex:   1,
	})

	// Decode first: a bad file must fail before any device is claimed.
	paths := map[int]string{0: f.a, 1: f.b}
	for deck, path := range paths {
		if path == "" {
			continue
		}
		track, err := audio.Decode(path)
		if err != nil {
			return fmt.Errorf("deck %d: %w", deck, err)
		}
		// The grid is built from track.Duration and installed with LoadNow, not
		// from eng.Deck(deck).Duration() after an async Load. Load queues a
		// command that only the render goroutine drains, and the pump has not
		// started yet: Duration() read 0, so the grid carried a BPM and no
		// beats. applyScript below then armed nothing at all, and discarded the
		// errors, so -loop-a and -sync silently did nothing.
		var grid *analyze.BeatGrid
		if bpm := deckBPM(deck, f); bpm > 0 {
			grid = syntheticGrid(bpm, track.Duration)
		}
		eng.Deck(deck).LoadNow(track, grid, nil)
	}

	out, err := pipewire.NewOutput(pipewire.OutputConfig{
		SampleRate: rate,
		RingFrames: f.ringFrames,
		MainTarget: f.mainTarget,
		CueTarget:  f.cueTarget,
	})
	if err != nil {
		return err
	}
	defer out.Close()

	if err := out.Start(); err != nil {
		return err
	}

	// -dump is the verification bridge between the device path and the offline
	// path: it records the exact MAIN mix the device received, so if it diverges
	// from a mixrender of the same script the bug is in the device path, which
	// the offline render cannot have.
	if f.dump != "" {
		rec := &engineRecorder{w: audio.NewWAVWriter(rate)}
		eng.SetRecorder(rec)
		defer func() {
			eng.SetRecorder(nil)
			if err := rec.w.Close(f.dump); err != nil {
				fmt.Fprintf(os.Stderr, "mixplay: dump: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "mixplay: wrote %s\n", f.dump)
			}
		}()
	}

	pump := pipewire.NewPumper(out, eng, pipewire.PumpConfig{})
	pumpCtx, cancelPump := context.WithCancel(ctx)
	defer cancelPump()
	// Apply the scripted controls BEFORE starting the pump. Several of these
	// (SetLoopBeat, PushRoll, QuantizeToNearestBeat, EngageSync) mutate deck
	// state directly on this goroutine rather than posting through the command
	// queue, so running them while the pump is rendering the same decks is a
	// data race.
	applyScript(eng, f)

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		_ = pump.Run(pumpCtx)
	}()

	fmt.Fprintf(os.Stderr, "mixplay: %.1fs at %d Hz\n", f.seconds, out.SampleRate())
	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(f.seconds * float64(time.Second))):
	}

	cancelPump()
	<-pumpDone
	_ = out.Stop()

	if f.stats {
		printStats(eng, out, pump)
	}
	return nil
}

func deckBPM(deck int, f flags) float64 {
	if deck == 0 {
		return f.bpmA
	}
	return f.bpmB
}

// syntheticGrid builds a uniform beatgrid. mixplay trusts -bpm-a/-bpm-b rather
// than detecting: analysis takes seconds per track, and a performance tool
// must be ready before the first beat.
func syntheticGrid(bpm float64, dur time.Duration) *analyze.BeatGrid {
	g := &analyze.BeatGrid{BPM: bpm, Confidence: 0.9}
	if bpm <= 0 || dur <= 0 {
		return g
	}
	per := time.Duration(float64(time.Second) * 60 / bpm)
	for at := time.Duration(0); at < dur; at += per {
		g.Beats = append(g.Beats, at)
	}
	return g
}

// applyScript applies the beat-dependent controls, in the order that keeps the
// grid intact: loops are armed before sync, so engaging sync cannot move a
// loop the user just placed (spec §5.6).
func applyScript(eng *engine.Engine, f flags) {
	for deck, beats := range map[int]float64{0: f.loopA, 1: f.loopB} {
		if beats > 0 {
			_ = eng.Deck(deck).SetLoopBeat(beats)
		}
	}
	if f.sync {
		_ = eng.SyncPair(1, 0)
	}
	if f.quantize {
		for d := 0; d < deckCount; d++ {
			_ = eng.Deck(d).QuantizeToNearestBeat()
		}
	}
	if f.roll > 0 {
		for d := 0; d < deckCount; d++ {
			_ = eng.Deck(d).PushRoll(f.roll)
		}
	}
	eng.Apply(engine.Action{Kind: engine.MixerCrossfader, Value: f.xf})

	playing := f.play
	if len(playing) == 0 {
		for d, p := range map[int]string{0: f.a, 1: f.b} {
			if p != "" {
				playing = append(playing, d)
			}
		}
	}
	for _, d := range playing {
		eng.Apply(engine.Action{Kind: engine.DeckPlay, Target: d})
	}
}

func printStats(eng *engine.Engine, out *pipewire.Output, pump *pipewire.Pumper) {
	s := eng.Snapshot()
	main, cue := out.Stats()
	ps := pump.Stats()

	fmt.Println("engine:")
	fmt.Printf("  master      rms=%.4f peak=%.4f\n", s.MasterLevel, s.MasterPeak)
	fmt.Printf("  cue         rms=%.4f peak=%.4f\n", s.CueLevel, s.CuePeak)
	fmt.Printf("  overruns    %d\n", s.Overruns)
	for _, d := range s.Decks {
		fmt.Printf("  deck %d  loaded=%v playing=%v pos=%v pitch=%+.2f bpm=%.1f grid=%v level=%.4f\n",
			d.Index, d.Loaded, d.Playing, d.Position.Truncate(time.Millisecond),
			d.Pitch, d.BPM, d.HasBeatGrid, d.Level)
	}
	fmt.Println("device:")
	fmt.Printf("  MAIN        callbacks=%d frames=%d starves=%d shorts=%d rate=%d\n",
		main.Callbacks, main.FramesDrained, main.Starves, main.Shorts, main.NegotiatedRate)
	fmt.Printf("  CUE         callbacks=%d frames=%d starves=%d shorts=%d\n",
		cue.Callbacks, cue.FramesDrained, cue.Starves, cue.Shorts)
	if main.RateMismatch {
		fmt.Printf("  RATE        MISMATCH: device granted %dHz, engine renders %dHz — "+
			"the mix plays at the wrong speed\n", main.NegotiatedRate, out.SampleRate())
	}
	fmt.Printf("  ring underruns %d\n", out.Underruns())
	fmt.Println("pump:")
	fmt.Printf("  blocks=%d wallMax=%v targetMisses=%d cancelClean=%v\n",
		ps.BlocksRendered, ps.RenderWallMax.Round(time.Microsecond),
		ps.TargetMisses, ps.CancelClean)
}

// engineRecorder adapts a WAVWriter to the engine's Recorder interface.
//
// RecordMain runs on the pump goroutine, never the realtime drain, so growing
// the output buffer here is safe. The block is consumed synchronously before
// the next mix overwrites the scratch buffer it points at.
type engineRecorder struct{ w *audio.WAVWriter }

func (r *engineRecorder) RecordMain(block dsp.Stereo) { r.w.WriteStereo(block) }
