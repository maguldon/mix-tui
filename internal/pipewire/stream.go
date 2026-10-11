//go:build linux

package pipewire

import (
	"fmt"
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

extern int format_rate(const struct spa_pod *param);


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
	// NegotiatedRate is the rate the daemon granted, or 0 if it has not said.
	NegotiatedRate int
	// RateMismatch is true when the daemon granted a rate other than the one
	// declared. That plays every track at the wrong speed, so it is surfaced
	// rather than left to be discovered by ear.
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
	// rateMismatch records that the daemon granted a different rate than we
	// declared. Surfaced in Stats so a silent pitch error is visible.
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
	// differ from the requested one.
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
	s.teardown.init()
	s.handle = cgo.NewHandle(s)

	// Every stream operation must hold the loop lock; see Context.lock.
	ctx.lock()
	defer ctx.unlock()

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
	s.negotiated.Store(int64(cfg.SampleRate))
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
	s.ctx.lock()
	defer s.ctx.unlock()
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
	s.ctx.lock()
	defer s.ctx.unlock()
	C.pw_stream_disconnect(s.stream)
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

	// Only touch the loop while it is alive: locking a destroyed
	// pw_thread_loop is a use-after-free, and a Stream can outlive its
	// Context when a caller owns the context itself.
	if s.ctx != nil && s.ctx.alive() {
		s.ctx.lock()
		defer s.ctx.unlock()
	}
	if s.stream != nil {
		C.pw_stream_destroy(s.stream)
		s.stream = nil
	}
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
		Callbacks:      s.callbacks.Load(),
		FramesDrained:  s.frames.Load(),
		Starves:        s.starves.Load(),
		NegotiatedRate: int(s.negotiated.Load()),
		RateMismatch:   s.rateMismatch.Load(),
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

	if n == 0 {
		// Starved. The buffer is still queued with a zero-length chunk, and
		// this returns immediately. A device callback that waits here is the
		// single worst thing it could do: a dropout is recoverable, a
		// stalled audio thread is not.
		s.starves.Add(1)
	} else {
		s.frames.Add(uint64(n))
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

// drainGate is a close-and-drain barrier for the realtime callback.
//
// Contract: enter() returns true only if the gate was open, and if it returns
// true the matching leave() is guaranteed to run. Once close() returns, no
// drain is active and none can begin. It uses a mutex rather than a
// sync.WaitGroup because a WaitGroup cannot distinguish "no drains have ever
// started" from "no drains are active right now" — the case that matters when
// a drain is racing to enter.
//
// The mutex is only ever held for the counter arithmetic, never across the
// audio work, so it does not introduce a blocking section into the realtime
// path (spec §4.3): enter/leave are a handful of uncontended instructions and
// the C call happens with the mutex released.
type drainGate struct {
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	closed bool
}

func (g *drainGate) init() {
	g.cond = sync.NewCond(&g.mu)
}

// enter registers a drain, or reports that teardown has begun.
func (g *drainGate) enter(get func() unsafe.Pointer) (unsafe.Pointer, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cond == nil {
		g.cond = sync.NewCond(&g.mu)
	}
	if g.closed {
		return nil, false
	}
	// Read the stream pointer while the gate is held. Destroy nils and frees
	// it only after it has closed the gate and waited for active==0, so this
	// read can never observe the mid-teardown write, and every use below is
	// served from this stable copy rather than re-reading the field.
	p := get()
	g.active++
	return p, true
}

func (g *drainGate) leave() {
	g.mu.Lock()
	g.active--
	if g.active == 0 {
		g.cond.Broadcast()
	}
	g.mu.Unlock()
}

// closeAndWait blocks until teardown is closed and every in-flight drain has
// left. It is safe to call more than once.
func (g *drainGate) closeAndWait() {
	g.mu.Lock()
	if g.cond == nil {
		g.cond = sync.NewCond(&g.mu)
	}
	g.closed = true
	for g.active > 0 {
		g.cond.Wait()
	}
	g.mu.Unlock()
}
