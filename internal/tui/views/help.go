package views

import "strings"

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
	for _, b := range bindings {
		lines = append(lines, pad(labelStyle.Render(truncate(b, w)), w))
	}
	lines = append(lines, "")
	lines = append(lines, pad(dimStyle.Render("esc  back"), w))
	return frame(lines, w, h)
}
