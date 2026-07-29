package transport

import (
	"context"
	"sync"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/logthrottle"
	"github.com/groundcover-com/groundcover-go/internal/ringbuf"
	"github.com/groundcover-com/groundcover-go/internal/safeguard"
)

// Encoder turns a batch of items into an uncompressed JSON request body.
type Encoder[T any] func(items []T) ([]byte, error)

// WorkerConfig configures a Worker. Zero fields fall back to defaults.
type WorkerConfig struct {
	// BatchSize is the maximum number of items per request.
	BatchSize int
	// MaxBatchBytes is the maximum estimated size per request.
	MaxBatchBytes int
	// FlushInterval is the periodic flush cadence.
	FlushInterval time.Duration
	// MaxRetries is the maximum number of retry attempts after the first try.
	MaxRetries int
	// BaseBackoff is the initial backoff used for exponential backoff.
	BaseBackoff time.Duration
	// RetryMax caps the exponential backoff.
	RetryMax time.Duration
	// RateLimitBackoff is the minimum backoff applied to a 429 response.
	RateLimitBackoff time.Duration
	// MaxConcurrent caps concurrent outbound requests (semaphore).
	MaxConcurrent int
	// Now overrides the clock (tests). Defaults to time.Now.
	Now func() time.Time
	// Sleep overrides the cancelable sleep (tests). Defaults to a timer.
	Sleep func(ctx context.Context, d time.Duration)
}

func (c *WorkerConfig) withDefaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = 250
	}
	if c.MaxBatchBytes <= 0 {
		c.MaxBatchBytes = 512 << 10
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 5 * time.Second
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = 200 * time.Millisecond
	}
	if c.RetryMax <= 0 {
		c.RetryMax = 30 * time.Second
	}
	if c.RateLimitBackoff <= 0 {
		c.RateLimitBackoff = 30 * time.Second
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 4
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Sleep == nil {
		c.Sleep = sleepCtx
	}
}

// Observer receives delivery outcomes for self-metrics. All methods must be
// non-blocking and panic-free.
type Observer interface {
	OnSent(n int)
	OnRetry()
	OnRateLimited()
	OnSendExhausted(n int)
	OnQueue(items, bytes int)
	OnSubsystemDisabled()
}

// Worker batches items from a bounded buffer and delivers them via a Sender. A
// single goroutine owns the loop; sends may run concurrently up to a configured
// limit. It is the sole network owner.
type Worker[T any] struct {
	ring    *ringbuf.Buffer[T]
	sender  Sender
	encode  Encoder[T]
	obs     Observer
	log     *logthrottle.Throttler
	cfg     WorkerConfig
	onPanic safeguard.Handler

	trigger chan struct{}
	stop    chan struct{}
	stopped chan struct{}

	baseCtx    context.Context
	cancelBase context.CancelFunc

	sem      chan struct{}
	inflight sync.WaitGroup

	rng *jitterRNG

	startOnce sync.Once
	closeOnce sync.Once
}

// NewWorker constructs a Worker. ring, sender and encode are required; obs and
// log may be nil.
func NewWorker[T any](
	ring *ringbuf.Buffer[T],
	sender Sender,
	encode Encoder[T],
	obs Observer,
	log *logthrottle.Throttler,
	onPanic safeguard.Handler,
	cfg WorkerConfig,
) *Worker[T] {
	cfg.withDefaults()
	baseCtx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancelBase is invoked in Close
	return &Worker[T]{
		ring:       ring,
		sender:     sender,
		encode:     encode,
		obs:        obs,
		log:        log,
		cfg:        cfg,
		onPanic:    onPanic,
		trigger:    make(chan struct{}, 1),
		stop:       make(chan struct{}),
		stopped:    make(chan struct{}),
		baseCtx:    baseCtx,
		cancelBase: cancel,
		sem:        make(chan struct{}, cfg.MaxConcurrent),
		rng:        newJitterRNG(),
	}
}

// Start launches the worker loop. It is safe to call once; subsequent calls are
// no-ops.
func (w *Worker[T]) Start() {
	w.startOnce.Do(func() {
		safeguard.Go(w.loop, func(info safeguard.PanicInfo) {
			// A panicking loop self-disables rather than crash-looping. The
			// loop's own deferred close(w.stopped) has already run during
			// unwinding, so we must not close it again here. Flush/Close still
			// drain the ring caller-side, so pending events are not lost.
			if w.obs != nil {
				w.obs.OnSubsystemDisabled()
			}
			if w.onPanic != nil {
				w.onPanic(info)
			}
		})
	})
}

// Notify hints that items are pending so the worker can flush before the next
// interval tick. It never blocks.
func (w *Worker[T]) Notify() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

func (w *Worker[T]) loop() {
	ticker := time.NewTicker(w.cfg.FlushInterval)
	defer ticker.Stop()
	defer close(w.stopped)

	for {
		select {
		case <-w.stop:
			w.drain(w.baseCtx)
			return
		case <-ticker.C:
			w.dispatchReady(w.baseCtx)
		case <-w.trigger:
			w.dispatchReady(w.baseCtx)
		}
		w.reportQueue()
	}
}

// dispatchReady pops and dispatches all currently-buffered batches.
func (w *Worker[T]) dispatchReady(ctx context.Context) {
	for {
		batch := w.ring.PopBatch(w.cfg.BatchSize, w.cfg.MaxBatchBytes)
		if len(batch) == 0 {
			return
		}
		w.dispatch(ctx, batch)
	}
}

// drain dispatches everything remaining (used during shutdown).
func (w *Worker[T]) drain(ctx context.Context) {
	w.dispatchReady(ctx)
}

// dispatch sends a batch, bounded by the concurrency semaphore. If the semaphore
// is full it sends synchronously to apply natural backpressure on the loop
// (never on the caller).
func (w *Worker[T]) dispatch(ctx context.Context, batch []T) {
	select {
	case w.sem <- struct{}{}:
		w.inflight.Add(1)
		safeguard.Go(func() {
			defer w.inflight.Done()
			defer func() { <-w.sem }()
			w.sendWithRetry(ctx, batch)
		}, w.onPanic)
	default:
		w.sendWithRetry(ctx, batch)
	}
}

// sendWithRetry delivers a batch, guarded so a panic in encoding or in the
// sender is contained, counted as an exhausted drop, and never propagates to the
// worker loop (where it would self-disable the whole subsystem over one batch).
func (w *Worker[T]) sendWithRetry(ctx context.Context, batch []T) {
	if ok := safeguard.Do(func() { w.deliverBatch(ctx, batch) }, w.onPanic); !ok {
		w.observeExhausted(len(batch))
	}
}

// deliverBatch encodes and delivers a batch, applying the retry/backoff policy.
func (w *Worker[T]) deliverBatch(ctx context.Context, batch []T) {
	body, err := w.encode(batch)
	if err != nil {
		w.logf(logthrottle.LevelError, "encode failed, dropping batch")
		w.observeExhausted(len(batch))
		return
	}

	for attempt := 0; ; attempt++ {
		sendErr := w.sender.Send(ctx, body)
		if sendErr == nil {
			if w.obs != nil {
				w.obs.OnSent(len(batch))
			}
			return
		}

		backoff, giveUp := w.classifyRetry(sendErr, attempt)
		if giveUp {
			w.observeExhausted(len(batch))
			return
		}
		if w.obs != nil {
			w.obs.OnRetry()
		}
		w.cfg.Sleep(ctx, backoff)
		if ctx.Err() != nil {
			// Shutting down: drop rather than hang.
			w.observeExhausted(len(batch))
			return
		}
	}
}

// classifyRetry decides the backoff for the next attempt and whether to give up.
func (w *Worker[T]) classifyRetry(err error, attempt int) (backoff time.Duration, giveUp bool) {
	se, ok := asSendError(err)
	if ok && se.RateLimited {
		if w.obs != nil {
			w.obs.OnRateLimited()
		}
		if attempt >= w.cfg.MaxRetries {
			return 0, true
		}
		backoff = w.cfg.RateLimitBackoff
		if se.RetryAfter > backoff {
			backoff = se.RetryAfter
		}
		return backoff, false
	}
	if ok && !se.Retryable {
		return 0, true // permanent error
	}
	if attempt >= w.cfg.MaxRetries {
		return 0, true
	}
	return w.expBackoff(attempt), false
}

// expBackoff computes a full-jittered exponential backoff capped at RetryMax.
func (w *Worker[T]) expBackoff(attempt int) time.Duration {
	d := w.cfg.BaseBackoff
	for range attempt {
		d *= 2
		if d >= w.cfg.RetryMax {
			d = w.cfg.RetryMax
			break
		}
	}
	// Full jitter: pseudo-random in [0, d]. Jitter is not security-sensitive.
	return time.Duration(w.rng.intn(int64(d) + 1))
}

// Flush drains the buffer and waits for in-flight sends, bounded by ctx. It
// drains even if the worker loop has self-disabled — a caller-driven dispatch —
// so pending events are never silently left behind on a "successful" flush.
func (w *Worker[T]) Flush(ctx context.Context) error {
	w.dispatchReady(ctx)
	// reportQueue is guarded here because Flush runs on the caller's goroutine;
	// a misbehaving Observer must not panic the caller.
	_ = safeguard.Do(w.reportQueue, w.onPanic)
	return w.waitInflight(ctx)
}

// Close stops the worker, drains remaining items, and waits for in-flight sends,
// all bounded by ctx. It is idempotent. The drain is caller-driven so it also
// covers the case where the loop already exited (e.g. after self-disable).
func (w *Worker[T]) Close(ctx context.Context) error {
	var err error
	w.closeOnce.Do(func() {
		close(w.stop)
		select {
		case <-w.stopped:
		case <-ctx.Done():
			err = ctx.Err()
		}
		w.dispatchReady(ctx) // drain anything the loop did not (e.g. after self-disable)
		if waitErr := w.waitInflight(ctx); waitErr != nil && err == nil {
			err = waitErr
		}
		w.cancelBase()
	})
	return err
}

// waitInflight blocks until all in-flight sends complete or ctx expires.
func (w *Worker[T]) waitInflight(ctx context.Context) error {
	done := make(chan struct{})
	safeguard.Go(func() {
		w.inflight.Wait()
		close(done)
	}, w.onPanic)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker[T]) reportQueue() {
	if w.obs != nil {
		w.obs.OnQueue(w.ring.Len(), w.ring.Bytes())
	}
}

func (w *Worker[T]) observeExhausted(n int) {
	if w.obs != nil {
		w.obs.OnSendExhausted(n)
	}
}

func (w *Worker[T]) logf(level logthrottle.Level, msg string) {
	if w.log != nil {
		w.log.Log(level, msg)
	}
}

func asSendError(err error) (*SendError, bool) {
	var se *SendError
	if as(err, &se) {
		return se, true
	}
	return nil, false
}
