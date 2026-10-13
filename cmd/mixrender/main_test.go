package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
)

// A grid built for a track of a known length must actually contain beats.
//
// The startup path built its grid from eng.Deck(deck).Duration() right after an
// async Load, before the render loop had drained the load. Duration() read 0,
// so the grid carried a BPM and an empty beat list, and every beat-dependent
// control failed with "no beatgrid" however -bpm-a was set. A grid with a BPM
// and no beats looks correct in a snapshot, which is why it went unnoticed.
func TestSyntheticGridCoversTheTrack(t *testing.T) {
	const dur = 4 * time.Second
	g := syntheticGrid(128, dur)
	if g.BPM != 128 {
		t.Errorf("BPM = %v, want 128", g.BPM)
	}
	if len(g.Beats) == 0 {
		t.Fatal("no beats: every beat-dependent control would refuse this grid")
	}
	if g.Beats[0] != 0 {
		t.Errorf("first beat at %v, want 0", g.Beats[0])
	}
	for _, b := range g.Beats {
		if b < 0 || b >= dur {
			t.Errorf("beat at %v is outside the track (%v)", b, dur)
		}
	}
	// 4s at 128 BPM is 8 beats, give or take the exact beat period.
	if n := len(g.Beats); n < 8 || n > 9 {
		t.Errorf("beat count = %d, want about 8 for 4s at 128 BPM", n)
	}
}

// A zero-length track must not produce a grid full of beats at zero.
func TestSyntheticGridZeroDuration(t *testing.T) {
	g := syntheticGrid(128, 0)
	if g.BPM != 128 {
		t.Errorf("BPM = %v, want 128", g.BPM)
	}
	if len(g.Beats) != 0 {
		t.Errorf("beat count = %d, want 0 for a zero-length track", len(g.Beats))
	}
}

func TestDeckBMP(t *testing.T) {
	if got := deckBMP(0, 128, 124); got != 128 {
		t.Errorf("deckBMP(0) = %v, want 128", got)
	}
	if got := deckBMP(1, 128, 124); got != 124 {
		t.Errorf("deckBMP(1) = %v, want 124", got)
	}
	// An unset -bpm-a/-bpm-b must stay 0 so no grid is attached at all.
	if got := deckBMP(1, 128, 0); got != 0 {
		t.Errorf("deckBMP(1) with no -bpm-b = %v, want 0", got)
	}
}

// LoadNow installs the grid before anything reads it, which is the property
// the startup path depends on: applyLoad on the render goroutine would leave
// the deck beatgrid-free at the moment SetLoopBeat runs.
func TestLoadNowInstallsBeatGridImmediately(t *testing.T) {
	eng := engine.New(engine.Config{
		SampleRate:  dsp.SampleRate,
		BlockFrames: engine.BlockFrames,
		RingFrames:  dsp.SampleRate / 5,
		DeckCount:   2,
	})
	tr := engine.NewTrackFromPCM(dsp.Sine(440, 2))
	if tr.Duration != 2*time.Second {
		t.Fatalf("fixture duration = %v, want 2s", tr.Duration)
	}
	eng.Deck(0).LoadNow(tr, &analyze.BeatGrid{BPM: 128, Confidence: 0.9}, nil)

	if !eng.Deck(0).HasBeatGrid() {
		t.Fatal("HasBeatGrid() = false right after LoadNow")
	}
	if err := eng.Deck(0).SetLoopBeat(4); err != nil {
		t.Errorf("SetLoopBeat after LoadNow: %v", err)
	}
}

func TestPcm16Clamps(t *testing.T) {
	cases := []struct {
		in   float32
		want int16
	}{
		{0, 0},
		{1, 32767},
		{-1, -32767},
		{2, 32767},   // above full scale must not wrap
		{-2, -32768}, // ditto
		{float32(math.NaN()), 0},
	}
	for _, c := range cases {
		if got := pcm16(c.in); got != c.want {
			t.Errorf("pcm16(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// A round trip through the writer must produce a file the decoder can read
// back, which is the whole point of mixrender being a verification harness.
func TestWavWriterRoundTrips(t *testing.T) {
	pcm := dsp.Sine(440, 0.05)

	w := &wavWriter{sampleRate: dsp.SampleRate}
	if err := w.writeFrames(pcm); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "out.wav")
	if err := w.close(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 44+len(pcm)*2 {
		t.Fatalf("file is %d bytes, want %d (44 header + %d samples)",
			len(data), 44+len(pcm)*2, len(pcm))
	}

	// Header fields a decoder depends on.
	if got := string(data[0:4]); got != "RIFF" {
		t.Errorf("magic = %q, want RIFF", got)
	}
	if got := string(data[8:12]); got != "WAVE" {
		t.Errorf("form = %q, want WAVE", got)
	}
	if got := binary.LittleEndian.Uint32(data[24:28]); got != dsp.SampleRate {
		t.Errorf("sample rate = %d, want %d", got, dsp.SampleRate)
	}
	if got := binary.LittleEndian.Uint16(data[22:24]); got != 2 {
		t.Errorf("channels = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint16(data[34:36]); got != 16 {
		t.Errorf("bits per sample = %d, want 16", got)
	}

	// The declared sizes must agree with the file, or players misread it.
	dataLen := binary.LittleEndian.Uint32(data[40:44])
	if int(dataLen) != len(pcm)*2 {
		t.Errorf("data chunk says %d bytes, file has %d", dataLen, len(pcm)*2)
	}
	riffLen := binary.LittleEndian.Uint32(data[4:8])
	if int(riffLen) != len(data)-8 {
		t.Errorf("RIFF size says %d, file has %d", riffLen, len(data)-8)
	}

	// Audio must survive the conversion, not just the header. dsp.Sine is a
	// 0.5-amplitude tone, so the peak lands near half of full scale (16384)
	// rather than at 32767.
	peak := 0
	for i := 44; i+1 < len(data); i += 2 {
		v := binary.LittleEndian.Uint16(data[i : i+2])
		a := int16(v)
		if a < 0 {
			a = -a
		}
		if int(a) > peak {
			peak = int(a)
		}
	}
	if peak < 16000 || peak > 16600 {
		t.Errorf("peak sample %d, want ~16384 for a 0.5-amplitude sine", peak)
	}
}
