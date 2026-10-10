package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/maguldon/mix-tui/internal/dsp"
)

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
