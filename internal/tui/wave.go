package tui

// WaveSpec describes a waveform row to draw. Task 4 replaces this with the real
// braille renderer; the shape exists now so panels can carry waveform rows.
type WaveSpec struct {
	// Samples are interleaved stereo, or empty to draw a flat line.
	Samples []float32
	// Playhead is a 0..1 position; samples before it draw as "played".
	Playhead float64
	// Bars is how many braille rows tall the waveform is.
	Bars int
}

// Render draws the waveform into exactly w columns.
func (s *WaveSpec) Render(w int) string {
	if s == nil || w <= 0 {
		return spaces(w)
	}
	// Placeholder until Task 4: a flat centre line. The contract that matters
	// now is the exact width, so panels compose correctly.
	if len(s.Samples) == 0 {
		return spaces(w)
	}
	return spaces(w)
}
