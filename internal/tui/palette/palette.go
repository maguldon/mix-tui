// Package palette holds the interface's colours and styles.
//
// It is a leaf package on purpose. The palette used to be declared twice --
// once in internal/tui and once in internal/tui/views -- and the two copies had
// already drifted, most visibly on the word "accent", which meant #58a6ff in one
// file and #7ee787 in the other. A shared package cannot fix that on its own
// because internal/tui imports internal/tui/views, so a views-to-tui import
// would be a cycle. A leaf both can import is the only arrangement that gives
// one definition.
package palette

import "github.com/charmbracelet/lipgloss"

// The colours. Every view refers to these names rather than embedding a hex
// literal, so recolouring the whole interface is a single edit and the colours
// cannot drift between panels.
var (
	// Played is waveform behind the playhead, Unplayed is waveform ahead of it.
	// Spec 6.2 asks for exactly this split: "played audio renders in one
	// colour, unplayed in another". These two existed with those comments for
	// the whole life of the project and were never applied to anything.
	Played   = lipgloss.Color("#f97583")
	Unplayed = lipgloss.Color("#316dca")
	Beat     = lipgloss.Color("#ffa657") // beatgrid downbeats
	Cue      = lipgloss.Color("#d2a8ff") // hotcues
	Loop     = lipgloss.Color("#ffa657") // active loop edges
	Playhead = lipgloss.Color("#f0f6fc") // the playhead column
	Ok       = lipgloss.Color("#7ee787") // active or armed
	// Dim is recessed-but-readable content: the artist name, the progress bar,
	// the EQ and filter meters, the off-state transport labels, and the
	// secondary rows of every overlay.
	//
	// It was #3d444d with the comment "box drawing", which was both wrong about
	// its own use -- it is applied to body text in every panel and every view --
	// and unreadable: against a #0d1117-class background that is 1.9:1, well
	// under WCAG AA's 4.5:1. Box drawing has its own entry below, at 1.6:1,
	// which is what a frame is allowed to be.
	//
	// #7d8590 is GitHub's fg.muted, the same scale this palette is otherwise
	// drawn from, and measures 5.1:1. The resulting ramp is deliberate and
	// monotonic: Border 1.6 < Dim 5.1 < Text 12.3, with Accent above all of them.
	Dim    = lipgloss.Color("#7d8590")
	Warn   = lipgloss.Color("#ffa657") // low-confidence key
	Border = lipgloss.Color("#30363d")
	Text   = lipgloss.Color("#c9d1d9")
	Accent = lipgloss.Color("#58a6ff")
)

// The shared styles. lipgloss styles are values, so these are cheap to copy.
var (
	TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	LabelStyle = lipgloss.NewStyle().Foreground(Text)
	DimStyle   = lipgloss.NewStyle().Foreground(Dim)
	WarnStyle  = lipgloss.NewStyle().Foreground(Warn)
	ErrStyle   = lipgloss.NewStyle().Bold(true).Foreground(Warn)
	OkStyle    = lipgloss.NewStyle().Foreground(Ok)
	BorderBox  = lipgloss.NewStyle().Foreground(Border)
)
