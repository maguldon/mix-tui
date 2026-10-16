//go:build linux

package pipewire

import (
	"fmt"
	"runtime"
	"runtime/cgo"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/engine"
)

/*
#cgo CFLAGS: -I/usr/include/pipewire-0.3 -I/usr/include/spa-0.2 -D_REENTRANT
#include <pipewire/pipewire.h>
#include <spa/buffer/buffer.h>
#include <stdlib.h>
#include <string.h>

// Defined in trampoline.c.
extern void stream_state_trampoline(void *data, int32_t old, int32_t state,
                                    const char *error);
extern void stream_param_trampoline(void *data, uint32_t id,
                                    const struct spa_pod *param);
extern void stream_process_trampoline(void *data);
extern void add_stream_listener(struct pw_stream *stream, struct spa_hook *hook,
                                struct pw_stream_events *ev, uintptr_t handle);

// allocStreamEvents / allocStreamHook hand back C memory, because
// libpipewire retains both for the life of the listener.
struct pw_stream_events *alloc_stream_events(void);
struct spa_hook *alloc_stream_hook(void);
extern int set_stream_active(struct pw_stream *stream, int active);

extern struct spa_pod **make_connect_params(int rate, int channels);
extern int format_rate(const struct spa_pod *param);
extern int connect_params_selfcheck(int rate, int channels);

extern int format_rate(const struct spa_pod *param);
extern int connect_params_selfcheck(int rate, int channels);


extern struct pw_properties *make_stream_props(const char *node_name,
                                              const char *target,
                                              int rate, int channels);
*/
import "C"

// StreamConfig describes one playback stream.
type StreamConfig struct {
	// NodeName is the stream's name as it appears in the graph.
	NodeName string
	// Target optionally names a node.name or node.id to connect to. Empty
	// means the daemon's default sink.
	Target string
	// SampleRate is the rate the stream declares. It must match the engine's.
	SampleRate int
	// Channels is 2 for this project; the drain path assumes interleaved
	// stereo, which is what dsp.Stereo is.
	Channels int
}

type Stats struct {
	// Callbacks is the number of drain callback invocations.
	Callbacks uint64
	// FramesDrained is the number of frames actually delivered to the device.
	FramesDrained uint64
	// Starves counts callbacks that found the ring empty.
	Starves uint64
	// MaxBufferFrames is the largest single buffer the device has asked for.
	//
	// The pump needs it: a ring kept at a fixed target can only serve a
	// target-sized callback, so when the device asks for more than the target
	// the pump must raise its target or every callback comes up short. That
	// number is only obtainable by observing the device, which is what this is.
	MaxBufferFrames int
	// Shorts counts callbacks where the ring held SOME audio but not as much as
	// the device asked for, so part of the buffer the device wanted went out as
	// whatever the driver had in it.
	//
	// This is not a smaller version of Starves and must not be reported as one.
	// Starves counts only the case where the ring was empty, which means a ring
	// that is chronically too SHORT for the buffers the device asks for — the
	// common case, and an audible one — reports zero starves forever while
	// every single callback comes up short.
	Shorts uint64
	// NegotiatedRate is the rate the daemon granted, or 0 if it has not said.
	NegotiatedRate int
	// RateMismatch is true when the daemon's most recent granted rate is
	// other than the declared one. That plays every track at the wrong speed,
	// so it is surfaced rather than left to be discovered by ear.
	//
	// It describes the current disagreement, not whether one ever happened:
	// a grant that later matches the declared rate clears it, so a reader
	// can trust that a true here means the device is mismatched now.
	RateMismatch bool
}

// Stream is one playback stream draining a ring buffer.
//
// Every field the drain callback touches is a plain field or a lock-free
// atomic, never a map, a lock, or anything that can allocate. The callback
// runs on libpipewire's real-time data-loop thread; work there that can block
// or allocate is a dropout generator that only manifests under load.
type Stream struct {
	ctx    *Context
	ring   *audio.Ring
	cfg    StreamConfig
	stream *C.struct_pw_stream

	// handle is the cgo.Handle the C side carries as its user-data. libpipewire
	// retains that pointer for the life of the listener, so it must not be a
	// Go pointer; a handle index is the supported alternative.
	handle cgo.Handle

	callbacks atomic.Uint64
	frames    atomic.Uint64
	starves   atomic.Uint64
	shorts    atomic.Uint64
	// maxBuffer is the largest capacity the device has asked for, in frames.
	maxBuffer atomic.Int64

	// name is a C string libpipewire borrows for the stream's lifetime, so it
	// is owned here and freed in Destroy.
	name *C.char
	// teardown is the drain/teardown barrier. A drain enters it with
	// enter() and leaves with leave(); Destroy closes the gate and then blocks
	// in waitIdle() until every entered drain has left, so it can never be
	// inside pw_stream_dequeue_buffer when pw_stream_destroy runs.
	//
	// This started as a sync.WaitGroup, which is the wrong primitive here: Add
	// racing Wait can let Wait observe a transient zero counter and return
	// while a drain that had not yet incremented is still on its way in. That
	// is a use-after-free, and it showed up as a reproducible SIGSEGV in
	// TestDestroyRacesDrainSafely. A WaitGroup requires a strictly positive
	// count established before any Wait, which a lock-free drain cannot
	// guarantee; an explicit active-counter that is only ever incremented
	// while the gate is open can.
	teardown drainGate

	// events and hook are C-allocated because libpipewire retains them.
	// They are owned here and freed in Destroy.
	events *C.struct_pw_stream_events
	hook   *C.struct_spa_hook
	// state and stateErr record the last state the daemon reported, so a
	// stream that connects but never processes can be diagnosed from outside
	// rather than guessed at.
	// rateMismatch records whether the daemon's latest granted rate differs from
	// the one we declared. Surfaced in Stats so a silent pitch error is
	// visible. Rewritten on every grant rather than only raised, so it
	// reports the disagreement in force instead of the first one seen.
	rateMismatch atomic.Bool
	state        atomic.Int64
	// stateErr is a C string owned by this struct. It is guarded by a mutex
	// rather than an atomic.Pointer: the listener replaces it on every state
	// change, and freeing the old value from Store would race a concurrent
	// State() reader that is mid-read of the pointer it just loaded. Writes
	// happen on the listener thread and reads on the UI thread, neither of
	// which is the realtime drain, so a mutex costs nothing that matters and
	// removes the use-after-free entirely.
	stateErrMu sync.Mutex
	stateErr   *C.char
	started    atomic.Bool
	stopped    atomic.Bool
	// negotiated records the rate the daemon actually gave us, which may
	// differ from the requested one. It stays 0 until streamParamChanged
	// observes a grant, and nothing seeds it.
	negotiated atomic.Int64
}

// NewStream creates a playback stream feeding from ring.
func NewStream(ctx *Context, cfg StreamConfig, ring *audio.Ring) (*Stream, error) {
	if ctx == nil || ring == nil {
		return nil, fmt.Errorf("pipewire: nil context or ring")
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 44100
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 2
	}
	if cfg.Channels != 2 {
		// The drain path indexes interleaved stereo directly. Refusing here
		// is better than corrupting memory later.
		return nil, fmt.Errorf("pipewire: only 2 channels supported, got %d", cfg.Channels)
	}

	s := &Stream{ctx: ctx, ring: ring, cfg: cfg}
	s.handle = cgo.NewHandle(s)

	// Every stream operation must hold the loop lock; see Context.lock.
	held := ctx.lock()
	defer ctx.unlock(held)

	props := buildStreamProps(cfg)

	// pw_stream_new TAKES OWNERSHIP of props. Freeing it here as well is a
	// double free: the stream keeps the pointer, so it dangles, and the
	// resulting heap corruption surfaced as an alternating segfault and
	// "unaligned tcache chunk" abort inside pw_stream_connect.
	//
	// The name, by contrast, is only borrowed — pw_stream_get_name hands it
	// back — so it must outlive the stream and is freed in Destroy.
	s.name = C.CString("Audio/Source")
	s.stream = C.pw_stream_new(ctx.pwcore(), s.name, props)
	if s.stream == nil {
		// Ownership was not taken on the failure path, so free here.
		C.pw_properties_free(props)
		C.free(unsafe.Pointer(s.name))
		s.name = nil
		s.handle.Delete()
		return nil, fmt.Errorf("pipewire: pw_stream_new returned NULL")
	}
	// User data attaches to the LISTENER, not the stream: this version has no
	// pw_stream_set_user_data. The handle travels as the listener's data and
	// reaches every callback.
	// The events struct and the hook must live in C MEMORY, not on the Go
	// heap. libpipewire retains both for the life of the listener and calls
	// through them; a Go-allocated `events` is a local here, so once
	// NewStream returns nothing references it, the collector frees it, and
	// the next callback jumps through freed memory. This is the same cgo
	// pointer rule as the user-data handle, one object over.
	s.events = allocStreamEvents()
	s.hook = allocStreamHook()
	C.add_stream_listener(s.stream, s.hook, s.events, C.uintptr_t(s.handle))
	// `negotiated` is deliberately left at 0 — "the daemon has not said". It is
	// seeded nowhere, because the only writer that can fill it honestly is
	// streamParamChanged, and nothing else has observed the device.
	//
	// It used to be seeded here with cfg.SampleRate, which made the documented
	// zero state unreachable: NewStream has already coerced a non-positive rate
	// to 44100 by this point, so the store could only ever write a positive
	// number, and the overlay's "sample rate 44100 (device 44100)" was the
	// number we asked for echoed back as a confirmation the device never gave.
	// The declared rate is not lost by dropping this — it is cfg.SampleRate, and
	// Output.SampleRate() is what feeds views.DebugStats.DeclaredRate.
	return s, nil
}

// buildStreamProps assembles the stream's properties.
//
// The audio properties are set as STRINGS rather than as assembled format
// bits. spa accepts "F32P" / "44100" / "2" and parses them, which avoids
// naming enum constants that differ between spa releases — this tree's spa
// spells the sample format SPA_AUDIO_FORMAT_F32P where older trees spell it
// SPA_FORMAT_f32_planar, and the same constants moved between headers.
// Interleaved F32P is what matches dsp.Stereo, so the drain is a straight
// copy with no deinterleave step on the audio thread.
func buildStreamProps(cfg StreamConfig) *C.struct_pw_properties {
	name := C.CString(cfg.NodeName)
	defer C.free(unsafe.Pointer(name))
	target := C.CString(cfg.Target)
	defer C.free(unsafe.Pointer(target))
	return C.make_stream_props(name, target, C.int(cfg.SampleRate), C.int(cfg.Channels))
}

// Start begins processing. After it returns, the daemon may call the drain.
func (s *Stream) Start() error {
	if s == nil || s.stream == nil {
		return fmt.Errorf("pipewire: stream not created")
	}
	if !s.started.CompareAndSwap(false, true) {
		return nil // idempotent
	}
	// PW_DIRECTION_OUTPUT: a playback stream PRODUCES audio into the graph.
	held := s.ctx.lock()
	defer s.ctx.unlock(held)
	// Declare the format the stream accepts. A stream with no EnumFormat pod
	// gives the graph nothing to negotiate against, and connect then fails on
	// the half-built object — which is why the helper existed but was never
	// called.
	params := C.make_connect_params(C.int(s.cfg.SampleRate), C.int(s.cfg.Channels))

	if rc := C.pw_stream_connect(s.stream, C.PW_DIRECTION_OUTPUT, C.PW_ID_ANY,
		0, params, 1); rc != 0 {
		return fmt.Errorf("pipewire: pw_stream_connect failed with code %d", int(rc))
	}
	// Connect alone does not start processing: the stream must be made active
	// before the graph drives it, otherwise it sits in "paused" and the drain
	// callback never fires.
	C.set_stream_active(s.stream, 1)

	s.stopped.Store(false)
	return nil
}

// Stop stops processing. It is idempotent.
func (s *Stream) Stop() error {
	if s == nil || s.stream == nil {
		return nil
	}
	if !s.stopped.CompareAndSwap(false, true) {
		return nil
	}
	// pw_stream_disconnect marshals through the loop and blocks for a reply, so
	// it is only valid while the loop is alive. Once the context is closed the
	// loop is freed and this would segfault or hang; mark the stream stopped and
	// return instead. Destroy() releases what remains.
	if s.ctx == nil || !s.ctx.alive() {
		s.started.Store(false)
		return nil
	}
	held := s.ctx.lock()
	defer s.ctx.unlock(held)
	C.pw_stream_disconnect(s.stream)
	// A disconnected pw_stream can be reconnected, so clear started to let a
	// later Start reconnect. Without this, Stop/Start cycles - which Output
	// uses for stop-then-play - silently no-op and the stream stays dead.
	s.started.Store(false)
	return nil
}

// Destroy releases the stream. It must not be called while the drain callback
// can still fire.
func (s *Stream) Destroy() error {
	if s == nil || s.stream == nil {
		return nil
	}
	// Close the gate and wait for any in-flight drain. After this returns, no
	// drain is running and none can start, so freeing the stream cannot race
	// the RT thread.
	s.teardown.closeAndWait()

	// pw_stream_destroy must happen on a live loop, under its lock. The stream's
	// implementation node hangs off that loop, so destroying the stream after
	// the context is gone dereferences freed memory -- it segfaulted.
	//
	// When the context is already closed the loop is gone and there is nothing
	// to destroy through. The stream's own allocations are still freed below;
	// only the call into libpipewire is skipped.
	if s.ctx != nil && s.ctx.alive() {
		held := s.ctx.lock()
		C.pw_stream_destroy(s.stream)
		s.ctx.unlock(held)
	}
	s.stream = nil
	s.stateErrMu.Lock()
	if s.stateErr != nil {
		C.free(unsafe.Pointer(s.stateErr))
		s.stateErr = nil
	}
	s.stateErrMu.Unlock()

	if s.events != nil {
		C.free(unsafe.Pointer(s.events))
		s.events = nil
	}
	if s.hook != nil {
		C.free(unsafe.Pointer(s.hook))
		s.hook = nil
	}
	if s.name != nil {
		C.free(unsafe.Pointer(s.name))
		s.name = nil
	}
	if s.handle != 0 {
		s.handle.Delete()
		s.handle = 0
	}
	return nil
}

// Stats reports drain-thread health.
func (s *Stream) Stats() Stats {
	if s == nil {
		return Stats{}
	}
	return Stats{
		Callbacks:       s.callbacks.Load(),
		FramesDrained:   s.frames.Load(),
		Starves:         s.starves.Load(),
		Shorts:          s.shorts.Load(),
		MaxBufferFrames: int(s.maxBuffer.Load()),
		NegotiatedRate:  int(s.negotiated.Load()),
		RateMismatch:    s.rateMismatch.Load(),
	}
}

// AsRenderTarget adapts the stream to the engine's consumer interface.
func (s *Stream) AsRenderTarget() engine.RenderTarget { return engine.RingTarget(s.ring) }

// State returns the last pw_stream_state the daemon reported, and any error
// string it supplied. Zero is pw_stream_state_unconnected.
func (s *Stream) State() (int32, string) {
	s.stateErrMu.Lock()
	var msg string
	if s.stateErr != nil {
		msg = cstr(unsafe.Pointer(s.stateErr))
	}
	s.stateErrMu.Unlock()
	return int32(s.state.Load()), msg
}

// NegotiatedRate reports the rate the daemon actually granted, or 0 if it has
// not said yet.
func (s *Stream) NegotiatedRate() int { return int(s.negotiated.Load()) }

// drain is the realtime body of the stream's process callback.
//
// It is deliberately the smallest thing that moves audio: dequeue a buffer,
// copy from the ring, mark the chunk, hand the buffer back. Everything else is
// a lock-free atomic or nothing. Nothing here may block, allocate, or take a
// lock (spec §4.3).
func (s *Stream) drain() {
	if s == nil {
		return
	}
	// Enter the teardown barrier before touching the stream. enter() refuses
	// once Destroy has closed the gate, and every accepted drain is guaranteed
	// to be counted before waitIdle() can return. The stream pointer comes
	// back from enter() so it is read under the gate rather than racing
	// Destroy's `s.stream = nil`; the nil check happens on that copy because
	// calling into libpipewire with a NULL stream dereferences it and kills
	// the process.
	stream, ok := s.teardown.enter(func() unsafe.Pointer {
		if s.stream == nil {
			return nil
		}
		return unsafe.Pointer(s.stream)
	})
	if !ok || stream == nil {
		return
	}
	defer s.teardown.leave()

	s.callbacks.Add(1)

	buf := C.pw_stream_dequeue_buffer((*C.struct_pw_stream)(stream))
	if buf == nil {
		// Nothing available yet. Not an error.
		return
	}

	spaBuf := buf.buffer
	if spaBuf == nil || spaBuf.n_datas == 0 || spaBuf.datas == nil {
		C.pw_stream_queue_buffer((*C.struct_pw_stream)(stream), buf)
		return
	}

	// This PipeWire exposes buffer contents as plain structs: there is no
	// spa_buffer_get_datas/set_datas, and validity is expressed through
	// spa_data.chunk rather than a size field.
	data := spaBuf.datas
	if data.data == nil || data.chunk == nil {
		C.pw_stream_queue_buffer((*C.struct_pw_stream)(stream), buf)
		return
	}

	stride := s.cfg.Channels * 4 // interleaved float32
	capacity := int(data.maxsize) / stride
	if capacity <= 0 {
		C.pw_stream_queue_buffer((*C.struct_pw_stream)(stream), buf)
		return
	}

	dst := unsafe.Slice((*float32)(unsafe.Pointer(data.data)), capacity*s.cfg.Channels)
	n := s.ring.Read(dst)

	// Claim only what was actually filled. Reporting the full capacity while
	// the ring held less would play stale buffer contents as if they were
	// fresh audio.
	data.chunk.offset = 0
	data.chunk.stride = C.int32_t(stride)
	data.chunk.size = C.uint32_t(n * stride)
	data.flags |= C.SPA_DATA_FLAG_READWRITE

	// Record the largest buffer this device asks for, so the pump can keep the
	// ring deeper than it. A CAS loop rather than a load-and-store because this
	// is the audio thread and a lost update would be silent. It is recorded
	// before the starve check, because the first callback of a session is very
	// often the one that runs dry, and that is precisely the callback the pump
	// needs to learn from.
	for {
		cur := s.maxBuffer.Load()
		if int64(capacity) <= cur || s.maxBuffer.CompareAndSwap(cur, int64(capacity)) {
			break
		}
	}

	if n == 0 {
		// Starved. The buffer is still queued with a zero-length chunk, and
		// this returns immediately. A device callback that waits here is the
		// single worst thing it could do: a dropout is recoverable, a
		// stalled audio thread is not.
		s.starves.Add(1)
	} else {
		s.frames.Add(uint64(n))
		if n < capacity {
			// PARTIAL, which is the common failure and the one that used to be
			// invisible. The ring held something, so starves stayed at zero,
			// but the buffer came up short and the device plays the remainder
			// as whatever its buffer already contained.
			//
			// This is what a pump targeting less than one buffer produces: the
			// pump fills the ring to target, the device asks for more than the
			// ring can ever hold, and so every callback is short. Reported
			// separately from starves because the fix is a deeper TARGET, not
			// a faster pump.
			s.shorts.Add(1)
		}
	}

	C.pw_stream_queue_buffer((*C.struct_pw_stream)(stream), buf)
}

// allocStreamEvents returns a zeroed events struct in C memory, pre-wired to
// the exported Go trampolines.
//
// C.malloc is used deliberately rather than a Go variable: libpipewire keeps
// this pointer and calls through it long after this function returns, so Go
// memory would be collected out from under it.
func allocStreamEvents() *C.struct_pw_stream_events {
	return C.alloc_stream_events()
}

// allocStreamHook returns a zeroed hook in C memory, for the same reason.
func allocStreamHook() *C.struct_spa_hook {
	return C.alloc_stream_hook()
}

// drainGate is a lock-free close-and-drain barrier for the realtime callback.
//
// Contract: enter() returns true only if the gate was open, and every accepted
// enter has a matching leave. Once closeAndWait() returns, no drain is active
// and none can begin.
//
// The state is one atomic word: bit 0 is the closed flag and the remaining bits
// count active drains. enter() is a single CAS that refuses to increment once
// the closed bit is set; leave() is a plain decrement. There is no mutex and no
// condition variable, because this runs on the PipeWire process callback and
// spec 4.3 forbids locks there — a short uncontended mutex is still a lock, and
// it can block the audio thread for as long as Destroy holds it while it waits
// for the count to fall, which is precisely the deadlock the spec says is
// unrecoverable.
//
// This replaced a sync.WaitGroup, which was wrong for a different reason: Add
// racing Wait can let Wait observe a transient zero and return while a drain
// that has not yet incremented is still entering, letting Destroy free the
// stream out from under it. The single CAS removes that window, because the
// closed bit and the count change atomically together: either a drain's
// increment lands before the close (and closeAndWait waits for it) or after (and
// enter refuses).
type drainGate struct {
	// state packs: bit 0 = closed, bits 1..31 = active drain count.
	state atomic.Int32
}

const (
	gateClosed   = 1
	gateCountOne = 2 // one active drain, shifted past the closed bit
)

// enter registers a drain and returns the value from get, or reports that
// teardown has begun. It is a bounded, non-blocking CAS loop: the audio thread
// never waits.
func (g *drainGate) enter(get func() unsafe.Pointer) (unsafe.Pointer, bool) {
	for {
		cur := g.state.Load()
		if cur&gateClosed != 0 {
			return nil, false
		}
		if g.state.CompareAndSwap(cur, cur+gateCountOne) {
			// We hold a slot. Read the stream pointer now: Destroy frees it
			// only after closing the gate and waiting for active==0, so this
			// read cannot observe the mid-teardown write, and the rest of the
			// drain is served from this stable copy.
			return get(), true
		}
	}
}

func (g *drainGate) leave() {
	g.state.Add(-gateCountOne)
}

// closeAndWait blocks (off the audio thread) until teardown is closed and every
// in-flight drain has left. Safe to call more than once.
func (g *drainGate) closeAndWait() {
	g.state.Or(gateClosed)
	for g.state.Load() != gateClosed {
		// count > 0: an in-flight drain is still inside the callback. Yield so
		// it can finish; this is the teardown goroutine, not the audio thread.
		runtime.Gosched()
	}
}

// connectParamsSelfcheck builds the EnumFormat pod and parses it back,
// returning the rate it carries, or 0 if the pod is malformed.
func connectParamsSelfcheck(rate, channels int) int {
	return int(C.connect_params_selfcheck(C.int(rate), C.int(channels)))
}
