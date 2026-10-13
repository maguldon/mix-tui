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
	Dim      = lipgloss.Color("#3d444d") // box drawing
	Warn     = lipgloss.Color("#ffa657") // low-confidence key
	Border   = lipgloss.Color("#30363d")
	Text     = lipgloss.Color("#c9d1d9")
	Accent   = lipgloss.Color("#58a6ff")
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
