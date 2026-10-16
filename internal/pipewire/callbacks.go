package pipewire

/*
#include <stdlib.h>
#include <spa/pod/pod.h>

extern int format_rate(const struct spa_pod *param);
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// This file holds the C-visible callbacks.
//
// The //export directive forbids C types in an exported function's signature,
// so the entry point below is written in plain Go types and trampoline.c
// supplies a C shim carrying the real signature.
//
// The import "C" below is load-bearing even though no C symbol is referenced
// here: cgo only scans files that import "C" for //export directives, so a
// file that omits it has its exported function silently dropped. The package
// then builds, archives, and fails to link with an undefined reference — a
// failure that appears only when something actually links an executable.

// streamStateChanged reports a state transition. It runs on the loop thread,
// not the realtime one, but the same discipline applies: recover, keep it
// trivial.
//
//export streamStateChanged
func streamStateChanged(data unsafe.Pointer, old, state int32, errs unsafe.Pointer) {
	defer func() { _ = recover() }()
	s, ok := streamFromHandle(cgo.Handle(uintptr(data)))
	if !ok || s == nil {
		return
	}
	s.state.Store(int64(state))
	if errs != nil {
		// Copied into C memory: the daemon's pointer is only valid for this
		// callback, but the value is read later from State(). The previous
		// copy is freed here rather than at Destroy, because a stream that
		// reconnects reports several states and would otherwise accumulate one
		// leaked string per transition.
		s.stateErrMu.Lock()
		old := s.stateErr
		s.stateErr = C.CString(cstr(errs))
		s.stateErrMu.Unlock()
		if old != nil {
			C.free(unsafe.Pointer(old))
		}
	}
}

// recordGrant stores the rate the daemon granted and brings the mismatch flag
// in line with it.
//
// The flag is mirrored, not latched. It used to be stored true and never stored
// false, so one early disagreement -- the daemon reconfiguring the stream during
// startup, before it settles on the requested rate -- pinned RateMismatch on for
// the life of the process. The overlay and mixplay's stats both re-read it on
// every refresh, so from then on they warned that the mix was playing at the
// wrong speed while the two rates on screen matched, with nothing on screen to
// tell a stale warning from a live one.
//
// The comparison is the entire truth: the daemon's most recent grant either is
// the rate we declared or it is not. NegotiatedRate beside it is what makes the
// warning actionable, since it carries the rate that actually disagreed.
func (s *Stream) recordGrant(rate int) {
	// A pod stating no format is not a grant of zero, so the last known rate
	// stands. Only a positive rate is a claim about what the device is running.
	if rate <= 0 {
		return
	}
	s.negotiated.Store(int64(rate))
	s.rateMismatch.Store(rate != s.cfg.SampleRate)
}

// streamParamChanged is where the negotiated format is captured.
//
// The daemon tells us the format it granted here, which may differ from the
// one requested; recording it is the only way to notice a mismatch rather
// than render 44.1k audio into a 48k stream.
//
//export streamParamChanged
func streamParamChanged(data unsafe.Pointer, id uint32, param unsafe.Pointer) {
	defer func() { _ = recover() }()
	s, ok := streamFromHandle(cgo.Handle(uintptr(data)))
	if !ok || s == nil {
		return
	}
	// Record the rate the daemon actually granted. This is the only place the
	// device's true rate becomes visible; without it there is no way to notice
	// that we declared 44100 and the graph gave us 48000, which plays every
	// track at the wrong speed.
	s.recordGrant(int(C.format_rate((*C.struct_spa_pod)(param))))
}

// streamProcess is the realtime drain. This is spec §4.3's contract.
//
// It runs on libpipewire's real-time data-loop thread. Everything that could
// stall the thread is excluded by construction: the work is one ring read and
// two atomic increments, no locks, no allocation, no logging, no map lookups.
// A single defer, the recover, guards against a panic crossing the cgo
// boundary — which aborts the process rather than unwinding, taking the audio
// thread with it.
//
//export streamProcess
func streamProcess(data unsafe.Pointer) {
	defer func() { _ = recover() }()

	s, ok := streamFromHandle(cgo.Handle(uintptr(data)))
	if !ok || s == nil {
		return
	}
	s.drain()
}

// streamFromHandle recovers the stream behind a handle token.
func streamFromHandle(h cgo.Handle) (*Stream, bool) {
	if h == 0 {
		return nil, false
	}
	v := h.Value()
	s, ok := v.(*Stream)
	return s, ok
}
