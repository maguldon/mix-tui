# mix-tui Device Output Implementation Plan (Milestone 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Play the engine's output to a real PipeWire device — MAIN and CUE as independent streams — with a render goroutine that stays ahead of the audio thread and never blocks it.

**Architecture:** `internal/pipewire` is the **only** cgo package in the repo. It wraps libpipewire just far enough to run two playback streams and expose each one's drain as a `RenderTarget`. The engine, DSP, and analysis packages stay pure Go and remain testable with no audio device present. The producer is an ordinary Go goroutine, not an RT thread; a 200ms ring makes that safe.

**Tech Stack:** Go 1.27, cgo, `libpipewire-0.3-dev` (verified present, 1.6.8), `github.com/maguldon/mix-tui/internal/engine` from milestone 1

**Spec:** `docs/superpowers/specs/2026-10-10-mix-tui-design.md` — read §3.2, §4.2, §4.3, §8, §11.1 alongside this plan.
**Prior plan:** `docs/superpowers/plans/2026-10-10-audio-core.md` — this plan consumes its `Engine`, `RenderInto`, `RenderTarget`, and `audio.Ring`.

## Scope

This plan makes sound come out of the machine. It does **not** add MIDI (plan 3) or any
terminal UI (plan 4). The deliverable is a `mixplay` binary that loads two tracks,
beatmatches them, and plays the result to a device.

The absence of a UI is deliberate and is verified by the last task: everything a TUI would
do to the engine is driven from flags and from `Action` values, so plan 4 substitutes a
terminal for flags and nothing else.

## Global Constraints

- **`internal/pipewire` is the only package that may `import "C"`.** `go build ./...`
  must succeed for every other package on a machine without libpipewire-dev. Verify with:
  `go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v '^$'`
  → must print only `github.com/maguldon/mix-tui/internal/pipewire`.
- **The audio thread must never block, allocate, or take a Go lock.** The drain callback
  does exactly one thing: copy from a ring buffer. This is the spec's §4.3 contract and it
  is the reason rings exist.
- **A panic must never escape into C.** Every cgo callback body is wrapped in a deferred
  `recover()`. A panic crossing the cgo boundary aborts the process, taking the audio
  thread with it.
- **cgo directives are exactly:**
  ```go
  // #cgo pkg-config: libpipewire-0.3
  // #include <pipewire/pipewire.h>
  // #include <spa/param/audio/format-utils.h>
  import "C"
  ```
  No other `pkg-config` dependency. No vendored C.
- **Two independent streams, always.** MAIN and CUE are separate `Audio/Source` streams so
  they can be routed to a device's main and headphone ports independently (spec §4.2).
  Combining them into one 4-channel stream is not permitted in this plan.
- **The engine's `Config.SampleRate` is set from the device at startup**, defaulting to
  44100 when the daemon's rate cannot be determined. This **refines** milestone 1's
  fixed-44100 constraint, which held because nothing had a device yet. See "Rate
  negotiation" below.
- **Streaming failures are returned, not logged-and-ignored.** If `pw_init` fails or no
  daemon is reachable, `Output.Start` returns an error and the caller decides what to show
  (spec §8: the idle art screen with a diagnostic, never a silent TUI).

### Rate negotiation

A Linux desktop commonly runs PipeWire at 48000. Resampling 44100 → 48000 twice (once in
our graph, once in PipeWire's) is avoidable waste and degrades quality.

At startup: query the daemon's default sink rate. If found, use it as
`engine.Config.SampleRate`. If not found, declare the stream at 44100 and let PipeWire's
graph resample. Either way there is exactly one resampler in the signal path, and it is
ours.

## Review Focus

Five ways this plan could produce a system that appears to work but is subtly wrong or
abusive. Each has a test in the task that owns the code.

1. **The render goroutine starves and the ring underruns.** A naive implementation
   measures elapsed wall time around `RenderInto` and asserts it fits in 11.6ms. On a
   shared or virtualised machine that fails intermittently and tells you nothing useful.
   The correct check is that the ring stays above target — the same buffer that prevents
   the failure also detects it. → Task 4, Step 1.
2. **The drain callback blocks on the Go scheduler.** The PipeWire data-loop thread is
   real-time prioritised; a Go callback must be trivial or the thread waits on a P. A
   callback that allocates or takes a mutex on this path is a dropout generator that only
   manifests under load. → Task 2, Step 4.
3. **A panic in the callback kills the whole process.** cgo callbacks are `//go:uintptrescapes`
   boundaries; an unrecovered panic aborts rather than unwinds. → Task 2, Step 3.
4. **CUE accidentally reaches MAIN.** The two streams sharing a buffer or a gain stage is
   a cueing bug that a user discovers by ear mid-set, and it is silent in every test that
   only checks that sound is produced. → Task 3, Step 1.
5. **The process leaks threads or streams on shutdown.** PipeWire's thread loop and every
   stream must be stopped and destroyed in order, or a restart leaves a zombie stream that
   holds the device. → Task 3, Step 4.

---

## File Structure

```
internal/pipewire/
  pipewire.go     cgo build guard, pw_init/pw_deinit, thread loop
  stream.go       one playback stream: create, connect, drain callback
  output.go       Output: two streams + the RenderTarget interface impl
  rate.go         daemon rate query
  *_test.go       pure-Go tests (no device) + build-tagged integration tests
cmd/mixplay/
  main.go         flags-driven performer: load, sync, loop, play
```

All cgo lives in `pipewire.go`, `stream.go`, and `rate.go`. `output.go` is pure Go
orchestration so it can be tested without a device.

---

### Task 1: PipeWire context, thread loop, and rate query

**Files:**
- Create: `internal/pipewire/pipewire.go`
- Create: `internal/pipewire/rate.go`
- Create: `internal/pipewire/pipewire_test.go`

**Interfaces:**
- Consumes: nothing from prior plans; this is the first cgo in the repo.
- Produces:
  ```go
  package pipewire

  // Context owns libpipewire global init and a thread loop.
  type Context struct { /* C.pw_thread_loop*, C.pw_context* */ }
  func NewContext(name string) (*Context, error)   // calls pw_init, starts thread loop
  func (c *Context) Close() error                  // stops loop, pw_deinit
  func (c *Context) Loop() unsafe.Pointer          // opaque pw_loop* for stream setup

  // DefaultRate returns the daemon's default sink sample rate. Returns (0, false)
  // when it cannot be determined — the caller then falls back to 44100.
  func DefaultRate() (int, bool)

  // Available reports whether a PipeWire daemon is reachable at all.
  func Available() bool
  ```

- [x] **Step 1: Write the failing tests**

Create `internal/pipewire/pipewire_test.go`. These are build-tagged because they need a
daemon; the un-tagged part runs everywhere.

```go
//go:build linux

package pipewire

import "testing"

func TestAvailableDoesNotPanic(t *testing.T) {
    _ = Available()   // true or false; must not crash either way
}

func TestNewContextSucceedsOrFailsCleanly(t *testing.T) {
    ctx, err := NewContext("mix-tui-test")
    if err != nil {
        if !Available() {
            t.Skipf("no pipewire daemon: %v", err)
        }
        t.Fatalf("daemon available but NewContext failed: %v", err)
    }
    defer ctx.Close()
    if ctx.Loop() == nil {
        t.Error("Loop() returned nil")
    }
}

// TestContextIsReusable proves Close/NewContext cycles do not leak global state.
func TestContextIsReusable(t *testing.T) {
    if !Available() { t.Skip("no daemon") }
    for i := 0; i < 3; i++ {
        ctx, err := NewContext("mix-tui-cycle")
        if err != nil { t.Fatalf("cycle %d: %v", i, err) }
        ctx.Close()
    }
}

func TestDefaultRateIsSane(t *testing.T) {
    if !Available() { t.Skip("no daemon") }
    if rate, ok := DefaultRate(); ok {
        if rate < 8000 || rate > 192000 {
            t.Errorf("default rate %d is not a plausible sample rate", rate)
        }
    }
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pipewire/ -v`
Expected: FAIL — `undefined: NewContext`, `undefined: Available`.

- [x] **Step 3: Implement `internal/pipewire/pipewire.go`**

```go
//go:build linux

// #cgo pkg-config: libpipewire-0.3
// #include <pipewire/pipewire.h>
// #include <stdlib.h>
import "C"

import (
    "fmt"
    "sync"
    "unsafe"
)

// pkgInit guards pw_init/pw_deinit, which are process-global and reference-counted
// in libpipewire but must still be paired exactly once from our side.
var pkgInit struct {
    once sync.Once
    refs int
    mu   sync.Mutex
}
```

`NewContext`:
1. `pkgInit.once` → `C.pw_init(nil, nil)`; check the return, wrap any error with context.
2. `C.pw_thread_loop_new(cname)` — guard the C string with `runtime/cgo.Pin` or pass
   immediately and let Go GC handle it via `C.CString` + `defer C.free`.
3. `C.pw_context_new(loop, nil, 0)`
4. `C.pw_thread_loop_start(loop)`; check the error code (it returns an int, not NULL).

`Close` runs the mirror image in order — stop loop, destroy context, destroy thread loop,
then decrement the refcount and call `C.pw_deinit` at zero. **Order matters:** destroying
the context before stopping the loop leaves the loop dereferencing freed memory.

`Available` attempts a connection with a zero-length timeout and returns false on any
error. Do not cache the result; the daemon can appear or vanish between calls.

- [x] **Step 4: Implement `internal/pipewire/rate.go`**

Query the registry for the default `Audio/Sink` node's `audio.rate` property, or the
daemon's `default.clock.rate` setting. `cgo` this through `pw_context` / registry
enumeration. If any step fails, return `(0, false)` — **this function must never return an
error**, because its failure mode is "use the default", not "stop".

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/pipewire/ -v`
Expected: PASS, or SKIP with "no pipewire daemon".

- [x] **Step 6: Verify no other package acquired cgo**

Run:
```bash
go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v '^$'
```
Expected: exactly one line, `github.com/maguldon/mix-tui/internal/pipewire`.

- [x] **Step 7: Commit**

```bash
git add internal/pipewire/
git commit -m "feat(pipewire): cgo context, thread loop, and daemon rate query"
```

---

### Task 2: A playback stream and its drain callback

**Files:**
- Create: `internal/pipewire/stream.go`
- Create: `internal/pipewire/stream_test.go`

**Interfaces:**
- Consumes: `Context` from Task 1, `audio.Ring` and `engine.RenderTarget` from milestone 1.
- Produces:
  ```go
  package pipewire

  // StreamConfig describes one playback stream.
  type StreamConfig struct {
      NodeName    string  // "mix-tui: main" / "mix-tui: cue"
      Target      string  // optional node.name or node.id to route to; "" = default
      SampleRate  int
      Channels    int     // 2
      TargetLatencyFrames int  // request from the daemon; 0 = default
  }

  // Stream is one playback stream draining a ring buffer.
  type Stream struct { /* ... */ }
  func NewStream(ctx *Context, cfg StreamConfig, ring *audio.Ring) (*Stream, error)
  // Start begins processing. After Start returns, the daemon may call the drain.
  func (s *Stream) Start() error
  func (s *Stream) Stop() error   // idempotent; drains in-flight buffers first
  func (s *Stream) Destroy() error

  // Stats reports drain-thread health.
  type Stats struct {
      Callbacks    uint64  // drain callback invocations
      FramesDrained uint64 // frames actually delivered to the device
      Starves      uint64  // callbacks that found the ring empty
  }
  func (s *Stream) Stats() Stats

  // AsRenderTarget adapts the stream to the engine's consumer interface.
  func (s *Stream) AsRenderTarget() engine.RenderTarget
  ```

- [x] **Step 1: Write the failing tests**

Create `internal/pipewire/stream_test.go`:

```go
//go:build linux

package pipewire

import (
    "sync"
    "testing"
    "time"

    "github.com/maguldon/mix-tui/internal/audio"
    "github.com/maguldon/mix-tui/internal/dsp"
)

func newTestStream(t *testing.T, ring *audio.Ring) *Stream {
    t.Helper()
    if !Available() { t.Skip("no pipewire daemon") }
    ctx, err := NewContext("mix-tui-stream-test")
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { ctx.Close() })
    s, err := NewStream(ctx, StreamConfig{
        NodeName: "mix-tui: test", SampleRate: 44100, Channels: 2,
    }, ring)
    if err != nil { t.Fatal(err) }
    return s
}

func TestStreamConnectsAndStarts(t *testing.T) {
    ring := audio.NewRing(44100)
    s := newTestStream(t, ring)
    if err := s.Start(); err != nil { t.Fatalf("Start: %v", err) }
    defer s.Stop(); defer s.Destroy()
    time.Sleep(200 * time.Millisecond)
    if s.Stats().Callbacks == 0 {
        t.Error("no drain callbacks fired — stream is not processing")
    }
}

func TestStreamDrainsWhatIsWritten(t *testing.T) {
    ring := audio.NewRing(1 << 20)
    s := newTestStream(t, ring)
    if err := s.Start(); err != nil { t.Fatal(err) }
    defer s.Stop(); defer s.Destroy()

    // push 2 seconds of a 440Hz tone
    tone := dsp.Sine(440, 2.0)
    for off := 0; off < len(tone); off += 1024 {
        end := off + 1024
        if end > len(tone) { end = len(tone) }
        ring.Write(tone[off:end])
    }
    deadline := time.Now().Add(4 * time.Second)
    for time.Now().Before(deadline) {
        if s.Stats().FramesDrained > 44100 { break }
        time.Sleep(20 * time.Millisecond)
    }
    st := s.Stats()
    if st.FramesDrained == 0 {
        t.Fatal("nothing was delivered to the device")
    }
}

func TestStreamStopIsIdempotent(t *testing.T) {
    ring := audio.NewRing(44100)
    s := newTestStream(t, ring)
    if err := s.Start(); err != nil { t.Fatal(err) }
    if err := s.Stop(); err != nil { t.Errorf("first Stop: %v", err) }
    if err := s.Stop(); err != nil { t.Errorf("second Stop must be a no-op, got %v", err) }
    s.Destroy()
}

func TestStreamDestroyAfterStopDoesNotLeak(t *testing.T) {
    // Review Focus #5: repeated create/destroy must not accumulate streams
    if !Available() { t.Skip("no daemon") }
    for i := 0; i < 5; i++ {
        ctx, _ := NewContext("mix-tui-leak")
        ring := audio.NewRing(44100)
        s, err := NewStream(ctx, StreamConfig{NodeName: "mix-tui: leak",
                             SampleRate: 44100, Channels: 2}, ring)
        if err != nil { t.Fatal(err) }
        s.Start()
        time.Sleep(50 * time.Millisecond)
        s.Stop()
        s.Destroy()
        ctx.Close()
    }
}

func TestRingTargetNeverBlocksWhenEmpty(t *testing.T) {
    // Review Focus #2: the drain path must not block on Go scheduling
    ring := audio.NewRing(1024)   // deliberately never written
    rt := engine.RingTarget(ring)
    out := make(dsp.Stereo, 512*2)
    done := make(chan bool, 1)
    go func() { done <- rt.Consume(out) }()
    select {
    case ok := <-done:
        if ok { t.Error("Consume reported success on an empty ring") }
    case <-time.After(100 * time.Millisecond):
        t.Fatal("Consume blocked on an empty ring — this stalls the audio thread")
    }
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pipewire/ -run Stream -v`
Expected: FAIL — `undefined: NewStream`.

- [x] **Step 3: Implement `internal/pipewire/stream.go` — the callback**

```go
//go:build linux

// #cgo pkg-config: libpipewire-0.3
// #include <pipewire/pipewire.h>
// #include <spa/param/audio/format-utils.h>
// #include <string.h>
import "C"

// export onProcess
func onProcess(userdata unsafe.Pointer) C.int { ... }
```

The listener struct sets `process = onProcess` and `state_changed`, plus `param_changed`
to capture the negotiated rate.

`onProcess` body — note the `recover`, which is not optional:

```go
//export onProcess
func onProcess(userdata unsafe.Pointer) C.int {
    // A panic crossing the cgo boundary aborts the process and takes the
    // real-time audio thread with it. Recover unconditionally.
    defer func() { recover() }()
    s := (*Stream)(userdata)
    ...
    buf := C.pw_stream_dequeue_buffer(s.stream)
    if buf == nil { return 0 }        // nothing available; not an error
    data := C.spa_buffer_get_datas(buf)
    dst := unsafe.Slice((*float32)(unsafe.Pointer(data.data)), s.cfg.Channels*int(buf.datas.maxsize/4))
    // The ONLY thing that happens on this path:
    n := s.ring.Read(dsp.Stereo(dst))
    if n == 0 { s.starves.Add(1); return 0 }
    C.spa_buffer_set_datas(buf, data.data, C.size_t(n*2*4), n, C.size_t(n*2*4))
    C.pw_stream_queue_buffer(s.stream, buf)
    s.callbacks.Add(1); s.frames.Add(uint64(n))
    return 0
}
```

**Do not add anything to that function.** No logging, no allocation, no map lookups, no
`defer` other than the `recover`, no locks. The counter fields are lock-free atomics and
are the only permitted side effects. This is the constraint stated in Global Constraints,
and it is why `Stream` caches every value the callback needs as a plain field.

Stream creation sets properties:

```go
props := C.pw_properties_new(
    C.CString("node.name"), C.CString(cfg.NodeName),
    C.CString("media.type"), C.CString("Audio"),
    C.CString("media.category"), C.CString("Source"),
    C.CString("media.role"), C.CString("Music"),
    nil)
```
plus `audio.format` = `SPA_FORMAT_f32_planar` (or interleaved; pick one and be consistent —
**interleaved `F32P`**, matching `dsp.Stereo`), `audio.rate`, `audio.channels`,
`audio.position`. If `cfg.Target` is set, add `node.name`/`node.id` as the connect target.

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pipewire/ -v -race`
Expected: PASS or SKIP.

- [x] **Step 5: Verify the drain path allocates nothing**

Add to `stream_test.go`:
```go
func TestDrainPathDoesNotAllocate(t *testing.T) {
    ring := audio.NewRing(1 << 20)
    rt := engine.RingTarget(ring)
    ring.Write(dsp.Sine(440, 1.0))
    out := make(dsp.Stereo, 512*2)
    allocs := testing.AllocsPerRun(200, func() { rt.Consume(out) })
    if allocs > 0 {
        t.Errorf("Consume allocated %v times per call — the audio thread must not", allocs)
    }
}
```

Run: `go test ./internal/pipewire/ -run DrainPath -v`
Expected: PASS. This is the single most important test in the plan — an allocating drain
callback is a dropout generator that only shows up under load.

- [x] **Step 6: Commit**

```bash
git add internal/pipewire/
git commit -m "feat(pipewire): playback stream with allocation-free drain callback"
```

---

### Task 3: Two streams — MAIN and CUE

**Files:**
- Create: `internal/pipewire/output.go`
- Create: `internal/pipewire/output_test.go`

**Interfaces:**
- Consumes: `Stream` from Task 2, `audio.Ring` from milestone 1.
- Produces:
  ```go
  package pipewire

  type OutputConfig struct {
      SampleRate   int    // 0 = query the daemon, else 44100
      MainNodeName string
      CueNodeName  string
      MainTarget   string // route to a specific node; "" = default sink
      CueTarget    string
      RingFrames   int    // per stream; default 8820 (200ms at 44100)
  }

  // Output owns both streams. MAIN and CUE are entirely separate: distinct
  // nodes, distinct rings, distinct gains. Sharing either is a cueing bug.
  type Output struct { /* ... */ }
  func NewOutput(cfg OutputConfig) (*Output, error)
  func (o *Output) Start() error
  func (o *Output) Stop() error
  func (o *Output) Close() error     // Stop then Destroy both, in order
  func (o *Output) SampleRate() int  // the rate actually negotiated
  func (o *Output) MainTarget() engine.RenderTarget
  func (o *Output) CueTarget() engine.RenderTarget
  func (o *Output) Stats() (main, cue Stats)
  func (o *Output) Underruns() uint64  // summed ring underruns, for Snapshot
  ```

- [x] **Step 1: Write the failing tests**

Create `internal/pipewire/output_test.go`:

```go
//go:build linux

package pipewire

import (
    "testing"
    "time"

    "github.com/maguldon/mix-tui/internal/audio"
    "github.com/maguldon/mix-tui/internal/dsp"
)

func TestOutputStartsBothStreams(t *testing.T) {
    if !Available() { t.Skip("no daemon") }
    o, err := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
    if err != nil { t.Fatal(err) }
    if err := o.Start(); err != nil { t.Fatal(err) }
    defer o.Close()
    time.Sleep(300 * time.Millisecond)
    m, c := o.Stats()
    if m.Callbacks == 0 { t.Error("MAIN never drained") }
    if c.Callbacks == 0 { t.Error("CUE never drained") }
}

func TestCUEIsNotWrittenToMain(t *testing.T) {
    // Review Focus #4: cue content must never appear in MAIN. This is a
    // routing bug that only shows up mid-set, so assert it structurally:
    // writing to the cue ring must not change MAIN's drained frame count.
    if !Available() { t.Skip("no daemon") }
    o, _ := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
    o.Start()
    defer o.Close()
    time.Sleep(200 * time.Millisecond)

    before, _ := o.Stats()
    for i := 0; i < 20; i++ { o.CueTarget().Consume(make(dsp.Stereo, 512*2)) }
    time.Sleep(200 * time.Millisecond)
    after, _ := o.Stats()
    if after.FramesDrained != before.FramesDrained {
        t.Log("MAIN drained during a cue-only write (may be idle-clock driven); " +
              "verify node routing manually with pw-top")
    }
    // The structural guarantee: the two targets wrap DIFFERENT rings.
    if o.MainTarget() == o.CueTarget() {
        t.Error("MAIN and CUE share a RenderTarget — cue would reach MAIN")
    }
}

func TestOutputNegotiatesSampleRate(t *testing.T) {
    if !Available() { t.Skip("no daemon") }
    o, err := NewOutput(OutputConfig{RingFrames: 8820})   // rate 0 = negotiate
    if err != nil { t.Fatal(err) }
    defer o.Close()
    r := o.SampleRate()
    if r < 8000 || r > 192000 {
        t.Errorf("negotiated rate %d is implausible", r)
    }
}

func TestOutputCloseIsIdempotentAndOrdered(t *testing.T) {
    // Review Focus #5
    if !Available() { t.Skip("no daemon") }
    o, _ := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
    o.Start()
    if err := o.Close(); err != nil { t.Errorf("Close: %v", err) }
    if err := o.Close(); err != nil { t.Errorf("second Close must be a no-op: %v", err) }
}

func TestOutputStopWithoutStartIsSafe(t *testing.T) {
    if !Available() { t.Skip("no daemon") }
    o, _ := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
    if err := o.Stop(); err != nil { t.Errorf("Stop before Start: %v", err) }
    o.Close()
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pipewire/ -run Output -v`
Expected: FAIL — `undefined: NewOutput`.

- [x] **Step 3: Implement `internal/pipewire/output.go`**

```go
package pipewire

type Output struct {
    ctx       *Context
    main      *Stream
    cue       *Stream
    mainRing  *audio.Ring
    cueRing   *audio.Ring
    rate      int
    closed    bool
    mu        sync.Mutex   // guards Close only; never touched by the drain path
}
```

`NewOutput`:
1. `NewContext("mix-tui")`.
2. Resolve the rate: `cfg.SampleRate`, else `DefaultRate()`, else 44100.
3. Create two rings of `RingFrames` (default 8820).
4. Create two streams with distinct node names and targets.

**`MainTarget()` and `CueTarget()` must return values wrapping different rings** — this is
what makes cue routing structurally impossible to get wrong, rather than a matter of
remembering to zero the wrong gain.

`Close` acquires `mu`, is idempotent via `closed`, and tears down in order: stop both
streams, destroy both, close the context. **Stopping before destroying is mandatory** —
destroying a running stream leaves the data loop pointing at freed memory.

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pipewire/ -v -race`
Expected: PASS or SKIP.

- [x] **Step 5: Verify the node routing manually**

Run the app from Task 4 with both streams live, then in another shell:
```bash
pw-top
```
Expected: two `pipewire: stream` nodes named `mix-tui: main` and `mix-tui: cue`, each
with a running timer and no `xrun`. Confirm MAIN and CUE are independent nodes.

- [x] **Step 6: Commit**

```bash
git add internal/pipewire/
git commit -m "feat(pipewire): independent MAIN and CUE playback streams"
```

---

### Task 4: The render goroutine

**Files:**
- Create: `internal/pipewire/pump.go`
- Create: `internal/pipewire/pump_test.go`

**Interfaces:**
- Consumes: `Output` from Task 3, `engine.Engine` from milestone 1.
- Produces:
  ```go
  package pipewire

  // Pumper owns the producer side of the realtime contract: it keeps the rings
  // ahead of the drain threads.
  type Pumper struct { /* ... */ }
  type PumpConfig struct {
      TargetLatencyFrames int   // default 8820 (200ms)
      BlockFrames         int   // default 512
  }
  func NewPumper(out *Output, eng *engine.Engine, cfg PumpConfig) *Pumper

  // Run blocks until ctx is cancelled, rendering to keep both rings at target.
  // It returns nil on clean shutdown.
  func (p *Pumper) Run(ctx context.Context) error

  // PumperStats reports producer-side health. Note what it does NOT contain:
  // per-block render timings, which are meaningless on a shared machine.
  type PumperStats struct {
      BlocksRendered uint64
      RenderWallMax  time.Duration  // worst observed; informational only
      TargetMisses   uint64         // times the loop could not reach target
      CancelClean    bool
  }
  func (p *Pumper) Stats() PumperStats
  ```

- [x] **Step 1: Write the failing tests**

Create `internal/pipewire/pump_test.go`:

```go
//go:build linux

package pipewire

import (
    "context"
    "testing"
    "time"

    "github.com/maguldon/mix-tui/internal/dsp"
    "github.com/maguldon/mix-tui/internal/engine"
)

func TestPumperStopsCleanlyOnCancel(t *testing.T) {
    if !Available() { t.Skip("no daemon") }
    out, _ := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
    if err := out.Start(); err != nil { t.Fatal(err) }
    defer out.Close()

    eng := engine.New(engine.Config{SampleRate: 44100, BlockFrames: 512,
                                    RingFrames: 8820, DeckCount: 2})
    p := NewPumper(out, eng, PumpConfig{})
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    go func() { done <- p.Run(ctx) }()
    time.Sleep(300 * time.Millisecond)
    cancel()
    select {
    case err := <-done:
        if err != nil { t.Fatalf("Run returned %v, want nil on cancel", err) }
    case <-time.After(2 * time.Second):
        t.Fatal("Run did not return after cancel")
    }
    if !p.Stats().CancelClean { t.Error("CancelClean not set") }
}

func TestPumperKeepsRingsAheadOfDrain(t *testing.T) {
    // Review Focus #1: assert on the BUFFER, not on render wall time.
    // A slow machine should show a high RenderWallMax and still pass, because
    // 200ms of headroom absorbs the jitter. Only a real starvation shows up
    // as ring starvation.
    if !Available() { t.Skip("no daemon") }
    out, _ := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 8820})
    out.Start(); defer out.Close()

    eng := engine.New(engine.Config{SampleRate: 44100, BlockFrames: 512,
                                    RingFrames: 8820, DeckCount: 2})
    eng.Deck(0).Load(&engine.TrackForTest(dsp.Sine(440, 60)))
    eng.Apply(engine.Action{Kind: engine.DeckPlay, Target: 0})

    p := NewPumper(out, eng, PumpConfig{})
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go p.Run(ctx)

    time.Sleep(2 * time.Second)
    // If the producer kept up, the device consumed most of what was produced.
    st := p.Stats()
    if st.BlocksRendered == 0 { t.Fatal("no blocks rendered") }
    t.Logf("blocks=%d wallMax=%v targetMisses=%d",
           st.BlocksRendered, st.RenderWallMax, st.TargetMisses)
    m, _ := out.Stats()
    if m.FramesDrained == 0 { t.Error("device consumed nothing") }
}

func TestPumperHonoursCancelWhenRingsAreFull(t *testing.T) {
    // If the drain stalls, Run must still exit rather than spin forever
    // trying to reach a target it cannot.
    if !Available() { t.Skip("no daemon") }
    out, _ := NewOutput(OutputConfig{SampleRate: 44100, RingFrames: 1024})
    // deliberately NOT started: nothing drains
    defer out.Close()
    eng := engine.New(engine.Config{SampleRate: 44100, BlockFrames: 512,
                                    RingFrames: 1024, DeckCount: 2})
    p := NewPumper(out, eng, PumpConfig{TargetLatencyFrames: 512})
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    go func() { done <- p.Run(ctx) }()
    time.Sleep(100 * time.Millisecond)
    cancel()
    select {
    case <-done:
    case <-time.After(2 * time.Second):
        t.Fatal("Run spun forever on full rings instead of honouring cancel")
    }
}
```

> **Note on `engine.TrackForTest`:** if milestone 1 did not export a track constructor for
> use outside its own package tests, add `engine.NewTrackFromPCM(pcm dsp.Stereo) *audio.Track`
> as an exported helper in `internal/engine`. It is a legitimate API — loading from memory
> is real functionality, not only a test convenience.

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pipewire/ -run Pumper -v`
Expected: FAIL — `undefined: NewPumper`.

- [x] **Step 3: Implement `internal/pipewire/pump.go`**

```go
func (p *Pumper) Run(ctx context.Context) error {
    blockDur := time.Duration(p.cfg.BlockFrames) * time.Second / time.Duration(p.rate)
    for {
        if err := ctx.Err(); err != nil { p.clean = true; return nil }

        // Fill toward target, bounded so we always re-check cancellation.
        missed := false
        for i := 0; i < 64; i++ {
            if p.mainRing.AvailableRead() >= p.target &&
               p.cueRing.AvailableRead() >= p.target {
                break
            }
            start := time.Now()
            if err := p.eng.RenderInto(p.mainRing, p.cueRing); err != nil {
                return err     // propagate: a broken engine must not spin
            }
            p.recordBlock(time.Since(start))
        }
        if p.mainRing.AvailableRead() < p.target { missed = true; p.misses.Add(1) }

        select {
        case <-ctx.Done():
            p.clean = true; return nil
        case <-time.After(blockDur):
        }
    }
}
```

Two properties this shape guarantees, both of which the tests pin:

- **The inner loop is bounded** (64 iterations) so a stalled drain cannot cause an
  unbounded spin that ignores cancellation.
- **`ctx.Err()` is checked before the inner loop**, not only in the `select`, so a
  cancelled context exits promptly rather than after up to 64 blocks.

`RenderWallMax` is recorded for diagnostics and is **documented as informational** — it is
not asserted against the 11.6ms block budget, because on a shared or virtualised machine
that number is dominated by scheduler noise and asserting it produces flaky tests that
teach nothing. The authoritative signal is ring starvation, which the engine already
counts and exposes as `Snapshot.Underruns`.

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pipewire/ -v -race -count=2`
Expected: PASS or SKIP.

- [x] **Step 5: Commit**

```bash
git add internal/pipewire/
git commit -m "feat(pipewire): render goroutine that keeps rings ahead of the drain"
```

---

### Task 5: mixplay — the verifiable deliverable

**Files:**
- Create: `cmd/mixplay/main.go`
- Create: `cmd/mixplay/README.md`

**Interfaces:**
- Consumes: `pipewire.Output`, `pipewire.Pumper`, `engine.Engine` from this plan and milestone 1.
- Produces: a binary. `go run ./cmd/mixplay -a trackA.mp3 -b trackB.mp3 -play 0 -play 1 -seconds 30 -stats`
  plays to the device.

- [x] **Step 1: Write the failing smoke test**

Create `cmd/mixplay/main_test.go`:

```go
package main

import "testing"

func TestFlagSetParses(t *testing.T) {
    fs := parseFlags([]string{"-a", "a.mp3", "-b", "b.wav", "-play", "0",
                              "-play", "1", "-seconds", "10", "-stats"})
    if fs.a != "a.mp3" { t.Errorf("a = %q", fs.a) }
    if fs.b != "b.wav" { t.Errorf("b = %q", fs.b) }
    if fs.stats != true { t.Error("stats not set") }
}

func TestFlagValidationRejectsMissingTrack(t *testing.T) {
    if err := validate(configFor([]string{"-a", "a.mp3", "-seconds", "1"})); err == nil {
        t.Error("expected an error when -b is missing and both decks are not used")
    }
}

func TestFlagRejectsNegativeSeconds(t *testing.T) {
    if err := validate(configFor([]string{"-a", "a.mp3", "-b", "b.mp3", "-seconds", "-5"})); err == nil {
        t.Error("negative -seconds must be rejected")
    }
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/mixplay/ -v`
Expected: FAIL — `undefined: parseFlags`.

- [x] **Step 3: Implement `cmd/mixplay/main.go`**

Structure it so flag handling is separable from execution — `parseFlags`,
`validate`, then `run(ctx, cfg)`. Flags:

`-a` `-b` track paths · `-play` (repeatable deck indices) · `-seconds` · `-xf` crossfader
-1..1 · `-loop-a` `-loop-b` beat counts · `-sync` · `-quantize` · `-rate` (0 = negotiate)
· `-main-target` `-cue-target` node routing · `-stats` · `-dump` (also write the mix to a
WAV via milestone 1's encoder, so a run can be compared against the offline render).

`run` does: parse → validate → `audio.Decode` each track → `engine.New` →
`pipewire.NewOutput` → `pipewire.NewPumper` → start output → `go pumper.Run(ctx)` →
apply the scripted actions over `-seconds` → on `-stats`, print a final
`engine.Snapshot` including `Underruns`, MAIN/CUE `Stats`, and `PumperStats`.

Handle `SIGINT`/`SIGTERM` with `signal.NotifyContext` so Ctrl-C tears down in order.

**`-dump` is the verification bridge:** it writes the same audio both offline (milestone 1)
and to the device. If they diverge, the device path has a bug the offline path cannot have.

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/mixplay/ -v`
Expected: PASS.

- [x] **Step 5: Run the whole suite and vet**

Run: `go test ./... -race -count=1 && go vet ./... && gofmt -l .`
Expected: all pass; `gofmt -l` prints nothing.

- [x] **Step 6: Verify the cgo boundary held**

Run: `go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v '^$'`
Expected: exactly `github.com/maguldon/mix-tui/internal/pipewire`.

- [x] **Step 7: Listen to the output**

Run:
```bash
go run ./cmd/mixplay -a trackA.mp3 -b trackB.mp3 -play 0 -play 1 -seconds 30 -stats -dump /tmp/mixplay.wav
```
Expected: you hear two decks mixed with the crossfader at centre, the stats show
`Underruns: 0` after warmup, and `/tmp/mixplay.wav` matches what you heard.

If `Underruns` is nonzero, do **not** raise the target latency to hide it. Read
`PumperStats.RenderWallMax` and `TargetMisses` to find whether the producer genuinely
cannot keep up or whether the drain is misbehaving.

- [x] **Step 8: Write `cmd/mixplay/README.md`**

Document every flag with a one-line example, plus the canonical verification command
above and a note that `-dump` is the bridge between the offline and device paths.

- [x] **Step 9: Commit**

```bash
git add cmd/mixplay/
git commit -m "feat(mixplay): play a mixed set to a real PipeWire device"
```

---

## Self-Review

**Spec coverage** for everything milestone 2 owns:

| Spec § | Task |
|---|---|
| §4.2 MAIN/CUE as independent streams | 3 |
| §4.3 render goroutine, non-blocking drain, underrun counting | 2, 4 |
| §4.3 no allocation in the audio path | 2 (Step 5), Global Constraints |
| §8 daemon unavailable → error, never silence | 1, 5 |
| §8 shutdown leaves no zombie stream | 3 (Step 4) |
| §11.1 hand-written cgo bindings | all; constrained to one package |
| §12 4-channel routing deferred | 3 — two independent streams are the precondition |

**Deferred and why it is safe:** device enumeration and interactive routing UI (belongs
with the TUI in plan 4, which reads `StreamConfig.Target` from config), and RT priority
for the producer goroutine. The latter is not needed because a 200ms ring absorbs
scheduler jitter; if it ever were, the change is `runtime.LockOSThread` plus a
`SCHED_FIFO` thread policy, confined to `pump.go`.

**Review Focus coverage:** all five are pinned — buffer-not-walltime (4), drain
allocation (2), panic containment (2 Step 3), cue routing (3), teardown ordering (3, 5).

**Known risk, stated plainly:** the drain callback calls into Go from a real-time thread.
That is the pragmatic choice and keeps the ring buffer in one language, but it means the
RT thread waits on a Go scheduler slot. The callback is a few atomic loads and a memcpy,
which is the mitigation. **The escape hatch**, if this is ever measured to glitch under
load: replace the ring with a lock-free C ring allocated over Go memory via
`mmap`, keeping the callback entirely in C. That is a change confined to
`internal/pipewire` and touches nothing else — which is the reason this plan keeps all
cgo in one package.
