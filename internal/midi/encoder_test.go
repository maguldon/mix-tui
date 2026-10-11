package midi

import (
	"math"
	"testing"

	"github.com/maguldon/mix-tui/internal/engine"
)

// last is the most recent Action emitted by the handler under test.
var last engine.Action

func encoderMapping(t *testing.T) *Table {
	return tableFrom(t, `
device: "T"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
    mode: relative
`)
}

func absoluteMapping(t *testing.T) *Table {
	return tableFrom(t, `
device: "T"
controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
    mode: absolute
`)
}

func TestAbsoluteMapsFullRange(t *testing.T) {
	if math.Abs(Absolute(0)+1) > 1e-9 {
		t.Errorf("Absolute(0) = %v", Absolute(0))
	}
	if math.Abs(Absolute(127)-1) > 1e-9 {
		t.Errorf("Absolute(127) = %v", Absolute(127))
	}
	if math.Abs(Absolute(64)) > 0.01 {
		t.Errorf("Absolute(64) = %v, want ~0", Absolute(64))
	}
}

func TestRelativeDeltaAroundNull(t *testing.T) {
	// Review Focus #1: one detent must move the value a little, not to the rail
	if d := RelativeDelta(RelativeNull); d != 0 {
		t.Errorf("null point must be 0, got %v", d)
	}
	if d := RelativeDelta(RelativeNull + 2); math.Abs(d) > 0.05 {
		t.Errorf("single detent %v is too large", d)
	}
	if RelativeDelta(RelativeNull-2)*RelativeDelta(RelativeNull+2) >= 0 {
		t.Error("up and down detents must have opposite signs")
	}
}

func TestRelativeEncoderAccumulatesNotJumps(t *testing.T) {
	// Review Focus #1: 10 detents up from centre must reach a modest value,
	// not +1.0. This is the jog-wheel bug.
	h := NewHandler(encoderMapping(t), func(a engine.Action) { last = a })
	for i := 0; i < 10; i++ {
		h.Handle(Message{Kind: KindCC, Channel: 1, Num: 9, Value: RelativeNull + 2})
	}
	if last.Value <= 0.05 || last.Value >= 0.5 {
		t.Errorf("10 detents gave Value = %v, want a small increment", last.Value)
	}
}

func TestRelativeEncoderReachesFullScaleEventually(t *testing.T) {
	h := NewHandler(encoderMapping(t), func(a engine.Action) { last = a })
	for i := 0; i < 100; i++ {
		h.Handle(Message{Kind: KindCC, Channel: 1, Num: 9, Value: RelativeNull + 2})
	}
	if last.Value < 0.99 {
		t.Errorf("100 detents gave %v, want ~1.0 (clamped)", last.Value)
	}
}

func TestAbsoluteControlIgnoresNullPoint(t *testing.T) {
	h := NewHandler(absoluteMapping(t), func(a engine.Action) { last = a })
	h.Handle(Message{Kind: KindCC, Channel: 1, Num: 9, Value: 127})
	if math.Abs(last.Value-1) > 1e-6 {
		t.Errorf("absolute 127 gave %v, want 1", last.Value)
	}
}

func TestValueIsClamped(t *testing.T) {
	h := NewHandler(encoderMapping(t), func(a engine.Action) { last = a })
	for i := 0; i < 500; i++ {
		h.Handle(Message{Kind: KindCC, Channel: 1, Num: 9, Value: RelativeNull + 2})
	}
	if last.Value > 1.0 {
		t.Errorf("Value escaped range: %v", last.Value)
	}
}

// TestValueIsClampedLow guards the negative rail too: turning a jog wheel down
// forever must stop at -1, not wrap.
func TestValueIsClampedLow(t *testing.T) {
	h := NewHandler(encoderMapping(t), func(a engine.Action) { last = a })
	for i := 0; i < 500; i++ {
		h.Handle(Message{Kind: KindCC, Channel: 1, Num: 9, Value: RelativeNull - 2})
	}
	if last.Value < -1.0 {
		t.Errorf("Value escaped low range: %v", last.Value)
	}
}
