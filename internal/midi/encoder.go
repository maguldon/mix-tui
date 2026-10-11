package midi

// RelativeNull is the encoder null point from spec §5.2: a relative encoder
// rests at 64 and reports small positive/negative offsets from it.
const RelativeNull = 64

// Relative encoders report small positive/negative offsets from the null point.
// The scale sets how far one detent moves the control: dividing the offset by
// RelativeSpan makes the full ±63 range span the control's full travel, so a
// typical ±2 detent moves ~1/32 of the way and ~32 detents saturate it. This is
// a design decision, not a magic number — a smaller step makes a jog wheel feel
// unresponsive, a larger one makes one detent slam a fader to the rail (review
// focus #1).
const RelativeSpan = 64.0

// Absolute maps a 0..127 CC to -1..1, with 64 at centre.
func Absolute(v uint8) float64 { return float64(v)/127.0*2.0 - 1.0 }

// RelativeDelta converts a relative-encoder value to an increment. The null
// point yields exactly zero and one detent is a small step, so a jog wheel
// accumulates rather than jumping.
func RelativeDelta(v uint8) float64 {
	return float64(int(v)-RelativeNull) / RelativeSpan
}
