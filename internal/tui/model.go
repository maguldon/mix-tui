package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/maguldon/mix-tui/internal/config"
	"github.com/maguldon/mix-tui/internal/engine"
	"github.com/maguldon/mix-tui/internal/midi"
	"github.com/maguldon/mix-tui/internal/tui/library"
	"github.com/maguldon/mix-tui/internal/tui/views"
)

// tickInterval is the refresh rate. ~30fps is enough for a level meter to read
// as live and cheap enough to leave headroom for the audio goroutine.
const tickInterval = 33 * time.Millisecond

// Engine is the narrow interface the TUI needs. Depending on this rather than
// *engine.Engine keeps the TUI testable with a fake, and keeps the view layer
// from reaching into audio state.
type Engine interface {
	Apply(engine.Action)
	Snapshot() engine.Snapshot
	DeckCount() int
}

// Model is the bubbletea model. It owns interaction state only — focus, zoom,
// which layout — and never audio state.
type Model struct {
	eng  Engine
	cfg  *config.Config
	keys *config.KeyResolver

	// lib is the scanned library. It may be nil before the first scan, which
	// every view must tolerate: the browser renders an empty library rather
	// than failing.
	lib *library.Index
	// stats feeds the debug overlay.
	stats views.DebugStats
	// midi is attached when a device is opened, so the debug overlay can show
	// ignored-message counts.
	midi *midi.Handler
	// loadFunc decodes and loads a track into a deck. The TUI cannot decode
	// itself: that is the audio package's job, and doing it here would put file
	// I/O in the view layer. The binary supplies it.
	loadFunc func(deck int, path string) error
	// statsFunc is supplied by the binary to report device and pump health.
	//
	// It is a function rather than a pipewire type so the TUI does not import
	// the device layer: doing so would drag cgo into the view package and break
	// the "no engine access below the view" boundary the whole test strategy
	// rests on.
	statsFunc func() views.DebugStats

	st ViewState

	w, h int

	// panicOnRender is a test hook: it forces the render path to panic so the
	// recovery screen can be asserted. Never set in production.
	panicOnRender bool
}

// New builds a Model. The initial layout comes from config (D by default,
// spec §6.1); a nil cfg or keys falls back to defaults rather than panicking,
// because a missing config must not take the interface down.
func New(eng Engine, cfg *config.Config, keys *config.KeyResolver) *Model {
	if cfg == nil {
		cfg = config.Defaults()
	}
	if keys == nil {
		k, err := config.NewKeyResolver(cfg)
		if err != nil {
			k = nil
		}
		keys = k
	}
	return &Model{
		eng:  eng,
		cfg:  cfg,
		keys: keys,
		st:   ViewState{Layout: layoutFromName(cfg.UI.DefaultView), Focus: 0, ZoomBars: 8},
		w:    80,
		h:    24,
	}
}

// State returns the interaction state, for tests.
func (m *Model) State() ViewState { return m.st }

// Engine returns the engine interface, so a caller (the binary) can seed state
// that a MIDI message would normally produce.
func (m *Model) Engine() Engine { return m.eng }

// SetLibrary attaches a scanned library for the browser and overlay.
func (m *Model) SetLibrary(ix *library.Index) { m.lib = ix }

// SetLoader supplies the track-loading function the library and overlay use.
func (m *Model) SetLoader(f func(deck int, path string) error) { m.loadFunc = f }

// LoadSelected loads a path into a deck, reporting why it failed.
func (m *Model) LoadSelected(deck int, path string) error {
	if m.loadFunc == nil {
		return fmt.Errorf("loading is not available")
	}
	if err := m.loadFunc(deck, path); err != nil {
		m.setError(err.Error())
		return err
	}
	m.setError("")
	return nil
}

func (m *Model) setError(msg string) {
	m.st.LastErr = msg
	m.st.LastErrAt = time.Now()
}

// SetStatsFunc supplies a source of device and pump statistics for the debug
// overlay. Passing nil restores the default, which reports zeros.
func (m *Model) SetStatsFunc(f func() views.DebugStats) { m.statsFunc = f }

// SetStats sets the diagnostic surface directly.
func (m *Model) SetStats(s views.DebugStats) { m.stats = s }

// MIDIHandler returns a midi.Handler that applies to this model's engine.
//
// The TUI builds the handler rather than receiving one so that a keypress and
// a controller press converge on exactly the same Action path (spec §5.1).
func (m *Model) MIDIHandler() *midi.Handler {
	return midi.NewHandler(nil, m.eng.Apply)
}

// AttachMIDI points the model at its MIDI handler so the debug overlay can
// show ignored-message counts. It is separate from MIDIHandler so a headless
// run never constructs a device.
func (m *Model) AttachMIDI(h *midi.Handler) { m.midi = h }

// Init starts the render tick.
func (m *Model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg{t: t} })
}

// Update handles the message stream.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Resize reflows rather than assuming the startup size (review focus
		// #1); RenderAt clamps to the new bounds on the next tick.
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		m.expireErr()
		m.refreshMIDICounts()
		m.refreshStats()
		// Mirror authoritative control values from the snapshot each tick
		// rather than keeping a second copy that can drift.
		m.mirrorFromSnapshot()
		return m, tick()

	case keyMsg:
		m.handleKey(string(msg))
		return m, nil

	default:
		return m, nil
	}
}

// bindingName maps a physical key to the spelling config uses.
//
// bubbletea reports the space bar as " ", but a mapping table spells it "space";
// without this translation the most-used key in the application would silently
// do nothing.
func bindingName(k string) string {
	switch k {
	case " ":
		return "space"
	case "esc":
		return "escape"
	}
	return k
}

// refreshStats samples device and pump health for the debug overlay.
func (m *Model) refreshStats() {
	if m.statsFunc != nil {
		m.stats = m.statsFunc()
	}
}

// refreshMIDICounts copies the MIDI handler's tallies into the debug stats, so
// the overlay shows why a controller is not behaving (spec §7).
func (m *Model) refreshMIDICounts() {
	if m.midi == nil {
		return
	}
	m.stats.MIDIIgnored = m.midi.Counts()
}

// expireErr clears an error older than errTTL.
func (m *Model) expireErr() {
	if m.st.LastErr == "" {
		return
	}
	if time.Since(m.st.LastErrAt) > errTTL {
		m.st.LastErr = ""
	}
}

// mirrorFromSnapshot copies the authoritative crossfader position into the
// view state. Snapshot is the source of truth; this is only what to draw.
func (m *Model) mirrorFromSnapshot() {
	s := m.eng.Snapshot()

	// The crossfader is mirrored from the Snapshot rather than tracked
	// separately: a MIDI crossfader move arrives as an Action, is applied to the
	// engine, and comes back through the Snapshot on the next tick. Keeping a
	// second copy would let it drift and leave two parts of the interface
	// disagreeing about where the fader is.
	m.st.Xfade = s.Crossfader

	// The focused panel's dBEAT readout needs the other deck's state. It is
	// carried as a value here so the view stays pure.
	if len(s.Decks) > 0 {
		other := (m.st.Focus + 1) % len(s.Decks)
		m.st.OtherDeck = s.Decks[other]
	}
}

// RenderAt renders the interface at exactly w×h.
//
// It is the pure entry point the tests exercise: no I/O, no globals, no engine
// access beyond a Snapshot value copy. Every line is truncated to w display
// columns and the output is capped at h lines, because a wrapped line or an
// over-tall frame corrupts a full-screen app.
func (m *Model) RenderAt(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}

	if m.panicOnRender {
		// The hook panics from inside the real render path, so the recovery in
		// View is genuinely exercised rather than short-circuited by a flag
		// check above it.
		panic("forced render panic (test hook)")
	}

	var body string
	switch m.st.Mode {
	case ModeLibrary:
		body = views.RenderLibrary(w, h, m.libOrEmpty(), m.libState())
	case ModeOverlay:
		body = views.RenderOverlay(w, h, m.libOrEmpty(), m.st.Query, m.st.TrackSel)
	case ModeHelp:
		var bindings []string
		if m.keys != nil {
			bindings = m.keys.DescribeLines()
		}
		body = views.RenderHelp(w, h, bindings)
	case ModeDebug:
		body = views.RenderDebug(w, h, m.snapshot(), m.stats)
	default:
		body = Render(w, h-1, m.snapshot(), m.st)
	}

	lines := splitLines(body)

	// Cap to h and pad every line to exactly w so the frame is a clean
	// rectangle; an unpadded ragged frame makes bubbletea repaint noise.
	if len(lines) > h {
		lines = lines[:h]
	}
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = padRight(lines[i], w)
		} else {
			out[i] = spaces(w)
		}
	}
	if h > 0 && m.st.Mode == ModeMix {
		// The footer names the active mode's keys, so a key that means
		// different things in two places is never a surprise.
		out[h-1] = padRight(dimStyle.Render(truncate(modeFooter(m.st.Mode), w)), w)
	}
	return strings.Join(out, "\n")
}

// libOrEmpty returns the library, or an empty one, so a view is never handed a
// nil index.
func (m *Model) libOrEmpty() *library.Index {
	if m.lib != nil {
		return m.lib
	}
	return library.NewIndex()
}

func (m *Model) libState() views.LibraryState {
	return views.LibraryState{TrackSel: m.st.TrackSel, Query: m.st.Query, ShowSort: m.st.ShowSort}
}

func (m *Model) snapshot() engine.Snapshot {
	if m.eng == nil {
		return engine.Snapshot{}
	}
	return m.eng.Snapshot()
}

// View renders using the last known terminal size.
//
// The recover here is load-bearing (review focus #4): a panic in the render path
// must not take the interface down while audio keeps playing, because the user
// would hear the set continuing with no way to stop it. We show a minimal
// screen instead.
func (m *Model) View() (out string) {
	// The recovery must name a result: a bare `defer recover()` leaves the
	// return value as its zero value, so a genuine panic produced an empty
	// frame -- a blank screen, which is precisely what review focus 4 says
	// must not happen. Audio keeps playing and the user has no way to tell the
	// interface died.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(devNull{}, "mix-tui: render panic: %v\n", r)
			out = m.errorFrame(fmt.Sprintf("%v", r))
		}
	}()
	return m.RenderAt(m.w, m.h)
}

// errorFrame is the screen shown when rendering panics: it says what happened
// and what to do, rather than going blank.
func (m *Model) errorFrame(reason string) string {
	w, h := m.w, m.h
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	lines := []string{
		titleStyle.Render("mix-tui: the interface hit an error"),
		"",
		"  " + TruncateTo(reason, maxInt(0, w-4)),
		"",
		dimStyle.Render("  audio is still playing"),
		dimStyle.Render("  press q to quit"),
	}
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = padRight(lines[i], w)
		} else {
			out[i] = spaces(w)
		}
	}
	return strings.Join(out, "\n")
}

// devNull discards render-panic diagnostics; the TUI has no stderr of its own
// and must not block writing to a terminal it does not own.
type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// keyMsg and tickMsg are the two message kinds the model handles. keyMsg is
// the plain string form of a key so tests need no terminal.
type keyMsg string

// tickMsg is the periodic refresh.
type tickMsg struct{ t time.Time }

// splitLines splits rendered output into lines for assertions.
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
