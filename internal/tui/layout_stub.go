package tui

import (
	"fmt"
	"strings"

	"github.com/maguldon/mix-tui/internal/engine"
)

// renderLayout renders the current layout. Task 2 replaces this with the real
// panel composition; for now it produces a bounded, correct-shaped frame so the
// model's sizing, truncation, and recovery behaviour are testable.
func renderLayout(st ViewState, w, h int, s engine.Snapshot) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(truncate(fmt.Sprintf("mix-tui  layout %c  focus %d", layoutRune(st.Layout), st.Focus), w)))
	b.WriteByte('\n')

	if st.LastErr != "" {
		b.WriteString(warnStyle.Render(truncate("! "+st.LastErr, w)))
		b.WriteByte('\n')
	}

	deckLines := 1 + len(s.Decks)
	for i, d := range s.Decks {
		if i >= deckLines-1 || i+1 > h {
			break
		}
		b.WriteString(deckLine(d, w))
		b.WriteByte('\n')
	}
	return b.String()
}

func deckLine(d engine.DeckState, w int) string {
	title := d.Title
	if title == "" {
		title = "--"
	}
	state := "stopped"
	if d.Loaded {
		state = "stopped"
		if d.Playing {
			state = "PLAY"
		}
	}
	left := fmt.Sprintf("deck %d %-16s %-7s", d.Index, title, state)
	if d.HasBeatGrid {
		left += fmt.Sprintf(" %3.0fbpm", d.BPM)
	}
	return dimStyle.Render(truncate(left, w))
}

func layoutRune(l LayoutKind) rune { return rune("DCBA"[int(l)&3]) }
