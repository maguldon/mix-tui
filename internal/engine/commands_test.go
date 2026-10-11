package engine

import (
	"sync"
	"testing"
)

// C4 RED: the command queue's fullness check must agree with its index
// arithmetic.
//
// buf[w % (commandQueueSize-1)] made the ring 255 slots while the fullness
// check accepted 256 outstanding commands. The 256th post therefore wrote to
// the slot the reader was about to read, destroying the oldest unread command
// AND racing the reader — with droppedCount never firing, so the one counter
// meant to make a glitch observable stayed silent.
func TestCommandQueueNeverOverwritesUnread(t *testing.T) {
	var q commandQueue

	// Post one more than the ring can hold.
	for i := 0; i < commandQueueSize+1; i++ {
		q.post(command{kind: cmdPitch, value: float64(i)})
	}

	// Drain everything the queue believes it accepted.
	var seen []float64
	q.drain(func(c command) { seen = append(seen, c.value) })

	// Every accepted command must come back exactly once, in order.
	accepted := len(seen)
	if q.dropped() == 0 && accepted > commandQueueSize-1 {
		t.Fatalf("queue accepted %d commands but the ring holds %d",
			accepted, commandQueueSize-1)
	}

	// No duplicates, and no corruption: each value must be distinct.
	seenSet := map[float64]bool{}
	for _, v := range seen {
		if seenSet[v] {
			t.Fatalf("value %v delivered more than once; a slot was overwritten", v)
		}
		seenSet[v] = true
	}

	// And the values must be the FIRST `accepted` ones posted, in order:
	// an SPSC queue drops the NEWEST when full, it never rewrites old ones.
	for i, v := range seen {
		if v != float64(i) {
			t.Fatalf("delivered[%d] = %v, want %d; ordering broken", i, v, i)
		}
	}
}

// The queue must actually be usable: it should accept a full buffer's worth
// and deliver every one of them in order.
func TestCommandQueueDeliversInOrder(t *testing.T) {
	var q commandQueue
	const n = commandQueueSize / 2
	for i := 0; i < n; i++ {
		q.post(command{kind: cmdPitch, value: float64(i)})
	}
	var seen []float64
	q.drain(func(c command) { seen = append(seen, c.value) })
	if len(seen) != n {
		t.Fatalf("drained %d commands, want %d", len(seen), n)
	}
	for i, v := range seen {
		if v != float64(i) {
			t.Fatalf("delivered[%d] = %v, want %d", i, v, i)
		}
	}
	if got := q.dropped(); got != 0 {
		t.Errorf("dropped = %d for a half-full queue, want 0", got)
	}
}

// TestCommandQueueNeverIndexesPastItsBuffer covers a real crash: the ring array
// was sized to commandQueueMask (255) while the index wraps with the same mask,
// so w&mask could reach 255 and index one past the end. Under a burst of posts
// from another goroutine that panicked with "index out of range [255] with
// length 255".
func TestCommandQueueNeverIndexesPastItsBuffer(t *testing.T) {
	var q commandQueue
	if len(q.buf) != commandQueueSize {
		t.Fatalf("ring buffer holds %d entries, want %d so every masked index is valid",
			len(q.buf), commandQueueSize)
	}

	// Post past capacity from one writer and drain from the single reader,
	// which is the shape the real code uses: Apply posts, the render goroutine
	// drains. The wrap happens here, which is what indexed past the old buffer.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100_000; i++ {
			q.post(command{kind: cmdPlay})
			if i%7 == 0 {
				q.drain(func(command) {})
			}
		}
	}()
	wg.Wait()
	q.drain(func(command) {})
}
