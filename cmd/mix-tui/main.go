// Command mix-tui is a terminal DJ application: two decks, a mixer, a library,
// MIDI and keyboard control, playing to a PipeWire device.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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

// scanTimeout bounds a library scan. It is generous because scanning only
// stats files -- it decodes nothing -- and a real collection is tens of
// thousands of them, but it is a bound rather than an open-ended walk so a
// root on a stalled network mount cannot hang startup forever.
const scanTimeout = 30 * time.Second

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
	// Diagnostics go to a file, not stderr: the TUI owns the terminal, and a
	// background analysis pass writing to stderr would corrupt the alt-screen
	// frame it is running behind.
	log := newLogger()
	defer log.Close()

	slogLog := log.slog()

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 2. Audio device, so the engine can be built at the rate the device
	//    actually granted rather than the one we asked for.
	rate := cfg.Audio.SampleRate
	// A device is opened once here and the session can retry in the background,
	// so a missing sound card costs a banner rather than silence (spec §8).
	audioCfg := pipewire.OutputConfig{
		SampleRate: rate,
		RingFrames: cfg.Audio.RingFrames,
		MainTarget: cfg.Audio.MainTarget,
		CueTarget:  cfg.Audio.CueTarget,
	}
	var session *audioSession
	if !args.noAudio {
		session = startupAudioWith(ctx, &audioCfg, pipewire.PumpConfig{
			TargetLatencyFrames: cfg.Audio.TargetLatencyFrames,
			BlockFrames:         engine.BlockFrames,
		})
		// Compare against what the DEVICE granted, not against what we declared.
		//
		// session.rate() is the declared rate -- the same number audioCfg asked
		// for -- so this condition could not fire for the disagreement it names,
		// and the only inputs that reached it were a negative or zero config
		// value. It would have printed "device runs at 44100 Hz, config asked
		// for 0", which is a true statement about nothing.
		//
		// The granted rate arrives asynchronously, so at startup there may not be
		// one yet. That is not an error: PipeWire resamples, and the debug
		// overlay reports the granted rate once the daemon states it.
		if r := session.negotiatedRate(); r > 0 && rate != 0 && r != rate {
			fmt.Fprintf(os.Stderr, "mix-tui: device runs at %d Hz, config asked for %d; using the device\n",
				r, rate)
		}
		if r := session.rate(); r > 0 {
			rate = r
		}
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
	//
	//    The root comes from -library if given, else ui.library_root. The flag
	//    wins because a path typed at the prompt is an explicit request for this
	//    run; the config value is the fallback, and was the only way to point at
	//    a library without retyping the flag on every launch.
	lib := library.NewIndex()
	libRoot := config.ResolveLibraryRoot(args.libraryRoot, cfg.UI.LibraryRoot)

	cache := library.OpenCache(filepath.Join(cacheDir(), "analysis"))

	// Hotcues and saved loops go in the CONFIG directory, not the cache
	// directory, even though analysis has moved to the cache.
	//
	// They are not derived data: a DJ sets them deliberately, over a set, and
	// they are the one thing here that cannot be recomputed from the audio. A
	// cache is expected to be evictable -- clearing it is routine, and the next
	// run rebuilds it. Clearing this one would silently destroy a set. Spec
	// §10 puts them under the config directory for the same reason.
	eng.SetCueStore(engine.OpenCueStore(configDir()))
	analyzer := NewAnalyzer(cache, eng, slogLog)
	// Push analysis results back into the library index as they land, so the
	// browser's BPM and KEY columns fill in for tracks the user has actually
	// played. The analyzer held no reference to the index before, so those
	// columns read "--" and "?" for every track regardless of what had been
	// analysed.
	analyzer.OnResult(func(path string, r *analyze.Result) {
		lib.SetAnalysis(path, analysisFrom(r))
	})

	// Indexing runs off the startup path. It used to run inline, before the
	// first frame, so the app sat on a blank terminal for as long as the walk
	// took: on a 40GB collection of ~7,000 tracks it blew through the 30s cap
	// and failed on every single launch, so the freeze was not the exception --
	// it was the normal case for anyone with a real library.
	//
	// A -screenshot frame skips the walk entirely, which is safe rather than a
	// shortcut: -view selects a deck layout and nothing on the command line sets
	// st.Mode, so no screenshot can render ModeLibrary or ModeOverlay, and those
	// are the only two modes that draw a track. Leaving the scan running beside
	// the capture would instead make the frame depend on whether the walk had
	// finished, which is how TestAcceptanceHeadlessIsStable came to require
	// byte-identical output from a process doing 30 seconds of I/O.
	if args.screenshot == "" {
		go scanLibrary(ctx, lib, libRoot, cache)
	}

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
		// A cache miss is the common case for a track seen for the first time,
		// and without this the deck would simply never have a beatgrid: the
		// analysis code existed but nothing called it, so sync, quantise,
		// beat loops and zoom were all inert in a running application.
		if grid == nil {
			analyzer.Analyze(i, path, track.PCM)
		}
	}

	// 5. Controls.
	model := tui.New(eng, cfg, keys)
	model.SetLibrary(lib)

	// Crates. They live beside the config, and the file is optional: a fresh
	// install has none and that is not a failure. They used to have no
	// production caller at all, so the crate pane showed placeholders and a DJ
	// could not build a set (spec 6.5).
	cratePath := library.DefaultCratesPath(configDir())
	crates, err := library.LoadCrates(cratePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mix-tui: crates: %v (starting with none)\n", err)
	}
	lib.SetCrates(crates)
	if n := len(crates); n > 0 {
		fmt.Fprintf(os.Stderr, "mix-tui: %d crate(s) from %s\n", n, cratePath)
	}
	model.SetCratePersister(func(c []library.Crate) error {
		return library.SaveCrates(cratePath, c)
	})

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
		// Analysis is scheduled rather than run here. A full STFT over a
		// five-minute track takes long enough to be felt as a stall, and this
		// is the path the user is waiting on; it applies itself when done.
		if grid == nil {
			analyzer.Analyze(deck, path, track.PCM)
		}
		return nil
	}
	model.SetLoader(loadInto)

	// The settings view's two back-ends. Both are the binary's for the same
	// reason the loader is: scanning a filesystem and rewriting the user's
	// config are not things a view layer should be able to do.
	//
	// The rescan builds a NEW index and swaps it in, so a root that cannot be
	// read leaves the library the user already had rather than emptying it.
	model.SetLibraryScanner(func(root string) (*library.Index, error) {
		ix := library.NewIndex()
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		err := ix.Scan(ctx, []string{root}, nil, nil)
		return ix, err
	})
	model.SetLibrarySources(configPath(args.configPath), args.libraryRoot)
	if args.configPath == "" && discoverConfig("") == "" {
		// No config file on disk. Say so once, on stderr, which the alt-screen
		// program has not taken yet: otherwise the first save from inside the
		// application creates a file the user never knew did not exist.
		fmt.Fprintf(os.Stderr, "mix-tui: no config file at %s; one will be written if you save a setting\n",
			configPath(""))
	}
	if args.configPath == "" {
		model.SetConfigSaver(func(root string) error {
			return config.SaveLibraryRoot(configPath(""), root)
		})
	} else {
		model.SetConfigSaver(func(root string) error {
			return config.SaveLibraryRoot(args.configPath, root)
		})
	}

	if !args.noMIDI {
		handler := model.MIDIHandler()
		listener, err := midi.OpenListener(handler)
		state := midiStartup(err, openedPorts(listener))
		if state == midiOK {
			defer listener.Close()
			model.AttachMIDI(handler)
		} else {
			// No controller is a normal state, not a failure. Spec 8 asks for the
			// available devices and the mapping directory alongside it: "no
			// MIDI" on its own leaves the user with no way to find out whether
			// the device is missing, or the mapping.
			for _, line := range midiReportLines(state, err, configDir()) {
				fmt.Fprintln(os.Stderr, line)
			}
			if state == midiFailed {
				reportMIDIDevices()
			}
		}
	}

	// Scripted input, applied before the first frame so the screenshot shows the
	// settled state rather than a race between seeding and rendering.
	applyCrossfader(model, args.xfade)
	if args.seedTestMIDI {
		model.AttachMIDI(seedMIDI(model))
	}

	// Hot-reload the mapping table. Without this the watcher existed but was
	// never started, so editing a mapping mid-set did nothing -- while the README
	// told users it would take effect within two seconds.
	if handler := model.MIDIHandlerIfAny(); handler != nil {
		dir := filepath.Join(configDir(), "mappings")
		if _, err := os.ReadDir(dir); err == nil {
			// Reload failures go to the interface, not only to the log.
			//
			// They used to go to stderr, which during a session is the middle of
			// a rendered alt-screen frame -- the same reason the diagnostic log
			// exists. So a user who broke their mapping mid-set saw a corrupted
			// frame for an instant, and nothing else: the log had the detail and
			// the interface did not. Their controller had simply stopped
			// responding, with no indication of why.
			//
			// Model.ShowError existed for exactly this and had no callers.
			w := midi.NewWatcher(dir, handler, func(f string, a ...any) {
				model.ShowError(strings.TrimSuffix(f, "\n") + ": " + fmt.Sprint(a...))
				slogLog.Warn("mapping reload failed", "msg", fmt.Sprintf(f, a...))
			})
			// One scan, not two. The second ReloadOnce always saw nothing new,
			// so "loaded N mapping(s)" could never print -- a user with a
			// working mapping was told nothing about it.
			n, errs := w.ReloadOnce()
			// The errors themselves, not a count. Spec §8 asks for the file and
			// the line, and "3 mapping error(s) at startup" is the one form of
			// this message that cannot make a typo findable -- the user knows
			// something is wrong and has no idea which file or where.
			//
			// The function that already did this, mappingStartupLines, was called
			// only from its own test: a test asserting behaviour the production
			// path did not have.
			// mappingStartupLines reports both the errors and the loaded count,
			// so printing them separately here would have double-reported the
			// count.
			for _, line := range mappingStartupLines(n, errs) {
				fmt.Fprintln(os.Stderr, line)
			}
			go w.Run(ctx)
		}
	}

	// 6. Audio. The session was opened before the engine existed so the engine
	//    could be built at the device's real rate; now it gets the engine, and
	//    from here it can come up on its own if the device appears later.
	if session != nil {
		session.adopt(ctx, eng)
		defer session.Close()
		go session.retry(ctx)

		// Fold the analysis pipeline into the stats the debug screen already
		// reads. The overlay has always had a "scan:" row and the analyzer has
		// always had a pipeline state; neither was connected to the other, so
		// both were dead and a slow analysis pass looked identical to a wedged
		// one.
		sessionStats := session.Stats
		model.SetStatsFunc(func() views.DebugStats {
			st := sessionStats()
			st.AnalysisState = analyzer.State()
			return st
		})
		model.SetAudioStatus(func() (bool, string) { return session.Active(), session.Reason() })
	}

	// 7. A single frame to a file, without a terminal. This is the one flag
	//    that makes layout work verifiable in CI.
	if args.screenshot != "" {
		// Let in-flight analysis land first. Analysis is asynchronous so it
		// never sits on the path a user waits on, but a screenshot is a single
		// settled frame: taken too early it shows "--" for BPM and key on a
		// track that has both.
		waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
		analyzer.Wait(waitCtx)
		waitCancel()

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

	// Boot art, once, before the alt screen takes the terminal over. Spec 6.4
	// names audio initialisation as its place, and this is the only moment the
	// reason for a missing device is readable without scrolling back.
	printSplash(os.Stderr, 120, session)

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, runErr := p.Run()
	stop()
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

// scanLibrary indexes the collection and then attaches what the cache already
// knows about it.
//
// It runs on its own goroutine, and that is not a new arrangement: the package
// it calls documents that scanning "runs in the background and never blocks the
// caller: a DJ opening a 20,000-track library must still be able to hit play
// while it indexes" (spec §8), and Index guards every field with an RWMutex
// precisely so a walk can run beside the UI. The caller was the part that had
// not caught up. Running it inline meant the splash did not appear until the
// walk finished, and on this machine's library that walk reliably outlived
// scanTimeout -- so every launch opened on a 30-second blank screen and then
// printed a deadline error.
//
// The cache work moved in here with it because both steps need an index that is
// already populated. ApplyAnalysis against an empty index restores nothing and
// reports zero, which is not a harmless no-op: it is the difference between a
// track showing its BPM on first paint and showing one after a second play.
//
// An empty root is a fresh install, and returns without touching anything.
func scanLibrary(ctx context.Context, ix *library.Index, root string, cache *library.Cache) {
	if root == "" {
		return
	}
	// Derived from the app context, so an interrupt ends the walk rather than
	// leaving a goroutine stat'ing a user's music folder after the UI is gone.
	scanCtx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	if err := ix.Scan(scanCtx, []string{root}, nil, nil); err != nil {
		fmt.Fprintf(os.Stderr, "mix-tui: library scan: %v\n", err)
	}

	// Fill the index from the cache so already-analysed tracks show their values
	// immediately rather than only after a second play. Reading the cache
	// directory once beats a lookup per track, which on a large collection was
	// one failed file open per track to discover a mostly-empty cache.
	if n := ix.ApplyAnalysis(cache.All()); n > 0 {
		fmt.Fprintf(os.Stderr, "mix-tui: %d track(s) restored from the analysis cache\n", n)
	}
	// Drop analyses for tracks that have since been deleted (spec 8). The index
	// is rebuilt from the filesystem each start, so without this the cache grows
	// forever describing a collection the user no longer has. Only ever removes
	// entries whose recorded path is gone, so it is safe beside a running
	// analyzer.
	if n := cache.EvictMissing(); n > 0 {
		fmt.Fprintf(os.Stderr, "mix-tui: dropped %d cached analysis file(s) for missing tracks\n", n)
	}
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

// newLogger opens the diagnostic log.
//
// The TUI is a full-screen alt-screen program, so anything written to stderr
// while it runs lands in the middle of a rendered frame. Diagnostics go to a
// file instead; without this, a background analysis pass would have had
// nowhere safe to report that it finished.
func newLogger() *diagLogger {
	f, err := os.OpenFile(filepath.Join(cacheDir(), "mix-tui.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		// No log file is not a reason to refuse to start.
		return &diagLogger{logger: slog.New(slog.NewTextHandler(discard{}, nil))}
	}
	return &diagLogger{
		file:   f,
		logger: slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// discard is an io.Writer that drops everything, used when no log file can be
// opened.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// diagLogger owns the diagnostic log file so it can be closed on exit.
type diagLogger struct {
	file   *os.File
	logger *slog.Logger
}

func (d *diagLogger) slog() *slog.Logger { return d.logger }

func (d *diagLogger) Close() {
	if d.file != nil {
		_ = d.file.Close()
	}
}

// reportMIDIDevices lists the inputs the driver can see.
//
// Spec 8: when no device opens, say what was found. An empty list is itself
// the answer -- it distinguishes "the driver sees nothing" from "the
// application failed to open something that is there".
func reportMIDIDevices() {
	devices, err := midi.OpenDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mix-tui: could not enumerate MIDI inputs: %v\n", err)
		return
	}
	if len(devices) == 0 {
		fmt.Fprintf(os.Stderr, "mix-tui: no MIDI inputs are visible to the driver\n")
		return
	}
	fmt.Fprintf(os.Stderr, "mix-tui: MIDI inputs found:\n")
	for _, d := range devices {
		fmt.Fprintf(os.Stderr, "  %d  %s\n", d.Index, d.Name)
	}
}

// midiStartupState is what happened when MIDI was opened.
type midiStartupState int

const (
	// midiOK means a listener is running with at least one port attached.
	midiOK midiStartupState = iota
	// midiNoInputs means the driver is healthy but nothing is plugged in.
	midiNoInputs
	// midiFailed means the driver or the port enumeration failed.
	midiFailed
)

// midiStartup classifies the result of opening MIDI.
//
// It exists because "the driver failed" and "nothing is plugged in" are
// different problems with different advice, and only the first one ever
// reached the user. OpenListener returns success with zero ports, so the
// common case -- a healthy driver and no controller -- printed nothing at all
// (spec 8).
func midiStartup(err error, ports int) midiStartupState {
	switch {
	case err != nil:
		return midiFailed
	case ports <= 0:
		return midiNoInputs
	default:
		return midiOK
	}
}

// openedPorts counts a listener's attached ports, tolerating a nil listener so
// the failure path can call it.
func openedPorts(l *midi.Listener) int {
	if l == nil {
		return 0
	}
	return len(l.Devices())
}

// midiReportLines builds the keyboard-only notice.
//
// Both halves matter. "No MIDI" alone leaves the user unable to tell a missing
// device from a missing mapping, which is the distinction spec 8 is asking for.
func midiReportLines(state midiStartupState, err error, cfgDir string) []string {
	var head string
	switch state {
	case midiNoInputs:
		head = "mix-tui: no MIDI inputs are attached (keyboard only)"
	case midiFailed:
		head = fmt.Sprintf("mix-tui: no MIDI: %v (keyboard only)", err)
	default:
		return nil
	}
	return []string{
		head,
		"mix-tui: mappings are read from " + filepath.Join(cfgDir, "mappings"),
	}
}

// mappingStartupLines builds what the startup mapping scan reports.
//
// The errors themselves rather than a count: spec 8 asks for the file and the
// line, and a count is the one thing that cannot make a typo findable. These go
// to stderr because the alt screen has not taken the terminal yet -- the
// watcher's ongoing reports do not, since those arrive mid-session.
func mappingStartupLines(loaded int, errs []error) []string {
	var out []string
	for _, e := range errs {
		out = append(out, "mix-tui: mapping: "+e.Error())
	}
	if loaded > 0 {
		out = append(out, fmt.Sprintf("mix-tui: loaded %d mapping(s)", loaded))
	}
	return out
}
