package tui

// Block characters for the level meter, quietest to loudest. These are all
// single-width, so a meter never shifts the columns around it.
const meterBlocks = " ▁▂▃▄▅▆▇█"

// fullBlock is the lit end of the bar, held separately so the meter can be
// built from runes (meterBlocks is a byte-indexed string literal).
const fullBlock = '█'

// RenderMeter draws a level bar from v (nominal 0..1) at exactly width columns.
//
// Values outside 0..1 are clamped rather than allowed to overflow the bar: a
// meter that grows past its column pushes everything after it sideways.
func RenderMeter(v float64, width int) string {
	if width <= 0 {
		return ""
	}
	v = clampFloat(v, 0, 1)
	filled := int(v*float64(width) + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	// Build with runes, not bytes: the block glyphs are three UTF-8 bytes each,
	// so appending bytes would produce a bar a third of its intended width.
	b := make([]rune, width)
	for i := 0; i < width; i++ {
		if i < filled {
			b[i] = fullBlock
		} else {
			b[i] = ' '
		}
	}
	return string(b)
}

// RenderPeakHold draws the held peak as a marker within a level bar.
func RenderPeakHold(peak, current float64, width int) string {
	if width <= 0 {
		return ""
	}
	bar := RenderMeter(current, width)
	p := clampFloat(peak, 0, 1)
	idx := int(p*float64(width-1) + 0.5)
	if idx < 0 {
		idx = 0
	}
	if idx >= width {
		idx = width - 1
	}
	b := []rune(bar)
	if b[idx] == ' ' {
		b[idx] = '.'
	}
	return string(b)
}
