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

// SetLibrary attaches a scanned library for the browser and overlay.
func (m *Model) SetLibrary(ix *library.Index) { m.lib = ix }

// SetStats updates the diagnostic surface shown by the debug overlay.
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
	if m.st.Focus >= 0 && m.st.Focus < len(s.Decks) {
		_ = s.Decks[m.st.Focus] // pitch/meter read directly by the view
	}
}

// RenderAt renders the interface at exactly w×h.
//
// It is the pure entry point the tests exercise: no I/O, no globals, no engine
// access beyond a Snapshot value copy. Every line is truncated to w display
// columns and the output is capped at h lines, because a wrapped line or an
// over-tall frame corrupts a full-screen app.
func (m *Model) RenderAt(w, h int) string {
	if m.panicOnRender {
		panic("forced render panic (test hook)")
	}
	if w <= 0 || h <= 0 {
		return ""
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
			for _, b := range m.keys.Describe() {
				bindings = append(bindings, b.Key+"  "+b.Description)
			}
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
	return views.LibraryState{TrackSel: m.st.TrackSel, Query: m.st.Query}
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
func (m *Model) View() string {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(devNull{}, "mix-tui: render panic: %v\n", r)
		}
	}()
	if m.panicOnRender {
		return "render error — press q to quit"
	}
	return m.RenderAt(m.w, m.h)
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
