package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// RenderHelp draws the keybinding list.
//
// Bindings come from config.KeyResolver.Bindings(), already sorted, so the
// listing does not reorder itself between openings — a help screen that
// reshuffles is one nobody scans.
func RenderHelp(w, h int, bindings []string) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := make([]string, 0, h)
	lines = append(lines, pad(titleStyle.Render("KEYS"), w))
	lines = append(lines, pad(dimStyle.Render(strings.Repeat("-", minInt(w, 40))), w))

	if len(bindings) == 0 {
		lines = append(lines, pad(dimStyle.Render("  no bindings configured"), w))
	}
	// Bindings arrive as "key  description". Align the key column so the
	// descriptions line up; a ragged second column is what makes a help screen
	// hard to scan.
	keyW := 0
	for _, b := range bindings {
		if k := keyColumn(b); lipgloss.Width(k) > keyW {
			keyW = lipgloss.Width(k)
		}
	}
	keyW = minInt(keyW, 16)
	for _, b := range bindings {
		k, desc := splitBinding(b)
		row := padRight(k, keyW) + "  " + desc
		lines = append(lines, pad(labelStyle.Render(truncate(row, w)), w))
	}
	lines = append(lines, "")
	lines = append(lines, pad(dimStyle.Render("esc  back to decks"), w))
	return frame(lines, w, h)
}

// keyColumn is the leading whitespace-delimited token of a binding line. It
// returns "" for a blank line rather than indexing an empty slice.
func keyColumn(b string) string {
	f := strings.Fields(b)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// splitBinding separates "key  description" on the first run of two spaces.
func splitBinding(b string) (key, desc string) {
	if i := strings.Index(b, "  "); i >= 0 {
		return strings.TrimSpace(b[:i]), strings.TrimSpace(b[i:])
	}
	return strings.TrimSpace(b), ""
}
