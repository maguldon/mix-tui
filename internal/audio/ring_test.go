package audio

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/maguldon/mix-tui/internal/dsp"
)

func TestRingWriteReadRoundTrip(t *testing.T) {
	r := NewRing(1024)
	capacity := r.Capacity() // NewRing rounds up with a floor
	src := dsp.Sine(440, float64(capacity*3)/dsp.SampleRate)
	n := r.Write(src)
	if n != capacity {
		t.Errorf("wrote %d, want %d (capacity)", n, capacity)
	}
	if r.AvailableRead() != capacity {
		t.Errorf("available = %d, want %d", r.AvailableRead(), capacity)
	}
	dst := make(dsp.Stereo, capacity*2)
	got := r.Read(dst)
	if got != capacity {
		t.Errorf("read %d, want %d", got, capacity)
	}
	for i := 0; i < got*2; i++ {
		if dst[i] != src[i] {
			t.Fatalf("sample %d differs: %v vs %v", i, dst[i], src[i])
		}
	}
}

// TestRingReadEmptyReturnsZeroImmediately is the safety property that makes a
// dropout recoverable: the audio callback must never block waiting for audio
// that has not been produced (spec §4.3).
func TestRingReadEmptyReturnsZeroImmediately(t *testing.T) {
	r := NewRing(1024)
	dst := make(dsp.Stereo, 512*2)
	done := make(chan int, 1)
	go func() { done <- r.Read(dst) }()
	select {
	case n := <-done:
		if n != 0 {
			t.Errorf("read %d from empty ring, want 0", n)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Read on an empty ring blocked — this would hang the audio thread")
	}
	if r.Underruns() != 1 {
		t.Errorf("underruns = %d, want 1", r.Underruns())
	}
}

func TestRingPartialWriteWhenFull(t *testing.T) {
	r := NewRing(100)
	src := dsp.Sine(440, float64(r.Capacity()*2)/dsp.SampleRate)
	r.Write(src)
	n := r.Write(src) // ring is full
	if n != 0 {
		t.Errorf("wrote %d to a full ring, want 0", n)
	}
	if r.AvailableWrite() != 0 {
		t.Errorf("availWrite = %d", r.AvailableWrite())
	}
}

func TestRingPartialReadWhenEmpty(t *testing.T) {
	r := NewRing(1024)
	r.Write(dsp.DC(0.5, 0.005)) // ~220 frames
	dst := make(dsp.Stereo, 512*2)
	n := r.Read(dst)
	if n == 0 || n >= 512 {
		t.Errorf("read %d, want partial", n)
	}
	n2 := r.Read(dst)
	if n2 != 0 {
		t.Errorf("second read = %d, want 0", n2)
	}
}

func TestRingZeroLengthOps(t *testing.T) {
	r := NewRing(1024)
	if n := r.Write(nil); n != 0 {
		t.Errorf("Write(nil) = %d", n)
	}
	if n := r.Write(dsp.Stereo{}); n != 0 {
		t.Errorf("Write(empty) = %d", n)
	}
	if n := r.Read(nil); n != 0 {
		t.Errorf("Read(nil) = %d", n)
	}
	if n := r.Read(make(dsp.Stereo, 0)); n != 0 {
		t.Errorf("Read(empty) = %d", n)
	}
}

func TestRingTinyAndOddCapacities(t *testing.T) {
	for _, cap := range []int{1, 2, 3, 7, 64, 4095, 100000} {
		r := NewRing(cap)
		actual := r.Capacity()
		src := dsp.DC(0.5, 0.01)
		w := r.Write(src)
		if w > actual {
			t.Errorf("cap=%d: wrote %d frames into capacity %d", cap, w, actual)
			continue
		}
		dst := make(dsp.Stereo, actual*4)
		got := r.Read(dst)
		if got != w {
			t.Errorf("cap=%d (actual %d): wrote %d, read %d", cap, actual, w, got)
		}
	}
}

// TestRingConcurrentSPSC is the real producer/consumer shape. It must pass
// under -race: the render goroutine writes while the audio thread reads.
func TestRingConcurrentSPSC(t *testing.T) {
	r := NewRing(4096)
	const total = 44100
	src := dsp.Sine(440, float64(total)/dsp.SampleRate)
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for off := 0; off < total; off += 512 {
			end := off + 512
			if end > total {
				end = total
			}
			chunk := src[off*2 : end*2]
			for len(chunk) > 0 {
				// A real producer retries until the ring has room, exactly as
				// the render goroutine does. Write returning 0 on a full ring
				// is a "try later", not a frame drop.
				n := r.Write(chunk)
				if n == 0 {
					runtime.Gosched()
					continue
				}
				chunk = chunk[n*2:]
			}
		}
		close(done)
	}()

	got := 0
	dst := make(dsp.Stereo, 512*2)
	for {
		n := r.Read(dst)
		got += n
		select {
		case <-done:
			wg.Wait()
			if r.AvailableRead() == 0 {
				break
			}
		default:
		}
		if got >= total {
			break
		}
	}
	if got != total {
		t.Errorf("consumed %d frames, want %d", got, total)
	}
}

func TestRingLatency(t *testing.T) {
	r := NewRing(4096)
	r.Write(dsp.DC(0.5, float64(1000)/dsp.SampleRate))
	if l := r.LatencyFrames(); l < 900 || l > 1100 {
		t.Errorf("latency = %d frames, want ~1000", l)
	}
}

func TestRingUnderrunsAccumulate(t *testing.T) {
	r := NewRing(1024)
	dst := make(dsp.Stereo, 512)
	for i := 0; i < 5; i++ {
		r.Read(dst)
	}
	if r.Underruns() != 5 {
		t.Errorf("underruns = %d, want 5", r.Underruns())
	}
}

func TestRingWrapAround(t *testing.T) {
	// Long-running use: the ring must keep working after many wraps without
	// corrupting data or losing frames.
	r := NewRing(1024)
	produced := make([]float32, 0, 20000*2)
	want := 0
	buf := make(dsp.Stereo, 128)
	for i := 0; i < 20000/128; i++ {
		for j := range buf {
			v := float32(i*128 + j/2)
			buf[j] = v
		}
		r.Write(buf)
		produced = append(produced, buf...)
		dst := make(dsp.Stereo, 256)
		n := r.Read(dst)
		for j := 0; j < n*2; j++ {
			if produced[want+j] != dst[j] {
				t.Fatalf("wrap corrupted at output frame %d", want/2)
			}
		}
		want += n * 2
	}
	if want < 15000 {
		t.Errorf("only moved %d samples through the ring", want/2)
	}
}

func TestRingReadsDoNotAllocate(t *testing.T) {
	// The drain path runs on the audio thread, which must not allocate.
	r := NewRing(1 << 20)
	r.Write(dsp.Sine(440, 0.5))
	dst := make(dsp.Stereo, 512*2)
	// warm up
	r.Read(dst)
	allocs := testing.AllocsPerRun(1000, func() { r.Read(dst) })
	if allocs > 0 {
		t.Errorf("Read allocated %v times per call — the audio thread must not", allocs)
	}
}

func TestRingWritesDoNotAllocate(t *testing.T) {
	r := NewRing(1 << 20)
	src := dsp.Sine(440, 0.5)
	dst := make(dsp.Stereo, 512*2)
	for i := 0; i < 100; i++ {
		r.Write(src[:512*2])
		r.Read(dst)
	}
	allocs := testing.AllocsPerRun(1000, func() {
		r.Write(src[:512*2])
		r.Read(dst)
	})
	if allocs > 0 {
		t.Errorf("Write/Read allocated %v times per call", allocs)
	}
}

func BenchmarkRingWriteRead(b *testing.B) {
	r := NewRing(1 << 20)
	src := dsp.Sine(440, 1.0)
	dst := make(dsp.Stereo, 512*2)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Write(src[:512*2])
		r.Read(dst)
	}
}
