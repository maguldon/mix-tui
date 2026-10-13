package views

import "strings"

// settingsLabelW is the width of the label column, so the values line up.
const settingsLabelW = 14

// SettingsState is everything the settings view draws.
//
// It is a value, not a pointer to the model, so the view stays a pure function
// of its inputs and the test suite can assert on a frame without an engine, a
// terminal, or a config file.
type SettingsState struct {
	// Root is the configured library root, after "~" expansion.
	Root string
	// FlagRoot is the -library value, when one was given. It is shown because it
	// wins over Root: without saying so, a user edits the config, presses enter,
	// and watches the scan return the same tracks, with nothing on screen
	// explaining why.
	FlagRoot string
	// ConfigPath is the file the edit is written to.
	ConfigPath string
	// Tracks is how many tracks the current index holds.
	Tracks int
	// Busy is true while a rescan is running.
	Busy bool
	// Note is the last result: what was saved, or what a scan found.
	Note string
	// Input is the editable path field.
	Input string
}

// RenderSettings draws the settings screen: what the library is set to, and a
// field to change it.
//
// It has no minimum width, unlike the full library browser. This is the screen
// a user reaches for when the browser is showing them nothing, so refusing to
// draw because the terminal is narrow would leave them with no way forward at
// exactly the moment they need one.
func RenderSettings(w, h int, st SettingsState) string {
	if w <= 0 || h <= 0 {
		return ""
	}

	lines := make([]string, 0, 16)
	lines = append(lines, titleStyle.Render("SETTINGS"))
	lines = append(lines, dimStyle.Render(strings.Repeat("-", minInt(w, 60))))

	lines = append(lines, settingsRow(w, "library root", rootDisplay(st.Root), st.Root == ""))
	lines = append(lines, settingsRow(w, "tracks", itoa(st.Tracks), st.Tracks == 0))
	lines = append(lines, settingsRow(w, "config", st.ConfigPath, st.ConfigPath == ""))

	if st.FlagRoot != "" {
		lines = append(lines, "")
		lines = append(lines, warnStyle.Render(pad(truncate(
			"  -library "+st.FlagRoot+" overrides the config for this run", w), w)))
	}

	lines = append(lines, "")
	lines = append(lines, labelStyle.Render(pad(truncate("set root", w), w)))
	lines = append(lines, accentStyle.Render(pad(truncate("  "+st.Input+"_", w), w)))
	lines = append(lines, dimStyle.Render(strings.Repeat("-", minInt(w, 60))))

	switch {
	case st.Busy:
		lines = append(lines, warnStyle.Render(pad(truncate("scanning... the decks keep playing", w), w)))
	case st.Note != "":
		lines = append(lines, dimStyle.Render(pad(truncate(st.Note, w), w)))
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(pad(truncate("⏎ scan and save    esc cancel", w), w)))
	return frame(lines, w, h)
}

// settingsRow renders one "label  value" line, dimming an empty value so a
// missing setting reads as missing rather than as blank space.
func settingsRow(w int, label, value string, empty bool) string {
	row := padRight(label, settingsLabelW) + "  " + value
	if empty {
		return dimStyle.Render(pad(truncate(row, w), w))
	}
	return labelStyle.Render(pad(truncate(row, w), w))
}

// rootDisplay renders an unset root as something readable rather than empty.
func rootDisplay(root string) string {
	if root == "" {
		return "(not set -- the library is empty)"
	}
	return root
}
