# mix-tui — Design Specification

**Date:** 2026-10-10
**Status:** Complete — awaiting review
**Subject:** A terminal-based DJ mixing system for two (later, N) decks.

---

## 1. Purpose and Success Criteria

mix-tui is a terminal DJ application in the lineage of Serato, Mixxx, and Traktor. It
plays two decks of audio, mixes them with pitch, EQ, filters, and a crossfader, and
supports beatmatching through detected beatgrids with tempo sync and beat quantize.

It is driven by MIDI controllers (primary target: **Allen & Heath Xone K2**, a
class-compliant device exposing its own 4-channel soundcard), by keyboard, and by both
at once. Every control is remappable through per-device configuration files.

Tracks come from a **local library**: scanned directories, saved crates, tag metadata,
and automatically detected beatgrid and Camelot key (§6.5).

Success for the MVP is: **a user can browse a library, load two tracks, beatmatch them by
ear and by eye, and mix them out to a live PipeWire stream using a MIDI controller**, with
no mouse and no GUI.

Out of scope for the MVP, deliberately:

- Samplers, effects, recording, and streaming
- Multi-deck beyond 2 (the data model supports N; the MVP ships 2)
- Rule-based / smart crates (static lists only — see §6.5)
- 4-channel device routing (the streams are independent; routing is config — see §12)
- MIDI clock / DJLink sync to external tables
- Sysex, program change, and MIDI clock messages (counted and ignored, see §7)

Loops **are** in the MVP (§4.4) — beat-quantised, saved, and loop roll. The work is mostly
the seek primitive they share with hotcues and sync, so scoping them later would not have
saved any effort.

---

## 2. Key Technical Decisions

These were settled during design and constrain everything else.

| Decision | Choice | Rationale |
|---|---|---|
| Process architecture | Engine owns all state; TUI is a thin view | MIDI and repaint have incompatible timing needs. Decoupling keeps knob turns responsive. Makes the engine testable with no terminal and no sound card. |
| Audio output | PipeWire via hand-written cgo bindings | Native on Linux; independent MAIN/CUE streams make 4-channel routing a config change, not a redesign. See §11.1. |
| Audio decoding | Pure Go | Static binary, no system codec deps, cgo-free DSP core. |
| Realtime safety | Render-ahead into lock-free ring buffers; PipeWire callback only memcpys | Go's GC and scheduler will otherwise produce audible clicks. |
| Pitch control | Resample ratio (pitch and tempo change together) | This is what a turntable pitch fader does. Avoids building a time-stretcher. |
| Verification | Offline render to WAV, assertions on the signal | Pitch/EQ/sync bugs are inaudible in a diff. |
| Config format | YAML for app config and MIDI mappings | Human-editable with comments; mappings nest heavily. |
| Config location | `~/.config/mix-tui/`, repo-local `.devconfig/` for testing | Real config stays untouched during development. |

### 2.1 The pitch fader is a resampler

A DJ pitch fader changes pitch **and** tempo together, exactly like a turntable. So
`deck.pitch` is a resample ratio and nothing more. ±8% pitch is ±8% tempo.

Beatmatching therefore means matching tempo with that fader — which is precisely how it
works on real gear. This removes any need for a WSOLA or phase-vocoder time-stretcher,
which would have been the single largest piece of DSP work in the project.

### 2.2 Deck count is not hardcoded

`Mixer` holds `[]Strip` and iterates over it. The crossfader operates on two *selected*
decks by index (A/B in the MVP), not on hardcoded A and B. Each deck owns a cue send.
Adding a fourth deck means appending a strip and assigning it to a cue bus; no
refactoring. `engine` never assumes a count.

---

## 3. Architecture

### 3.1 Package layout

```
cmd/mix-tui/          main entrypoint
cmd/mixrender/        offline render CLI — dumps engine state to WAV for testing
internal/
  audio/
    decode/           mp3, wav, flac, ogg → PCM frames (pure Go)
    dsp/              resample, 3-band EQ, filter, crossfader, limiter
    ring/             lock-free SPSC ring buffer
  engine/
    deck.go           per-deck: position, pitch, EQ, filter, hotcues, beatgrid
    loop.go           loop state, beat loops, saved loops, loop roll
    seek.go           playhead seek — primes resampler/filter state (see §4.4)
    mixer.go          N channel strips, crossfader over 2 selected decks, master
    engine.go         owns all state; Apply(Action); Snapshot()
    sync.go           tempo match + beat quantize
  analyze/
    fft.go            shared STFT — feeds both beatgrid and key detection
    beatgrid.go       onset → tempo → beat tracking
    key.go            chroma → Camelot key + confidence
    cache.go          analysis cache keyed by path + size + mtime
  library/
    library.go        scan roots, build track index
    crate.go          static crate lists
    scan.go           background scanner with progress
  pipewire/
    output.go         stream + render goroutine + ring buffers
  midi/               device discovery, mapping → Action
  config/             YAML app config, library config, per-device mappings
  tui/                bubbletea model, lipgloss views, braille waveforms, ASCII art
```

### 3.2 The two load-bearing boundaries

**`engine.Engine.Apply(Action)` / `engine.Engine.Snapshot()`**

The only door into the audio world. Callers send Actions in and read an immutable
Snapshot out. Snapshot returns a value type — deck positions, BPM, levels, cue state,
flags, ring-buffer underrun counters. Cheap to copy, safe to render from any goroutine,
never holds a reference into mutable engine state.

**`engine` imports nothing from `pipewire`, `midi`, `tui`, `library`, or `analyze`.**

The engine takes PCM in and emits PCM out. It knows nothing about devices, terminals, or
the file system. Beatgrid and key data reach it as already-computed values on the Deck,
not as a dependency on the code that computed them.
This is what makes offline rendering to WAV possible: the identical code path that
plays to the speaker plays to a file.

---

## 4. Audio Pipeline

### 4.1 Load time (offline, unconstrained)

```
file → decode → PCM in memory → [shared FFT analysis] → waveform peaks → Deck.loaded
```

Decoders are pure Go (module paths in §11.3): `go-mp3`, `mewkiz/flac`,
`jfreymuth/oggvorbis`, and `go-audio/wav`. Tracks stay in memory as 32-bit float stereo.
A 60-minute track at 44.1kHz is roughly 200MB, which is acceptable and buys instant
seeking and waveform rendering.

**One FFT pass serves both analyses.** A single STFT over the decoded buffer yields:

- **Beatgrid** — spectral-flux onset detection → autocorrelation tempo estimate →
  dynamic-programming beat tracking (Ellis-style)
- **Key detection** — chroma vectors from the same spectrogram, correlated against key
  profiles (§6.5)

Computing the spectrogram once is what makes key detection nearly free: it adds no
second pass over the audio. The FFT window size and hop are chosen to serve both — fine
enough for chroma resolution, coarse enough for onset timing.

Analysis takes seconds for a full track, so it runs on a goroutine reporting progress,
and the deck moves through `analyzing → ready`. **A track is playable before analysis
completes**, and the key and grid fill in when they land.

Waveform peaks are computed once at load and cached per (track, resolution) so the TUI
never rescans PCM on a resize or zoom change.

#### Analysis cache

Analysis results persist across restarts under `~/.config/mix-tui/analyze/`, keyed by
**file path hash + size + mtime**. Moving or renaming a track keeps its grid and key;
replacing a track's audio invalidates them. This avoids hashing file contents, which
would mean reading every file in the library on every launch.

A path-only key would be faster still, but a replaced file would silently keep a wrong
beatgrid — a failure that surfaces as mysteriously bad sync, long after the cause.

### 4.2 Realtime path (per 512-frame block, ~11.6ms at 44.1kHz)

```
                         ┌─────────────────────────────────────────┐
                         │  per deck, in DSP order                 │
 deck PCM               │                                         │
   │                    │                                         │
   ▼                    │                                         │
 read at pos ──▶ resample(pitch) ──▶ 3-band EQ ──▶ filter ──▶ gain│
                                                                 │
                    ┌────────────────────────────────────────────┘
                    │
        ┌───────────┴────────────┐
        ▼                        ▼
 crossfader                 cue send
 (2 selected decks)     (per deck, always available)
        │                        │
        ▼                        ▼
   ┌─────────┐              ┌─────────┐
   │  MAIN   │              │   CUE   │
   │  MIX    │              │  BUS    │
   └────┬────┘              └────┬────┘
        │                        │
        ▼                        ▼
    limiter ──▶ MAIN OUT     CUE OUT
```

MAIN and CUE OUT are separate PipeWire streams, so the cue mix reaches the headphones
without contaminating the main mix. The MVP targets stereo out, which every Linux
desktop has. Because the two outputs are already independent streams, pointing them at a
4-channel device's main and headphone ports is a routing change rather than an audio
architecture change.

### 4.3 The realtime thread contract

Two paths run concurrently and must not touch:

- **The render goroutine** owns the DSP loop above. It stays ~200ms ahead of playback,
  filling two ring buffers (main stereo, cue stereo), then sleeping until it is time to
  fill again.
- **The PipeWire process callback** only copies from the ring buffer. No allocation, no
  locks, no decoding, no filesystem access.

If a ring runs dry — GC pause, scheduler hiccup, MIDI storm — the callback emits silence
for that block rather than blocking. **A dropout is recoverable; blocking the audio
thread is not.**

This is the project's single largest technical risk, and it is architected for from the
start. An underrun counter is exported in the Snapshot and surfaced in the TUI debug
overlay, so a glitch always has exactly one observable cause: a ring went empty.

### 4.4 Loops

Three loop behaviours share one mechanism.

#### 4.4.1 A loop wrap is a seek

This is the framing that makes loops tractable. Playing from `posA`, jumping to `posB`,
and wrapping a loop all reduce to the same primitive: **advance the read position, with
correct resampler and filter state at the destination.**

Getting the state right at a discontinuity is the whole problem. The resampler carries
filter history from the samples immediately *before* the old position; after a jump that
history describes audio that is no longer adjacent, and resuming on top of it produces an
audible click. So the deck's playback head exposes a single `seek(target)` that:

1. Reads the resampler's history window back from the decoded buffer *at the destination*.
2. Runs that window through the filter without emitting output, priming the state.
3. Resumes emitting from the target sample.

Because `seek` is correct for jumps, hotcues, nudges, and sync corrections alike, loops
need no seam logic of their own — a loop wrap is `seek(loopStart)` at the moment the read
head reaches `loopEnd`. **One primitive, correctly implemented, gives loop-seamless
behaviour for free.** This is why loops belong in the MVP rather than being deferred: the
hard part is the seek primitive, which the MVP needs regardless.

#### 4.4.2 Beat-quantised loops

Triggered by a rotary encoder (or keys): set a loop of ¼, ½, 1, 2, 4, 8, 16, or 32 beats,
starting from the next beat boundary and snapped to the beatgrid. Length in **beats**, not
seconds, so the loop tracks the pitch fader — the musical property that makes a beat loop
usable while mixing.

Requires an analysed beatgrid. If the deck has none, beat loops are unavailable for it and
the encoder is inert rather than snapping to a grid that does not exist.

#### 4.4.3 Saved loops

Free in/out points at any position, not quantised to the beat. Set `loop in` and `loop
out`, save with a slot number, and recall later. Persisted per track alongside hotcues
(§10), so a loop set on a particular track returns on the next load.

This is the same shape as hotcues (§5) and reuses their slot model and storage.

#### 4.4.4 Loop roll

A **momentary** loop: hold the button and the deck plays a short loop (1 beat by default,
adjustable); release and it returns to exactly where it was.

Loop roll is the one feature with real state-management risk. Releasing must restore the
position captured at press, and it must do so whether or not the deck was already looping.
The deck therefore keeps a small stack: pressing pushes the current play state (position,
loop state, sync state), and releasing pops it and `seek`s back. If the stack is empty on
release, the roll is a no-op rather than a jump to zero.

---

## 5. Input: the Action Layer

Every control — key or knob — resolves to a named Action. **No code path exists in which
a raw MIDI message or keypress touches deck state.**

```go
type Action struct {
    Kind   ActionKind // DeckPitch, DeckEQHigh, HotcueSet, CueAssign, Sync, Zoom, ...
    Target int        // deck index / strip index
    Value  float64    // -1..1 normalized, or 0/1 for toggles
    Slot   int        // hotcue/loop slot, where the action takes one
}
```

Loop and hotcue actions carry a `Slot`, since both are recalled by number.

### 5.1 Producers

Three producers, one consumer (`engine.Apply`):

- **Keyboard** — a bindings table, `key → Action`, rebindable in YAML.
- **MIDI** — per-device mapping files in `~/.config/mix-tui/mappings/`, matched by device
  name substring (controllers rename themselves across reconnects).
- **TUI** — menus and views emit Actions too, so "nudge EQ up" from the keyboard and
  "turn the high knob" from the Xone K2 are the same operation.

Because `Value` is normalized to `-1..1`, a knob's full physical travel maps to the same
range regardless of the controller, making mappings portable across devices.

### 5.2 Two MIDI message shapes

- **Absolute** — knob position 0..127 maps to -1..1.
- **Relative** — jog wheels send ±1/±2 with a null point at 64; the delta is applied
  incrementally.

The mapping file declares which, per control. Without this, jog wheels jump wildly on
every detent.

### 5.3 Modifiers

A modifier is input-layer state — a set of named latches — held on the engine because
both the MIDI goroutine and the keyboard handler set it, and the TUI displays it. It is a
small `map[string]bool` that never touches deck state.

The binding key is **(modifiers, message)**:

```go
type Binding struct {
    Mods     ModifierSet   // {shift}, {shift,ctrl}, {}
    Chan     uint8
    Num      uint8         // CC number or note number
    Kind     MsgKind       // CC or Note
    Action   Action
    ValueMode ValueMode    // absolute | relative | momentary | toggle
}
```

**Matching is most-specific-wins with fallback.** With `{shift}` held and a CC
incoming, try `{shift,ctrl}` → `{ctrl}` → `{shift}` → `{}` and take the first hit. So
shift+hotcue-1 deletes while bare hotcue-1 sets, from one physical button, with no
duplication in the file. If nothing matches at any specificity the message is dropped and
counted.

Modifier state comes from config, not convention, because controllers disagree:

```yaml
# ~/.config/mix-tui/mappings/xone-k2.yaml
device: "Xone K2"
match: substring

modifiers:
  shift:
    kind: cc          # or: note
    chan: 1
    num: 49
    pressed: 127      # for cc: the value meaning "down"
    released: 0
    mode: momentary   # momentary | latching | toggle

controls:
  - midi: {kind: cc, chan: 1, num: 9}
    action: {kind: DeckPitch, target: 0}
  - midi: {kind: cc, chan: 1, num: 20}
    action: {kind: CueAssign, target: 0}
    momentary: true            # press = cue, release = return
  - midi: {kind: note, chan: 1, num: 40}
    mods: [shift]
    action: {kind: HotcueDelete, target: 0}
    slot: 1
```

- `momentary` — held while down; released on note-off or the released CC value
- `latching` — the controller toggles it; we track the toggle state ourselves
- `toggle` — first press engages, second disengages, independent of the controller

Buttons vary across modern controllers (note-on/off, CC 0/127, toggle). All three forms
are expressed in config; none are hardcoded.

### 5.4 Hot-reload

The mapping directory is watched. On change the table is swapped atomically — no
restart, no dropped audio. **The config file is the app's extension surface**: adding a
feature means adding an `ActionKind`, and it becomes mappable for free.

### 5.5 Loop actions

Loops are ordinary Actions, so they work from the encoder, the keyboard, and the TUI
alike:

| Action | Value | Notes |
|---|---|---|
| `LoopBeat` | 0..1 → ¼…32 beats | Relative encoder; most knobs do not reach 32 |
| `LoopIn` / `LoopOut` | — | Set free loop points at the current position |
| `LoopSave` | `Slot` | Persist the current loop to a slot |
| `LoopRecall` | `Slot` | Re-enter a saved loop |
| `LoopRelease` | — | Exit the loop, continue from the loop end |
| `LoopRoll` | momentary | 1 beat by default; release restores prior state |

Because `LoopBeat` is a relative encoder control, the loop size is a **MIDI-mappable
performance control**, not a menu setting — which is how it works on hardware.

### 5.6 Loops and sync

Sync and loops interact, and the interaction needs a stated rule rather than whatever
falls out of the implementation:

- A loop's length is defined **in beats**, so tempo changes scale it. A 4-beat loop stays
  4 beats at any pitch. This is what makes beat loops usable while mixing.
- Sync may adjust the looped deck's pitch to match the master. It must **not** re-quantise
  or re-lay the loop itself — the loop keeps its beat-length and moves with the pitch.
- Quantise may nudge the loop *start* to the beatgrid when the loop is created, but never
  while it is playing. Moving a loop under the playhead would be a seam every bar.
- A saved (free) loop keeps its **absolute sample positions**. It is anchored to the audio,
  not to the beatgrid, so it does not move when tempo changes.

---

## 6. TUI

### 6.1 Views

**Four layouts**, switchable at runtime by keybinding, with a configurable default. View
state is purely a TUI concern; the engine Snapshot is identical across all of them.

| Key | Layout | Description | Min width |
|---|---|---|---|
| A | Side-by-side | Deck A, Deck B, mixer beside them | ~100 cols |
| **D** | **Mixer centered** *(default)* | **Channel faders flank the crossfader; closest to the Xone K2's physical layout** | ~100 cols |
| B | Focused | One deck full-detail, other collapses to a strip; TAB switches | ~70 cols |
| C | Stacked | Vertical decks, mixer footer | ~80 cols |

In layout D, per-deck EQ lives in the deck panels so the center mixer column stays
narrow enough for faders, crossfader, and master.

Zoomed waveform tracking (the CDJ-style view designed during brainstorming, referred to
as "view E" there) is **not a fifth layout**. It is a per-deck toggle available in every
layout — see §6.3.

### 6.2 Waveform rendering

Waveforms render as **Unicode braille**. Each cell is a 2×4 dot grid, so a 4-row block
resolves 16 amplitude levels per column pair versus the 8 that block characters (▁▂▃▄▅▆▇█)
provide. Played audio renders in one color, unplayed in another, with hotcues, **loop
regions**, and beatgrid overlaid.

#### 6.2.1 Showing loops on the waveform

An active loop is drawn as a shaded region between its in and out points, with the
playhead visibly cycling inside it. This matters more than it sounds: **a looping deck
playing past the track's end would otherwise look like a stall**, and the waveform is the
only place the user can confirm the loop is doing what they think.

Saved loop slots appear as small numbered markers in the same row as hotcues, so both are
visible in one glance rather than in separate UI.

#### 6.2.2 Loop state in the transport row

The transport row shows the current loop size when one is active (`LOOP 4`), and the loop
indicator is lit while looping. The beat-loop encoder position is always visible, so
turning it has visible effect — an encoder with no readout feels broken.

### 6.3 Zoom tracking (CDJ-style)

Zoom is a **per-deck toggle available in every layout**, not a separate screen. A deck
panel flips between the full-track overview and a scrolling zoomed window, so the mixer
and the neighbouring deck never move. This keeps the beatmatching affordance available
without a layout switch that would hide the mixer at the moment you need it.

Zoomed, both decks display the same time scale with playheads pinned at the same screen
position. If tempos match, the beat markers line up; as they drift, you watch the columns
slide. **This visual alignment is the core beatmatching affordance** — it replaces the
third monitor that a hardware DJ would use.

The zoom level (2 / 4 / 8 / 16 / 32 bars) is a live, MIDI-mappable control. A `ΔBEAT`
readout shows the millisecond offset between the two decks' nearest beats against the
current phase window, so the user knows how close they are to lock.

Each zoomed panel also carries a full-track **overview strip** showing where the window
sits within the whole track.

### 6.4 ASCII art

Art is load-bearing rather than decorative: **it fills space that would otherwise be an
empty box.** When a deck has no track loaded, its waveform area shows art instead, so the
layout does not reflow when a track is loaded mid-set.

- **Boot/splash** — full-screen art during audio initialisation; also the idle state when
  no PipeWire stream is found.
- **Empty decks** — per-deck art in the waveform region.
- **While playing** — small VU-style art accents in the deck footer. Deliberately
  restrained: a large animated block in peripheral vision is a distraction while mixing.

### 6.5 Track library

Two navigation modes, because browsing and mid-set loading have opposite requirements.

**Full-screen library** — three panes: crates, tracks, detail. `ENTER` loads to the
focused deck, `SHIFT+ENTER` to the other. `TAB` cycles pane focus.

**Quick overlay** — floats over the deck view, which stays visible and playing behind it.
Type to filter, `↑↓` to select, `ENTER` to load. No mode change and no lost position,
because during a set the deck view must not disappear.

The library view **requires ~120 columns** and shows a resize hint below that rather than
permanently truncating artist names. This is wider than the mixer layouts need; the two
requirements differ and pretending otherwise just produces clipped text.

#### Sort

Sortable by BPM, key, artist, title, or date added, ascending or descending. The sort
control occupies the detail pane rather than adding a fourth pane — at 120 columns there
is no room, and only one of the two is useful at a time.

**Sorting changes the view only and never rewrites the crate file.** A crate is a curated
running order; a stray keypress while browsing must not reorder a set. Sort state resets
when the view is left.

#### Crates

Crates are **static lists of track references** in the MVP. Adding a file to a watched
directory does not add it to any crate; tracks are added explicitly. The crate file format
is designed so that rule-based crates (§12) can be added later without migrating
existing files.

#### Key detection

Camelot keys are detected automatically and displayed **Camelot-only in the track list**
(`8A`); the musical name (`C minor`) appears in the detail pane. Detection always carries
a **confidence** marker rather than a bare value:

| Marker | Meaning |
|---|---|
| `✓` | High confidence |
| `~` | Low confidence — shown, but flagged |
| `?` | Below threshold; reported as undetected |

The threshold exists because key detection on percussive or atonal material — drum and
bass breaks, noise, unstrung instruments — yields confident nonsense. **A wrong key
displayed confidently is worse than no key**, because the user would crate-match against
it. Below threshold we report nothing rather than guessing.

Key detection shares a single FFT pass with beatgrid analysis (§4.1), so it adds
negligible cost once analysis exists.

---

## 7. MIDI Message Scope

**Handled:** Control Change (absolute, relative, momentary, toggle) and Note On/Off
(with velocity available as a value).

**Ignored and counted:** Sysex, Program Change, Pitch Bend, Channel and Poly Aftertouch,
MIDI Clock, and Start/Stop/Continue.

Ignoring MIDI clock is deliberate. Beat-syncing to an external DJLink or Traktor table
requires it, but that is a later feature. Ignored messages increment a per-category
counter visible in the debug overlay, so a controller with unexpected behaviour is
diagnosable rather than mysterious.

---

## 8. Error Handling

| Condition | Behaviour |
|---|---|
| PipeWire unavailable or no stream | Show the idle art screen with a diagnostic; retry in the background; never start a TUI that silently produces no sound. |
| Unsupported/corrupt file | Reject at load with the file and reason on screen; other decks unaffected. |
| Decoder error mid-stream | Emit silence for that deck, mark it faulted in the Snapshot, keep the other deck playing. A bad file must not take down the mix. |
| Beatgrid analysis fails or is low-confidence | Mark the deck `ready (no grid)`; it plays normally, sync/quantize disable for it, everything else works. |
| Ring buffer underrun | Emit silence for that block, increment the counter, surface in the debug overlay. Never block the audio thread. |
| Malformed mapping YAML | Log the parse error with file and line, keep the last valid table. A typo must not kill a live performance. |
| No MIDI devices | Run keyboard-only; print available devices and the mapping directory path. |
| Library root missing or unreadable | Skip the root, list it as unreachable with the reason, keep scanning the others. One bad path does not empty the library. |
| Track file deleted since last scan | Drop from the library and remove its cached analysis, hotcues, and crate entries. |
| Key detection below confidence threshold | Display `?` (undetected). Never display a low-confidence key as though it were certain (§6.5). |
| Beat loop requested on a deck with no beatgrid | Reject and leave the encoder inert. Snapping to a grid that does not exist would silently produce a loop on the wrong beats. |
| Loop roll released with no saved state | No-op. Never seek to position zero — a stray release must not move the playhead. |
| Loop wrap at track end | Wrap to the loop start, not to the track end. A loop near the end of a track is normal and must not fall out of bounds. |
| Terminal too small for library view (<120 cols) | Show a resize hint naming the required width; do not render truncated columns. Mixer layouts are unaffected and remain usable at their own narrower widths. |

The governing rule: **an error degrades a feature, never the session.**

---

## 9. Testing

The engine is pure DSP with no device dependency, which is exactly what makes this
possible.

- **Offline render.** `cmd/mixrender` drives the engine with a scripted action sequence
  and writes the output to a WAV. The same code path that plays to the speaker plays to
  the file.
- **Signal assertions, not golden-file diffs.** Rendered output is measured rather than
  byte-compared: tests assert on RMS, peak, zero-crossing rate, and spectral content. A
  resampler set to 1.0 must pass a 1kHz tone unchanged; a +6% pitch fader must raise the
  measured zero-crossing rate by ~6%; a low-pass filter at 200Hz must attenuate a 2kHz
  tone relative to a 100Hz one. This catches real DSP bugs — a pitch shifter that is
  subtly detuned still passes a file diff — and survives encoder changes.
- **Beatgrid fixtures** — committed short audio clips with known BPM, asserting detection
  lands within a stated tolerance.
- **Ring buffer** — dedicated concurrency tests asserting SPSC safety under
  `go test -race`.
- **Mapping resolution** — table-driven tests for most-specific-wins fallback and
  relative-encoder accumulation, including the null-point case at 64.
- **Decoder coverage** — a committed sample per supported format, asserting duration and
  peak level.
- **Key detection fixtures** — committed clips in known keys, asserting the detected
  Camelot value *and* that the confidence marker is correct. The confidence behaviour is
  the part that matters: a test that only checks the key of tonal material would pass even
  if the threshold were removed and atonal clips returned confident nonsense.
- **Analysis cache invalidation** — asserting that changing mtime or size invalidates a
  cached grid, and that renaming does not.
- **Sort semantics** — asserting that sorting a crate changes the view ordering while
  leaving the crate file byte-identical.
- **Seek correctness** — the load-bearing primitive for loops (§4.4.1). After a seek
  mid-signal, assert the output is continuous (no sample-to-sample discontinuity larger
  than the signal's own maximum slope) and that a steady tone resumes at the expected
  phase and amplitude. **This is the test that would catch an audible loop seam**, and it
  is written before any loop code exists.
- **Loop wrap continuity** — render a looping deck and assert the sample-level
  discontinuity at the wrap point is within the same bound as elsewhere in the loop.
- **Beat loop tracks pitch** — assert a 4-beat loop contains exactly 4 detected beats at
  pitch +8%, 0%, and −8%.
- **Loop roll restoration** — press and release a roll from various states (idle, playing,
  already looping, already rolling) and assert the deck returns to exactly its prior
  position and state, and that a release with an empty stack is a no-op.

---

## 10. Configuration

```
~/.config/mix-tui/
  config.yaml          audio device, buffer size, keybindings, default view
  library.yaml         library roots and crate definitions
  mappings/
    xone-k2.yaml
    generic-2deck.yaml
  analyze/             cached beatgrids and detected keys (see §4.1)
  hotcues/             per-track hotcue persistence
  loops/               per-track saved loop persistence
```

Hotcues and saved loops are stored in parallel per-track directories keyed by the same
path identity, so both are invalidated together when a file is replaced or moved (§4.1).

A repo-local `.devconfig/` provides the same structure for manual testing without
touching the real configuration.

`library.yaml` declares the scanned roots and the crates:

```yaml
roots:
  - ~/Music/techno
  - ~/Music/house

crates:
  - name: Warmed Up
    tracks: [~/Music/techno/inner-speaker.mp3, ...]   # explicit references
  - name: Deep Cuts
    tracks: [...]
```

Crates hold explicit track references rather than query rules, matching §6.5. The schema
is versioned so that rule-based crates (§12) can be added without rewriting existing
crate files.

---

## 11. Dependencies

**cgo is required at two edges: the audio device and MIDI input.** The audio DSP core
remains cgo-free. cgo at the I/O boundary is normal and contained; what matters is that
the DSP itself stays portable and testable.

### 11.1 PipeWire — bindings written in-project

There is **no mature Go binding for libpipewire**. The `internal/pipewire` package
therefore contains hand-written cgo bindings written in-project, restricted to the small
API surface actually needed:

- `pw_init`, `pw_deinit`, and thread-loop helpers
- one `pw_stream` in playback mode, connected to an `Audio/Source` node
- a `struct pw_buffer` / `struct pw_buffer` deque per stream
- the `on_process` callback, which is the realtime memcpy-only path

This is scoped deliberately: two streams (main and cue), playback only, no capture, no
SPA plugin work. It is contained enough that hand-writing is lower risk than depending
on an unmaintained binding.

Build dependency: `libpipewire-0.3-dev`, linked via `pkg-config`. **Verified present on
the development machine at version 1.6.8.**

### 11.2 MIDI — `gitlab.com/gomidi/midi/v2`

MIDI input uses `gitlab.com/gomidi/midi/v2` with its `rtmididrv` driver, which wraps
rtmidi's ALSA backend. It is the actively maintained Go MIDI library and is used only for
message parsing and driver plumbing.

Build dependency: `libasound2-dev`. **Verified present on the development machine.**

### 11.3 Pure Go

| Purpose | Module | Verified |
|---|---|---|
| TUI framework | `github.com/charmbracelet/bubbletea` | v1.3.10 |
| Styling | `github.com/charmbracelet/lipgloss` | v1.1.0 |
| Widgets | `github.com/charmbracelet/bubbles` | v1.0.0 |
| Config parsing | `gopkg.in/yaml.v3` | v3.0.1 |
| MP3 decode | `github.com/hajimehoshi/go-mp3` | v0.3.4 |
| WAV decode/encode | `github.com/go-audio/wav` | v1.1.0 |
| FLAC decode | `github.com/mewkiz/flac` | v1.0.14 |
| Vorbis decode | `github.com/jfreymuth/oggvorbis` | v1.0.5 |
| FFT (shared analysis pass) | `gonum.org/v1/gonum/fft` | v0.17.0 |

`gonum` is used only for the FFT in the offline analysis path (§4.1), not in the
realtime thread.

All module paths above were resolved against the Go module proxy during design.

Note: the vorbis and FLAC decoders are noticeably less actively maintained than the rest
of the table. If either proves unworkable in practice, the fallback is a contained cgo
binding to `libvorbisfile` / `libFLAC` at the decoder edge only — which does not affect
the DSP core or the engine boundary.

---

## 12. Deferred

Noted so the architecture is not accidentally hostile to them:

- **Effects** — an insert chain per deck. The mixer strip boundary is the natural
  insertion point.
- **Additional decks** — data model already supports N.
- **Rule-based crates** — smart playlists backed by a query (folder, BPM range,
  added-this-week). The crate schema is versioned so these can be added without
  migrating existing static crates.
- **4-channel output routing** — the MAIN and CUE streams are already independent, so
  pointing them at an Xone K2's dedicated main and headphone ports is a routing
  configuration change, not an audio architecture change.
- **MIDI clock sync** — requires implementing the ignored clock messages (§7) plus
  table-side beatgrid exchange.
- **Recording and streaming.**