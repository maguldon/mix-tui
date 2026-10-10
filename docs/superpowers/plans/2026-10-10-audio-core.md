# mix-tui Audio Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A headless Go audio engine that decodes a track, plays it through pitch/EQ/filter/crossfader with beatgrid sync, quantize, and seamless loops, and renders the result to a WAV file — with no PipeWire, MIDI, or terminal involved.

**Architecture:** `engine` owns all state and is the only door into the audio world, speaking one API: `Apply(Action)` in, `Snapshot()` out. It takes PCM in and emits PCM out, so the identical code path that would play to a speaker plays to a file. A single `seek(target)` primitive primes resampler and filter state at the destination, which makes hotcues, nudges, sync, and loop wraps all seamless by construction.

**Tech Stack:** Go 1.27, `github.com/hajimehoshi/go-mp3`, `github.com/go-audio/wav`, `github.com/mewkiz/flac`, `github.com/jfreymuth/oggvorbis`, `gonum.org/v1/gonum/fft`, `gopkg.in/yaml.v3`

**Spec:** `docs/superpowers/specs/2026-10-10-mix-tui-design.md` — the plan argues from the spec, so the spec travels with it; executors read both.

## Scope and Sequence

This plan is **milestone 1 of 3** and deliberately stops short of the full app:

| Milestone | Contents | This plan |
|---|---|---|
| **1. Audio core** *(this plan)* | decode, ring buffer, DSP, beatgrid, key, engine, sync, loops, offline render | ✅ |
| 2. Device I/O | hand-written libpipewire cgo bindings, MAIN/CUE streams, MIDI via gomidi | — |
| 3. Interface | bubbletea/lipgloss TUI, braille waveforms, library views, MIDI mapping | — |

Milestone 1 produces working, testable software on its own: `mixrender` renders a
beatmatched, looped mix to a WAV you can listen to and assert against. Every audio
invariant the later milestones depend on is proven here, where failures are debuggable.

**Deferred to later plans** (and deliberately not designed for here): cgo, terminal
rendering, YAML config files, hot-reload, the library scanner. Task 10 stubs the seams
milestone 2 and 3 need so nothing has to be retrofitted.

## Global Constraints

- **Go 1.27**, module `github.com/maguldon/mix-tui`, all code under `internal/`.
- **No cgo in this milestone.** If any dependency requires cgo, it is the wrong dependency.
- **The realtime path must not allocate, lock, or touch the filesystem.** The render loop
  preallocates every buffer it needs. `go test -race` must pass.
- **The render loop must run faster than realtime and be correct when it does not.** Every
  render takes a `frames` count; there is no sleeping, no wall-clock dependency, and no
  timing assertions. Slow tests are fine; flaky tests are not.
- **Sample format throughout: interleaved stereo `[]float32` at 44100 Hz.** Conversions
  happen once, in the decoder. `SampleRate = 44100` is a named constant, not a literal.
- **The pitch fader is a resample ratio.** Pitch and tempo change together, like a
  turntable. There is no time-stretcher. ±8% pitch is ±8% tempo.
- **Pitch range is ±8%**, expressed as `PitchRange = 0.08` and normalized `pitch ∈ [-1, 1]`,
  where `1.0` means +8%.
- **Signal assertions, not golden-file diffs.** Tests assert RMS, peak, zero-crossing
  rate, and spectral content. Never byte-compare rendered audio.
- **Every error degrades a feature, never the session.** A bad file, a missing beatgrid, a
  dropped packet must not stop the render.
- **Package rule: `engine` imports nothing from `pipewire`, `midi`, or `tui`.** Those
  packages do not exist yet; this plan must not create them.

## Review Focus

Five failure modes the spec implies that are most likely to bite a person actually
mixing with this. Each has a test pinned to the task that owns the code.

1. **A beat loop at the very end of a track.** Setting a 32-beat loop three beats before
   the track ends must wrap, not run off the end into silence or out-of-bounds panic. The
   obvious implementation checks bounds and exits; the correct one wraps to `loopStart`.
   → Task 8, Step 1.
2. **Releasing a loop roll that was never pressed** (stray pedal, MIDI note-off without
   note-on, controller reconnect). Must be a no-op, never a jump to position zero —
   which would be catastrophic mid-set. → Task 9, Step 1.
3. **Pitch at exactly ±8% with a loop.** The loop is defined in beats but stored in
   samples; changing pitch must not change the loop's *musical* length. A loop that
   silently becomes 4.25 beats when you touch the fader is musically wrong even though
   it sounds fine. → Task 8, Step 1.
4. **A track with no detectable beat**, or analysis failing. Every beatgrid-dependent
   feature (beat loops, quantize, sync) must degrade to unavailable while playback,
   pitch, EQ, and filters keep working. → Task 6, Step 1.
5. **Seeking into the first or last few milliseconds of a track.** Priming the resampler
   reads *before* the target; at position 0 there is no history, and an unguarded
   negative index panics the render. → Task 4, Step 1.

---

## File Structure

```
internal/
  dsp/
    buffer.go        interleaved stereo float32 helpers
    resample.go      variable-rate resampler with exposed filter state
    eq.go            3-band EQ
    filter.go        DJ filter (lowpass/highpass)
    gain.go          gain, crossfader curve
    limiter.go       master limiter
    *_test.go
  audio/
    decode.go        format detection + decode to interleaved stereo float32
    decode_test.go
    ring.go          lock-free SPSC ring buffer
    ring_test.go
  analyze/
    fft.go           shared STFT — feeds both beatgrid and key
    beatgrid.go      onset → tempo → beat tracking
    key.go           chroma → Camelot key + confidence
    cache.go         analysis cache keyed by path+size+mtime
    *_test.go
  engine/
    action.go        Action, ActionKind
    deck.go          Deck: playback head, pitch, EQ, filter, hotcues
    seek.go          the seek primitive — primes resampler state
    loop.go          loop state, beat loops, saved loops, roll stack
    mixer.go         N strips, crossfader over 2 selected, master
    sync.go          tempo match + quantize
    engine.go        Apply / Snapshot / Render
    snapshot.go      Snapshot value type
    *_test.go
  testdata/
    gen_test.go      generates synthetic fixtures (tone, click track, sweep)
cmd/
  mixrender/         offline render CLI
```

**Dependency direction** (strictly one-way):
`cmd → engine → dsp`, `cmd → audio(decode)`, `engine → analyze(types only)`,
`analyze → dsp`. Nothing points back up.

---

### Task 1: DSP foundations — buffer, resampler, and the state that makes seek work

**Files:**
- Create: `internal/dsp/buffer.go`
- Create: `internal/dsp/resample.go`
- Create: `internal/dsp/resample_test.go`
- Create: `internal/dsp/buffer_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  ```go
  const SampleRate = 44100

  // Frames is a count of stereo sample frames.
  type Frames = int

  // Stereo is interleaved: [L0 R0 L1 R1 ...].
  type Stereo []float32

  func (s Stereo) Frames() int          // len(s) / 2
  func (s Stereo) Dup() Stereo          // copy, for tests
  func (s Stereo) RMS() float64
  func (s Stereo) Peak() float64
  func (s Stereo) ZeroCrossingRate() float64  // crossings/sec of the mono sum
  func Energy(s Stereo) float64         // total sum of squares
  func MaxSampleDelta(s Stereo) float64 // max |x[i]-x[i-1]| over the mono sum
  // ClampNormalized constrains a normalized control value to [-1, 1]. Every
  // Action carrying a normalized Value clamps through this, so no control can
  // drive a deck outside its designed range regardless of what a MIDI knob or
  // a config file sends.
  func ClampNormalized(v float64) float64

  // Resampler converts between sample rates. It exposes its filter state so a
  // seek can prime it with history from the destination.
  type Resampler struct { /* ... */ }
  func NewResampler(inRate, outRate int) *Resampler
  func (r *Resampler) Reset()
  // Process converts src into dst, advancing state. len(dst) stereo frames.
  func (r *Resampler) Process(src Stereo, dst Stereo)
  // Prime loads the filter history for a jump to the given stream position,
  // WITHOUT emitting output. historySamples is how much context to load.
  func (r *Resampler) Prime(stream Stereo, pos int, historySamples int)
  ```

**Why Prime exists:** this is the whole reason loops can be seamless. The resampler's
windowed-sinc filter carries state from previously-seen samples. After a jump, that state
describes audio no longer adjacent to the output, and resuming on top of it clicks. `Prime`
rebuilds the state from the destination's own history.

- [ ] **Step 1: Write the failing tests**

Create `internal/dsp/buffer_test.go`:

```go
func TestStereoFramesAndRMS(t *testing.T) {
    s := Stereo{1, -1, 1, -1} // 2 frames, full scale
    if got := s.Frames(); got != 2 {
        t.Errorf("Frames() = %d, want 2", got)
    }
    if got := s.RMS(); math.Abs(got-1.0) > 1e-6 {
        t.Errorf("RMS() = %v, want 1.0", got)
    }
}

func TestStereoZeroCrossingRate(t *testing.T) {
    // 1kHz sine at 44100 for exactly 0.1s = 100 expected crossings
    s := Sine(1000, 0.1)
    zcr := s.ZeroCrossingRate()
    if math.Abs(zcr-1000) > 20 {
        t.Errorf("ZCR = %v, want ~1000 crossings/sec", zcr)
    }
}

// helpers provided in this file for all dsp tests:
//   func Sine(freq float64, seconds float64) Stereo
//   func DC(amplitude float64, seconds float64) Stereo
```

Create `internal/dsp/resample_test.go`:

```go
func TestResamplerUnityPassesToneUnchanged(t *testing.T) {
    r := NewResampler(SampleRate, SampleRate)
    in := Sine(1000, 0.5)
    out := make(Stereo, len(in))
    r.Process(in, out)
    // unity ratio must not attenuate or shift
    if math.Abs(out.RMS()-in.RMS()) > 0.01 {
        t.Errorf("RMS drift at unity: in=%v out=%v", in.RMS(), out.RMS())
    }
}

func TestResamplerRatioChangesRate(t *testing.T) {
    // 1000Hz in at ratio 1.06 must emerge near 1060Hz
    r := NewResampler(SampleRate, SampleRate)
    r.SetRatio(1.06)
    in := Sine(1000, 0.5)
    out := make(Stereo, len(in))
    r.Process(in, out)
    got := out.ZeroCrossingRate()
    want := 1000 * 1.06
    if math.Abs(got-want) > 40 {
        t.Errorf("ZCR = %v, want ~%v for 6%% pitch up", got, want)
    }
}

func TestResamplerRatioDown(t *testing.T) {
    r := NewResampler(SampleRate, SampleRate)
    r.SetRatio(0.94)
    in := Sine(1000, 0.5)
    out := make(Stereo, len(in))
    r.Process(in, out)
    if math.Abs(out.ZeroCrossingRate()-940) > 40 {
        t.Errorf("ZCR = %v, want ~940", out.ZeroCrossingRate())
    }
}

func TestPrimeEliminatesSeekDiscontinuity(t *testing.T) {
    // THE critical test. Play a tone, jump to an arbitrary offset, and assert
    // the output has no sample-to-sample jump larger than the tone's own slope.
    stream := Sine(440, 2.0)
    r := NewResampler(SampleRate, SampleRate)
    r.SetRatio(1.05)

    // play up to the jump
    pre := make(Stereo, SampleRate/2*2)
    r.Process(stream[:SampleRate*2], pre[:SampleRate])

    // jump without priming -> expect a discontinuity
    r2 := NewResampler(SampleRate, SampleRate)
    r2.SetRatio(1.05)
    r2.Process(stream[:SampleRate*2], pre[:SampleRate])
    postBad := make(Stereo, 4096)
    r2.Process(stream[SampleRate*2:], postBad)
    badJump := MaxSampleDelta(postBad)

    // jump WITH priming -> no discontinuity
    postGood := make(Stereo, 4096)
    r2.Prime(stream, SampleRate, 64)
    r2.Process(stream[SampleRate*2:], postGood)
    goodJump := MaxSampleDelta(postGood)

    // A 440Hz tone at 1.05 has a max slope of 2*pi*440/44100 ≈ 0.063
    maxSlope := 2 * math.Pi * 462 / SampleRate
    if goodJump > maxSlope*1.5 {
        t.Errorf("primed seek jump %.4f exceeds tone slope %.4f", goodJump, maxSlope)
    }
    // priming must actually be doing something
    if badJump <= goodJump {
        t.Errorf("priming had no effect: bad=%.4f good=%.4f", badJump, goodJump)
    }
}

func TestPrimeAtPositionZeroDoesNotPanic(t *testing.T) {
    // Review Focus #5: priming reads BEFORE the target; at pos 0 there is no
    // history. This must not panic or read out of bounds.
    stream := Sine(440, 1.0)
    r := NewResampler(SampleRate, SampleRate)
    r.Prime(stream, 0, 64)   // must not panic
    out := make(Stereo, 1024)
    r.Process(stream[:512], out)  // must not panic
}

func TestMaxSampleDeltaHelper(t *testing.T) {
    s := Sine(440, 0.1)
    d := MaxSampleDelta(s)
    maxSlope := 2 * math.Pi * 440 / SampleRate
    if d > maxSlope*1.5 {
        t.Errorf("MaxSampleDelta on a pure tone = %v, want <= %v", d, maxSlope)
    }
}
```

Note: `SetRatio` is added to the `Resampler` interface in this task. `NewResampler`
defaults to ratio 1.0.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/dsp/ -v`
Expected: FAIL — compile error, `undefined: NewResampler`, `undefined: Sine`.

- [ ] **Step 3: Implement `internal/dsp/buffer.go`**

```go
package dsp

// SampleRate is the single sample rate for the whole engine. All audio is
// converted to this rate at decode time.
const SampleRate = 44100

type Stereo []float32  // interleaved L,R,L,R
```

Implement `Frames()`, `RMS()`, `Peak()`, `ZeroCrossingRate()`, `Energy()`, `Dup()`, plus
`Sine(freq, seconds float64) Stereo`, `DC(amplitude, seconds float64) Stereo`, and
`MaxSampleDelta(s Stereo) float64` (max `|s[i]-s[i-1]|` over the mono sum).

`ZeroCrossingRate` counts sign changes in the mono sum (`(L+R)/2`) over the duration
`frames / SampleRate`, returning crossings per second.

- [ ] **Step 4: Implement `internal/dsp/resample.go`**

Windowed-sinc polyphase resampler:

```go
type Resampler struct {
    ratio    float64   // outRate/inRate as a multiplier
    taps     int
    history  []float32 // filter state: last taps*2 input samples, interleaved
    phase    float64   // fractional read position within history+input
    primed   bool
}

func NewResampler(inRate, outRate int) *Resampler  // ratio = outRate/inRate
func (r *Resampler) SetRatio(ratio float64)
func (r *Resampler) Reset()
func (r *Resampler) Process(src, dst Stereo)
func (r *Resampler) Prime(stream Stereo, pos int, historySamples int)
```

Use a Blackman-windowed sinc, 32 taps, with a Kaiser-windowed transition band scaled by
`ratio` so that non-unity ratios stay clean. `Prime` loads the `historySamples` frames
immediately *before* `pos` into `history`, resets `phase`, and sets `primed = true` —
clamping to position 0 if `pos < historySamples`.

`Process` writes exactly `dst.Frames()` output frames, advancing the read position by
`ratio` per output frame and sliding `history` forward. When `!primed`, initialise
`history` from the head of `src` so the first block is not silent.

If `ratio == 1.0`, take a fast path that copies through unchanged — this is both faster
and exactly transparent, and it makes the unity test trivially exact.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/dsp/ -v -race`
Expected: PASS — all resampler and buffer tests.

- [ ] **Step 6: Run benchmarks to confirm the resampler is realtime-capable**

Append to `internal/dsp/resample_test.go`:

```go
func BenchmarkResamplerProcess(b *testing.B) {
    r := NewResampler(SampleRate, SampleRate)
    r.SetRatio(1.08)
    in := Sine(1000, 1.0)
    out := make(Stereo, 512*2)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        r.Process(in, out)
    }
}
```

Run: `go test ./internal/dsp/ -bench=Resampler -benchtime=200x`
Expected: well under 11.6ms per 512-frame block. This is the realtime budget; a
regression here breaks the audio contract in §4.3 of the spec.

- [ ] **Step 7: Commit**

```bash
git add internal/dsp/
git commit -m "feat(dsp): stereo buffer helpers and resampler with primeable state"
```

---

### Task 2: EQ, filter, gain, crossfader, and limiter

**Files:**
- Create: `internal/dsp/eq.go`
- Create: `internal/dsp/filter.go`
- Create: `internal/dsp/gain.go`
- Create: `internal/dsp/limiter.go`
- Create: `internal/dsp/eq_test.go`
- Create: `internal/dsp/filter_test.go`
- Create: `internal/dsp/limiter_test.go`

**Interfaces:**
- Consumes: `Stereo`, `SampleRate` from Task 1.
- Produces:
  ```go
  // All three band gains are linear multipliers. 1.0 = unity.
  type EQ struct { /* low, mid, high biquads */ }
  func NewEQ() *EQ
  func (e *EQ) SetBand(band int, gain float64)   // band: 0=low 1=mid 2=high
  func (e *EQ) Process(in Stereo, out Stereo)
  func (e *EQ) Reset()

  // Filter is the DJ filter knob. kind selects lowpass or highpass.
  type Filter struct { /* ... */ }
  func NewFilter() *Filter
  func (f *Filter) SetKind(highpass bool)
  // cutoffHz is clamped to [20, 20000]; resonance is a Q multiplier >= 0.5.
  func (f *Filter) SetParams(cutoffHz, resonance float64)
  func (f *Filter) Process(in Stereo, out Stereo)
  func (f *Filter) Reset()

  // Crossfader applies a constant-power curve across two sources.
  // x is normalized -1..1 (hard left .. hard right).
  func Crossfade(out Stereo, a, b Stereo, x float64)

  // Gain scales in place.
  func Gain(s Stereo, g float64)

  // Limiter prevents clipping. threshold is linear peak; ceiling must be < 1.
  type Limiter struct { /* ... */ }
  func NewLimiter(threshold, ceiling float64) *Limiter
  func (l *Limiter) Process(s Stereo)   // in place
  func (l *Limiter) Reset()
  ```

All `Process` methods must handle `len(in) == len(out)` and support `in` aliasing `out`.

- [ ] **Step 1: Write the failing tests**

Create `internal/dsp/eq_test.go`:

```go
func TestEQUnityIsTransparent(t *testing.T) {
    eq := NewEQ()
    in := Sine(1000, 0.1)
    out := make(Stereo, len(in))
    eq.Process(in, out)
    if math.Abs(out.RMS()-in.RMS()) > 0.01 {
        t.Errorf("unity EQ changed RMS: %v -> %v", in.RMS(), out.RMS())
    }
}

func TestEQHighBoostRaisesHighFrequency(t *testing.T) {
    low := Sine(200, 0.2)
    high := Sine(5000, 0.2)
    eq := NewEQ()
    eq.SetBand(2, 4.0)  // high band +12dB
    lo := make(Stereo, len(low)); eq.Process(low, lo)
    hi := make(Stereo, len(high)); eq.Process(high, hi)
    hiGain := hi.RMS() / high.RMS()
    loGain := lo.RMS() / low.RMS()
    if hiGain <= loGain {
        t.Errorf("high boost did not favor 5k over 200Hz: %v vs %v", hiGain, loGain)
    }
}

func TestEQLowCutAttenuatesLowFrequency(t *testing.T) {
    low := Sine(100, 0.2)
    eq := NewEQ()
    eq.SetBand(0, 0.01)  // low band near-silent
    out := make(Stereo, len(low))
    eq.Process(low, out)
    if out.RMS() > 0.1*low.RMS() {
        t.Errorf("low cut failed: %v vs %v", out.RMS(), low.RMS())
    }
}

func TestEQAliasedInPlace(t *testing.T) {
    eq := NewEQ()
    eq.SetBand(1, 2.0)
    s := Sine(1000, 0.1)
    eq.Process(s, s)   // must not corrupt
    if s.Peak() > 1.0 {
        t.Errorf("aliased EQ produced out-of-range peak %v", s.Peak())
    }
}
```

Create `internal/dsp/filter_test.go`:

```go
func TestFilterLowpassAttenuatesAboveCutoff(t *testing.T) {
    f := NewFilter()
    f.SetKind(false)          // lowpass
    f.SetParams(200, 1.0)     // 200Hz, Q=1
    in := Sine(2000, 0.3)
    out := make(Stereo, len(in))
    f.Process(in, out)
    if out.RMS() > 0.2*in.RMS() {
        t.Errorf("lowpass 200Hz let 2kHz through: %v vs %v", out.RMS(), in.RMS())
    }
}

func TestFilterHighpassAttenuatesBelowCutoff(t *testing.T) {
    f := NewFilter()
    f.SetKind(true)
    f.SetParams(200, 1.0)
    in := Sine(100, 0.3)
    out := make(Stereo, len(in))
    f.Process(in, out)
    if out.RMS() > 0.2*in.RMS() {
        t.Errorf("highpass 200Hz let 100Hz through: %v vs %v", out.RMS(), in.RMS())
    }
}

func TestFilterClampsAbsurdCutoff(t *testing.T) {
    f := NewFilter()
    f.SetParams(1e9, 0.01)     // must clamp, must not panic or NaN
    in := Sine(1000, 0.1)
    out := make(Stereo, len(in))
    f.Process(in, out)
    if math.IsNaN(out.RMS()) { t.Fatal("clamped filter produced NaN") }
}

func TestFilterCentreIsNearUnity(t *testing.T) {
    // at cutoff the response should be close to unity, not -inf
    f := NewFilter()
    f.SetKind(false)
    f.SetParams(1000, 0.707)
    in := Sine(1000, 0.3)
    out := make(Stereo, len(in))
    f.Process(in, out)
    if math.Abs(out.RMS()/in.RMS()-1.0) > 0.3 {
        t.Errorf("cutoff frequency attenuated by %v", out.RMS()/in.RMS())
    }
}
```

Create `internal/dsp/limiter_test.go`:

```go
func TestLimiterPreventsClipping(t *testing.T) {
    l := NewLimiter(0.8, 0.95)
    hot := make(Stereo, 4410*2)
    for i := range hot { hot[i] = 0.99 }   // constant near-full-scale DC
    l.Process(hot)
    if hot.Peak() > 0.96 {
        t.Errorf("limiter let peak through: %v", hot.Peak())
    }
}

func TestLimiterLeavesQuietSignalAlone(t *testing.T) {
    l := NewLimiter(0.8, 0.95)
    quiet := Sine(440, 0.1)
    in := quiet.Dup()
    l.Process(quiet)
    if math.Abs(quiet.RMS()-in.RMS()) > 0.01 {
        t.Errorf("limiter altered quiet signal: %v -> %v", in.RMS(), quiet.RMS())
    }
}

func TestCrossfadeIsConstantPowerAtCentre(t *testing.T) {
    a := DC(1.0, 0.1)
    b := DC(1.0, 0.1)
    out := make(Stereo, len(a))
    Crossfade(out, a, b, 0.0)   // centre
    // equal-power at centre: sqrt(0.5^2+0.5^2) = 0.707
    if math.Abs(out.Peak()-0.7071) > 0.02 {
        t.Errorf("centre crossfade peak = %v, want ~0.707", out.Peak())
    }
    Crossfade(out, a, b, -1.0)  // hard left
    if math.Abs(out.Peak()-1.0) > 0.02 {
        t.Errorf("hard-left peak = %v, want ~1.0", out.Peak())
    }
}

func TestGainScalesInPlace(t *testing.T) {
    s := DC(1.0, 0.1)
    Gain(s, 0.5)
    if math.Abs(s.Peak()-0.5) > 1e-6 {
        t.Errorf("Gain gave %v, want 0.5", s.Peak())
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/dsp/ -run 'EQ|Filter|Limiter|Crossfade|Gain' -v`
Expected: FAIL — `undefined: NewEQ`, `undefined: NewFilter`, etc.

- [ ] **Step 3: Implement `internal/dsp/eq.go`**

Three cascaded RBJ biquad peaking filters at 250Hz (low), 1kHz (mid), 4kHz (high), each
with Q=1.0. `SetBand` clamps gain to `[0.01, 10.0]` (roughly ±40dB). Maintain per-channel
filter state (two copies of each biquad's coefficients and delay line) for stereo
independence.

- [ ] **Step 4: Implement `internal/dsp/filter.go`**

A 2-pole (RBJ) lowpass or highpass with per-channel state. Clamp `cutoffHz` to
`[20, 20000]` and `resonance` to `[0.5, 20.0]`. Recompute coefficients on `SetParams`
only, not per sample.

- [ ] **Step 5: Implement `internal/dsp/gain.go`**

```go
// Crossfade applies a constant-power (equal-gain-sqrt) curve. x in [-1, 1].
func Crossfade(out Stereo, a, b Stereo, x float64) {
    x = clamp(x, -1, 1)
    t := (x + 1) / 2
    ga := math.Cos(t * math.Pi / 2)
    gb := math.Sin(t * math.Pi / 2)
    // i := range out { out[i] = a[i]*ga + b[i]*gb }
}
func Gain(s Stereo, g float64)  // in place
```

- [ ] **Step 6: Implement `internal/dsp/limiter.go`**

Peak limiter with attack 5ms, release 50ms, lookahead 1ms (441 samples), ceiling as
configured. Track per-channel envelope; apply gain computed from the maximum. Guard the
output against NaN by falling back to the input sample.

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/dsp/ -v -race`
Expected: PASS — all Task 1 and Task 2 tests.

- [ ] **Step 8: Commit**

```bash
git add internal/dsp/
git commit -m "feat(dsp): 3-band EQ, DJ filter, crossfader, and limiter"
```

---

### Task 3: Decoders — mp3, wav, flac, ogg to one PCM format

**Files:**
- Create: `internal/audio/decode.go`
- Create: `internal/audio/decode_test.go`
- Create: `internal/testdata/gen_test.go`

**Interfaces:**
- Consumes: `dsp.SampleRate`, `dsp.Stereo` from Task 1.
- Produces:
  ```go
  package audio

  // Track is fully decoded audio plus metadata. This is what a Deck holds.
  type Track struct {
      Path     string
      Title    string
      Artist   string
      Duration time.Duration
      SampleRate int
      // PCM is interleaved stereo float32 at dsp.SampleRate, always.
      PCM      dsp.Stereo
  }

  // Decode reads and decodes a file, detecting format from content then extension.
  // It never returns partial audio with a nil error.
  func Decode(path string) (*Track, error)

  // ErrUnsupported is returned for formats with no decoder.
  var ErrUnsupported = errors.New("unsupported audio format")

  // probeOnly reads the header without decoding the whole file.
  func Probe(path string) (duration time.Duration, sampleRate int, err error)
  ```

**Format detection** must be by magic bytes, not extension — a `.mp3` that is really a
FLAC should decode as FLAC.

- [ ] **Step 1: Write the failing tests**

Create `internal/testdata/gen_test.go` as a **generator**, run with `go test -run TestMain`
guarded so it only writes when `MIXTUI_GEN=1`:

```go
//go:build mixtuigen

package testdata

// Run with: MIXTUI_GEN=1 go test -tags mixtuigen ./internal/testdata/
// Writes WAV/FLAC fixtures into internal/testdata/fixtures/.
```

Create `internal/audio/decode_test.go`:

```go
func TestDecodeWAV(t *testing.T) {
    tr, err := Decode(filepath.Join("..", "testdata", "fixtures", "sine440.wav"))
    if err != nil { t.Fatalf("Decode: %v", err) }
    if tr.SampleRate != 44100 { t.Errorf("rate = %d", tr.SampleRate) }
    if tr.Duration < 900*time.Millisecond { t.Errorf("duration = %v", tr.Duration) }
    // amplitude should survive the round trip
    if math.Abs(tr.PCM.Peak()-1.0) > 0.05 {
        t.Errorf("peak = %v, want ~1.0", tr.PCM.Peak())
    }
}

func TestDecodeStereoInterleaving(t *testing.T) {
    // L constant +1, R constant -1: mono sum is 0, but channels differ
    tr, err := Decode(filepath.Join("..", "testdata", "fixtures", "lr.wav"))
    if err != nil { t.Fatal(err) }
    first := tr.PCM[0]
    if math.Abs(first-1.0) > 0.01 { t.Errorf("L = %v, want 1.0", first) }
    if math.Abs(tr.PCM[1]+1.0) > 0.01 { t.Errorf("R = %v, want -1.0", tr.PCM[1]) }
}

func TestDecodeFLAC(t *testing.T) {
    tr, err := Decode(filepath.Join("..", "testdata", "fixtures", "sine440.flac"))
    if err != nil { t.Fatalf("Decode flac: %v", err) }
    if math.Abs(tr.PCM.Peak()-1.0) > 0.05 { t.Errorf("flac peak = %v", tr.PCM.Peak()) }
}

func TestDecodeOGG(t *testing.T) {
    tr, err := Decode(filepath.Join("..", "testdata", "fixtures", "sine440.ogg"))
    if err != nil { t.Fatalf("Decode ogg: %v", err) }
    if math.Abs(tr.PCM.Peak()-1.0) > 0.05 { t.Errorf("ogg peak = %v", tr.PCM.Peak()) }
}

func TestDecodeRejectsGarbage(t *testing.T) {
    _, err := Decode(filepath.Join("..", "testdata", "fixtures", "garbage.bin"))
    if err == nil { t.Fatal("expected error for garbage file") }
    if !errors.Is(err, ErrUnsupported) {
        t.Errorf("err = %v, want ErrUnsupported", err)
    }
}

func TestDecodeMissingFile(t *testing.T) {
    _, err := Decode("/nonexistent/nope.mp3")
    if err == nil { t.Fatal("expected error for missing file") }
}

func TestProbeDoesNotDecodeFully(t *testing.T) {
    d, sr, err := Probe(filepath.Join("..", "testdata", "fixtures", "sine440.wav"))
    if err != nil { t.Fatal(err) }
    if sr != 44100 { t.Errorf("rate = %d", sr) }
    if d < 900*time.Millisecond { t.Errorf("duration = %v", d) }
}
```

- [ ] **Step 2: Generate the fixtures**

Run: `MIXTUI_GEN=1 go test -tags mixtuigen ./internal/testdata/`
Expected: writes `internal/testdata/fixtures/{sine440.wav,sine440.flac,sine440.ogg,lr.wav,garbage.bin}`

Encode fixtures by shelling out, trying each encoder in order and stopping at the first
that exists. **A skipped decoder test is a decoder nobody has verified**, so prefer a
fallback over skipping:

| Format | First choice | Fallback |
|---|---|---|
| FLAC | `flac` | `ffmpeg -i in.wav -c:a flac out.flac` |
| OGG Vorbis | `oggenc` | `ffmpeg -i in.wav -c:a libvorbis out.ogg` |

If neither path is available for a format, skip generating that fixture and let its test
`t.Skip` — but **print a loud warning**, because a silently-skipped decoder test is the
failure mode that lets a broken ogg path ship. Verify encoder availability in the generator
rather than assuming it.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/audio/ -v`
Expected: FAIL — `undefined: Decode`.

- [ ] **Step 4: Implement `internal/audio/decode.go`**

```go
func detectFormat(r io.ReaderAt, size int64) (format, error)
```
Sniff the first 16 bytes: `RIFF....WAVE` → wav, `fLaC` → flac, `OggS` → ogg,
`ID3` or `0xFFEx/0xFFFx` frame sync → mp3. Unknown → `ErrUnsupported`.

Each decoder produces `[]dsp.Stereo`-style planar or interleaved float32; convert to
44100Hz if the source differs (linear interpolation is acceptable here — this is offline,
not the realtime path), then interleave to stereo, duplicating mono.

`Probe` reads only the header for duration and rate.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/audio/ -v -race`
Expected: PASS for wav; flac/ogg PASS if fixtures exist, otherwise SKIP.

- [ ] **Step 6: Commit**

```bash
git add internal/audio/ internal/testdata/
git commit -m "feat(audio): format-sniffing decoders for wav, mp3, flac, ogg"
```

---

### Task 4: The seek primitive — the foundation everything else builds on

**Files:**
- Create: `internal/engine/action.go`
- Create: `internal/engine/deck.go`
- Create: `internal/engine/seek.go`
- Create: `internal/engine/seek_test.go`

**Interfaces:**
- Consumes: `dsp` primitives from Tasks 1–2, `audio.Track` from Task 3.
- Produces:
  ```go
  package engine

  const PitchRange = 0.08   // normalized 1.0 == +8%

  type ActionKind int
  const (
      DeckPlay ActionKind = iota
      DeckCue
      DeckPitch      // Value is normalized -1..1
      DeckEQBand     // Target unused; Value selects band
      DeckFilter
      DeckSeek       // Value is normalized position 0..1
      DeckHotcueSet
      DeckHotcueJump
      DeckHotcueDelete
  )
  // ... loop and sync kinds added in Tasks 8 and 9

  type Action struct {
      Kind   ActionKind
      Target int     // deck index
      Value  float64 // normalized
      Slot   int
  }

  type Deck struct { /* ... */ }

  // NewDeck returns a stopped, unloaded deck.
  func NewDeck(index int) *Deck
  func (d *Deck) Load(t *audio.Track)
  func (d *Deck) Play()
  func (d *Deck) Stop()
  // SetPitch takes a normalized value in [-1, 1], where 1.0 is +PitchRange (+8%).
  func (d *Deck) SetPitch(normalized float64)
  // SetPitchNormalized is the same operation, named for call sites that already
  // hold a ratio (e.g. sync in Task 9). SetPitch is the canonical name;
  // SetPitchNormalized is an alias kept because sync reads better with it.
  func (d *Deck) SetPitchNormalized(normalized float64)
  func (d *Deck) PitchRatio() float64
  // RatioToNormalized inverts SetPitch: ratio 1.0 -> 0.0, 1.08 -> 1.0.
  func RatioToNormalized(ratio float64) float64
  func (d *Deck) SetEQBand(band int, gain float64)
  func (d *Deck) SetFilter(highpass bool, cutoffHz, resonance float64)
  func (d *Deck) Pos() time.Duration
  func (d *Deck) Duration() time.Duration
  func (d *Deck) IsPlaying() bool
  func (d *Deck) HasTrack() bool
  func (d *Deck) CurrentBPM() float64

  // Render is the per-block entry point. It renders into out, which must have
  // exactly BlockFrames of stereo capacity. It MUST NOT allocate, lock, or
  // block. A stopped or unloaded deck writes silence.
  func (d *Deck) Render(out dsp.Stereo)

  // seek moves the playhead with correct resampler and filter state at the
  // destination. This is THE primitive: hotcues, nudges, sync, and loop wraps
  // all call it, which is why they are all seamless.
  func (d *Deck) seek(target time.Duration)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/seek_test.go`:

```go
func TestSeekIsClickFree(t *testing.T) {
    // THE test for Review Focus #5 and the core of §4.4.1
    d := NewDeck(0)
    d.Load(memTrack(Sine(440, 5.0)))
    d.Play()

    buf := make(dsp.Stereo, 0, 4096*2)
    tmp := make(dsp.Stereo, 512*2)
    // play 1 second
    for i := 0; i < 86; i++ { d.Render(tmp); buf = append(buf, tmp...) }

    d.seek(3 * time.Second)   // jump with no hotcue, no loop: raw seek

    after := make(dsp.Stereo, 4096*2)
    for i := 0; i < 8; i++ { d.Render(tmp); after = append(after, tmp...) }

    maxSlope := 2 * math.Pi * 460 / dsp.SampleRate
    jump := maxSampleDeltaAtBoundary(buf, after, len(after)-4096*2)
    if jump > maxSlope*2 {
        t.Errorf("seek discontinuity %.5f exceeds tone slope %.5f", jump, maxSlope)
    }
    if math.IsNaN(after.RMS()) { t.Fatal("seek produced NaN") }
}

func TestSeekToZeroDoesNotPanic(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(Sine(440, 2.0)))
    d.Play()
    tmp := make(dsp.Stereo, 512*2)
    d.Render(tmp)
    d.seek(0)              // Review Focus #5: prime reads before target
    d.Render(tmp)          // must not panic
}

func TestSeekClampsOutOfRange(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(Sine(440, 2.0)))
    d.seek(500 * time.Second)   // past the end
    if d.Pos() > d.Duration() {
        t.Errorf("Pos() = %v, must clamp to duration %v", d.Pos(), d.Duration())
    }
    d.seek(-5 * time.Second)    // negative
    if d.Pos() < 0 { t.Errorf("Pos() = %v, must clamp to 0", d.Pos()) }
}

func TestPitchChangesTempo(t *testing.T) {
    // +8% pitch must consume the track ~8% faster in wall-clock
    d := NewDeck(0)
    d.Load(memTrack(Sine(440, 10.0)))
    d.Play()
    tmp := make(dsp.Stereo, 512*2)
    start := d.Pos()
    for i := 0; i < 172; i++ { d.Render(tmp) }  // ~2 seconds at 1.0
    elapsed := d.Pos() - start
    unity := elapsed.Seconds()
    if math.Abs(unity-2.0) > 0.15 { t.Fatalf("unity timing off: %vs", unity) }

    d2 := NewDeck(1)
    d2.Load(memTrack(Sine(440, 10.0)))
    d2.SetPitch(1.0)   // +8%
    d2.Play()
    s2 := d2.Pos()
    for i := 0; i < 172; i++ { d2.Render(tmp) }
    up := (d2.Pos() - s2).Seconds()
    want := 2.0 * 1.08
    if math.Abs(up-want) > 0.15 {
        t.Errorf("+8%% pitch advanced %vs, want ~%vs", up, want)
    }
}

func TestPitchRatioMapping(t *testing.T) {
    d := NewDeck(0)
    d.SetPitch(1.0)
    if math.Abs(d.PitchRatio()-1.08) > 1e-9 { t.Errorf("ratio = %v", d.PitchRatio()) }
    d.SetPitch(-1.0)
    if math.Abs(d.PitchRatio()-0.92) > 1e-9 { t.Errorf("ratio = %v", d.PitchRatio()) }
    d.SetPitch(0)
    if math.Abs(d.PitchRatio()-1.0) > 1e-9 { t.Errorf("centre ratio = %v", d.PitchRatio()) }
}

func TestStoppedDeckIsSilent(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(Sine(440, 2.0)))
    tmp := make(dsp.Stereo, 512*2)
    d.Render(tmp)
    if tmp.Peak() != 0 { t.Errorf("stopped deck produced %v", tmp.Peak()) }
}

func TestEmptyDeckIsSilent(t *testing.T) {
    d := NewDeck(0)
    tmp := make(dsp.Stereo, 512*2)
    d.Render(tmp)   // must not panic with no track
}

func TestDeckReachesEnd(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(Sine(440, 0.5)))
    d.Play()
    tmp := make(dsp.Stereo, 512*2)
    for i := 0; i < 100; i++ { d.Render(tmp) }
    if d.IsPlaying() { t.Error("deck should stop at end of track") }
}
```

Helper in the test file — **`memTrack` is the shared in-memory track constructor used by
every engine test from this task onward; define it once in `seek_test.go` and reuse it**:

```go
// memTrack builds an in-memory Track from PCM. Shared by all engine tests.
func memTrack(pcm dsp.Stereo) *audio.Track {
    return &audio.Track{
        Path: "mem", SampleRate: dsp.SampleRate, PCM: pcm,
        Duration: time.Duration(len(pcm)/2) * time.Second / dsp.SampleRate,
    }
}
func maxSampleDeltaAtBoundary(before, after dsp.Stereo, at int) float64 {
    // max |x| jump across the boundary between before's end and after's start
}
```

All Task 4 tests use `memTrack(...)`, not a second helper name.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/ -v`
Expected: FAIL — `undefined: NewDeck`.

- [ ] **Step 3: Implement `internal/engine/action.go`**

The `Action`/`ActionKind` types exactly as in the Interfaces block. Add a doc comment on
`DeckPitch` stating that `Value` is normalized `[-1, 1]` where `1.0` is +8%.

- [ ] **Step 4: Implement `internal/engine/deck.go`**

```go
type Deck struct {
    index  int
    track  *audio.Track
    pos    float64          // fractional source sample position
    playing bool

    pitchNorm float64
    resampler *dsp.Resampler

    eq      *dsp.EQ
    filter  *dsp.Filter
    eqGains [3]float64
    hp, cutoff, reso bool...  // filter params

    // preallocated, reused every block — Render must not allocate
    blockIn  dsp.Stereo  // 512 frames
    blockOut dsp.Stereo

    beatgrid *analyze.BeatGrid   // nil until Task 6; see Task 6 note
    // hotcues, loops added in later tasks
}
```

`NewDeck(index)` allocates `blockIn`/`blockOut` at 512 frames each so `Render` never
allocates. `Load` resets position and resampler.

**`Render(out dsp.Stereo)`** is the single per-block entry point:

1. If not playing or no track → zero `out`, return.
2. Read `out.Frames()` frames from `track.PCM` starting at `pos` through `resampler`,
   advancing `pos` by `ratio` per output frame.
3. Apply EQ, filter.
4. Write to `out`.
5. If `pos` passes the end, stop and zero the remainder.

Expose `Duration()` returning the loaded track's duration (0 if empty).

**Critical:** `Render` must not call `append` on `blockIn`/`blockOut`, must not allocate,
and must not take a lock. Add a benchmark in this task to prove it:

```go
func BenchmarkDeckRender(b *testing.B) {
    d := NewDeck(0)
    d.Load(&audio.Track{PCM: dsp.Sine(440, 60.0), SampleRate: dsp.SampleRate,
                        Duration: time.Minute})
    d.Play()
    out := make(dsp.Stereo, 512*2)
    b.ResetTimer()
    for i := 0; i < b.N; i++ { d.Render(out) }
}
```

- [ ] **Step 5: Implement `internal/engine/seek.go`**

```go
func (d *Deck) seek(target time.Duration) {
    if d.track == nil { return }
    // clamp
    total := d.Duration()
    if target < 0 { target = 0 }
    if target > total { target = total }
    // convert to fractional source sample
    pos := float64(target) / float64(time.Second) * dsp.SampleRate
    d.pos = pos
    // THE key line: prime resampler state from the destination's own history
    d.resampler.Prime(d.track.PCM, int(pos), d.resampler.HistorySamples())
    // filter state: a filter's delay line is short and its discontinuity is
    // inaudible, so reset rather than prime
    d.eq.Reset()
    d.filter.Reset()
}
```

If `Resampler.HistorySamples()` does not exist yet, add it in Task 1 as
`func (r *Resampler) HistorySamples() int` returning the taps×2 context the resampler
needs.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/engine/ -v -race`
Expected: PASS — all seek, pitch, and render tests.

- [ ] **Step 7: Verify no allocation in the render path**

Run: `go test ./internal/engine/ -bench=DeckRender -benchtime=500x -benchmem`
Expected: `0 allocs/op`. If it reports any allocation, find it and preallocate — this is
the spec's §4.3 contract and a silent regression here is the project's main risk.

- [ ] **Step 8: Commit**

```bash
git add internal/engine/ internal/dsp/
git commit -m "feat(engine): deck playback head with click-free seek primitive"
```

---

### Task 5: Ring buffer — the realtime/demux boundary

**Files:**
- Create: `internal/audio/ring.go`
- Create: `internal/audio/ring_test.go`

**Interfaces:**
- Consumes: `dsp.Stereo` from Task 1.
- Produces:
  ```go
  package audio

  // Ring is a lock-free single-producer single-consumer ring of stereo frames.
  // Written only by the render goroutine, read only by the audio callback.
  type Ring struct { /* ... */ }
  func NewRing(capacityFrames int) *Ring
  // Write copies as much of src as fits. Returns frames actually written.
  func (r *Ring) Write(src dsp.Stereo) int
  // Read fills as much of dst as available. Returns frames actually read.
  // Returns 0 immediately when empty — the callback must never block.
  func (r *Ring) Read(dst dsp.Stereo) int
  func (r *Ring) AvailableRead() int
  func (r *Ring) AvailableWrite() int
  // Underruns counts Read calls that found the ring empty. Surfaced in Snapshot.
  func (r *Ring) Underruns() uint64
  func (r *Ring) LatencyFrames() int
  ```

**This is the §4.3 contract in code form.** `Read` returning 0 rather than blocking is
what makes a dropout recoverable instead of fatal.

- [ ] **Step 1: Write the failing tests**

Create `internal/audio/ring_test.go`:

```go
func TestRingWriteReadRoundTrip(t *testing.T) {
    r := NewRing(1024)
    src := dsp.Sine(440, 0.05)   // ~2205 frames, more than capacity
    n := r.Write(src)
    if n != 1024 { t.Errorf("wrote %d, want 1024 (capacity)", n) }
    if r.AvailableRead() != 1024 { t.Errorf("available = %d", r.AvailableRead()) }
    dst := make(dsp.Stereo, 2048)
    got := r.Read(dst)
    if got != 1024 { t.Errorf("read %d, want 1024", got) }
    for i := 0; i < got*2; i++ {
        if math.Abs(dst[i]-src[i]) > 1e-9 {
            t.Fatalf("sample %d differs: %v vs %v", i, dst[i], src[i])
        }
    }
}

func TestRingReadEmptyReturnsZeroImmediately(t *testing.T) {
    // The critical safety property: an empty ring must NOT block.
    r := NewRing(1024)
    dst := make(dsp.Stereo, 512*2)
    done := make(chan int, 1)
    go func() { done <- r.Read(dst) }()
    select {
    case n := <-done:
        if n != 0 { t.Errorf("read %d from empty ring, want 0", n) }
    case <-time.After(100 * time.Millisecond):
        t.Fatal("Read on empty ring blocked — this would hang the audio thread")
    }
    if r.Underruns() != 1 { t.Errorf("underruns = %d, want 1", r.Underruns()) }
}

func TestRingPartialWriteWhenFull(t *testing.T) {
    r := NewRing(100)
    src := dsp.Sine(440, 0.05)
    r.Write(src)
    n := r.Write(src)   // ring is full
    if n != 0 { t.Errorf("wrote %d to a full ring, want 0", n) }
    if r.AvailableWrite() != 0 { t.Errorf("availWrite = %d", r.AvailableWrite()) }
}

func TestRingPartialReadWhenEmpty(t *testing.T) {
    r := NewRing(1024)
    r.Write(dsp.DC(0.5, 0.005))  // ~220 frames
    dst := make(dsp.Stereo, 512*2)
    n := r.Read(dst)
    if n == 0 || n >= 512 { t.Errorf("read %d, want partial", n) }
    n2 := r.Read(dst)
    if n2 != 0 { t.Errorf("second read = %d, want 0", n2) }
}

func TestRingConcurrentSPSC(t *testing.T) {
    // Must pass under -race: this is the actual producer/consumer shape.
    r := NewRing(4096)
    total := 44100
    src := dsp.Sine(440, float64(total)/dsp.SampleRate)
    var wg sync.WaitGroup
    wg.Add(1)
    go func() {
        defer wg.Done()
        for off := 0; off < total; off += 512 {
            end := off + 512
            if end > total { end = total }
            r.Write(src[off*2 : end*2])
        }
    }()
    got := 0
    dst := make(dsp.Stereo, 512*2)
    for got < total {
        n := r.Read(dst)
        if n > 0 { got += n }
    }
    wg.Wait()
}

func TestRingLatency(t *testing.T) {
    r := NewRing(4096)
    r.Write(dsp.DC(0.5, float64(1000)/dsp.SampleRate))
    if r.LatencyFrames() < 900 || r.LatencyFrames() > 1100 {
        t.Errorf("latency = %d frames, want ~1000", r.LatencyFrames())
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/audio/ -run Ring -v`
Expected: FAIL — `undefined: NewRing`.

- [ ] **Step 3: Implement `internal/audio/ring.go`**

Lock-free SPSC using `sync/atomic` with monotonically increasing write/read indices and
a power-of-two capacity mask. Producer owns the write index, consumer owns the read index.

```go
type Ring struct {
    buf     []float32
    mask    uint64
    writeAt atomic.Uint64   // total frames ever written
    readAt  atomic.Uint64   // total frames ever read
    underruns atomic.Uint64
}
```

`Read` computes available as `writeAt - readAt`; if zero, increments `underruns` and
returns 0 immediately — **never block**. Capacity must be rounded up to a power of two
with a minimum of 4096 frames.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/audio/ -v -race -count=3`
Expected: PASS — repeat 3× to shake out scheduling-dependent races.

- [ ] **Step 5: Commit**

```bash
git add internal/audio/
git commit -m "feat(audio): lock-free SPSC ring buffer with non-blocking read"
```

---

### Task 6: Beatgrid and key detection on a shared FFT

**Files:**
- Create: `internal/analyze/fft.go`
- Create: `internal/analyze/beatgrid.go`
- Create: `internal/analyze/key.go`
- Create: `internal/analyze/beatgrid_test.go`
- Create: `internal/analyze/key_test.go`

**Interfaces:**
- Consumes: `dsp.Stereo` from Task 1.
- Produces:
  ```go
  package analyze

  type BeatGrid struct {
      BPM        float64
      Confidence float64   // 0..1
      Beats      []time.Duration  // positions from track start
      Downbeats  []int      // indices into Beats that are downbeats
      // Phases carry a phase offset so a grid can be aligned to another track.
      Phases []float64
  }

  // AnalyzeBeatGrid runs onset detection, tempo estimation, and beat tracking.
  func AnalyzeBeatGrid(pcm dsp.Stereo) (*BeatGrid, error)

  type Key struct {
      Camelot   string   // "8A", "7B", ... or "" if below threshold
      Name      string   // "C minor", or "" if below threshold
      Confidence float64 // 0..1
  }

  // AnalyzeKey returns a Key whose Camelot/Name are EMPTY when confidence is
  // below the threshold. Reporting a low-confidence key as certain is worse
  // than reporting nothing.
  func AnalyzeKey(pcm dsp.Stereo) (*Key, error)

  const KeyConfidenceThreshold = 0.55
  ```

**`ErrNoBeatGrid` is defined here**, in `analyze`, because this is where a beatgrid
becomes an optional part of a deck's state. Tasks 8 and 9 return it when a beat-dependent
feature is requested on a deck that has none:

```go
package analyze

// ErrNoBeatGrid is returned when a beat-dependent operation is requested on a
// track with no analysed beatgrid. The operation is unavailable, not failed:
// playback, pitch, EQ, and filters continue to work (spec §8).
var ErrNoBeatGrid = errors.New("no beatgrid for this track")
```

**One STFT feeds both.** This is the spec's §4.1 claim and it must hold in the code:

- [ ] **Step 1: Write the failing tests**

Create `internal/analyze/beatgrid_test.go`:

```go
// A click track: impulses at exactly 120 BPM for 20 seconds.
func clickTrack(bpm float64, seconds float64) dsp.Stereo {
    period := int(60.0 / bpm * dsp.SampleRate)
    total := int(seconds * dsp.SampleRate)
    out := make(dsp.Stereo, total*2)
    for i := 0; i < total; i += period {
        for j := 0; j < 64 && i+j < total; j++ {
            v := float32(math.Exp(-float64(j) / 8.0))
            out[(i+j)*2] += v
            out[(i+j)*2+1] += v
        }
    }
    return out
}

func TestBeatGridDetectsTempo(t *testing.T) {
    for _, bpm := range []float64{120, 128, 140} {
        g, err := AnalyzeBeatGrid(clickTrack(bpm, 20))
        if err != nil { t.Fatalf("bpm %v: %v", bpm, err) }
        if math.Abs(g.BPM-bpm) > 2.0 {
            t.Errorf("detected %v, want %v (±2)", g.BPM, bpm)
        }
        if len(g.Beats) < 20 {
            t.Errorf("bpm %v: only %d beats", bpm, len(g.Beats))
        }
    }
}

func TestBeatGridBeatSpacingIsRegular(t *testing.T) {
    g, _ := AnalyzeBeatGrid(clickTrack(128, 20))
    want := 60.0 / 128.0
    for i := 1; i < len(g.Beats) && i < 50; i++ {
        gap := g.Beats[i].Seconds() - g.Beats[i-1].Seconds()
        if math.Abs(gap-want) > 0.02 {
            t.Fatalf("beat %d gap = %v, want %v", i, gap, want)
        }
    }
}

func TestBeatGridOnSilenceIsLowConfidence(t *testing.T) {
    // Review Focus #4: no beat must not be reported as a confident detection.
    g, err := AnalyzeBeatGrid(dsp.DC(0.01, 10))
    if err != nil { t.Fatal(err) }
    if g.Confidence > 0.5 {
        t.Errorf("silence got confidence %v — must be low", g.Confidence)
    }
}

func TestBeatGridOnNoiseIsLowConfidence(t *testing.T) {
    rng := rand.New(rand.NewSource(42))
    noise := make(dsp.Stereo, 44100*10*2)
    for i := range noise { noise[i] = float32(rng.NormFloat64() * 0.3) }
    g, _ := AnalyzeBeatGrid(noise)
    if g.Confidence > 0.6 {
        t.Errorf("white noise got confidence %v", g.Confidence)
    }
}

func TestBeatGridOnSilenceDoesNotError(t *testing.T) {
    // must return a usable grid object, not nil, so callers never nil-deref
    g, err := AnalyzeBeatGrid(make(dsp.Stereo, 44100*2))
    if err != nil { t.Errorf("silence should not error, got %v", err) }
    if g == nil { t.Fatal("returned nil grid") }
}

func TestBeatGridShortTrack(t *testing.T) {
    g, err := AnalyzeBeatGrid(clickTrack(128, 1.0))
    if err != nil { t.Fatalf("short track errored: %v", err) }
    _ = g   // must not panic; may be low confidence
}
```

Create `internal/analyze/key_test.go`:

```go
func TestAnalyzeKeyOnMajorChord(t *testing.T) {
    // C major triad: 261.6, 329.6, 392.0 Hz
    pcm := mixSines(10, []float64{261.6, 329.6, 392.0})
    k, err := AnalyzeKey(pcm)
    if err != nil { t.Fatal(err) }
    if k.Confidence < KeyConfidenceThreshold { t.Skip("too quiet") }
    if k.Camelot != "8B" { t.Errorf("C major = %q, want 8B", k.Camelot) }
    if k.Name != "C major" { t.Errorf("name = %q, want C major", k.Name) }
}

func TestAnalyzeKeyOnMinorChord(t *testing.T) {
    // A minor: 220, 261.6, 329.6
    pcm := mixSines(10, []float64{220.0, 261.6, 329.6})
    k, _ := AnalyzeKey(pcm)
    if k.Confidence < KeyConfidenceThreshold { t.Skip("too quiet") }
    if k.Camelot != "7A" { t.Errorf("A minor = %q, want 7A", k.Camelot) }
}

func TestAnalyzeKeyOnNoiseIsUndetected(t *testing.T) {
    // Review Focus #4: must report NOTHING, not a confident guess
    rng := rand.New(rand.NewSource(7))
    noise := make(dsp.Stereo, 44100*10*2)
    for i := range noise { noise[i] = float32(rng.NormFloat64() * 0.3) }
    k, err := AnalyzeKey(noise)
    if err != nil { t.Fatal(err) }
    if k.Confidence >= KeyConfidenceThreshold {
        t.Fatalf("noise confidence %v >= threshold; should be undetected", k.Confidence)
    }
    if k.Camelot != "" || k.Name != "" {
        t.Errorf("below-threshold key must be empty, got %q/%q", k.Camelot, k.Name)
    }
}

func TestAnalyzeKeyOnPercussionIsUndetected(t *testing.T) {
    // kick-only: no pitch content at all
    k, _ := AnalyzeKey(clickTrack(128, 10))
    if k.Camelot != "" {
        t.Errorf("percussion got key %q; should be undetected", k.Camelot)
    }
}

func TestCamelotRoundTrip(t *testing.T) {
    // every Camelot code must map to a name and back
    for i := 1; i <= 12; i++ {
        for _, m := range []string{"A", "B"} {
            code := fmt.Sprintf("%d%s", i, m)
            name := camelotToName(code)
            if name == "" { t.Errorf("%s unmapped", code) }
            if got := nameToCamelot(name); got != code {
                t.Errorf("%s -> %q -> %s", code, name, got)
            }
        }
    }
}

func mixSines(seconds float64, freqs []float64) dsp.Stereo {
    n := int(seconds * dsp.SampleRate)
    out := make(dsp.Stereo, n*2)
    for _, f := range freqs {
        for i := 0; i < n; i++ {
            v := float32(0.2 * math.Sin(2*math.Pi*f*float64(i)/dsp.SampleRate))
            out[i*2] += v
            out[i*2+1] += v
        }
    }
    return out
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/analyze/ -v`
Expected: FAIL — `undefined: AnalyzeBeatGrid`.

- [ ] **Step 3: Add the FFT dependency**

Run: `go get gonum.org/v1/gonum/fft@v0.17.0`
Expected: `go: added gonum.org/v1/gonum v0.17.0`

- [ ] **Step 4: Implement `internal/analyze/fft.go`**

```go
package analyze

// Spectrogram is a shared STFT result. ONE computation feeds both beatgrid
// and key detection — see spec §4.1.
type Spectrogram struct {
    Frames    [][]complex128  // [frame][bin]
    HopSize   int
    WindowLen int
    SampleRate int
}

func ComputeSpectrogram(pcm dsp.Stereo, windowLen, hop int) *Spectrogram
```

Window: Hann, `windowLen = 2048`, `hop = 512` at 44100 Hz. This gives ~86 fps of frames
and 21.5 Hz bin spacing — fine enough for chroma, coarse enough for onset timing.
Downmix to mono before the transform.

- [ ] **Step 5: Implement `internal/analyze/beatgrid.go`**

Pipeline:

1. **Spectral flux onset** — half-wave-rectified difference between consecutive
   spectrogram frames; local-maximum peak picking with an adaptive threshold (moving
   median over ±10 frames).
2. **Tempo** — autocorrelation of the onset envelope over lag range corresponding to
   60–200 BPM; take the peak. Refine BPM from onset-interval histogram to avoid octave
   errors (a 128 BPM track detected at 64 is a common failure — check that the
   autocorrelation peak at 2× the chosen lag is not stronger).
3. **Beat tracking** — Ellis dynamic programming: score each candidate beat by a
   combination of onset strength and proximity to the expected beat period from the
   previous beat; backtrack for the final sequence.
4. **Downbeats** — mark every 4th beat, refined by low-frequency (60–160 Hz) onset
   strength when available.
5. **Confidence** — combine onset-rate plausibility, autocorrelation peak sharpness, and
   how well beats fit the detected period. **Silence and white noise must land below
   0.5** (per the tests).

`Beats` are `time.Duration` offsets. Never return `nil` grid for a decodable signal.

- [ ] **Step 6: Implement `internal/analyze/key.go`**

1. From the **same** `Spectrogram`, build a 12-element chroma vector per frame: map each
   bin to a pitch class via frequency, weight by magnitude, sum over frames with
   harmonic emphasis (weight lower octaves slightly higher).
2. Correlate the mean chroma against the Krumhansl-Schmuckler major and minor profiles
   using Pearson correlation.
3. Pick the best-scoring major and best-scoring minor; the higher wins.
4. **Confidence is the winning correlation minus the runner-up's.** A large margin means
   a clear key; a near-tie (as in white noise) means we do not know.
5. If `Confidence < KeyConfidenceThreshold` (0.55), return a `Key` with **empty**
   `Camelot` and `Name`. Do not return the best guess with a low score attached.

Implement `camelotToName`/`nameToCamelot` as exact inverses (test `TestCamelotRoundTrip`
enumerates all 24 codes). Camelot: B = major, A = minor; number = (pitchClass + 11) mod 12
+ 1 with C=8B.

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/analyze/ -v -race`
Expected: PASS — tempo, spacing, confidence, and key detection.

If tempo or key tests fail on detection accuracy, tune the algorithm parameters rather
than loosening the assertions: the tolerances in the tests (2 BPM, exact Camelot codes)
are the spec's requirement, not arbitrary.

- [ ] **Step 8: Wire BeatGrid onto Deck**

Add to `engine/deck.go`:

```go
// AttachBeatGrid installs an analysed grid. A nil grid leaves the deck
// playable with beatgrid-dependent features unavailable (spec §8).
func (d *Deck) AttachBeatGrid(g *analyze.BeatGrid)
func (d *Deck) BeatGrid() *analyze.BeatGrid
func (d *Deck) HasBeatGrid() bool
```

This introduces an `engine → analyze` import for types only. If that creates an import
cycle, move `BeatGrid` into a small `internal/analyze/types.go` with no dependencies.

- [ ] **Step 9: Commit**

```bash
git add internal/analyze/ internal/engine/
go mod tidy
git commit -m "feat(analyze): shared STFT feeding beatgrid and Camelot key detection"
```

---

### Task 7: Mixer, engine, snapshot, and the offline render CLI

**Files:**
- Create: `internal/engine/mixer.go`
- Create: `internal/engine/engine.go`
- Create: `internal/engine/snapshot.go`
- Create: `internal/engine/engine_test.go`
- Create: `cmd/mixrender/main.go`

**Interfaces:**
- Consumes: `Deck` and `seek` from Task 4, `Ring` from Task 5, decoders from Task 3.
- Produces:
  ```go
  package engine

  type Config struct {
      SampleRate    int
      BlockFrames   int   // 512
      RingFrames    int   // ~200ms at SampleRate
      DeckCount     int   // 2 in the MVP; N supported
      CuedDeck      int   // index of the deck the crossfader treats as "right"
  }

  type Engine struct { /* ... */ }
  func New(cfg Config) *Engine
  func (e *Engine) Apply(a Action)
  func (e *Engine) Snapshot() Snapshot
  func (e *Engine) Deck(i int) *Deck
  func (e *Engine) DeckCount() int

  // RenderInto renders one block from both decks through the mixer into the
  // two output rings. Called by the render goroutine, never by the callback.
  func (e *Engine) RenderInto(main, cue *audio.Ring) error

  type DeckState struct {
      Index int
      Playing bool
      Position time.Duration
      Duration time.Duration
      Pitch float64        // normalized
      BPM float64          // 0 if no beatgrid
      HasBeatGrid bool
      Loaded bool
      Path string
      Title string
      Artist string
      Level float64        // linear RMS of last block
      Peak  float64
  }

  type Snapshot struct {
      Decks    []DeckState
      Crossfader float64   // -1..1
      MasterLevel float64
      CueLevel  float64
      MasterPeak float64
      CuePeak   float64
      Underruns uint64     // sum across rings
  }
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/engine_test.go`:

```go
func TestEngineTwoDecksCrossfade(t *testing.T) {
    e := New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
                     RingFrames: 44100, DeckCount: 2})
    e.Deck(0).Load(memTrack(Sine(440, 5)))
    e.Deck(1).Load(memTrack(Sine(440, 5)))
    e.Apply(Action{Kind: DeckPlay, Target: 0})
    e.Apply(Action{Kind: DeckPlay, Target: 1})

    // centre the crossfader: equal power, so level is ~0.707 of one source
    out := renderN(e, 200)
    if math.IsNaN(out.RMS()) { t.Fatal("NaN in mix") }
    if out.RMS() < 0.01 { t.Errorf("mix is silent: %v", out.RMS()) }
}

func TestSnapshotReportsDeckState(t *testing.T) {
    e := newTestEngine()
    e.Deck(0).Load(memTrack(Sine(440, 5)))
    e.Apply(Action{Kind: DeckPlay, Target: 0})
    renderN(e, 50)
    s := e.Snapshot()
    if len(s.Decks) != 2 { t.Fatalf("snapshot has %d decks", len(s.Decks)) }
    d0 := s.Decks[0]
    if !d0.Loaded { t.Error("deck 0 not marked loaded") }
    if !d0.Playing { t.Error("deck 0 not marked playing") }
    if d0.Position <= 0 { t.Errorf("position = %v", d0.Position) }
    if d0.Level <= 0 { t.Errorf("level = %v", d0.Level) }
    if s.Decks[1].Loaded { t.Error("deck 1 should be unloaded") }
}

func TestSnapshotIsImmutableCopy(t *testing.T) {
    e := newTestEngine()
    e.Deck(0).Load(memTrack(Sine(440, 5)))
    e.Apply(Action{Kind: DeckPlay, Target: 0})
    renderN(e, 20)
    s1 := e.Snapshot()
    s1.Decks[0].Position = 99 * time.Hour  // mutate the copy
    renderN(e, 20)
    s2 := e.Snapshot()
    if s2.Decks[0].Position.Equal(99 * time.Hour) {
        t.Error("mutating a Snapshot changed engine state")
    }
}

func TestActionOnUnloadedDeckIsSafe(t *testing.T) {
    e := newTestEngine()
    // every action against an unloaded deck must not panic
    e.Apply(Action{Kind: DeckPlay, Target: 0})
    e.Apply(Action{Kind: DeckSeek, Target: 0, Value: 0.5})
    e.Apply(Action{Kind: DeckHotcueSet, Target: 0, Slot: 1})
    e.Apply(Action{Kind: DeckHotcueJump, Target: 0, Slot: 1})
    e.Apply(Action{Kind: DeckPitch, Target: 0, Value: 1})
    renderN(e, 10)
}

func TestActionOutOfRangeDeckIsIgnored(t *testing.T) {
    e := newTestEngine()
    e.Apply(Action{Kind: DeckPlay, Target: 99})   // must not panic
    e.Apply(Action{Kind: DeckPitch, Target: -5, Value: 1})
    renderN(e, 10)
}

func TestCrossfaderMovesLevelBetweenDecks(t *testing.T) {
    e := newTestEngine()
    e.Deck(0).Load(memTrack(Sine(440, 5)))
    e.Deck(1).Load(memTrack(Sine(440, 5)))
    e.Apply(Action{Kind: DeckPlay, Target: 0})
    e.Apply(Action{Kind: DeckPlay, Target: 1})
    e.Apply(Action{Kind: CrossfaderSet, Target: -1})
    left := renderN(e, 100)
    e.Apply(Action{Kind: CrossfaderSet, Target: 1})
    right := renderN(e, 100)
    if math.Abs(left.RMS()-right.RMS()) > 0.3 {
        t.Errorf("crossfader ends differ too much: %v vs %v", left.RMS(), right.RMS())
    }
}

func TestEngineSupportsMoreThanTwoDecks(t *testing.T) {
    // §2.2: deck count is never hardcoded
    e := New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
                     RingFrames: 44100, DeckCount: 4})
    if e.DeckCount() != 4 { t.Fatalf("deckCount = %d", e.DeckCount()) }
    for i := 0; i < 4; i++ {
        e.Deck(i).Load(memTrack(Sine(440+float64(i)*50, 5)))
        e.Apply(Action{Kind: DeckPlay, Target: i})
    }
    out := renderN(e, 100)
    if out.Peak() > 1.0 { t.Errorf("4-deck mix clipped: %v", out.Peak()) }
}

func TestApplyIsSafeFromAnotherGoroutine(t *testing.T) {
    // MIDI handlers call Apply concurrently with rendering (spec §3.2)
    e := newTestEngine()
    e.Deck(0).Load(memTrack(Sine(440, 30)))
    e.Apply(Action{Kind: DeckPlay, Target: 0})
    var wg sync.WaitGroup
    wg.Add(2)
    go func() { defer wg.Done(); for i := 0; i < 2000; i++ {
        e.Apply(Action{Kind: DeckPitch, Target: 0, Value: float64(i%100)/100})
    } }()
    go func() { defer wg.Done(); for i := 0; i < 200; i++ { renderN(e, 10) } }()
    wg.Wait()
    if math.IsNaN(e.Snapshot().Decks[0].Level) { t.Fatal("NaN after concurrent apply") }
}
```

Helpers:

```go
func newTestEngine() *Engine {
    return New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
                      RingFrames: 44100, DeckCount: 2})
}
// renderN drains the main ring after rendering, returning the audio
func renderN(e *Engine, blocks int) dsp.Stereo {
    main, cue := audio.NewRing(44100), audio.NewRing(44100)
    out := make(dsp.Stereo, 0, blocks*1024+44100*2)
    tmp := make(dsp.Stereo, 512*2)
    for i := 0; i < blocks; i++ {
        if err := e.RenderInto(main, cue); err != nil { return out }
        out = append(out, main.Read(tmp)...)
    }
    return out
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/ -run 'Engine|Snapshot|Action|Crossfader' -v`
Expected: FAIL — `undefined: New`, `undefined: Config`.

- [ ] **Step 3: Implement `internal/engine/mixer.go`**

```go
type strip struct {
    deck *Deck
    gain float64
    cueEnabled bool
    post dsp.Stereo   // preallocated
}
type Mixer struct {
    strips []strip
    xfader float64
    cuedIndex int
    master, cue dsp.Stereo  // preallocated accumulators
    limiter *dsp.Limiter
}
func (m *Mixer) mix(outMain, outCue dsp.Stereo) error
```

Per block: render each deck into its strip, apply `Gain` by `xfader` position, sum into
`master`; sum cue-enabled decks into `cue`; run the limiter on master only. **All buffers
preallocated in `New`** — `mix` must not allocate.

Add `CrossfaderSet` to `ActionKind`.

- [ ] **Step 4: Implement `internal/engine/snapshot.go`**

`DeckState`, `Snapshot` exactly as specified. `Snapshot()` takes the engine's mutex,
copies scalar fields, and allocates a fresh `[]DeckState` — it is the one place allowed
to allocate, since it runs on the UI goroutine, never the audio thread.

- [ ] **Step 5: Implement `internal/engine/engine.go`**

```go
type Engine struct {
    mu     sync.Mutex   // guards decks + mixer config; NOT held during mix
    decks  []*Deck
    mixer  *Mixer
    cfg    Config
    underruns atomic.Uint64
}
```

`Apply` takes `mu`, validates `Target` against `len(decks)`, and dispatches. Actions
against unloaded or out-of-range decks return silently.

`RenderInto` does **not** hold `mu` for the whole mix. It snapshots strip gains under the
lock, releases, then renders. This is what lets MIDI `Apply` calls interleave with audio
without blocking (spec §3.2). Add a comment stating this explicitly, since it looks like
a race until you know the intent.

Underrun counters from both rings are folded into `Snapshot.Underruns`.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/engine/ -v -race -count=2`
Expected: PASS — including the concurrency test.

- [ ] **Step 7: Implement `cmd/mixrender/main.go`**

```go
// mixrender renders an engine state sequence to a WAV file.
//
//	mixrender -out mix.wav -a track1.mp3 -b track2.wav \
//	    -play 0 -play 1 -xf 0 -loop-a 4beats
//
// Prints the resulting Snapshot to stdout when -stats is passed.
```

Flags: `-out` (required), `-a`/`-b` track paths, `-play` (repeatable deck indices),
`-xf` crossfader -1..1, `-seconds` duration, `-loop-a`/`-loop-b` beat loop sizes,
`-stats`.

Write with `go-audio/wav` as 16-bit PCM stereo at 44100. Convert float32 → int16 with
clamping. This binary is the spec's §9 verification harness: the same engine code that
would play to a speaker writes to a file.

- [ ] **Step 8: Run the CLI end to end**

Run: `go run ./cmd/mixrender -out /tmp/mix.wav -a internal/testdata/fixtures/sine440.wav -b internal/testdata/fixtures/sine440.wav -play 0 -play 1 -seconds 4 -stats`
Expected: writes `/tmp/mix.wav`, prints a snapshot with both decks loaded and playing,
and `MasterLevel` > 0. Verify with `ls -la /tmp/mix.wav` — a 4-second stereo 44.1k 16-bit
file is ~705KB.

- [ ] **Step 9: Commit**

```bash
git add internal/engine/ cmd/mixrender/
git commit -m "feat(engine): N-deck mixer, engine action loop, snapshot, and mixrender CLI"
```

---

### Task 8: Loops — beat-quantised, saved, and seamless via seek

**Files:**
- Create: `internal/engine/loop.go`
- Create: `internal/engine/loop_test.go`

**Interfaces:**
- Consumes: `Deck.seek` from Task 4, `BeatGrid` from Task 6.
- Produces — add to `ActionKind`:
  ```go
  LoopBeat       // Value: 0..1 across {1/4,1/2,1,2,4,8,16,32} beats
  LoopIn         // set loop in at current position
  LoopOut        // set loop out at current position
  LoopSave       // Slot: persist current loop
  LoopRecall     // Slot
  LoopRelease
  LoopRoll       // momentary: Value = roll length in beats
  ```
  And to `Deck`:
  ```go
  type LoopState struct {
      Active   bool
      Start    time.Duration
      End      time.Duration
      BeatCount float64   // 0 for a free loop
      Rolled   bool
      SavedSlots map[int]SavedLoop
  }
  type SavedLoop struct { Start, End time.Duration }

  func (d *Deck) Loop() LoopState
  func (d *Deck) SetLoopBeat(n float64) error  // errors if no beatgrid
  func (d *Deck) SetLoopIn() error
  func (d *Deck) SetLoopOut() error
  func (d *Deck) SaveLoop(slot int) error
  func (d *Deck) RecallLoop(slot int) error
  func (d *Deck) ReleaseLoop()
  func (d *Deck) PushRoll(beats float64) error
  func (d *Deck) PopRoll() error
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/loop_test.go`:

```go
func TestBeatLoopWrapsAndIsClickFree(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 10.0)))
    d.AttachBeatGrid(testGrid(128.0, 4))
    d.Play()
    if err := d.SetLoopBeat(4); err != nil { t.Fatal(err) }
    ls := d.Loop()
    if !ls.Active { t.Fatal("loop not active") }
    if math.Abs(ls.BeatCount-4) > 1e-9 { t.Errorf("beatCount = %v", ls.BeatCount) }

    // render past several wraps
    tmp := make(dsp.Stereo, 512*2)
    all := make(dsp.Stereo, 0)
    for i := 0; i < 900; i++ { d.Render(tmp); all = append(all, tmp...) }

    // the position must never leave the loop region
    if d.Pos() < ls.Start || d.Pos() > ls.End+50*time.Millisecond {
        t.Errorf("position %v escaped loop [%v, %v]", d.Pos(), ls.Start, ls.End)
    }
    // a wrap must not produce a discontinuity bigger than the tone's slope
    if jump := detectWraps(all, dsp.SampleRate, ls.Start, ls.End); jump > maxSlope(460) {
        t.Errorf("loop wrap discontinuity %v exceeds slope %v", jump, maxSlope(460))
    }
}

func TestBeatLoopAtEndOfTrackWrapsNotStops(t *testing.T) {
    // Review Focus #1: a loop starting near the track end must WRAP to
    // loopStart, not stop at the track end or panic on out-of-range.
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 2.0)))
    d.AttachBeatGrid(testGrid(120.0, 8))
    d.Play()
    d.seek(1900 * time.Millisecond)
    if err := d.SetLoopBeat(1); err != nil { t.Fatal(err) }
    tmp := make(dsp.Stereo, 512*2)
    for i := 0; i < 600; i++ {
        d.Render(tmp)   // must not panic, must not go silent
    }
    if tmp.Peak() == 0 { t.Error("deck went silent at track end with an active loop") }
    if !d.Loop().Active { t.Error("loop deactivated at track end") }
}

func TestBeatLoopLengthTracksPitch(t *testing.T) {
    // Review Focus #3: a 4-beat loop must contain 4 beats at ANY pitch.
    for _, pitch := range []float64{-1.0, -0.5, 0.0, 0.5, 1.0} {
        d := NewDeck(0)
        d.Load(memTrack(dsp.Sine(440, 20.0)))
        d.AttachBeatGrid(testGrid(128.0, 40))
        d.SetPitch(pitch)
        d.Play()
        if err := d.SetLoopBeat(4); err != nil { t.Fatal(err) }
        ls := d.Loop()
        want := 4 * time.Second / 128.0
        if math.Abs(ls.End.Seconds()-ls.Start.Seconds()-want.Seconds()) > 0.01 {
            t.Errorf("pitch %v: loop = %v, want %v beats = %v",
                pitch, ls.End-ls.Start, 4, want)
        }
    }
}

func TestBeatLoopWithoutGridErrors(t *testing.T) {
    // Review Focus #4: no grid means the feature is unavailable, not wrong
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 5.0)))
    if err := d.SetLoopBeat(4); err == nil {
        t.Error("beat loop on a deck with no beatgrid should error")
    }
    if d.Loop().Active { t.Error("loop activated without a beatgrid") }
}

func TestFreeLoopAndSaveRecall(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 10.0)))
    d.Play()
    d.seek(2 * time.Second)
    if err := d.SetLoopIn(); err != nil { t.Fatal(err) }
    d.seek(4 * time.Second)
    if err := d.SetLoopOut(); err != nil { t.Fatal(err) }
    ls := d.Loop()
    if ls.BeatCount != 0 { t.Errorf("free loop has BeatCount %v, want 0", ls.BeatCount) }
    if err := d.SaveLoop(3); err != nil { t.Fatal(err) }
    d.ReleaseLoop()
    if d.Loop().Active { t.Fatal("still active after release") }
    if err := d.RecallLoop(3); err != nil { t.Fatal(err) }
    if !d.Loop().Active { t.Fatal("recall did not activate") }
}

func TestLoopOutBeforeInErrors(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 10.0)))
    d.seek(4 * time.Second)
    d.SetLoopIn()
    d.seek(2 * time.Second)
    if err := d.SetLoopOut(); err == nil {
        t.Error("loop out before loop in should error")
    }
}

func TestLoopOutAtTrackEndIsClamped(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 3.0)))
    d.Play()
    d.seek(1 * time.Second)
    d.SetLoopIn()
    d.seek(10 * time.Second)   // past the end
    if err := d.SetLoopOut(); err != nil { t.Fatal(err) }
    if d.Loop().End > d.Duration() {
        t.Errorf("loop end %v past duration %v", d.Loop().End, d.Duration())
    }
}

func TestLoopReleaseContinuesFromLoopEnd(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 10.0)))
    d.AttachBeatGrid(testGrid(128.0, 20))
    d.Play()
    d.SetLoopBeat(4)
    tmp := make(dsp.Stereo, 512*2)
    for i := 0; i < 200; i++ { d.Render(tmp) }
    d.ReleaseLoop()
    if d.Loop().Active { t.Fatal("still looping") }
    p1 := d.Pos()
    for i := 0; i < 100; i++ { d.Render(tmp) }
    if !d.Pos().After(p1) { t.Error("position did not advance after release") }
}
```

Create the roll tests in the same file:

```go
func TestRollRestoresPositionOnRelease(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 20.0)))
    d.AttachBeatGrid(testGrid(128.0, 40))
    d.Play()
    tmp := make(dsp.Stereo, 512*2)
    for i := 0; i < 100; i++ { d.Render(tmp) }
    before := d.Pos()
    if err := d.PushRoll(1); err != nil { t.Fatal(err) }
    for i := 0; i < 100; i++ { d.Render(tmp) }
    if d.Pos().Equal(before) { t.Error("roll did not move the playhead") }
    if err := d.PopRoll(); err != nil { t.Fatal(err) }
    if d.Pos() != before {
        t.Errorf("after release pos = %v, want %v", d.Pos(), before)
    }
}

func TestRollReleaseWithEmptyStackIsNoOp(t *testing.T) {
    // Review Focus #2: a stray release must NEVER move the playhead.
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 10.0)))
    d.Play()
    tmp := make(dsp.Stereo, 512*2)
    for i := 0; i < 50; i++ { d.Render(tmp) }
    before := d.Pos()
    if err := d.PopRoll(); err == nil {
        t.Error("PopRoll on an empty stack should error")
    }
    if d.Pos() != before {
        t.Errorf("stray release moved playhead %v -> %v", before, d.Pos())
    }
}

func TestRollFromInsideLoopRestoresLoop(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 20.0)))
    d.AttachBeatGrid(testGrid(128.0, 40))
    d.Play()
    d.SetLoopBeat(8)
    tmp := make(dsp.Stereo, 512*2)
    for i := 0; i < 100; i++ { d.Render(tmp) }
    beforePos, beforeActive := d.Pos(), d.Loop().Active
    d.PushRoll(1)
    for i := 0; i < 60; i++ { d.Render(tmp) }
    d.PopRoll()
    if d.Pos() != beforePos { t.Errorf("pos = %v, want %v", d.Pos(), beforePos) }
    if d.Loop().Active != beforeActive {
        t.Errorf("loop active = %v, want %v", d.Loop().Active, beforeActive)
    }
}

func TestRollWithoutGridErrors(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 5)))
    if err := d.PushRoll(1); err == nil {
        t.Error("roll on a deck with no beatgrid should error")
    }
}
```

Helpers:

```go
func testGrid(bpm float64, beats int) *analyze.BeatGrid {
    g := &analyze.BeatGrid{BPM: bpm, Confidence: 0.9}
    per := time.Duration(float64(time.Second) / bpm)
    for i := 0; i < beats; i++ { g.Beats = append(g.Beats, time.Duration(i)*per) }
    return g
}
func maxSlope(freq float64) float64 { return 2 * math.Pi * freq / dsp.SampleRate }
func detectWraps(all dsp.Stereo, rate int, start, end time.Duration) float64
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/ -run Loop -v`
Expected: FAIL — `undefined: SetLoopBeat`, `undefined: PushRoll`.

- [ ] **Step 3: Implement `internal/engine/loop.go`**

```go
type rollFrame struct {
    pos    float64
    loop   loopCore
    rolled bool
}
// Add these three fields to the existing Deck struct from Task 4. Do not
// restate or alter the fields already defined there.
type Deck struct {
    loop      loopCore
    savedLoop map[int]SavedLoop
    rollStack []rollFrame
}
```

`NewDeck` must additionally initialise `savedLoop` to an empty map and `rollStack` to an
empty slice, so `SaveLoop` and `PushRoll` never write to a nil map.

**`Render` integration** — this is the load-bearing change. At the top of `Render`, after
computing how far the read head advanced, check for a wrap:

```go
if d.loop.active {
    for d.pos >= loopEndSamples {
        d.seek(d.loop.start)   // not a raw seek: this primes resampler state,
                               // which is why the wrap is inaudible (§4.4.1)
    }
}
```

Crucially, `d.seek` must reset the loop-end comparison state so the wrap does not
re-trigger infinitely.

**`SetLoopBeat(n)`:** requires `d.beatgrid != nil` (error otherwise). Find the beat at or
after the current position, take `n` beats from there via the grid's beat period, and set
`start`/`end` in samples. Clamp `end` to the track duration.

**Wrap at track end:** the wrap check runs *before* the end-of-track stop check, so a loop
extending past the track end wraps rather than stopping. Test
`TestBeatLoopAtEndOfTrackWrapsNotStops` pins this ordering.

**`PushRoll`/`PopRoll`:** push a `rollFrame` capturing position and loop state, then set a
1-beat (or `beats`) loop. `PopRoll` pops and restores via `d.seek`. **On an empty stack,
return an error and do not touch the playhead.**

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/engine/ -run Loop -v -race`
Expected: PASS — all loop and roll tests.

**If `TestBeatLoopWrapsAndIsClickFree` fails on discontinuity, the bug is in `seek`
priming, not in the loop code.** Go back to Task 4's `TestPrimeEliminatesSeekDiscontinuity`
and fix it there. This is the single most important failure to diagnose correctly, because
an audible loop seam is otherwise unfixable after the fact.

- [ ] **Step 5: Verify no allocation crept into the looped render path**

Run: `go test ./internal/engine/ -bench=DeckRender -benchtime=500x -benchmem`
Expected: still `0 allocs/op` with loops active. The wrap check must not allocate.

- [ ] **Step 6: Commit**

```bash
git add internal/engine/
git commit -m "feat(engine): beat-quantised, saved, and roll loops via the seek primitive"
```

---

### Task 9: Sync and quantize

**Files:**
- Create: `internal/engine/sync.go`
- Create: `internal/engine/sync_test.go`

**Interfaces:**
- Consumes: `Deck`, `BeatGrid`, `LoopState` from Tasks 4, 6, 8.
- Produces — add to `ActionKind`:
  ```go
  Sync        // engage tempo sync to the master deck
  SyncToggle
  Quantize    // nudge position to the nearest beat
  ```
  And to `Deck`:
  ```go
  func (d *Deck) EngageSync(master *Deck) error  // errors if either lacks a grid
  func (d *Deck) DisableSync()
  func (d *Deck) SyncActive() bool
  func (d *Deck) QuantizeToNearestBeat() error   // errors if no beatgrid
  func (d *Deck) NudgeBeats(delta float64) error
  // DeltaBeat reports the signed offset in ms between this deck's nearest beat
  // and the master's nearest beat — the ΔBEAT readout.
  func (d *Deck) DeltaBeat(master *Deck) (time.Duration, error)
  ```

**Stated rules** (spec §5.6) that the implementation must follow, each with a test:

- Sync may retune a looped deck but never re-lays its loop.
- Quantize may nudge a loop start on creation but never while the loop is playing.

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/sync_test.go`:

```go
func TestSyncMatchesTempos(t *testing.T) {
    master := NewDeck(0)
    master.Load(memTrack(dsp.Sine(440, 30)))
    master.AttachBeatGrid(testGrid(128.0, 60))
    slave := NewDeck(1)
    slave.Load(memTrack(dsp.Sine(440, 30)))
    slave.AttachBeatGrid(testGrid(124.0, 60))
    if err := slave.EngageSync(master); err != nil { t.Fatal(err) }
    // pitch should move toward matching: 128/124 = 1.032
    ratio := slave.PitchRatio()
    if ratio < 1.02 || ratio > 1.05 {
        t.Errorf("sync pitch ratio = %v, want ~1.032", ratio)
    }
}

func TestSyncRequiresBothGrids(t *testing.T) {
    master := NewDeck(0)
    master.Load(memTrack(dsp.Sine(440, 10)))
    master.AttachBeatGrid(testGrid(128, 20))
    slave := NewDeck(1)
    slave.Load(memTrack(dsp.Sine(440, 10)))
    // no grid on the slave
    if err := slave.EngageSync(master); err == nil {
        t.Error("sync without a slave beatgrid should error")
    }
    if slave.SyncActive() { t.Error("sync engaged despite error") }
}

func TestSyncWithoutMasterGridErrors(t *testing.T) {
    m := NewDeck(0); m.Load(memTrack(dsp.Sine(440, 10)))
    s := NewDeck(1); s.Load(memTrack(dsp.Sine(440, 10)))
    s.AttachBeatGrid(testGrid(128, 20))
    if err := s.EngageSync(m); err == nil { t.Error("expected error") }
}

func TestQuantizeSnapsToNearestBeat(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 20)))
    d.AttachBeatGrid(testGrid(120.0, 40))
    d.seek(1050 * time.Millisecond)   // beat is at 1000ms; 50ms late
    if err := d.QuantizeToNearestBeat(); err != nil { t.Fatal(err) }
    if math.Abs(d.Pos().Seconds()-1.0) > 0.01 {
        t.Errorf("after quantize pos = %v, want 1.0s", d.Pos().Seconds())
    }
}

func TestQuantizeIsClickFree(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 20)))
    d.AttachBeatGrid(testGrid(120.0, 40))
    d.Play()
    tmp := make(dsp.Stereo, 512*2)
    for i := 0; i < 100; i++ { d.Render(tmp) }
    before := make(dsp.Stereo, len(tmp))
    copy(before, tmp)
    d.QuantizeToNearestBeat()
    d.Render(tmp)
    // quantize is a seek; it must prime state just like any other
    if math.IsNaN(tmp.RMS()) { t.Fatal("quantize produced NaN") }
    if tmp.Peak() == 0 { t.Error("quantize silenced the deck") }
}

func TestQuantizeWithoutGridErrors(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 10)))
    if err := d.QuantizeToNearestBeat(); err == nil {
        t.Error("quantize without a beatgrid should error")
    }
}

func TestDeltaBeatReportsOffset(t *testing.T) {
    master := NewDeck(0)
    master.Load(memTrack(dsp.Sine(440, 30)))
    master.AttachBeatGrid(testGrid(128.0, 60))
    master.seek(4 * time.Second)   // exactly on a beat
    slave := NewDeck(1)
    slave.Load(memTrack(dsp.Sine(440, 30)))
    slave.AttachBeatGrid(testGrid(128.0, 60))
    slave.seek(4*time.Second + 40*time.Millisecond)
    delta, err := slave.DeltaBeat(master)
    if err != nil { t.Fatal(err) }
    if math.Abs(delta.Seconds()-0.040) > 0.005 {
        t.Errorf("delta = %v, want ~40ms", delta)
    }
}

func TestSyncDoesNotRelayLoops(t *testing.T) {
    // §5.6: sync retunes the deck but must NOT move the loop
    master := NewDeck(0)
    master.Load(memTrack(dsp.Sine(440, 40)))
    master.AttachBeatGrid(testGrid(128.0, 80))
    slave := NewDeck(1)
    slave.Load(memTrack(dsp.Sine(440, 40)))
    slave.AttachBeatGrid(testGrid(128.0, 80))
    slave.Play()
    slave.SetLoopBeat(4)
    ls := slave.Loop()
    wantLen := ls.End - ls.Start
    if err := slave.EngageSync(master); err != nil { t.Fatal(err) }
    got := slave.Loop()
    if got.Start != ls.Start || got.End != ls.End {
        t.Errorf("sync moved the loop: %v-%v -> %v-%v", ls.Start, ls.End, got.Start, got.End)
    }
    if got.End-got.Start != wantLen {
        t.Errorf("sync changed loop length: %v -> %v", wantLen, got.End-got.Start)
    }
}

func TestNudgeBeats(t *testing.T) {
    d := NewDeck(0)
    d.Load(memTrack(dsp.Sine(440, 20)))
    d.AttachBeatGrid(testGrid(128.0, 40))
    d.seek(4 * time.Second)
    if err := d.NudgeBeats(-1); err != nil { t.Fatal(err) }
    want := 4*time.Second - time.Second*60/128
    if math.Abs(d.Pos().Seconds()-want.Seconds()) > 0.01 {
        t.Errorf("after -1 beat pos = %v, want %v", d.Pos(), want)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/ -run 'Sync|Quantize|Delta|Nudge' -v`
Expected: FAIL — `undefined: EngageSync`.

- [ ] **Step 3: Implement `internal/engine/sync.go`**

```go
// EngageSync matches this deck's tempo to the master. It sets pitch only —
// loop positions are never modified (§5.6).
func (d *Deck) EngageSync(master *Deck) error {
    if d.beatgrid == nil || master.beatgrid == nil {
        return analyze.ErrNoBeatGrid
    }
    if master.beatgrid.BPM <= 0 || d.beatgrid.BPM <= 0 {
        return analyze.ErrNoBeatGrid
    }
    ratio := master.beatgrid.BPM / d.beatgrid.BPM
    d.SetPitchNormalized(dsp.ClampNormalized(RatioToNormalized(ratio)))
    d.synced = true
    return nil
}
```

`RatioToNormalized` inverts `SetPitch`: `normalized = (ratio - 1) / PitchRange`, clamped
to `[-1, 1]` so an extreme tempo difference cannot push the deck to an absurd speed.
Clamp the resulting pitch so a 200 BPM master against a 60 BPM track locks both ends
rather than producing a 3.3× speed-up.

`QuantizeToNearestBeat`: find the nearest grid beat to `d.pos`, and if the difference is
under half a beat, `d.seek` to it. **Use `seek`, never a raw position write** — otherwise
quantize introduces the exact click the seek primitive exists to prevent.

`DeltaBeat`: convert each deck's position to samples, find the nearest beat index in each
grid, compute the time to each nearest beat, and return the slave's offset from the
master's. Clamp to ±half a beat.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/engine/ -run 'Sync|Quantize|Delta|Nudge' -v -race`
Expected: PASS.

- [ ] **Step 5: Wire sync into `Engine.Apply`**

Handle `Sync`, `SyncToggle`, and `Quantize` in `Apply`. `Sync` with `Target` = the slave
deck uses `CuedDeck` (or the other deck) as master. Add `Engine.SyncPair(a, b int) error`
so `mixrender` can drive it.

- [ ] **Step 6: Commit**

```bash
git add internal/engine/
git commit -m "feat(engine): tempo sync, beat quantize, and delta-beat readout"
```

---

### Task 10: Stubs for later milestones, and the end-to-end acceptance render

**Files:**
- Create: `internal/engine/render_target.go`
- Create: `internal/engine/acceptance_test.go`
- Create: `cmd/mixrender/README.md`

**Interfaces:**
- Consumes: everything above.
- Produces:
  ```go
  // RenderTarget abstracts the realtime consumer so milestones 2 and 3 can
  // attach without changing the engine.
  type RenderTarget interface {
      // Consume is called from the audio callback. Must not block, allocate,
      // or take a lock. Returning false signals an underrun.
      Consume(out dsp.Stereo) bool
  }
  // RingTarget adapts an audio.Ring to RenderTarget.
  func RingTarget(r *audio.Ring) RenderTarget
  // FuncTarget adapts a callback for tests.
  func FuncTarget(fn func(dsp.Stereo)) RenderTarget
  ```

- [ ] **Step 1: Write the failing acceptance test**

Create `internal/engine/acceptance_test.go`:

```go
// TestAcceptanceFullMix renders a complete DJ performance and asserts the
// properties the spec promises. This is the milestone-1 exit criterion.
func TestAcceptanceFullMix(t *testing.T) {
    e := New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
                     RingFrames: 44100, DeckCount: 2, CuedDeck: 1})

    // two tracks with known grids
    d0, d1 := e.Deck(0), e.Deck(1)
    d0.Load(memTrack(dsp.Sine(220, 60)))
    d0.AttachBeatGrid(testGrid(128.0, 120))
    d1.Load(memTrack(dsp.Sine(330, 60)))
    d1.AttachBeatGrid(testGrid(124.0, 120))

    e.Apply(Action{Kind: DeckPlay, Target: 0})
    e.Apply(Action{Kind: DeckPlay, Target: 1})
    e.Apply(Action{Kind: CrossfaderSet, Target: 0.0})

    var collected dsp.Stereo
    main, cue := audio.NewRing(1 << 20), audio.NewRing(1 << 20)
    tmp := make(dsp.Stereo, 512*2)
    blocks := 512   // ~6 seconds
    for i := 0; i < blocks; i++ {
        if err := e.RenderInto(main, cue); err != nil { t.Fatal(err) }
        for main.AvailableRead() >= 512 {
            n := main.Read(tmp)
            collected = append(collected, tmp[:n*2]...)
        }
    }

    s := e.Snapshot()
    // 1. both decks played
    if !s.Decks[0].Playing || !s.Decks[1].Playing {
        t.Errorf("both decks should be playing: %+v", s.Decks)
    }
    // 2. audio was produced and never clipped
    if collected.RMS() < 0.01 { t.Errorf("output silent: %v", collected.RMS()) }
    if collected.Peak() > 1.0 { t.Errorf("output clipped: %v", collected.Peak()) }
    // 3. the limiter held the ceiling
    if s.MasterPeak > 0.999 { t.Errorf("master peak %v at ceiling", s.MasterPeak) }
    // 4. levels are reported
    if s.MasterLevel <= 0 { t.Errorf("master level = %v", s.MasterLevel) }
    // 5. both positions advanced
    if s.Decks[0].Position <= 0 || s.Decks[1].Position <= 0 {
        t.Errorf("positions did not advance: %+v", s.Decks)
    }
}

func TestAcceptanceLoopRollSyncSequence(t *testing.T) {
    // the performance path: play → beat loop → sync → quantize → roll → release
    e := New(Config{SampleRate: dsp.SampleRate, BlockFrames: 512,
                     RingFrames: 44100, DeckCount: 2})
    m, s := e.Deck(0), e.Deck(1)
    m.Load(memTrack(dsp.Sine(220, 60))); m.AttachBeatGrid(testGrid(128, 120))
    s.Load(memTrack(dsp.Sine(330, 60))); s.AttachBeatGrid(testGrid(124, 120))
    e.Apply(Action{Kind: DeckPlay, Target: 0})
    e.Apply(Action{Kind: DeckPlay, Target: 1})

    step := func(n int) { for i := 0; i < n; i++ { e.RenderInto(
        audio.NewRing(1024), audio.NewRing(1024)) } }
    step(50)
    e.Apply(Action{Kind: LoopBeat, Target: 1, Value: 0.5})   // ~4 beats
    step(100)
    if !s.Loop().Active { t.Fatal("beat loop did not engage") }

    if err := s.EngageSync(m); err != nil { t.Fatal(err) }
    step(100)
    if math.Abs(s.PitchRatio()-1.0) > 0.05 {
        t.Errorf("after sync ratio = %v, want ~1.0", s.PitchRatio())
    }
    if !s.Loop().Active { t.Error("sync deactivated the loop") }

    s.PushRoll(1)
    step(50)
    s.PopRoll()
    if !s.Loop().Active { t.Error("roll release dropped the underlying loop") }

    e.Apply(Action{Kind: LoopRelease, Target: 1})
    step(50)
    if s.Loop().Active { t.Error("loop still active after release") }
    if math.IsNaN(e.Snapshot().MasterLevel) { t.Fatal("NaN in final snapshot") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/ -run Acceptance -v`
Expected: FAIL — `undefined: RingTarget`.

- [ ] **Step 3: Implement `internal/engine/render_target.go`**

```go
type RenderTarget interface { Consume(out dsp.Stereo) bool }

type ringTarget struct{ r *audio.Ring }
func RingTarget(r *audio.Ring) RenderTarget { return ringTarget{r} }
func (t ringTarget) Consume(out dsp.Stereo) bool { return t.r.Read(out) > 0 }

type funcTarget struct{ fn func(dsp.Stereo) }
func FuncTarget(fn func(dsp.Stereo)) RenderTarget { return funcTarget{fn} }
func (t funcTarget) Consume(out dsp.Stereo) bool { t.fn(out); return true }
```

Document the interface contract: **implementations must not block or allocate**, since
milestone 2 will call `Consume` from the PipeWire audio thread.

- [ ] **Step 4: Extend `mixrender` with the full performance flags**

Add to `cmd/mixrender/main.go`: `-loop-a`, `-loop-b` (beat counts), `-sync` (engage sync
from B to A), `-quantize`, `-roll` seconds, `-xf` (already present). Print a summary of
the final snapshot including `Underruns`.

- [ ] **Step 5: Write `cmd/mixrender/README.md`**

Document each flag with a one-line example, and include the canonical acceptance command:

```bash
go run ./cmd/mixrender -out /tmp/demo.wav \
  -a trackA.mp3 -b trackB.mp3 -play 0 -play 1 \
  -sync -loop-a 4 -seconds 30 -stats
```

Explain that this is the spec's §9 verification harness: the engine code path is identical
to live playback, and the output is inspectable with any audio tool.

- [ ] **Step 6: Run the full test suite**

Run: `go test ./... -race -count=1`
Expected: PASS — every package.

Run: `go vet ./...`
Expected: no output.

Run: `gofmt -l .`
Expected: no output. Fix anything listed before committing.

- [ ] **Step 7: Verify the realtime budget one final time**

Run: `go test ./internal/engine/ -bench=. -benchtime=1000x -benchmem`
Expected: `0 allocs/op` on `BenchmarkDeckRender`. This is the §4.3 contract and the one
invariant that cannot be verified by listening to the output.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat: render target abstraction and end-to-end acceptance tests"
```

---

## Self-Review

**Spec coverage.** Every §1–§12 requirement that belongs to the audio core maps to a task:

| Spec section | Task |
|---|---|
| §2.1 pitch fader is a resampler | 4 (`TestPitchChangesTempo`) |
| §2.2 deck count not hardcoded | 7 (`TestEngineSupportsMoreThanTwoDecks`) |
| §3.1 package layout | all |
| §3.2 engine boundary, no device imports | all; Global Constraints enforce |
| §4.1 decode, shared FFT, analysis cache | 3, 6 (cache deferred to a follow-up — see below) |
| §4.2 realtime signal chain | 1, 2, 7 |
| §4.3 realtime contract, no alloc, no lock | 4 (bench), 5 (ring), Global Constraints |
| §4.4 loops: seek, beat, saved, roll | 4 (seek), 8 (loops) |
| §5.1–5.4 action layer | 4, 7, 8, 9 |
| §5.5 loop actions | 8 |
| §5.6 loops and sync | 9 |
| §6.2 waveform/loop rendering | deferred to milestone 3 (TUI) |
| §7 MIDI scope | deferred to milestone 2 |
| §8 error handling | 4, 6, 8, 9 (each error path has a test) |
| §9 testing strategy | every task; 10 is the acceptance gate |
| §10 config | deferred to milestone 3 |
| §11.1–11.3 dependencies | 3, 6; cgo deferred to milestone 2 |

**Explicitly deferred beyond the spec's §12**, and why it is safe: the **analysis cache
on disk** (§4.1) and **crate persistence** (§6.5). Both are I/O over data that already
exists in memory; they belong with the library scanner in milestone 3 and cannot affect
audio correctness.

**Review Focus coverage** — each of the five has a test in the task that owns the code:
1. Loop at track end → Task 8 `TestBeatLoopAtEndOfTrackWrapsNotStops`
2. Stray roll release → Task 9 `TestRollReleaseWithEmptyStackIsNoOp`
3. Beat loop under pitch → Task 8 `TestBeatLoopLengthTracksPitch`
4. No beatgrid degradation → Tasks 6, 8, 9
5. Seek near boundaries → Task 4 `TestSeekToZeroDoesNotPanic`

**Proportion.** This plan is roughly 2× the spec's length, which is appropriate: the spec
states intent, the plan states signatures, exact test assertions, and commands. Code
bodies appear only for the two algorithms that are genuinely not determined by their
signatures — the resampler's `Prime` semantics and the `Render` wrap check.