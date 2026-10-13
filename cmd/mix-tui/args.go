package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// appArgs is the parsed command line.
type appArgs struct {
	aPath, bPath string
	libraryRoot  string
	configPath   string
	view         string
	noMIDI       bool
	noAudio      bool
	seedTestMIDI bool
	screenshot   string
	// width and height override the render size for -screenshot. Zero means
	// the terminal's real size, or the 120x40 reference when headless.
	width, height int
	// xfade sets the crossfader at startup, so a scripted performance can start
	// from a known position rather than centre.
	xfade float64
}

// parseArgs parses and validates the command line.
//
// Validation happens here, at parse time, rather than at render time: a bad
// -view that survives until the first frame produces a blank screen with no
// explanation, which is the failure mode spec §8 rules out.
func parseArgs(argv []string) (appArgs, error) {
	var a appArgs
	fs := flag.NewFlagSet("mix-tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors are reported by us, with context
	fs.StringVar(&a.aPath, "a", "", "track for deck A")
	fs.StringVar(&a.bPath, "b", "", "track for deck B")
	fs.StringVar(&a.libraryRoot, "library", "", "library root directory")
	fs.StringVar(&a.configPath, "config", "", "config file (default: $XDG_CONFIG_HOME/mix-tui/config.yaml)")
	fs.StringVar(&a.view, "view", "", "default layout: A, B, C or D")
	fs.BoolVar(&a.noMIDI, "no-midi", false, "disable MIDI input")
	fs.BoolVar(&a.noAudio, "no-audio", false, "render without opening an audio device")
	fs.BoolVar(&a.seedTestMIDI, "seed-test-midi", false, "replay a mapping without hardware")
	fs.StringVar(&a.screenshot, "screenshot", "", "render one frame to this file and exit")
	fs.Float64Var(&a.xfade, "xf", 0, "crossfader position, -1..1")
	fs.IntVar(&a.width, "width", 0, "render width for -screenshot (default 120)")
	fs.IntVar(&a.height, "height", 0, "render height for -screenshot (default 40)")

	if err := fs.Parse(argv); err != nil {
		return a, err
	}
	if a.bPath != "" && a.aPath == "" {
		return a, fmt.Errorf("-b given without -a: both decks are loaded together")
	}
	if a.xfade < -1 || a.xfade > 1 {
		return a, fmt.Errorf("invalid -xf %v: want -1..1", a.xfade)
	}
	if a.view != "" {
		switch strings.ToUpper(a.view) {
		case "A", "B", "C", "D":
			a.view = strings.ToUpper(a.view)
		default:
			return a, fmt.Errorf("invalid -view %q: want A, B, C or D", a.view)
		}
	}
	return a, nil
}

// flagText renders the flag help, used by -h and by the help test.
func flagText() string {
	var a appArgs
	fs := flag.NewFlagSet("mix-tui", flag.ContinueOnError)
	fs.StringVar(&a.aPath, "a", "", "track for deck A")
	fs.StringVar(&a.bPath, "b", "", "track for deck B")
	fs.StringVar(&a.libraryRoot, "library", "", "library root directory")
	fs.StringVar(&a.configPath, "config", "", "config file")
	fs.StringVar(&a.view, "view", "", "default layout: A, B, C or D")
	fs.BoolVar(&a.noMIDI, "no-midi", false, "disable MIDI input")
	fs.BoolVar(&a.noAudio, "no-audio", false, "render without opening an audio device")
	fs.BoolVar(&a.seedTestMIDI, "seed-test-midi", false, "replay a mapping without hardware")
	fs.StringVar(&a.screenshot, "screenshot", "", "render one frame to this file and exit")
	fs.Float64Var(&a.xfade, "xf", 0, "crossfader position, -1..1")
	fs.IntVar(&a.width, "width", 0, "render width for -screenshot (default 120)")
	fs.IntVar(&a.height, "height", 0, "render height for -screenshot (default 40)")
	var b strings.Builder
	fs.SetOutput(&b)
	fs.PrintDefaults()
	return "mix-tui — a terminal DJ application\n\n" + b.String()
}

// discoverConfig resolves the config path: an explicit flag wins, then the XDG
// location. A missing file yields "" rather than an error, so a fresh install
// runs on defaults.
func discoverConfig(explicit string) string {
	if explicit != "" {
		return explicit
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	p := filepath.Join(base, "mix-tui", "config.yaml")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
