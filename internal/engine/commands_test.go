package engine

import "testing"

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
