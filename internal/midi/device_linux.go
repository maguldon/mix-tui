//go:build linux

package midi

// This is the only file in the module that imports gitlab.com/gomidi. Every
// layer above it works on the internal Message type, which is what makes the
// resolver, value modes, and modifier logic testable with no controller
// attached.
//
// The wire conversion below deliberately takes raw status bytes rather than
// gomidi message types: it keeps the conversion pure, so its tests run with no
// hardware and no driver.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"gitlab.com/gomidi/midi/v2/drivers"
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv" // registers the rtmididrv driver
)

// Status-byte nibbles and system bytes, from the MIDI 1.0 wire format.
const (
	statusNoteOff       = 0x80
	statusNoteOn        = 0x90
	statusPolyPressure  = 0xA0
	statusControlChange = 0xB0
	statusProgramChange = 0xC0
	statusChannelPress  = 0xD0
	statusPitchBend     = 0xE0

	statusSysex         = 0xF0
	statusSongPos       = 0xF2
	statusSongSelect    = 0xF3
	statusTuneRequest   = 0xF6
	statusClock         = 0xF8
	statusStart         = 0xFA
	statusContinue      = 0xFB
	statusStop          = 0xFC
	statusActiveSensing = 0xFE
	statusSystemReset   = 0xFF
)

// clamp127 keeps a value inside the 7-bit MIDI data range.
func clamp127(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 127 {
		return 127
	}
	return uint8(v)
}

// clampChannel keeps a channel inside 0-15. A malformed or out-of-range
// channel is clamped rather than wrapped, so a bad byte cannot silently
// retarget a control onto another channel.
func clampChannel(ch int) uint8 {
	if ch < 0 {
		return 0
	}
	if ch > 15 {
		return 15
	}
	return uint8(ch)
}

// fromWireCC builds a control-change Message from raw wire fields.
func fromWireCC(ch, num, val int) Message {
	return Message{Kind: KindCC, Channel: clampChannel(ch), Num: clamp127(num), Value: clamp127(val)}
}

// fromWireNote builds a note Message from raw wire fields. A velocity of 0 is a
// note-off and is preserved as such, so controls release.
func fromWireNote(ch, num, vel int) Message {
	return Message{Kind: KindNote, Channel: clampChannel(ch), Num: clamp127(num), Velocity: clamp127(vel)}
}

// fromWireRaw classifies a bare system status byte.
func fromWireRaw(status byte) Message {
	var cat OtherCategory
	switch status {
	case statusSysex:
		cat = OtherSysex
	case statusProgramChange:
		cat = OtherProgramChange
	case statusPitchBend:
		cat = OtherPitchBend
	case statusChannelPress, statusPolyPressure:
		cat = OtherAftertouch
	case statusClock:
		cat = OtherClock
	case statusStart, statusContinue, statusStop:
		cat = OtherTransport
	default:
		// Song position, song select, tune request, active sensing, and reset
		// are ignored too; bucket them with transport so they still show a
		// non-zero count rather than vanishing.
		cat = OtherTransport
	}
	return Message{Kind: KindOther, Other: cat}
}

// fromWire converts a raw MIDI message into the internal Message.
//
// A truncated message yields a KindOther rather than indexing past the end: a
// malformed byte from a flaky device must not crash the process mid-set.
func fromWire(raw []byte) Message {
	if len(raw) == 0 {
		return Message{Kind: KindOther, Other: OtherTransport}
	}
	status := raw[0]
	if status >= 0xF0 {
		return fromWireRaw(status)
	}
	kind := status & 0xF0
	ch := int(status & 0x0F)
	switch kind {
	case statusNoteOff, statusNoteOn:
		if len(raw) < 3 {
			return Message{Kind: KindOther, Other: OtherTransport}
		}
		return fromWireNote(ch, int(raw[1]), int(raw[2]))
	case statusControlChange:
		if len(raw) < 3 {
			return Message{Kind: KindOther, Other: OtherTransport}
		}
		return fromWireCC(ch, int(raw[1]), int(raw[2]))
	default:
		return fromWireRaw(status)
	}
}

// DeviceInfo describes one connected MIDI input port.
type DeviceInfo struct {
	Name  string
	Index int
}

// newDriver returns the registered MIDI driver. The rtmididrv blank import
// above registers itself, so this is the ALSA-seq backed driver on Linux. Get
// returns nil when nothing registered, which is not an error we can recover
// from, so it is reported as ErrNoDriver.
func newDriver() (drivers.Driver, error) {
	d := drivers.Get()
	if d == nil {
		return nil, ErrNoDriver
	}
	return d, nil
}

// OpenDevices lists connected MIDI input ports. A machine with no MIDI hardware
// yields an empty list, not an error: the caller decides whether that is fatal.
func OpenDevices() ([]DeviceInfo, error) {
	d, err := newDriver()
	if err != nil {
		return nil, err
	}
	defer d.Close()

	ins, err := d.Ins()
	if err != nil {
		return nil, err
	}
	out := make([]DeviceInfo, 0, len(ins))
	for _, in := range ins {
		out = append(out, DeviceInfo{Name: in.String(), Index: in.Number()})
	}
	return out, nil
}

// Listener reads every connected MIDI input and feeds messages to a Handler.
type Listener struct {
	drv     drivers.Driver
	h       *Handler
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closeMu sync.Mutex
	closed  bool
}

// OpenListener enumerates input ports and starts a goroutine per port feeding h.
func OpenListener(h *Handler) (*Listener, error) {
	d, err := newDriver()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &Listener{drv: d, h: h, cancel: cancel}

	ins, err := d.Ins()
	if err != nil {
		cancel()
		d.Close()
		return nil, err
	}
	for _, in := range ins {
		port := in
		if err := port.Open(); err != nil {
			// Skip a port we cannot open rather than failing the whole listener.
			continue
		}
		stop, err := port.Listen(func(raw []byte, _ int32) {
			// Check cancellation so a message is not handled after teardown.
			select {
			case <-ctx.Done():
				return
			default:
			}
			_ = l.h.Handle(fromWire(raw))
		}, drivers.ListenConfig{})
		if err != nil {
			_ = port.Close()
			continue
		}
		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			<-ctx.Done()
			stop()
			_ = port.Close()
		}()
	}
	return l, nil
}

// Devices lists the ports this listener is reading.
func (l *Listener) Devices() []DeviceInfo {
	if l == nil || l.drv == nil {
		return nil
	}
	ins, err := l.drv.Ins()
	if err != nil {
		return nil
	}
	out := make([]DeviceInfo, 0, len(ins))
	for _, in := range ins {
		out = append(out, DeviceInfo{Name: in.String(), Index: in.Number()})
	}
	return out
}

// Close stops reading and waits for the port goroutines. It is idempotent.
func (l *Listener) Close() error {
	if l == nil {
		return nil
	}
	l.closeMu.Lock()
	if l.closed {
		l.closeMu.Unlock()
		return nil
	}
	l.closed = true
	l.closeMu.Unlock()

	l.cancel()
	l.wg.Wait()
	if l.drv != nil {
		return l.drv.Close()
	}
	return nil
}

// ErrNoDriver reports that no MIDI driver could be created (no ALSA, or the
// driver failed to initialise).
var ErrNoDriver = errors.New("midi: no driver available")

// describeDriver wraps a driver-creation failure with enough context to act on.
func describeDriver(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrNoDriver, err)
}
