//go:build linux

package pipewire

import (
	"fmt"
	"runtime/cgo"
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
	// TargetLatencyFrames requests a latency from the daemon. Zero means
	// default.
	TargetLatencyFrames int
}

// Stats reports drain-thread health.
type Stats struct {
	// Callbacks is the number of drain callback invocations.
	Callbacks uint64
	// FramesDrained is the number of frames actually delivered to the device.
	FramesDrained uint64
	// Starves counts callbacks that found the ring empty.
	Starves uint64
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

	hook    C.struct_spa_hook
	started atomic.Bool
	stopped atomic.Bool
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
	s.handle = cgo.NewHandle(s)

	props := buildStreamProps(cfg)
	defer C.pw_properties_free(props)

	s.stream = C.pw_stream_new(ctx.pwcore(), C.CString("Audio/Source"), props)
	if s.stream == nil {
		s.handle.Delete()
		return nil, fmt.Errorf("pipewire: pw_stream_new returned NULL")
	}
	// User data attaches to the LISTENER, not the stream: this version has no
	// pw_stream_set_user_data. The handle travels as the listener's data and
	// reaches every callback.
	events := C.struct_pw_stream_events{
		version:       C.uint32_t(C.PW_VERSION_STREAM_EVENTS),
		state_changed: (*[0]byte)(unsafe.Pointer(C.stream_state_trampoline)),
		param_changed: (*[0]byte)(unsafe.Pointer(C.stream_param_trampoline)),
		process:       (*[0]byte)(unsafe.Pointer(C.stream_process_trampoline)),
	}
	C.add_stream_listener(s.stream, &s.hook, &events, C.uintptr_t(s.handle))
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
	if rc := C.pw_stream_connect(s.stream, C.PW_DIRECTION_OUTPUT, C.PW_ID_ANY, 0, nil, 0); rc != 0 {
		return fmt.Errorf("pipewire: pw_stream_connect failed with code %d", int(rc))
	}
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
	C.pw_stream_disconnect(s.stream)
	return nil
}

// Destroy releases the stream. It must not be called while the drain callback
// can still fire.
func (s *Stream) Destroy() error {
	if s == nil {
		return nil
	}
	if s.stream != nil {
		C.pw_stream_destroy(s.stream)
		s.stream = nil
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
		Callbacks:     s.callbacks.Load(),
		FramesDrained: s.frames.Load(),
		Starves:       s.starves.Load(),
	}
}

// AsRenderTarget adapts the stream to the engine's consumer interface.
func (s *Stream) AsRenderTarget() engine.RenderTarget { return engine.RingTarget(s.ring) }

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
	s.callbacks.Add(1)

	buf := C.pw_stream_dequeue_buffer(s.stream)
	if buf == nil {
		// Nothing available yet. Not an error.
		return
	}

	spaBuf := buf.buffer
	if spaBuf == nil || spaBuf.n_datas == 0 || spaBuf.datas == nil {
		C.pw_stream_queue_buffer(s.stream, buf)
		return
	}

	// This PipeWire exposes buffer contents as plain structs: there is no
	// spa_buffer_get_datas/set_datas, and validity is expressed through
	// spa_data.chunk rather than a size field.
	data := spaBuf.datas
	if data.data == nil || data.chunk == nil {
		C.pw_stream_queue_buffer(s.stream, buf)
		return
	}

	stride := s.cfg.Channels * 4 // interleaved float32
	capacity := int(data.maxsize) / stride
	if capacity <= 0 {
		C.pw_stream_queue_buffer(s.stream, buf)
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

	C.pw_stream_queue_buffer(s.stream, buf)
}
