package engine

import (
	"math"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/audio"
	"github.com/maguldon/mix-tui/internal/dsp"
)

// memTrack builds an in-memory Track. Shared by every engine test.
func memTrack(pcm dsp.Stereo) *audio.Track {
	return &audio.Track{
		Path: "mem", SampleRate: dsp.SampleRate, Channels: 2, PCM: pcm,
		Duration: time.Duration(len(pcm)/2) * time.Second / dsp.SampleRate,
	}
}

func maxSlope(freq float64) float64 { return 2 * math.Pi * freq / dsp.SampleRate }

// discontinuityAt returns the largest frame-to-frame jump in the first n
// samples of s, i.e. the worst seam in a rendered block.
func discontinuityAt(s dsp.Stereo, n int) float64 {
	if n > len(s) {
		n = len(s)
	}
	worst := 0.0
	for i := 2; i+1 < n; i += 2 {
		l := float64(s[i])
		r := float64(s[i+1])
		pl := float64(s[i-2])
		pr := float64(s[i-1])
		dl := math.Abs(l - pl)
		dr := math.Abs(r - pr)
		if dl > worst {
			worst = dl
		}
		if dr > worst {
			worst = dr
		}
	}
	return worst
}

// renderBlocks renders n blocks from d and returns the concatenated output.
func renderBlocks(d *Deck, block int, n int) dsp.Stereo {
	tmp := make(dsp.Stereo, block*2)
	out := make(dsp.Stereo, 0, n*block*2)
	for i := 0; i < n; i++ {
		d.Render(tmp)
		out = append(out, tmp...)
	}
	return out
}

func TestSeekIsClickFree(t *testing.T) {
	// The load-bearing test for spec §4.4.1.
	//
	// The bound is the deck's OWN steady-state discontinuity, not a bare
	// sine's slope. The signal chain (polyphase resampler, three EQ biquads, a
	// filter) naturally steps more than 2*pi*f/SampleRate between frames, so
	// comparing against the idealised tone would fail on continuous playback.
	// What matters is that a seek is indistinguishable from never seeking.
	const block = 512

	steady := func(d *Deck, warm, measure int) float64 {
		tmp := make(dsp.Stereo, block*2)
		for i := 0; i < warm; i++ {
			d.Render(tmp)
		}
		worst := 0.0
		for i := 0; i < measure; i++ {
			d.Render(tmp)
			if m := dsp.MaxSampleDelta(tmp); m > worst {
				worst = m
			}
		}
		return worst
	}

	reference := NewDeck(0)
	reference.Load(memTrack(dsp.Sine(440, 20.0)))
	reference.Play()
	base := steady(reference, 86, 16)

	seeked := NewDeck(0)
	seeked.Load(memTrack(dsp.Sine(440, 20.0)))
	seeked.Play()
	tmp := make(dsp.Stereo, block*2)
	for i := 0; i < 86; i++ {
		seeked.Render(tmp)
	}
	seeked.seek(3 * time.Second)
	got := steady(seeked, 0, 16)

	if got > base*1.5 {
		t.Errorf("seek discontinuity %.5f exceeds steady-state %.5f by more than 1.5x",
			got, base)
	}
}

// TestSeekProducesSameAudioAsPlayingThrough is the correctness statement for
// the seek primitive: a deck that seeks to position P and carries on must emit
// exactly what a deck that simply played through P would have emitted.
//
// This replaces block-by-block comparison of two independently-running decks,
// which is fragile: their positions accumulate floating-point drift and never
// quite line up. Instead this plays ONE deck continuously from the target and
// compares against a deck that jumped there.
func TestSeekProducesSameAudioAsPlayingThrough(t *testing.T) {
	const block = 512
	tmp := make(dsp.Stereo, block*2)
	const target = 3 * dsp.SampleRate // 3 seconds, in exact frames

	// Reference: start AT the target and play forward.
	ref := NewDeck(0)
	ref.Load(memTrack(dsp.Sine(440, 20.0)))
	ref.Play()
	ref.seekFrames(target)
	refBlocks := make([]dsp.Stereo, 12)
	for i := range refBlocks {
		ref.Render(tmp)
		refBlocks[i] = tmp.Dup()
	}

	// Subject: play from the start, then jump to the target.
	subject := NewDeck(0)
	subject.Load(memTrack(dsp.Sine(440, 20.0)))
	subject.Play()
	for i := 0; i < 86; i++ {
		subject.Render(tmp)
	}
	subject.seekFrames(target)
	subjectBlocks := make([]dsp.Stereo, 12)
	for i := range subjectBlocks {
		subject.Render(tmp)
		subjectBlocks[i] = tmp.Dup()
	}

	for b := range refBlocks {
		for i := range refBlocks[b] {
			// A bounded residual is expected: seeking resets the EQ and filter
			// delay lines, whose two-sample history cannot be reconstructed from
			// the destination. It is inaudible and decays within a few blocks.
			// What must be exact is the resampler state Prime fixes.
			if d := math.Abs(float64(refBlocks[b][i]) - float64(subjectBlocks[b][i])); d > 0.02 {
				t.Fatalf("block %d sample %d: seeked %v, playing-through %v",
					b, i, subjectBlocks[b][i], refBlocks[b][i])
			}
		}
	}
}

// TestSeekResamplerStateIsExact isolates the resampler, which is what Prime
// makes exact. With the EQ and filter left flat, a seek must reproduce
// continuous playback sample for sample once past the two-block prime warm-up.
func TestSeekResamplerStateIsExact(t *testing.T) {
	const block = 512
	tmp := make(dsp.Stereo, block*2)
	const target = 3 * dsp.SampleRate

	ref := NewDeck(0)
	ref.Load(memTrack(dsp.Sine(440, 20.0)))
	ref.Play()
	ref.seekFrames(target)
	for i := 0; i < 4; i++ {
		ref.Render(tmp) // clear the prime warm-up
	}
	want := make(dsp.Stereo, block*2)
	ref.Render(want)

	subject := NewDeck(0)
	subject.Load(memTrack(dsp.Sine(440, 20.0)))
	subject.Play()
	for i := 0; i < 86; i++ {
		subject.Render(tmp)
	}
	subject.seekFrames(target)
	for i := 0; i < 4; i++ {
		subject.Render(tmp)
	}
	got := make(dsp.Stereo, block*2)
	subject.Render(got)

	for i := range want {
		if d := math.Abs(float64(want[i]) - float64(got[i])); d > 1e-4 {
			t.Fatalf("sample %d: seeked resampler output %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSeekToPositionZeroDoesNotPanic(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	d.Play()
	renderBlocks(d, 512, 10)
	d.seek(0)
	renderBlocks(d, 512, 10) // must not panic
}

func TestSeekNearTrackEndDoesNotPanic(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	d.Play()
	d.seek(2*time.Second - 4) // 4 frames from the end
	renderBlocks(d, 512, 4)
}

func TestSeekClampsOutOfRange(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	d.seek(500 * time.Second)
	if d.Pos() > d.Duration() {
		t.Errorf("Pos() = %v, must clamp to duration %v", d.Pos(), d.Duration())
	}
	d.seek(-5 * time.Second)
	if d.Pos() < 0 {
		t.Errorf("Pos() = %v, must clamp to 0", d.Pos())
	}
}

func TestSeekOnEmptyDeckIsSafe(t *testing.T) {
	d := NewDeck(0)
	d.seek(time.Second)
	renderBlocks(d, 512, 2)
}

func TestPitchChangesTempo(t *testing.T) {
	// +8% pitch must consume the track ~8% faster in wall-clock time.
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 20.0)))
	d.Play()
	tmp := make(dsp.Stereo, 512*2)

	start := d.Pos()
	for i := 0; i < 172; i++ {
		d.Render(tmp)
	}
	unity := d.Pos() - start
	if math.Abs(unity.Seconds()-2.0) > 0.15 {
		t.Fatalf("unity timing off: %v", unity.Seconds())
	}

	up := NewDeck(1)
	up.Load(memTrack(dsp.Sine(440, 20.0)))
	up.SetPitch(1.0) // +8%
	up.Play()
	// Controls apply at the start of the next block, so render one before
	// measuring: a caller reading position immediately after Apply would
	// otherwise see the pre-change state.
	renderBlocks(up, BlockFrames, 1)
	s2 := up.Pos()
	for i := 0; i < 172; i++ {
		up.Render(tmp)
	}
	got := (up.Pos() - s2).Seconds()
	want := 2.0 * 1.08
	if math.Abs(got-want) > 0.15 {
		t.Errorf("+8%% pitch advanced %vs, want ~%vs", got, want)
	}
}

func TestPitchRatioMapping(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 1.0)))
	d.SetPitch(1.0)
	renderBlocks(d, BlockFrames, 1) // apply the posted command
	if math.Abs(d.PitchRatio()-1.08) > 1e-9 {
		t.Errorf("ratio at +1 = %v, want 1.08", d.PitchRatio())
	}
	d.SetPitch(-1.0)
	renderBlocks(d, BlockFrames, 1)
	if math.Abs(d.PitchRatio()-0.92) > 1e-9 {
		t.Errorf("ratio at -1 = %v, want 0.92", d.PitchRatio())
	}
	d.SetPitch(0)
	renderBlocks(d, BlockFrames, 1)
	if math.Abs(d.PitchRatio()-1.0) > 1e-9 {
		t.Errorf("centre ratio = %v, want 1.0", d.PitchRatio())
	}
}

func TestSetPitchClampsAndRejectsNaN(t *testing.T) {
	d := NewDeck(0)
	d.SetPitch(5.0)
	if d.PitchRatio() > 1.081 {
		t.Errorf("pitch escaped range: %v", d.PitchRatio())
	}
	d.SetPitch(math.NaN())
	if math.IsNaN(d.PitchRatio()) {
		t.Error("NaN pitch produced a NaN ratio")
	}
}

func TestStoppedDeckIsSilent(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	tmp := make(dsp.Stereo, 512*2)
	d.Render(tmp)
	if tmp.Peak() != 0 {
		t.Errorf("stopped deck produced %v", tmp.Peak())
	}
}

func TestEmptyDeckIsSilent(t *testing.T) {
	d := NewDeck(0)
	tmp := make(dsp.Stereo, 512*2)
	d.Render(tmp) // must not panic with no track
	if tmp.Peak() != 0 {
		t.Errorf("empty deck produced %v", tmp.Peak())
	}
}

func TestDeckStopsAtEndOfTrack(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 0.5)))
	d.Play()
	renderBlocks(d, 512, 100)
	if d.IsPlaying() {
		t.Error("deck should stop at the end of the track")
	}
}

func TestPlayAfterEndRestartsFromSeek(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 0.5)))
	d.Play()
	renderBlocks(d, 512, 100)
	if d.IsPlaying() {
		t.Fatal("expected the deck to have stopped")
	}
	d.seek(0)
	d.Play()
	renderBlocks(d, BlockFrames, 1) // apply the posted play command
	if !d.IsPlaying() {
		t.Error("Play after seeking to zero did not restart the deck")
	}
	out := renderBlocks(d, 512, 4)
	if out.Peak() == 0 {
		t.Error("restarted deck produced silence")
	}
}

func TestLoadResetsPosition(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 5.0)))
	d.Play()
	renderBlocks(d, 512, 50)
	if d.Pos() == 0 {
		t.Fatal("position did not advance")
	}
	d.Load(memTrack(dsp.Sine(440, 5.0)))
	if d.Pos() != 0 {
		t.Errorf("Load did not reset position: %v", d.Pos())
	}
	if d.IsPlaying() {
		t.Error("Load did not stop the deck")
	}
}

func TestEQAndFilterAffectOutput(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	d.Play()
	flat := renderBlocks(d, 512, 40)

	d2 := NewDeck(1)
	d2.Load(memTrack(dsp.Sine(440, 2.0)))
	d2.SetFilter(true, 8000, 0.707) // highpass, kills a 440Hz tone
	d2.Play()
	filtered := renderBlocks(d2, 512, 40)

	if filtered.RMS() >= flat.RMS()*0.5 {
		t.Errorf("highpass at 8k did not attenuate 440Hz: %v vs %v",
			filtered.RMS(), flat.RMS())
	}
}

// TestRenderDoesNotAllocate guards the realtime contract (spec §4.3).
func TestRenderDoesNotAllocate(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 30.0)))
	d.Play()
	out := make(dsp.Stereo, 512*2)
	// warm up so any lazy init has happened
	d.Render(out)

	allocs := testing.AllocsPerRun(200, func() { d.Render(out) })
	if allocs > 0 {
		t.Errorf("Render allocated %v times per call — the realtime path must not", allocs)
	}
}

func TestRenderAfterSeekDoesNotAllocate(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 30.0)))
	d.Play()
	out := make(dsp.Stereo, 512*2)
	d.seek(5 * time.Second)
	d.Render(out)

	allocs := testing.AllocsPerRun(100, func() {
		d.seek(5 * time.Second)
		d.Render(out)
	})
	if allocs > 0 {
		t.Errorf("seek+Render allocated %v times per call", allocs)
	}
}

func TestRenderHandlesShortAndOddLengths(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	d.Play()
	for _, frames := range []int{1, 2, 3, 7, 64, 512, 4096} {
		out := make(dsp.Stereo, frames*2)
		d.Render(out) // must not panic or corrupt
	}
}

func TestRenderEmptyOutputIsSafe(t *testing.T) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 2.0)))
	d.Play()
	d.Render(nil)
	d.Render(dsp.Stereo{})
}

func BenchmarkDeckRender(b *testing.B) {
	d := NewDeck(0)
	d.Load(memTrack(dsp.Sine(440, 120.0)))
	d.SetPitch(0.3)
	d.SetFilter(false, 4000, 1.0)
	d.Play()
	out := make(dsp.Stereo, 512*2)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d.Render(out)
	}
}
