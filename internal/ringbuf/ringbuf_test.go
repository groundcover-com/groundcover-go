package ringbuf_test

import (
	"sync"
	"testing"

	"github.com/groundcover-com/groundcover-go/internal/ringbuf"
)

func TestFIFOOrder(t *testing.T) {
	b := ringbuf.New[int](100, 0, nil)
	for i := range 10 {
		if dropped := b.Push(i); dropped != 0 {
			t.Fatalf("unexpected drop pushing %d", i)
		}
	}
	got := b.DrainAll()
	for i, v := range got {
		if v != i {
			t.Fatalf("FIFO violated at %d: got %d", i, v)
		}
	}
}

func TestDropOldestOnItemOverflow(t *testing.T) {
	b := ringbuf.New[int](3, 0, nil)
	var dropped int
	for i := range 5 { // push 0..4 into capacity 3
		dropped += b.Push(i)
	}
	if dropped != 2 {
		t.Fatalf("expected 2 drops, got %d", dropped)
	}
	got := b.DrainAll()
	want := []int{2, 3, 4} // oldest (0,1) evicted, newest win
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestDropOldestOnByteOverflow(t *testing.T) {
	// Each item costs 10 bytes; budget 25 holds at most 2.
	b := ringbuf.New[int](1000, 25, func(int) int { return 10 })
	for i := range 5 {
		b.Push(i)
	}
	if b.Len() != 2 {
		t.Fatalf("expected 2 items under byte budget, got %d", b.Len())
	}
	if b.Bytes() != 20 {
		t.Fatalf("expected 20 bytes, got %d", b.Bytes())
	}
	got := b.DrainAll()
	if got[0] != 3 || got[1] != 4 {
		t.Fatalf("expected newest [3 4], got %v", got)
	}
}

func TestSingleOversizedItemKept(t *testing.T) {
	b := ringbuf.New[int](10, 5, func(int) int { return 100 })
	b.Push(42)
	if b.Len() != 1 {
		t.Fatalf("an oversized newest item must be kept, len=%d", b.Len())
	}
}

func TestPopBatchRespectsLimits(t *testing.T) {
	b := ringbuf.New[int](1000, 0, func(int) int { return 10 })
	for i := range 10 {
		b.Push(i)
	}
	// Count limit.
	batch := b.PopBatch(3, 0)
	if len(batch) != 3 || batch[0] != 0 || batch[2] != 2 {
		t.Fatalf("count-limited batch wrong: %v", batch)
	}
	// Byte limit: 25 bytes / 10 each -> 2 items.
	batch = b.PopBatch(100, 25)
	if len(batch) != 2 {
		t.Fatalf("byte-limited batch should have 2, got %d", len(batch))
	}
	if b.Len() != 5 {
		t.Fatalf("expected 5 remaining, got %d", b.Len())
	}
}

func TestPopBatchAlwaysReturnsOne(t *testing.T) {
	b := ringbuf.New[int](10, 0, func(int) int { return 1000 })
	b.Push(1)
	batch := b.PopBatch(10, 1) // byte limit smaller than item
	if len(batch) != 1 {
		t.Fatalf("expected at least one item, got %d", len(batch))
	}
}

func TestConcurrentPush(t *testing.T) {
	b := ringbuf.New[int](100000, 0, nil)
	var wg sync.WaitGroup
	const g, n = 20, 1000
	wg.Add(g)
	for range g {
		go func() {
			defer wg.Done()
			for i := range n {
				b.Push(i)
			}
		}()
	}
	wg.Wait()
	if b.Len() != g*n {
		t.Fatalf("expected %d items, got %d", g*n, b.Len())
	}
}
