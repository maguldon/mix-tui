//go:build mixtuigen

// Command testdata generates the audio fixtures the decode tests need.
//
// Run with:
//
//	MIXTUI_GEN=1 go test -tags mixtuigen ./internal/testdata/
//
// Fixtures are committed, so this only needs running when the decode layer
// changes or a format is added. Each encoder is tried in preference order and
// a missing one falls through to ffmpeg: a silently skipped decoder test is a
// decoder nobody has verified.
package testdata

import (
	"fmt"
	"io"

	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("MIXTUI_GEN") != "1" {
		os.Exit(m.Run())
	}
	dir := filepath.Join("fixtures")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	generate(logger{w: os.Stderr}, dir)
	os.Exit(m.Run())
}

// logger gives generate the small subset of *testing.T it needs, so the same
// code can run from TestMain and from a test.
type logger struct{ w io.Writer }

func (l logger) Fatalf(format string, args ...any) {
	fmt.Fprintf(l.w, format+"\n", args...)
	os.Exit(1)
}

func (l logger) Logf(format string, args ...any) {
	fmt.Fprintf(l.w, format+"\n", args...)
}

func generate(t logger, dir string) {
	base := filepath.Join(dir, "sine440.wav")
	run(t, "ffmpeg", "-f", "lavfi", "-i",
		"sine=frequency=440:duration=1:sample_rate=44100",
		"-ac", "2", "-af", "volume=6", "-c:a", "pcm_s16le", base, "-y")

	// A stereo file with hard-opposed channels, for interleaving tests:
	// L is the tone, R is its negation, so the mono sum is exactly zero.
	lr := filepath.Join(dir, "lr.wav")
	run(t, "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i",
		"sine=frequency=440:duration=1:sample_rate=44100",
		"-filter_complex", "[0:a]volume=6,asplit=2[l][r];[r]volume=-1[rn];[l][rn]join=inputs=2:channel_layout=stereo[out]",
		"-map", "[out]", "-c:a", "pcm_s16le", lr, "-y")

	mono := filepath.Join(dir, "mono.wav")
	run(t, "ffmpeg", "-f", "lavfi", "-i",
		"sine=frequency=440:duration=1:sample_rate=44100",
		"-ac", "1", "-af", "volume=6", "-c:a", "pcm_s16le", mono, "-y")

	// Arrives at 48k and must be resampled to the engine rate on decode.
	r48 := filepath.Join(dir, "sine48k.wav")
	run(t, "ffmpeg", "-f", "lavfi", "-i",
		"sine=frequency=440:duration=1:sample_rate=48000",
		"-ac", "2", "-af", "volume=6", "-c:a", "pcm_s16le", r48, "-y")

	flac := filepath.Join(dir, "sine440.flac")
	if has("flac") {
		run(t, "flac", "--silent", "-f", base, "-o", flac)
	} else {
		t.Logf("WARN: flac encoder missing; sine440.flac test will SKIP")
	}

	ogg := filepath.Join(dir, "sine440.ogg")
	if has("oggenc") {
		run(t, "oggenc", "-Q", "-o", ogg, base)
	} else if has("ffmpeg") {
		run(t, "ffmpeg", "-loglevel", "error", "-i", base,
			"-c:a", "libvorbis", ogg, "-y")
	} else {
		t.Logf("WARN: no ogg encoder; sine440.ogg test will SKIP")
	}

	mp3 := filepath.Join(dir, "sine440.mp3")
	if has("ffmpeg") {
		run(t, "ffmpeg", "-loglevel", "error", "-i", base,
			"-c:a", "libmp3lame", "-b:a", "192k", mp3, "-y")
	} else if has("lame") {
		run(t, "lame", "--silent", base, mp3)
	} else {
		t.Logf("WARN: no mp3 encoder; sine440.mp3 test will SKIP")
	}

	garbage := filepath.Join(dir, "garbage.bin")
	if err := os.WriteFile(garbage, []byte{
		0x00, 0x01, 0x02, 0x03, 0xDE, 0xAD, 0xBE, 0xEF,
		0x7F, 0x00, 0x9F, 0x42, 0x13, 0x37, 0x00, 0x00,
	}, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
}

func has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func run(t logger, name string, args ...string) {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
