package groundcover

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/logthrottle"
	"github.com/groundcover-com/groundcover-go/internal/ringbuf"
	"github.com/groundcover-com/groundcover-go/internal/safeguard"
	"github.com/groundcover-com/groundcover-go/internal/selfmetrics"
	"github.com/groundcover-com/groundcover-go/internal/transport"
)

// eventType is the only event type emitted in v1 (error tracking).
const eventType = "exception"

// panicFlushTimeout bounds the best-effort flush performed by Recover before
// re-raising a panic.
const panicFlushTimeout = 2 * time.Second

// Client is an explicit SDK client. Most callers use the package-level API
// (Init + CaptureError); New is for tests and multi-config setups.
type Client struct {
	cfg      atomic.Pointer[Config]
	res      resource
	disabled bool

	metrics  *selfmetrics.Metrics
	throttle *logthrottle.Throttler
	onPanic  safeguard.Handler

	ring   *ringbuf.Buffer[*Event]
	worker *transport.Worker[*Event]
}

// New constructs an explicit Client. A Disabled config yields a no-op client.
func New(cfg Config) (*Client, error) {
	return newClient(cfg, nil)
}

// newClient builds a client, optionally with an injected sender (test seam).
func newClient(cfg Config, sender transport.Sender) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	resolved := cfg.withDefaults()

	c := &Client{metrics: selfmetrics.New()}
	c.cfg.Store(&resolved)
	if resolved.Disabled {
		c.disabled = true
		return c, nil
	}

	c.res = detectResource(resolved)
	c.throttle = logthrottle.New(loggerSink{logger: resolveLogger(resolved.Logger)}, logthrottle.Options{
		Window:       5 * time.Second,
		GlobalWindow: time.Second,
		GlobalCap:    20,
	})
	c.onPanic = func(info safeguard.PanicInfo) {
		c.metrics.IncPanicsRecovered()
		c.throttle.Log(logthrottle.LevelError, fmt.Sprintf("recovered SDK-internal panic: %v", info.Value))
	}

	c.ring = ringbuf.New[*Event](resolved.MaxQueue, resolved.MaxBytes, estimateSize)

	if sender == nil {
		client := resolved.HTTPClient
		if client == nil {
			client = &http.Client{Timeout: defaultHTTPTimeout}
		}
		sender = transport.NewHTTPSender(transport.HTTPConfig{
			Endpoint:     resolved.endpoint(),
			IngestionKey: resolved.IngestionKey,
			UserAgent:    userAgent(),
			Client:       client,
		})
	}

	res := c.res
	encode := func(items []*Event) ([]byte, error) { return encodeBatch(items, res) }

	c.worker = transport.NewWorker[*Event](
		c.ring, sender, encode, metricsObserver{m: c.metrics}, c.throttle, c.onPanic,
		transport.WorkerConfig{
			BatchSize:        resolved.BatchSize,
			MaxBatchBytes:    resolved.MaxBatchBytes,
			FlushInterval:    resolved.FlushInterval,
			MaxRetries:       resolved.MaxRetries,
			RetryMax:         resolved.RetryMax,
			RateLimitBackoff: resolved.RateLimitBackoff,
		},
	)
	c.worker.Start()
	return c, nil
}

func resolveLogger(l Logger) Logger {
	if l != nil {
		return l
	}
	return newSlogLogger()
}

// config returns the current resolved configuration snapshot.
func (c *Client) config() *Config { return c.cfg.Load() }

// CaptureError captures err (handled by default) and enqueues it for delivery.
// It never blocks on I/O and never affects control flow.
func (c *Client) CaptureError(ctx context.Context, err error, opts ...Option) {
	if c.disabled || err == nil {
		return
	}
	safeguard.Do(func() {
		e := c.newErrorEvent(err, true)
		c.finishAndEnqueue(ctx, e, opts)
	}, c.onPanic)
}

// CaptureMessage captures a non-error notice at the given level.
func (c *Client) CaptureMessage(ctx context.Context, msg string, level Level, opts ...Option) {
	if c.disabled {
		return
	}
	safeguard.Do(func() {
		cfg := c.config()
		e := &Event{
			ID:           newUUID(),
			Timestamp:    time.Now(),
			Type:         eventType,
			Level:        level,
			ErrorHandled: true,
			ErrorType:    "message",
			ErrorMessage: msg,
			Service:      Service{Name: c.res.serviceName, Version: c.res.release},
		}
		if !level.valid() {
			e.Level = LevelInfo
		}
		_ = cfg
		c.finishAndEnqueue(ctx, e, opts)
	}, c.onPanic)
}

// Recover captures a panic in the current goroutine (as an unhandled error),
// performs a short best-effort flush, and re-raises the panic. Use it deferred:
//
//	defer client.Recover(ctx)
func (c *Client) Recover(ctx context.Context) {
	r := recover()
	if r == nil {
		return
	}
	if !c.disabled {
		safeguard.Do(func() {
			e := c.newPanicEvent(r)
			c.finishAndEnqueue(ctx, e, nil)
			// A fresh context: the caller's ctx may already be cancelled by the
			// panic unwinding, but we still want a brief best-effort flush.
			flushCtx, cancel := context.WithTimeout(context.Background(), panicFlushTimeout)
			defer cancel()
			_ = c.worker.Flush(flushCtx) //nolint:contextcheck // deliberate detached best-effort flush before re-raise
		}, c.onPanic)
	}
	panic(r) // re-raise: we observe, never alter control flow
}

// CaptureRecovered captures an already-recovered panic value without re-raising.
// It is used by middleware that owns the response lifecycle.
func (c *Client) CaptureRecovered(ctx context.Context, recovered any, opts ...Option) {
	if c.disabled || recovered == nil {
		return
	}
	safeguard.Do(func() {
		e := c.newPanicEvent(recovered)
		c.finishAndEnqueue(ctx, e, opts)
	}, c.onPanic)
}

// newErrorEvent builds an event from an error.
func (c *Client) newErrorEvent(err error, handled bool) *Event {
	cfg := c.config()
	return &Event{
		ID:           newUUID(),
		Timestamp:    time.Now(),
		Type:         eventType,
		Level:        LevelError,
		ErrorHandled: handled,
		ErrorType:    errorType(err),
		ErrorMessage: err.Error(),
		Stacktrace:   captureStack(3, cfg.StackDepthMax, c.res.mainModule),
		Service:      Service{Name: c.res.serviceName, Version: c.res.release},
	}
}

// newPanicEvent builds an unhandled-error event from a recovered panic value.
func (c *Client) newPanicEvent(recovered any) *Event {
	cfg := c.config()
	var (
		etype string
		emsg  string
	)
	if err, ok := recovered.(error); ok {
		etype = errorType(err)
		emsg = err.Error()
	} else {
		etype = "panic"
		emsg = fmt.Sprintf("%v", recovered)
	}
	return &Event{
		ID:           newUUID(),
		Timestamp:    time.Now(),
		Type:         eventType,
		Level:        LevelFatal,
		ErrorHandled: false,
		ErrorType:    etype,
		ErrorMessage: emsg,
		Stacktrace:   captureStack(3, cfg.StackDepthMax, c.res.mainModule),
		Service:      Service{Name: c.res.serviceName, Version: c.res.release},
	}
}

// finishAndEnqueue applies scope and options, pseudonymizes identity, computes
// the fingerprint, runs BeforeSend, and enqueues the event.
func (c *Client) finishAndEnqueue(ctx context.Context, e *Event, opts []Option) {
	cfg := c.config()

	scopeFromContext(ctx).applyTo(e)
	for _, o := range opts {
		if o != nil {
			o(e)
		}
	}

	if cfg.Hasher != nil {
		e.User.ID = cfg.Hasher.HashIdentity(e.User.ID)
		e.User.Email = cfg.Hasher.HashIdentity(e.User.Email)
	}
	if e.Fingerprint == "" {
		e.Fingerprint = fingerprint(e)
	}

	if cfg.BeforeSend != nil {
		out := c.runBeforeSend(cfg.BeforeSend, e)
		if out == nil {
			c.metrics.AddDropped(selfmetrics.DropBeforeSend, 1)
			return
		}
		e = out
	}

	c.metrics.IncCaptured()
	c.enqueue(cfg, e)
}

// runBeforeSend invokes the user callback inside a panic guard. A panic is
// treated as "keep the event unmodified".
func (c *Client) runBeforeSend(fn func(*Event) *Event, e *Event) (out *Event) {
	out = e
	safeguard.Do(func() { out = fn(e) }, c.onPanic)
	return out
}

// enqueue performs the bounded, non-blocking hand-off to the pipeline.
func (c *Client) enqueue(cfg *Config, e *Event) {
	dropped := c.ring.Push(e)
	if dropped > 0 {
		c.metrics.AddDropped(selfmetrics.DropOverflow, int64(dropped))
		c.fireOnDrop(cfg.OnDrop, dropped)
	}
	c.metrics.SetQueuePending(int64(c.ring.Len()), int64(c.ring.Bytes()))
	if c.ring.Len() >= cfg.BatchSize {
		c.worker.Notify()
	}
}

func (c *Client) fireOnDrop(fn func(int), n int) {
	if fn == nil {
		return
	}
	safeguard.Do(func() { fn(n) }, c.onPanic)
}

// SetUser returns a context with the identity set on a fresh request scope.
func (c *Client) SetUser(ctx context.Context, u User) context.Context {
	newCtx, sc := cloneScopeIntoContext(ctx)
	sc.SetUser(u)
	return newCtx
}

// WithScope returns a context with a cloned scope, after applying fn to it.
func (c *Client) WithScope(ctx context.Context, fn func(*Scope)) context.Context {
	newCtx, sc := cloneScopeIntoContext(ctx)
	if fn != nil {
		safeguard.Do(func() { fn(sc) }, c.onPanic)
	}
	return newCtx
}

// Flush blocks until pending events are delivered or ctx expires.
func (c *Client) Flush(ctx context.Context) error {
	if c.disabled {
		return nil
	}
	return c.worker.Flush(ctx)
}

// Close flushes and stops the client. It is idempotent and bounded by ctx.
func (c *Client) Close(ctx context.Context) error {
	if c.disabled {
		return nil
	}
	return c.worker.Close(ctx)
}

// Stats returns a snapshot of the SDK's self-observability counters.
func (c *Client) Stats() Stats {
	return statsFromSnapshot(c.metrics.Snapshot())
}

// metricsObserver adapts the worker's Observer to the self-metrics counters.
type metricsObserver struct{ m *selfmetrics.Metrics }

func (o metricsObserver) OnSent(n int)   { o.m.AddSent(int64(n)) }
func (o metricsObserver) OnRetry()       { o.m.IncRetries() }
func (o metricsObserver) OnRateLimited() { o.m.IncRateLimited() }
func (o metricsObserver) OnSendExhausted(n int) {
	o.m.AddDropped(selfmetrics.DropSendExhausted, int64(n))
}
func (o metricsObserver) OnQueue(items, bytes int) {
	o.m.SetQueuePending(int64(items), int64(bytes))
}
