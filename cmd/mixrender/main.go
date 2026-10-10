// Command mixrender renders an engine state to a WAV file.
//
// This is the spec's §9 verification harness: the identical engine code that
// would play to a speaker writes to a file, so any DSP bug is reproducible
// offline and diffable.
//
//	mixrender -out mix.wav -a track1.mp3 -b track2.wav -play 0 -play 1 -stats
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

// intSlice collects a repeatable int flag.
type intSlice []int

func (s *intSlice) String() string {
	parts := make([]string, len(*s))
	for i, v := range *s {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ",")
}

func (s *intSlice) Set(v string) error {
	n, err := parseInt(v)
	if err != nil {
		return err
	}
	*s = append(*s, n)
	return nil
}

func parseInt(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil {
		return 0, fmt.Errorf("not an integer: %q", s)
	}
	return n, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mixrender:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		out      = flag.String("out", "", "output WAV path (required)")
		deckA    = flag.String("a", "", "track for deck 0")
		deckB    = flag.String("b", "", "track for deck 1")
		play     intSlice
		xf       = flag.Float64("xf", 0, "crossfader position, -1..1")
		seconds  = flag.Float64("seconds", 4, "duration to render")
		showStat = flag.Bool("stats", false, "print the final Snapshot")
		doSync   = flag.Bool("sync", false, "lock deck 1 to deck 0's tempo (analyses both)")
		bpmA     = flag.Float64("bpm-a", 0, "assume deck 0's track is this BPM")
		bpmB     = flag.Float64("bpm-b", 0, "assume deck 1's track is this BPM")
	)
	flag.Var(&play, "play", "deck index to play (repeatable)")
	flag.Parse()

	if *out == "" {
		flag.Usage()
		return errors.New("-out is required")
	}
	if *seconds <= 0 {
		return fmt.Errorf("-seconds must be positive, got %v", *seconds)
	}

	eng := engine.New(engine.Config{
		SampleRate:  dsp.SampleRate,
		BlockFrames: engine.BlockFrames,
		RingFrames:  dsp.SampleRate / 5,
		DeckCount:   2,
	})

	paths := map[int]string{0: *deckA, 1: *deckB}
	for deck, path := range paths {
		if path == "" {
			continue
		}
		track, err := audio.Decode(path)
		if err != nil {
			return fmt.Errorf("deck %d: %w", deck, err)
		}
		eng.Deck(deck).Load(track)
	}

	// Beatgrids are needed before sync or any loop can be armed. mixrender
	// trusts -bpm-a/-bpm-b rather than analysing: the tracks are fixtures, and
	// a full analysis would take seconds per run for no extra signal.
	if *bpmA > 0 {
		eng.Deck(0).AttachBeatGrid(syntheticGrid(*bpmA, eng.Deck(0).Duration()))
	}
	if *bpmB > 0 {
		eng.Deck(1).AttachBeatGrid(syntheticGrid(*bpmB, eng.Deck(1).Duration()))
	}
	if *doSync {
		if err := eng.SyncPair(1, 0); err != nil {
			return fmt.Errorf("-sync needs a beatgrid on both decks (use -bpm-a and -bpm-b): %w", err)
		}
	}

	eng.Apply(engine.Action{Kind: engine.MixerCrossfader, Value: *xf})
	if len(play) == 0 {
		// Default to playing every deck that has a track, which is what the
		// flags imply when -play is omitted.
		for deck, path := range paths {
			if path != "" {
				play = append(play, deck)
			}
		}
	}
	for _, deck := range play {
		if eng.Deck(deck) == nil {
			return fmt.Errorf("no such deck: %d", deck)
		}
		eng.Apply(engine.Action{Kind: engine.DeckPlay, Target: deck})
	}

	main, cue := audio.NewRing(65536), audio.NewRing(65536)
	w := &wavWriter{sampleRate: dsp.SampleRate}

	totalFrames := int(*seconds * float64(dsp.SampleRate))
	mainDrain := make(dsp.Stereo, engine.BlockFrames*2)
	cueDrain := make(dsp.Stereo, engine.BlockFrames*2)
	written := 0

	for written < totalFrames {
		if err := eng.RenderInto(main, cue); err != nil {
			return err
		}
		// Drain fully rather than one block per render. Rendering is ahead of
		// the file write, so a partial drain would let the ring fill and drop
		// audio the file should contain.
		//
		// The cue ring is drained too. A real output callback consumes it; a
		// renderer that ignores it would fill it and report overruns that no
		// device would ever produce.
		for {
			n := cue.Read(cueDrain)
			if n == 0 {
				break
			}
		}
		for {
			n := main.Read(mainDrain)
			if n == 0 {
				break
			}
			chunk := mainDrain[:n*2]
			if remain := totalFrames - written; n > remain {
				chunk = chunk[:remain*2]
				n = remain
			}
			if err := w.writeFrames(chunk); err != nil {
				return err
			}
			written += n
			if written >= totalFrames {
				break
			}
		}
	}

	if err := w.close(*out); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%d frames, %.1fs)\n", *out, written, float64(written)/float64(dsp.SampleRate))

	if *showStat {
		printSnapshot(eng.Snapshot())
	}
	return nil
}

func printSnapshot(s engine.Snapshot) {
	fmt.Println("Snapshot:")
	fmt.Printf("  crossfader  %+.2f\n", s.Crossfader)
	fmt.Printf("  master      rms=%.4f peak=%.4f\n", s.MasterLevel, s.MasterPeak)
	fmt.Printf("  cue         rms=%.4f peak=%.4f\n", s.CueLevel, s.CuePeak)
	fmt.Printf("  overruns    %d\n", s.Overruns)
	for _, d := range s.Decks {
		fmt.Printf("  deck %d  loaded=%v playing=%v pos=%v/%v pitch=%+.2f bpm=%.1f grid=%v level=%.4f\n",
			d.Index, d.Loaded, d.Playing, d.Position.Truncate(1e7), d.Duration.Truncate(1e7),
			d.Pitch, d.BPM, d.HasBeatGrid, d.Level)
		if d.Loaded {
			fmt.Printf("          %s - %s\n", d.Artist, d.Title)
		}
	}
}

// syntheticGrid builds a uniform beatgrid of the given tempo covering dur.
//
// The tracks rendered by this tool are test fixtures, so a grid is supplied
// rather than detected: analysis takes seconds per run and would add nothing
// the fixture's tempo does not already state exactly.
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

// wavWriter streams 16-bit PCM stereo WAV.
//
// The header carries a placeholder size and is rewritten on close, because
// the total length is only known after the last block is rendered.
type wavWriter struct {
	sampleRate int
	buf        []byte
}

// bytesPerFrame is two channels of 16-bit samples.
const bytesPerFrame = 4

func (w *wavWriter) writeFrames(s dsp.Stereo) error {
	var pair [bytesPerFrame]byte
	for i := 0; i+1 < len(s); i += 2 {
		binary.LittleEndian.PutUint16(pair[0:2], uint16(pcm16(s[i])))
		binary.LittleEndian.PutUint16(pair[2:4], uint16(pcm16(s[i+1])))
		w.buf = append(w.buf, pair[:]...)
	}
	return nil
}

// pcm16 converts a normalized sample to int16 with clamping. Rounding (rather
// than truncating) avoids a half-LSB downward bias, which is audible as a
// small DC offset on a quiet passage.
func pcm16(v float32) int16 {
	f := float64(v)
	if math.IsNaN(f) {
		return 0
	}
	f = math.Round(f * 32767)
	if f > 32767 {
		f = 32767
	}
	if f < -32768 {
		f = -32768
	}
	return int16(f)
}

func (w *wavWriter) close(path string) error {
	dataLen := len(w.buf)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+dataLen))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16) // PCM header size
	binary.LittleEndian.PutUint16(hdr[20:22], 1)  // format 1 = PCM
	binary.LittleEndian.PutUint16(hdr[22:24], 2)  // channels
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(w.sampleRate))
	byteRate := uint32(w.sampleRate * 2 * 2)
	binary.LittleEndian.PutUint32(hdr[28:32], byteRate)
	binary.LittleEndian.PutUint16(hdr[32:34], 4) // block align
	binary.LittleEndian.PutUint16(hdr[34:36], 16)
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(dataLen))

	if _, err := f.Write(hdr); err != nil {
		return err
	}
	_, err = f.Write(w.buf)
	return err
}
