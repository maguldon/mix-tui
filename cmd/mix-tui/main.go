// Command mix-tui is a terminal DJ application: two decks, a mixer, a library,
// MIDI and keyboard control, playing to a PipeWire device.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/maguldon/mix-tui/internal/analyze"
	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/dsp"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/midi"
	"github.com/maguldon/mix-tui/internal/pipewire"
	"github.com/maguldon/mix-tui/internal/tui"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Print(flagText())
		return
	}
	args, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mix-tui: %v\n\n%s", err, flagText())
		os.Exit(2)
	}
	if err := run(args); err != nil {
		fmt.Fprintf(os.Stderr, "mix-tui: %v\n", err)
		os.Exit(1)
	}
}

func run(args appArgs) error {
	// 1. Configuration. Loaded before anything opens a device, so a bad config
	//    is reported before audio starts.
	cfgPath := discoverConfig(args.configPath)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if args.view != "" {
		cfg.UI.DefaultView = args.view
	}
	keys, err := config.NewKeyResolver(cfg)
	if err != nil {
		return fmt.Errorf("keybindings: %w", err)
	}

	// 2. Audio device, so the engine can be built at the rate the device
	//    actually granted rather than the one we asked for.
	rate := cfg.Audio.SampleRate
	var out *pipewire.Output
	if !args.noAudio {
		out, err = pipewire.NewOutput(pipewire.OutputConfig{
			SampleRate: rate,
			RingFrames: cfg.Audio.RingFrames,
			MainTarget: cfg.Audio.MainTarget,
			CueTarget:  cfg.Audio.CueTarget,
		})
		if err != nil {
			// A missing device is not fatal: the interface still runs and can
			// say why there is no sound (spec §8). Audio simply will not play.
			fmt.Fprintf(os.Stderr, "mix-tui: no audio device: %v\n", err)
		} else if rate != 0 && out.SampleRate() != rate {
			// The device disagreed with the request. Rebuild at its rate rather
			// than rendering 44.1k into a 48k stream.
			fmt.Fprintf(os.Stderr, "mix-tui: device runs at %d Hz, config asked for %d; using the device\n",
				out.SampleRate(), rate)
		}
	}
	if out != nil {
		rate = out.SampleRate()
	}
	if rate <= 0 {
		rate = dsp.SampleRate
	}

	// 3. Engine at the real rate.
	eng := engine.New(engine.Config{
		SampleRate:  rate,
		BlockFrames: engine.BlockFrames,
		DeckCount:   2,
	})

	// 4. Tracks, with cached analysis attached so BPM and key show immediately.
	lib := library.NewIndex()
	if root := args.libraryRoot; root != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := lib.Scan(ctx, []string{root}, nil, nil); err != nil {
			fmt.Fprintf(os.Stderr, "mix-tui: library scan: %v\n", err)
		}
		cancel()
	}

	cache := library.OpenCache(filepath.Join(cacheDir(), "analysis"))
	for i, path := range []string{args.aPath, args.bPath} {
		if path == "" {
			continue
		}
		track, err := audio.Decode(path)
		if err != nil {
			return fmt.Errorf("deck %c: %w", 'A'+byte(i), err)
		}
		var grid *analyze.BeatGrid
		var key *analyze.Key
		if a, ok := cache.Get(path); ok {
			grid = beatGridFrom(a, track)
			key = keyFrom(a)
		}
		// Nothing is rendering yet, so the immediate path is safe here.
		eng.Deck(i).LoadNow(track, grid, key)
	}

	// 5. Controls.
	model := tui.New(eng, cfg, keys)
	model.SetLibrary(lib)

	// Loading decodes audio, which belongs to the audio package rather than the
	// view layer, so the binary supplies it. Without this the library and the
	// overlay could browse but never load, and the empty-deck art told the user
	// to press a key that did nothing.
	loadInto := func(deck int, path string) error {
		track, err := audio.Decode(path)
		if err != nil {
			return fmt.Errorf("load: %w", err)
		}
		// Posted, not applied: the pump goroutine may be rendering this deck
		// right now, and Load touches the resampler, the filter and the
		// playhead. Calling it directly from the UI goroutine is a data race.
		var grid *analyze.BeatGrid
		var key *analyze.Key
		if a, ok := cache.Get(path); ok {
			grid = beatGridFrom(a, track)
			key = keyFrom(a)
		}
		eng.Deck(deck).LoadWith(track, grid, key)
		return nil
	}
	model.SetLoader(loadInto)

	if !args.noMIDI {
		handler := model.MIDIHandler()
		listener, err := midi.OpenListener(handler)
		if err != nil {
			// No controller is a normal state, not a failure.
			fmt.Fprintf(os.Stderr, "mix-tui: no MIDI: %v (keyboard only)\n", err)
		} else {
			defer listener.Close()
			model.AttachMIDI(handler)
		}
	}

	// Scripted input, applied before the first frame so the screenshot shows the
	// settled state rather than a race between seeding and rendering.
	applyCrossfader(model, args.xfade)
	if args.seedTestMIDI {
		model.AttachMIDI(seedMIDI(model))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Hot-reload the mapping table. Without this the watcher existed but was
	// never started, so editing a mapping mid-set did nothing -- while the README
	// told users it would take effect within two seconds.
	if handler := model.MIDIHandlerIfAny(); handler != nil {
		dir := filepath.Join(configDir(), "mappings")
		if _, err := os.ReadDir(dir); err == nil {
			w := midi.NewWatcher(dir, handler, func(f string, a ...any) {
				fmt.Fprintf(os.Stderr, f+"\n", a...)
			})
			if _, errs := w.ReloadOnce(); len(errs) > 0 {
				fmt.Fprintf(os.Stderr, "mix-tui: %d mapping error(s) at startup\n", len(errs))
			} else if n, _ := w.ReloadOnce(); n > 0 {
				fmt.Fprintf(os.Stderr, "mix-tui: loaded %d mapping(s)\n", n)
			}
			go w.Run(ctx)
		}
	}

	// 6. Renderer.
	var pumpDone chan struct{}
	if out != nil {
		if err := out.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "mix-tui: audio start: %v\n", err)
		}
		pump := pipewire.NewPumper(out, eng, pipewire.PumpConfig{})
		pumpDone = make(chan struct{})
		go func() {
			defer close(pumpDone)
			_ = pump.Run(ctx)
		}()

		// Feed the debug overlay real numbers. Without this the overlay's
		// "no audio callbacks" warning is permanently true and its advice is
		// worse than useless -- it would tell a user with working audio that
		// their device is broken.
		model.SetStatsFunc(func() views.DebugStats {
			mainStats, cueStats := out.Stats()
			ps := pump.Stats()
			return views.DebugStats{
				AudioExpected:  true,
				Underruns:      mainStats.Starves + cueStats.Starves,
				MainCallbacks:  mainStats.Callbacks,
				CueCallbacks:   cueStats.Callbacks,
				BlocksRendered: ps.BlocksRendered,
				RenderWallMax:  ps.RenderWallMax,
			}
		})
	}

	// 7. A single frame to a file, without a terminal. This is the one flag
	//    that makes layout work verifiable in CI.
	if args.screenshot != "" {
		// Drain the engine's command queue before capturing.
		//
		// Deck actions do not mutate state directly: they are posted and applied
		// at the start of the next render block (milestone 1's realtime design).
		// Without a render, a seeded performance changes nothing visible and the
		// screenshot would show the pre-performance state.
		for i := 0; i < 4; i++ {
			if err := eng.RenderInto(audio.NewRing(1<<16), audio.NewRing(1<<16)); err != nil {
				return err
			}
		}
		w, h := args.width, args.height
		if w <= 0 {
			w = 120
		}
		if h <= 0 {
			h = 40
		}
		return writeScreenshot(model, args.screenshot, w, h)
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, runErr := p.Run()
	stop()
	if pumpDone != nil {
		<-pumpDone
	}
	if out != nil {
		_ = out.Stop()
		_ = out.Close()
	}
	return runErr
}

func writeScreenshot(m *tui.Model, path string, w, h int) error {
	// The default 120x40 is the spec's reference size; -width/-height override it
	// so layout can be verified at every size that matters without a terminal.
	out := m.RenderAt(w, h)
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}
	fmt.Fprintf(os.Stderr, "mix-tui: wrote %s\n", path)
	return nil
}

// configDir is where mix-tui keeps config and mappings.
func configDir() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "mix-tui")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "mix-tui")
	}
	return ".mix-tui"
}

func cacheDir() string {
	if base := os.Getenv("XDG_CACHE_HOME"); base != "" {
		return filepath.Join(base, "mix-tui")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "mix-tui")
	}
	return ".mix-tui-cache"
}
