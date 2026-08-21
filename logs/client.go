package logs

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/ringbuf"
	"github.com/groundcover-com/groundcover-go/internal/safeguard"
	"github.com/groundcover-com/groundcover-go/internal/selfmetrics"
	"github.com/groundcover-com/groundcover-go/internal/transport"
)

// Client is a standalone direct-to-groundcover logs client. It owns its queue,
// batching, retries, and shutdown lifecycle and does not initialize error
// tracking or any other signal.
type Client struct {
	cfg      Config
	res      resource
	disabled bool
	metrics  *selfmetrics.Metrics
	onPanic  safeguard.Handler
	ring     *ringbuf.Buffer[*Record]
	worker   *transport.Worker[*Record]
}

// New constructs a standalone logs Client.
func New(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	client := &Client{cfg: cfg, metrics: selfmetrics.New()}
	if cfg.Disabled {
		client.disabled = true
		return client, nil
	}

	client.res = detectResource(cfg)
	client.onPanic = func(safeguard.PanicInfo) {
		client.metrics.IncPanicsRecovered()
	}
	client.ring = ringbuf.New[*Record](cfg.MaxQueue, cfg.MaxBytes, estimateSize)
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	sender := transport.NewHTTPSender(transport.HTTPConfig{
		Endpoint:           cfg.endpoint(),
		IngestionKey:       cfg.IngestionKey,
		UserAgent:          "groundcover-go/logs",
		Client:             httpClient,
		ContentType:        "application/x-ndjson",
		DisableCompression: true,
	})
	res := client.res
	client.worker = transport.NewWorker[*Record](
		client.ring,
		sender,
		func(records []*Record) ([]byte, error) { return encodeBatch(records, res) },
		observer{metrics: client.metrics},
		nil,
		client.onPanic,
		transport.WorkerConfig{
			BatchSize:        cfg.BatchSize,
			MaxBatchBytes:    cfg.MaxBatchBytes,
			FlushInterval:    cfg.FlushInterval,
			MaxRetries:       cfg.MaxRetries,
			RetryMax:         cfg.RetryMax,
			RateLimitBackoff: cfg.RateLimitBackoff,
		},
	)
	client.worker.Start()
	return client, nil
}

// EmitLog captures a structured log record. It never blocks on network I/O.
func (c *Client) EmitLog(ctx context.Context, level Level, msg string, attrs ...Attributes) {
	record := Record{Timestamp: time.Now(), Level: level, Message: msg}
	for _, attributes := range attrs {
		record.Attributes = merge(record.Attributes, attributes)
	}
	c.EmitRecord(ctx, record)
}

// EmitRecord captures a pre-built record. It is primarily used by aggregate
// SDKs that enrich records with their own context scopes.
func (c *Client) EmitRecord(_ context.Context, record Record) {
	if c == nil || c.disabled {
		return
	}
	safeguard.Do(func() {
		if !record.Level.valid() {
			record.Level = LevelInfo
		}
		if record.Timestamp.IsZero() {
			record.Timestamp = time.Now()
		}
		current := &record
		if c.cfg.BeforeSend != nil {
			out := current
			safeguard.Do(func() { out = c.cfg.BeforeSend(current) }, c.onPanic)
			if out == nil {
				c.metrics.AddDropped(selfmetrics.DropBeforeSend, 1)
				return
			}
			current = out
		}
		if c.cfg.Hasher != nil {
			current.User.ID = c.cfg.Hasher.HashIdentity(current.User.ID)
			current.User.Email = c.cfg.Hasher.HashIdentity(current.User.Email)
		}
		current.Attributes = sanitize(current.Attributes)
		c.metrics.IncCaptured()
		dropped := c.ring.Push(current)
		if dropped > 0 {
			c.metrics.AddDropped(selfmetrics.DropOverflow, int64(dropped))
		}
		c.metrics.SetQueuePending(int64(c.ring.Len()), int64(c.ring.Bytes()))
		if c.ring.Len() >= c.cfg.BatchSize {
			c.worker.Notify()
		}
	}, c.onPanic)
}

// Debug emits a debug-level record.
func (c *Client) Debug(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelDebug, msg, attrs...)
}

// Info emits an info-level record.
func (c *Client) Info(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelInfo, msg, attrs...)
}

// Warn emits a warning-level record.
func (c *Client) Warn(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelWarning, msg, attrs...)
}

// Error emits an error-level record.
func (c *Client) Error(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelError, msg, attrs...)
}

// Debugf emits a formatted debug-level record.
func (c *Client) Debugf(ctx context.Context, format string, args ...any) {
	c.Debug(ctx, fmt.Sprintf(format, args...))
}

// Infof emits a formatted info-level record.
func (c *Client) Infof(ctx context.Context, format string, args ...any) {
	c.Info(ctx, fmt.Sprintf(format, args...))
}

// Warnf emits a formatted warning-level record.
func (c *Client) Warnf(ctx context.Context, format string, args ...any) {
	c.Warn(ctx, fmt.Sprintf(format, args...))
}

// Errorf emits a formatted error-level record.
func (c *Client) Errorf(ctx context.Context, format string, args ...any) {
	c.Error(ctx, fmt.Sprintf(format, args...))
}

// Flush waits until pending records are delivered or ctx expires.
func (c *Client) Flush(ctx context.Context) error {
	if c == nil || c.disabled {
		return nil
	}
	return c.worker.Flush(ctx)
}

// Close flushes and stops the client.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.disabled {
		return nil
	}
	return c.worker.Close(ctx)
}

// FlushTimeout flushes with a fresh context bounded by duration.
func (c *Client) FlushTimeout(duration time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	return c.Flush(ctx)
}

// CloseTimeout closes with a fresh context bounded by duration.
func (c *Client) CloseTimeout(duration time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	return c.Close(ctx)
}

// Stats returns delivery counters.
func (c *Client) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	return statsFromSnapshot(c.metrics.Snapshot())
}

type observer struct{ metrics *selfmetrics.Metrics }

func (o observer) OnSent(n int)         { o.metrics.AddSent(int64(n)) }
func (o observer) OnRetry()             { o.metrics.IncRetries() }
func (o observer) OnRateLimited()       { o.metrics.IncRateLimited() }
func (o observer) OnSubsystemDisabled() { o.metrics.IncSubsystemsDisabled() }
func (o observer) OnSendExhausted(n int) {
	o.metrics.AddDropped(selfmetrics.DropSendExhausted, int64(n))
}
func (o observer) OnQueue(items, bytes int) { o.metrics.SetQueuePending(int64(items), int64(bytes)) }
