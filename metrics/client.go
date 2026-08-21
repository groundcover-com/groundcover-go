package metrics

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/safeguard"
)

// maxErrorBodyBytes bounds how much of an error response body is kept for
// diagnostics.
const maxErrorBodyBytes = 4 << 10

// runtimeSourceName attributes the built-in runtime metrics source in errors
// and statistics.
const runtimeSourceName = "runtime"

// MetricSource is a source of Prometheus text exposition format (0.0.4). The
// built-in Registry implements it; any other producer (for example a
// third-party metrics library's text writer) can be plugged in directly.
// WritePrometheus must be safe for concurrent use with metric updates.
type MetricSource interface {
	// WritePrometheus writes the source's samples in Prometheus text format.
	WritePrometheus(w io.Writer)
}

// sourceNamer is implemented by sources that carry a name for error and
// statistics attribution. It is satisfied by the wrapper returned by Named.
type sourceNamer interface {
	sourceName() string
}

// namedSource is the wrapper returned by Named.
type namedSource struct {
	name   string
	source MetricSource
}

// WritePrometheus implements MetricSource by delegating to the wrapped
// source.
func (n namedSource) WritePrometheus(w io.Writer) { n.source.WritePrometheus(w) }

// sourceName implements sourceNamer.
func (n namedSource) sourceName() string { return n.name }

// Named wraps source with a name that is used only in error messages and
// per-source statistics attribution (for example, the child errors of a
// partial push read `metric source "application": ...`). Sources passed to
// New without Named work identically and are attributed by position as
// "source[i]"; the built-in runtime source is attributed as "runtime".
func Named(name string, source MetricSource) MetricSource {
	return namedSource{name: name, source: source}
}

// sourceEntry is one configured source plus its attribution name.
type sourceEntry struct {
	name   string
	source MetricSource
}

// StopFunc stops the background worker started by Start. It cancels the
// worker and waits for it to exit, bounded by ctx. It is idempotent.
type StopFunc func(ctx context.Context) error

// Client is a standalone push client that renders MetricSource sources and
// POSTs them to the groundcover Prometheus import endpoint. Pushes are
// serialized: a background worker push and a manual Push never overlap.
type Client struct {
	cfg        Config
	endpoint   string
	httpClient *http.Client
	sources    []sourceEntry
	fixed      []label
	disabled   bool
	onPanic    safeguard.Handler

	// pushMu serializes render+push across the worker and manual Push calls.
	pushMu sync.Mutex

	// lifecycleMu guards the background worker state.
	lifecycleMu sync.Mutex
	running     bool

	stats clientStats
}

// New constructs a standalone metrics Client from cfg and the given metric
// sources. Unless Config.DisableRuntimeMetrics is set, a built-in Go runtime
// and process metrics source is collected in addition to sources. New
// returns configuration errors and never panics.
func New(cfg Config, sources ...MetricSource) (*Client, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	client := &Client{
		cfg:      cfg,
		endpoint: cfg.pushEndpoint(),
		fixed:    cfg.fixedLabels(),
	}
	client.onPanic = func(safeguard.PanicInfo) {
		client.stats.sourcePanics.Add(1)
	}
	if cfg.Disabled {
		client.disabled = true
		return client, nil
	}
	client.sources = make([]sourceEntry, 0, len(sources)+1)
	for i, source := range sources {
		name := fmt.Sprintf("source[%d]", i)
		if namer, ok := source.(sourceNamer); ok && namer.sourceName() != "" {
			name = namer.sourceName()
		}
		client.sources = append(client.sources, sourceEntry{name: name, source: source})
	}
	if !cfg.DisableRuntimeMetrics {
		client.sources = append(client.sources, sourceEntry{name: runtimeSourceName, source: newRuntimeSource()})
	}
	client.httpClient = cfg.HTTPClient
	if client.httpClient == nil {
		// The zero transport is the default Go transport: proxy environment
		// variables (HTTP_PROXY / HTTPS_PROXY / NO_PROXY) are honored.
		client.httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	return client, nil
}

// MustNew is like New but panics on a configuration error. It is a
// convenience for main functions that cannot proceed without metrics.
func MustNew(cfg Config, sources ...MetricSource) *Client {
	client, err := New(cfg, sources...)
	if err != nil {
		panic(err)
	}
	return client
}

// Push performs one synchronous render-and-push of all sources. Sources are
// rendered and validated independently: a panicking or malformed source is
// reported but never prevents healthy sources from being delivered. When
// per-source failures occur the delivery still happens and Push returns an
// aggregate error (errors.Join) whose child errors name the failing sources;
// such a push counts as partial in Stats. A fatal limit violation or a
// failed delivery returns an error and counts as a push failure.
//
// Push is safe to call concurrently with a running background worker; pushes
// are serialized and never overlap. A nil or disabled client returns nil.
func (c *Client) Push(ctx context.Context) error {
	if c == nil || c.disabled {
		return nil
	}
	var err error
	if ok := safeguard.Do(func() { err = c.push(ctx) }, c.onPanic); !ok {
		return errors.New("metrics: internal panic during push")
	}
	return err
}

// Start launches the background worker: an immediate first push, then one
// push every Config.Interval. Push failures are surfaced through
// Config.OnError and never stop the worker; it stops when ctx is canceled or
// the returned StopFunc is called. Calling Start on a client whose worker is
// already running returns ErrAlreadyStarted; a stopped client may be started
// again.
func (c *Client) Start(ctx context.Context) (StopFunc, error) {
	if c == nil || c.disabled {
		return func(context.Context) error { return nil }, nil
	}
	c.lifecycleMu.Lock()
	if c.running {
		c.lifecycleMu.Unlock()
		return nil, ErrAlreadyStarted
	}
	c.running = true
	c.lifecycleMu.Unlock()

	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	safeguard.Go(func() {
		defer close(done)
		defer func() {
			c.lifecycleMu.Lock()
			c.running = false
			c.lifecycleMu.Unlock()
		}()
		c.runLoop(workerCtx)
	}, c.onPanic)

	stop := func(stopCtx context.Context) error {
		cancel()
		select {
		case <-done:
			return nil
		case <-stopCtx.Done():
			return stopCtx.Err()
		}
	}
	return stop, nil
}

// Stats returns a point-in-time snapshot of the client's push counters.
func (c *Client) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	c.lifecycleMu.Lock()
	running := c.running
	c.lifecycleMu.Unlock()
	return c.stats.snapshot(running)
}

// runLoop is the background worker body: immediate first push, then one push
// per interval until ctx is canceled. A failed push never exits the loop.
func (c *Client) runLoop(ctx context.Context) {
	c.pushAndReport(ctx)
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pushAndReport(ctx)
		}
	}
}

// pushAndReport runs one guarded push and surfaces a failure through the
// panic-guarded OnError callback.
func (c *Client) pushAndReport(ctx context.Context) {
	var err error
	safeguard.Do(func() { err = c.push(ctx) }, c.onPanic)
	if err != nil && c.cfg.OnError != nil {
		safeguard.Do(func() { c.cfg.OnError(err) }, c.onPanic)
	}
}

// push renders all sources and delivers the body. It holds pushMu for the
// full render+send so pushes never overlap.
func (c *Client) push(ctx context.Context) error {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()

	c.stats.pushes.Add(1)
	c.stats.lastAttemptUnixNano.Store(time.Now().UnixNano())

	outcome, fatal := c.render()
	c.stats.malformedLines.Add(outcome.malformed)
	c.stats.duplicateSeries.Add(outcome.duplicates)
	c.stats.labelOverrides.Add(outcome.overrides)
	c.stats.sourceFailures.Add(outcome.failedSources)

	if fatal != nil {
		err := errors.Join(append([]error{fatal}, outcome.srcErrs...)...)
		c.stats.pushFailures.Add(1)
		c.stats.setLastError(err.Error())
		return err
	}
	c.stats.seriesLastPush.Store(int64(outcome.series))
	c.stats.bytesLastPush.Store(int64(len(outcome.body)))

	if err := c.send(ctx, outcome.body); err != nil {
		err = errors.Join(append([]error{err}, outcome.srcErrs...)...)
		c.stats.pushFailures.Add(1)
		c.stats.setLastError(err.Error())
		return err
	}
	c.stats.lastSuccessUnixNano.Store(time.Now().UnixNano())
	if len(outcome.srcErrs) > 0 {
		aggregate := errors.Join(outcome.srcErrs...)
		c.stats.partialPushes.Add(1)
		c.stats.setLastError(aggregate.Error())
		return aggregate
	}
	return nil
}

// renderOutcome is the result of rendering all sources for one push.
type renderOutcome struct {
	body          []byte
	series        int
	srcErrs       []error
	failedSources uint64
	malformed     uint64
	duplicates    uint64
	overrides     uint64
}

// render produces the exposition body for one push: each source is rendered
// and validated independently with the fixed labels injected, subject to the
// configured limits. A panicking source is recovered, reported, and skipped;
// the remaining sources are still rendered. Only a limit violation (or a
// duplicate series in strict mode) is fatal.
func (c *Client) render() (renderOutcome, error) {
	r := renderer{cfg: c.cfg, fixed: c.fixed, seen: make(map[string]string)}
	var outcome renderOutcome
	for _, entry := range c.sources {
		var buf bytes.Buffer
		source := entry.source
		if ok := safeguard.Do(func() { source.WritePrometheus(&buf) }, c.onPanic); !ok {
			outcome.srcErrs = append(outcome.srcErrs, fmt.Errorf("metric source %q: panic while rendering", entry.name))
			outcome.failedSources++
			continue
		}
		srcErrs, fatal := r.addSource(entry.name, buf.Bytes())
		if len(srcErrs) > 0 {
			outcome.srcErrs = append(outcome.srcErrs, srcErrs...)
			outcome.failedSources++
		}
		if fatal != nil {
			outcome.malformed, outcome.duplicates, outcome.overrides = r.malformed, r.duplicates, r.overrides
			return outcome, fatal
		}
	}
	outcome.body = r.out.Bytes()
	outcome.series = r.series
	outcome.malformed, outcome.duplicates, outcome.overrides = r.malformed, r.duplicates, r.overrides
	return outcome, nil
}

// send gzips body and POSTs it to the import endpoint. Any 2xx status is
// success; other statuses produce a *StatusError with a bounded body excerpt.
// The ingestion key is never included in errors.
func (c *Client) send(ctx context.Context, body []byte) error {
	compressed, err := gzipBytes(body)
	if err != nil {
		return fmt.Errorf("metrics: gzip: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("metrics: build request: %w", err)
	}
	for key, values := range c.cfg.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "groundcover-go/metrics")
	}
	req.Header.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	req.Header.Set("Content-Encoding", "gzip")
	if c.cfg.IngestionKey != "" {
		req.Header.Set("apikey", c.cfg.IngestionKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("metrics: push: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil
	}
	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	return &StatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(excerpt))}
}

// gzipBytes returns the gzip-compressed form of b.
func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
