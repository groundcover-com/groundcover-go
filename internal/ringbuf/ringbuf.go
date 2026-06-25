// Package ringbuf implements the SDK's bounded pending buffer. It is a circular
// FIFO bounded by both an item count and a byte budget. On overflow it evicts
// the oldest entries ("drop-oldest, newest wins") and reports how many were
// dropped, so the pipeline can account for them. All operations are safe for
// concurrent use.
package ringbuf

import "sync"

// Sizer estimates the byte cost of a single item for the byte budget. The
// returned value is clamped to a minimum of one.
type Sizer[T any] func(T) int

// Buffer is a bounded circular FIFO. Construct one with New; the zero value is
// not usable.
type Buffer[T any] struct {
	mu       sync.Mutex
	buf      []T
	sz       []int
	head     int
	count    int
	bytes    int
	maxItems int
	maxBytes int
	sizeOf   Sizer[T]
}

const minInitialCap = 8

// New returns a buffer bounded by maxItems entries and maxBytes total estimated
// size. Non-positive bounds fall back to permissive defaults. sizeOf may be nil,
// in which case every item is counted as one byte.
func New[T any](maxItems, maxBytes int, sizeOf Sizer[T]) *Buffer[T] {
	if maxItems <= 0 {
		maxItems = 1
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 62 // effectively unbounded
	}
	if sizeOf == nil {
		sizeOf = func(T) int { return 1 }
	}
	return &Buffer[T]{
		maxItems: maxItems,
		maxBytes: maxBytes,
		sizeOf:   sizeOf,
	}
}

// Push appends item, evicting the oldest entries until the buffer is within both
// bounds. It always accepts item (the caller never blocks) and returns the
// number of entries evicted to make room.
func (b *Buffer[T]) Push(item T) (dropped int) {
	size := b.sizeOf(item)
	if size < 1 {
		size = 1
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == len(b.buf) && b.count < b.maxItems {
		b.grow()
	}
	if b.count == b.maxItems {
		b.popOldest()
		dropped++
	}

	tail := (b.head + b.count) % len(b.buf)
	b.buf[tail] = item
	b.sz[tail] = size
	b.count++
	b.bytes += size

	for b.bytes > b.maxBytes && b.count > 1 {
		b.popOldest()
		dropped++
	}
	return dropped
}

// PopBatch removes and returns up to maxItems oldest entries, stopping early if
// adding the next entry would exceed maxBytes (at least one entry is always
// returned when the buffer is non-empty). Non-positive limits are ignored.
func (b *Buffer[T]) PopBatch(maxItems, maxBytes int) []T {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return nil
	}
	out := make([]T, 0, min(b.count, nonZero(maxItems, b.count)))
	batchBytes := 0
	for b.count > 0 {
		if maxItems > 0 && len(out) >= maxItems {
			break
		}
		s := b.sz[b.head]
		if len(out) > 0 && maxBytes > 0 && batchBytes+s > maxBytes {
			break
		}
		out = append(out, b.buf[b.head])
		batchBytes += s
		b.popOldest()
	}
	return out
}

// DrainAll removes and returns every entry, oldest first.
func (b *Buffer[T]) DrainAll() []T {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count == 0 {
		return nil
	}
	out := make([]T, 0, b.count)
	for b.count > 0 {
		out = append(out, b.buf[b.head])
		b.popOldest()
	}
	return out
}

// Len returns the current number of buffered entries.
func (b *Buffer[T]) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

// Bytes returns the current estimated size of buffered entries.
func (b *Buffer[T]) Bytes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bytes
}

// popOldest removes the head entry. The caller must hold the mutex and ensure
// the buffer is non-empty.
func (b *Buffer[T]) popOldest() {
	var zero T
	b.bytes -= b.sz[b.head]
	b.buf[b.head] = zero // release the reference so it can be collected
	b.head = (b.head + 1) % len(b.buf)
	b.count--
}

// grow doubles the backing capacity (bounded by maxItems) and re-linearizes the
// entries so head becomes index zero. The caller must hold the mutex.
func (b *Buffer[T]) grow() {
	newCap := len(b.buf) * 2
	if newCap < minInitialCap {
		newCap = minInitialCap
	}
	if newCap > b.maxItems {
		newCap = b.maxItems
	}
	newBuf := make([]T, newCap)
	newSz := make([]int, newCap)
	for i := range b.count {
		idx := (b.head + i) % len(b.buf)
		newBuf[i] = b.buf[idx]
		newSz[i] = b.sz[idx]
	}
	b.buf = newBuf
	b.sz = newSz
	b.head = 0
}

func nonZero(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
