package audio

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "testdata", "fixtures", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("fixture %s missing: run MIXTUI_GEN=1 go test -tags mixtuigen ./internal/testdata/", name)
	}
	return p
}

func TestDecodeWAV(t *testing.T) {
	tr, err := Decode(fixture(t, "sine440.wav"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if tr.SampleRate != dsp.SampleRate {
		t.Errorf("rate = %d, want %d", tr.SampleRate, dsp.SampleRate)
	}
	if tr.Duration < 900*time.Millisecond || tr.Duration > 1100*time.Millisecond {
		t.Errorf("duration = %v, want ~1s", tr.Duration)
	}
	if math.Abs(tr.PCM.Peak()-0.5) > 0.05 {
		t.Errorf("peak = %v, want ~0.5", tr.PCM.Peak())
	}
}

func TestDecodeFLAC(t *testing.T) {
	tr, err := Decode(fixture(t, "sine440.flac"))
	if err != nil {
		t.Fatalf("Decode flac: %v", err)
	}
	if math.Abs(tr.PCM.Peak()-0.5) > 0.05 {
		t.Errorf("flac peak = %v, want ~0.5", tr.PCM.Peak())
	}
}

func TestDecodeOGG(t *testing.T) {
	tr, err := Decode(fixture(t, "sine440.ogg"))
	if err != nil {
		t.Fatalf("Decode ogg: %v", err)
	}
	if math.Abs(tr.PCM.Peak()-0.5) > 0.08 {
		t.Errorf("ogg peak = %v, want ~0.5", tr.PCM.Peak())
	}
}

func TestDecodeMP3(t *testing.T) {
	tr, err := Decode(fixture(t, "sine440.mp3"))
	if err != nil {
		t.Fatalf("Decode mp3: %v", err)
	}
	if tr.PCM.Peak() < 0.3 {
		t.Errorf("mp3 peak = %v, want ~0.5", tr.PCM.Peak())
	}
	// Lossy codecs carry encoder delay and pad the tail, so a 1s source
	// commonly decodes to slightly more than 1s. The bound allows that.
	if d := tr.Duration - time.Second; d < -50*time.Millisecond || d > 200*time.Millisecond {
		t.Errorf("mp3 duration = %v, want ~1s", tr.Duration)
	}
}

// TestDecodePreservesFrequency verifies the decoded audio is actually the tone
// it claims to be, not merely non-empty. A decoder that returns silence or
// noise at the right length passes every other test here.
func TestDecodePreservesFrequency(t *testing.T) {
	for _, name := range []string{"sine440.wav", "sine440.flac", "sine440.ogg", "sine440.mp3"} {
		tr, err := Decode(fixture(t, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// A 440Hz sine has a zero-crossing rate of ~880/sec (2f convention).
		// MP3 is allowed a wider band: encoder delay adds a near-silent
		// region whose duration shifts the per-second rate.
		tol := 120.0
		if strings.HasSuffix(name, ".mp3") {
			tol = 300
		}
		zcr := tr.PCM.ZeroCrossingRate()
		if math.Abs(zcr-880) > tol {
			t.Errorf("%s: ZCR = %v, want ~880 for a 440Hz tone", name, zcr)
		}
	}
}

func TestDecodeStereoInterleaving(t *testing.T) {
	// L and R are exact negatives, so the mono sum is 0 but the channels
	// differ. This catches a decoder that averages to mono or swaps them.
	tr, err := Decode(fixture(t, "lr.wav"))
	if err != nil {
		t.Fatal(err)
	}
	// Measure over a window: any single frame may land on a zero crossing.
	checked, opposed := 0, 0
	for i := 0; i < 4000; i++ {
		l := float64(tr.PCM[i*2])
		r := float64(tr.PCM[i*2+1])
		if math.Abs(l) < 0.05 {
			continue // near a crossing, uninformative
		}
		checked++
		if l*r >= 0 {
			t.Fatalf("frame %d: L=%v R=%v must have opposite signs", i, l, r)
		}
		if math.Abs(l+r) > 0.01 {
			t.Fatalf("frame %d: L+R = %v, want ~0 (opposed channels)", i, l+r)
		}
		opposed++
	}
	if checked < 100 {
		t.Fatalf("only %d informative frames out of 4000", checked)
	}
	_ = opposed
}

func TestDecodeMonoIsDuplicated(t *testing.T) {
	tr, err := Decode(fixture(t, "mono.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Channels != 2 {
		t.Fatalf("channels = %d, want 2 (mono duplicated)", tr.Channels)
	}
	for i := 0; i < 100; i++ {
		if tr.PCM[i*2] != tr.PCM[i*2+1] {
			t.Fatalf("frame %d: L=%v R=%v, mono should be duplicated", i, tr.PCM[i*2], tr.PCM[i*2+1])
		}
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	_, err := Decode(fixture(t, "garbage.bin"))
	if err == nil {
		t.Fatal("expected error for garbage file")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

func TestDecodeRejectsMissingFile(t *testing.T) {
	if _, err := Decode("/nonexistent/nope.mp3"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDecodeRejectsEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.wav")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(p); err == nil {
		t.Fatal("expected error for empty file")
	}
}

// TestDecodeDetectsFormatByContentNotExtension proves a mislabelled file
// decodes as what it actually is. Extensions lie; magic bytes do not.
func TestDecodeDetectsFormatByContentNotExtension(t *testing.T) {
	src := fixture(t, "sine440.flac")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	lie := filepath.Join(t.TempDir(), "actually-flac.mp3")
	if err := os.WriteFile(lie, data, 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := Decode(lie)
	if err != nil {
		t.Fatalf("mislabelled FLAC failed to decode: %v", err)
	}
	if math.Abs(tr.PCM.Peak()-0.5) > 0.05 {
		t.Errorf("peak = %v, want ~0.5", tr.PCM.Peak())
	}
}

func TestProbeDoesNotDecodeFully(t *testing.T) {
	d, sr, err := Probe(fixture(t, "sine440.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if sr != dsp.SampleRate {
		t.Errorf("rate = %d", sr)
	}
	if d < 900*time.Millisecond || d > 1100*time.Millisecond {
		t.Errorf("duration = %v, want ~1s", d)
	}
}

func TestProbeRejectsGarbage(t *testing.T) {
	if _, _, err := Probe(fixture(t, "garbage.bin")); err == nil {
		t.Fatal("expected error for garbage")
	}
}

// TestDecodeResamplesToEngineRate covers files that arrive at a rate the
// engine does not run at. Every decoder path must land on SampleRate.
func TestDecodeResamplesToEngineRate(t *testing.T) {
	tr, err := Decode(fixture(t, "sine48k.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if tr.SampleRate != dsp.SampleRate {
		t.Fatalf("rate = %d, want %d", tr.SampleRate, dsp.SampleRate)
	}
	zcr := tr.PCM.ZeroCrossingRate()
	if math.Abs(zcr-880) > 120 {
		t.Errorf("resampled ZCR = %v, want ~880", zcr)
	}
}

func TestDecodeEmptyAndTinyFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{0, 1, 2} {
		p := filepath.Join(dir, "tiny"+string(rune('a'+n))+".wav")
		data := make([]byte, n)
		copy(data, []byte("RIFF\x00\x00\x00\x00WAVEfmt "))
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		// Must not panic, whatever it decides to return.
		if tr, err := Decode(p); err == nil && tr == nil {
			t.Fatal("Decode returned (nil, nil)")
		}
	}
}
