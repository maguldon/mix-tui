package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBin compiles the application once for the acceptance tests.
func buildBin(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "mix-tui")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// runShot runs the binary headlessly and returns the rendered frame.
func runShot(t *testing.T, bin string, extra ...string) string {
	t.Helper()
	shot := filepath.Join(t.TempDir(), "frame.txt")
	args := append([]string{
		"-a", "../../internal/testdata/fixtures/sine440.wav",
		"-b", "../../internal/testdata/fixtures/sine440.wav",
		"-no-audio", "-no-midi", "-screenshot", shot,
	}, extra...)
	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	frame, err := os.ReadFile(shot)
	if err != nil {
		t.Fatal(err)
	}
	return string(frame)
}

// TestAcceptanceScreenshot is the exit criterion for the whole app: it builds
// the binary, runs a scripted performance headlessly, and asserts the rendered
// frame contains the state the performance should have produced.
func TestAcceptanceScreenshot(t *testing.T) {
	bin := buildBin(t)
	s := runShot(t, bin, "-seed-test-midi", "-xf", "0.2")

	for _, want := range []string{"MIXER", "MASTER", "PLAY"} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered frame missing %q:\n%s", want, s)
		}
	}

	// The seeded performance sets a loop, and loop actions need a beatgrid.
	// Analysis now runs on load -- it used not to, and this assertion passed
	// because the fixture never gained one. A 440Hz sine genuinely is
	// periodic, so the beat tracker reports a tempo for it, and LOOP
	// appearing is honest: the loop really was armed.
	//
	// What must still hold is that no BPM is fabricated from nothing. A track
	// with no analysis shows "--", never a number.
	if strings.Contains(s, "0.00") {
		t.Errorf("a zero BPM was displayed for a track with no usable tempo:\n%s", s)
	}

	for i, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "panic") {
			t.Errorf("frame contains a panic at line %d:\n%s", i, s)
		}
	}
}

// TestAcceptanceSeededMIDIDrivesEngine checks that -seed-test-midi actually
// drives the engine rather than being a flag that parses and does nothing.
func TestAcceptanceSeededMIDIDrivesEngine(t *testing.T) {
	bin := buildBin(t)
	plain := runShot(t, bin)
	seeded := runShot(t, bin, "-seed-test-midi")
	if plain == seeded {
		t.Error("-seed-test-midi changed nothing; the input path is not exercised")
	}
}

// TestAcceptanceHeadlessIsStable runs the screenshot repeatedly and requires
// identical output. Non-determinism in a render is a real bug -- map iteration
// order leaking into a frame, or a clock reading into the output.
func TestAcceptanceHeadlessIsStable(t *testing.T) {
	bin := buildBin(t)
	first := runShot(t, bin, "-seed-test-midi")
	for i := 0; i < 3; i++ {
		again := runShot(t, bin, "-seed-test-midi")
		if again != first {
			t.Fatalf("render %d differed from the first:\n--- first ---\n%s\n--- again ---\n%s",
				i+2, first, again)
		}
	}
}

// TestAcceptanceSizesAreCoherent renders at every size that matters and
// requires each to be a full rectangular frame with no panic.
func TestAcceptanceSizesAreCoherent(t *testing.T) {
	bin := buildBin(t)
	for _, sz := range [][2]int{{200, 50}, {120, 40}, {100, 30}, {80, 24}, {70, 20}, {40, 12}, {20, 5}} {
		w, h := sz[0], sz[1]
		shot := filepath.Join(t.TempDir(), "f.txt")
		cmd := exec.Command(bin,
			"-a", "../../internal/testdata/fixtures/sine440.wav",
			"-no-audio", "-no-midi",
			"-width", itoa(w), "-height", itoa(h), "-screenshot", shot)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%dx%d run: %v\n%s", w, h, err, out)
		}
		data, err := os.ReadFile(shot)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) != h {
			t.Errorf("%dx%d produced %d lines, want exactly %d", w, h, len(lines), h)
		}
		for i, l := range lines {
			if strings.Contains(l, "panic") {
				t.Errorf("%dx%d line %d contains a panic", w, h, i)
			}
		}
	}
}

// TestAcceptanceRejectsBadArgs covers the CLI contract from outside: a user
// mistyping a flag must get an error, not a silent default.
func TestAcceptanceRejectsBadArgs(t *testing.T) {
	bin := buildBin(t)
	for _, args := range [][]string{
		{"-seconds", "10"},                   // no tracks
		{"-a", "x.mp3", "-view", "Z"},        // bad layout
		{"-b", "y.mp3"},                      // B without A
		{"-a", "x.mp3", "-nonexistent-flag"}, // unknown flag
	} {
		cmd := exec.Command(bin, args...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("args %v were accepted; stderr:\n%s", args, out)
		}
		if !strings.Contains(string(out), "mix-tui:") {
			t.Errorf("args %v produced no diagnostic: %s", args, out)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
