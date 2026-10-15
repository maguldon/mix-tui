package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/pipewire"
	"github.com/maguldon/mix-tui/internal/tui"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// audioRetryInterval is how often a session with no device tries again.
//
// Three seconds is long enough not to hammer a device that is genuinely absent
// -- PipeWire logs every failed connection -- and short enough that plugging in
// a cable produces sound before the user has time to wonder why it did not.
const audioRetryInterval = 3 * time.Second

// audioSession owns the device, the pump, and their lifetime, and can be
// started again after a failure.
//
// Spec 8 says a missing PipeWire stream must never leave "a TUI that silently
// produces no sound", and asks for a background retry. Before this the output
// was a local: opened once, and if that failed the application carried on with
// a working interface and no audio and nothing on screen saying so. Worse, the
// debug overlay's own "no audio callbacks" warning was suppressed, because
// SetStatsFunc was only wired when a device had opened -- the warning was
// disabled exactly when it was needed.
//
// The engine's sample rate is fixed at construction and is not renegotiated when
// a device appears later; the README already lists that as a known gap. A late
// device at a different rate will render at the engine's rate.
type audioSession struct {
	cfg pipewire.OutputConfig
	eng *engine.Engine
	// pumpCfg carries the producer's settings. audio.target_latency_frames was
	// parsed, validated and documented, and then dropped: NewPumper was called
	// with an empty PumpConfig, so the value always fell back to rate/5 and the
	// setting was exactly the "silently ignored typo" the config package's own
	// doc warns about.
	pumpCfg pipewire.PumpConfig

	mu     sync.Mutex
	out    *pipewire.Output
	pump   *pipewire.Pumper
	done   chan struct{}
	err    string
	closed bool
	// onChange is called when audio comes up or goes down, so the interface can
	// drop or show its banner without polling for a field it cannot see.
	onChange func(active bool, reason string)
}

// start opens the device and runs the pump. It returns the error on failure and
// leaves the session inactive, so the caller can retry later.
func (s *audioSession) start(ctx context.Context) error {
	s.mu.Lock()
	if s.closed || s.activeLocked() {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	out, err := pipewire.NewOutput(s.cfg)
	if err != nil {
		s.setErr(err)
		return err
	}
	if err := out.Start(); err != nil {
		_ = out.Close()
		s.setErr(err)
		return err
	}

	// The device is opened before the engine exists -- that is how the engine
	// gets built at the rate the device actually granted. The pump cannot start
	// until adopt supplies an engine, so the device sits open and idle for the
	// few milliseconds in between.
	s.mu.Lock()
	s.out = out
	s.err = ""
	s.mu.Unlock()
	s.startPump(ctx)
	return nil
}

// startPump runs the pump if a device is open and an engine is available.
func (s *audioSession) startPump(ctx context.Context) {
	s.mu.Lock()
	if s.closed || s.out == nil || s.eng == nil || s.pump != nil {
		s.mu.Unlock()
		return
	}
	out, eng := s.out, s.eng
	pump := pipewire.NewPumper(out, eng, s.pumpCfg)
	done := make(chan struct{})
	s.pump, s.done = pump, done
	s.mu.Unlock()

	go func() {
		defer close(done)
		_ = pump.Run(ctx)
	}()

	// Audio is only live once something is actually rendering. Reporting the
	// open device as active before the pump ran claimed sound that was not
	// happening.
	s.notify(true, "")
}

// setErr records why audio is unavailable, without spamming the terminal: the
// interface shows the reason and the log has the detail.
func (s *audioSession) setErr(err error) {
	s.mu.Lock()
	s.err = err.Error()
	s.mu.Unlock()
	s.notify(false, err.Error())
}

// notify fans a state change out to the listener, if there is one.
func (s *audioSession) notify(active bool, reason string) {
	s.mu.Lock()
	f := s.onChange
	s.mu.Unlock()
	if f != nil {
		f(active, reason)
	}
}

// retry keeps trying to open a device until one appears or ctx is done.
//
// It exists so that plugging in a cable, or starting PipeWire after the
// application, fixes the sound without a restart -- which is what spec 8's
// "retry in the background" asks for, and what a user whose sound card was
// asleep at launch actually needs.
func (s *audioSession) retry(ctx context.Context) {
	t := time.NewTicker(audioRetryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.Active() {
				continue
			}
			_ = s.start(ctx)
		}
	}
}

// Active reports whether audio is genuinely running, which means a pump is
// rendering into a live stream. An open device with no pump produces silence, so
// counting it as active would claim sound that is not happening -- and would
// hide the banner at exactly the moment it is needed.
func (s *audioSession) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeLocked()
}

// activeLocked is Active's predicate, for callers that already hold s.mu.
//
// It exists because start() needs this test while holding the lock, and calling
// Active() there deadlocked: sync.Mutex is not reentrant, so the second Lock
// blocks forever and the application hangs at startup before the interface is
// ever drawn -- with no output, no banner, and no error. Every run with audio
// enabled and no device yet present did this, which is exactly the case the
// background retry exists to handle.
//
// The fix is a helper rather than an inlined comparison so "active" cannot mean
// one thing to a locking caller and another to a non-locking one.
func (s *audioSession) activeLocked() bool {
	return s.out != nil && s.pump != nil
}

// negotiatedRate is the rate the daemon actually granted, or 0 when it has not
// said yet or there is no device. This is the only number that can disagree with
// what was asked for; the declared rate cannot, because it is what was asked for.
func (s *audioSession) negotiatedRate() int {
	s.mu.Lock()
	out := s.out
	s.mu.Unlock()
	if out == nil {
		return 0
	}
	main, _ := out.Stats()
	return main.NegotiatedRate
}

// Reason is the last failure, empty when audio is running.
func (s *audioSession) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Stats feeds the debug overlay, and reports AudioExpected=false while there is
// no device so the overlay's own warning is meaningful.
func (s *audioSession) Stats() views.DebugStats {
	s.mu.Lock()
	out, pump := s.out, s.pump
	s.mu.Unlock()
	if out == nil || pump == nil {
		return views.DebugStats{AudioExpected: false}
	}
	mainStats, cueStats := out.Stats()
	ps := pump.Stats()
	return views.DebugStats{
		AudioExpected:  true,
		DeclaredRate:   out.SampleRate(),
		NegotiatedRate: mainStats.NegotiatedRate,
		RateMismatch:   mainStats.RateMismatch || cueStats.RateMismatch,
		Underruns:      mainStats.Starves + cueStats.Starves,
		Shorts:         mainStats.Shorts + cueStats.Shorts,
		// The larger of the two: MAIN and CUE are separate streams on separate
		// rings, and it is the greedier one that decides how deep a ring has to
		// be.
		MaxBufferFrames: maxStreamBuffer(mainStats.MaxBufferFrames, cueStats.MaxBufferFrames),
		MainCallbacks:   mainStats.Callbacks,
		CueCallbacks:    cueStats.Callbacks,
		BlocksRendered:  ps.BlocksRendered,
		RenderWallMax:   ps.RenderWallMax,
	}
}

// maxStreamBuffer returns the larger of the two streams' observed device buffer
// sizes.
func maxStreamBuffer(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Close stops the pump and releases the device.
func (s *audioSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	out, done := s.out, s.done
	s.out, s.pump, s.done = nil, nil, nil
	s.mu.Unlock()

	if out == nil {
		return
	}
	_ = out.Stop()
	if done != nil {
		<-done
	}
	_ = out.Close()
}

// adopt gives the session the engine, which must exist before the pump starts.
//
// The device is opened first so the engine can be built at the rate the device
// actually granted rather than the one that was asked for. A device that appears
// later is not renegotiated, which the README lists as a known gap.
func (s *audioSession) adopt(ctx context.Context, eng *engine.Engine) {
	s.mu.Lock()
	s.eng = eng
	s.mu.Unlock()
	// A device opened at startup is still sitting there with no pump.
	s.startPump(ctx)
}

// rate is the device's sample rate, or 0 when there is no device.
func (s *audioSession) rate() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out == nil {
		return 0
	}
	return s.out.SampleRate()
}

// startupAudio opens the device once, before the engine exists.
func startupAudio(ctx context.Context, cfg *pipewire.OutputConfig) *audioSession {
	return startupAudioWith(ctx, cfg, pipewire.PumpConfig{})
}

// startupAudioWith is startupAudio with the producer's settings supplied.
//
// A parameter rather than a field read later, because the pump is built once a
// device appears -- possibly minutes into the run, on the retry path -- so the
// setting has to be carried across rather than fetched from the config at that
// moment.
func startupAudioWith(ctx context.Context, cfg *pipewire.OutputConfig, pump pipewire.PumpConfig) *audioSession {
	s := &audioSession{cfg: *cfg, pumpCfg: pump}
	if err := s.start(ctx); err != nil {
		// Once, on stderr, before the alt screen takes the terminal. After that
		// the banner carries it and the log has the detail.
		fmt.Fprintf(os.Stderr, "mix-tui: no audio device: %v (retrying every %s)\n",
			err, audioRetryInterval)
	}
	return s
}

// printSplash draws the boot art and the state of the audio device.
//
// Spec 6.4 asks for full-screen art during audio initialisation, and names the
// idle state when no stream is found as the other place it belongs. The art has
// existed in internal/tui/art.go since the first commit and had no producer at
// all -- it was reachable only from a test.
//
// This is not the blocking idle screen: the interface starts regardless and a
// missing device is reported by a banner with a background retry, because
// stopping to wait for a sound card would block work that needs no audio. What
// it does is put the reason on screen once, before the alt screen takes the
// terminal, where it is readable rather than scrolled away.
func printSplash(w io.Writer, cols int, session *audioSession) {
	if cols < 20 {
		cols = 20
	}
	for _, line := range tui.RenderArt(cols, 6, "splash") {
		fmt.Fprintln(w, line)
	}
	switch {
	case session == nil:
		fmt.Fprintf(w, "  audio disabled (-no-audio)\n")
	case session.Active():
		fmt.Fprintf(w, "  audio device open at %d Hz\n", session.rate())
	default:
		fmt.Fprintf(w, "  no audio device: %s\n", session.Reason())
		fmt.Fprintf(w, "  retrying every %s -- the interface works without sound\n", audioRetryInterval)
	}
	fmt.Fprintln(w)
}
