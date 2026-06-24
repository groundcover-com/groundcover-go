package transport

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// as is a thin wrapper over errors.As for readability.
func as(err error, target any) bool { return errors.As(err, target) }

// sleepCtx sleeps for d or until ctx is done, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// atomicBool is a tiny boolean flag safe for concurrent use.
type atomicBool struct{ v atomic.Bool }

func (b *atomicBool) set(val bool) { b.v.Store(val) }
func (b *atomicBool) get() bool    { return b.v.Load() }

// jitterRNG is a tiny, concurrency-safe pseudo-random source used only for
// backoff jitter (not security-sensitive). It uses a splitmix64 step driven by
// an atomic counter, avoiding any dependency on math/rand.
type jitterRNG struct{ state atomic.Uint64 }

func newJitterRNG() *jitterRNG {
	r := &jitterRNG{}
	r.state.Store(uint64(time.Now().UnixNano()) | 1)
	return r
}

func (r *jitterRNG) next() uint64 {
	z := r.state.Add(0x9E3779B97F4A7C15)
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// intn returns a pseudo-random value in [0, n). It returns 0 for n <= 0.
func (r *jitterRNG) intn(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return int64(r.next() % uint64(n)) //nolint:gosec // bounded, non-negative
}
