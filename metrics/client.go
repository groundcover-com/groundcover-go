package metrics

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/endpoint"
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
// worker, waits for it to exit, and then performs one final push, all bounded
// by ctx. The final push matters because there is no later scrape in a push
// model: without it, everything recorded since the last interval is lost.
//
// It is idempotent, and returns only ctx's error: a failed final push is
// reported through Config.OnError and Stats, as every other push is.
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

	// pushGate serializes render+push across the worker and manual Push calls.
	// A channel is used so waiting respects the caller's context.
	pushGate chan struct{}

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
	cfg.Headers = cfg.Headers.Clone()
	cfg.ExtraLabels = cloneLabels(cfg.ExtraLabels)
	client := &Client{
		cfg:      cfg,
		endpoint: cfg.pushEndpoint(),
		fixed:    cfg.fixedLabels(),
		pushGate: make(chan struct{}, 1),
	}
	client.pushGate <- struct{}{}
	client.onPanic = func(safeguard.PanicInfo) {}
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
	} else {
		cloned := *client.httpClient
		client.httpClient = &cloned
	}
	client.httpClient.CheckRedirect = endpoint.SafeRedirectPolicy(client.httpClient.CheckRedirect)
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
		err = errors.New("metrics: internal panic during push")
		c.stats.pushFailures.Add(1)
		c.stats.setLastError(err.Error())
		return err
	}
	return err
}

// Start launches the background worker: an immediate first push, then one
// push every Config.Interval. Push failures are surfaced through
// Config.OnError and never stop the worker; it stops when ctx is canceled or
// the returned StopFunc is called. Prefer StopFunc over cancelling ctx, since
// it also flushes what accumulated since the last interval. Calling Start on a
// client whose worker is already running returns ErrAlreadyStarted; a stopped
// client may be started again.
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
	// Closing stopLoop retires the worker without cancelling a push it is in
	// the middle of, so the final push below never races one the intake is
	// still processing. Cancelling workerCtx is the harsher path, taken when
	// the caller's shutdown deadline runs out.
	stopLoop := make(chan struct{})
	done := make(chan struct{})
	safeguard.Go(func() {
		defer close(done)
		defer cancel()
		defer func() {
			c.lifecycleMu.Lock()
			c.running = false
			c.lifecycleMu.Unlock()
		}()
		c.runLoop(workerCtx, stopLoop)
	}, c.onPanic)

	var retire sync.Once
	finalPushGate := make(chan struct{}, 1)
	finalPushGate <- struct{}{}
	finalPushDone := false // guarded by finalPushGate
	stop := func(stopCtx context.Context) error {
		retire.Do(func() { close(stopLoop) })
		select {
		case <-done:
		case <-stopCtx.Done():
			// The shutdown budget is spent: abandon the push in flight.
			cancel()
			return stopCtx.Err()
		}
		// When the worker had already exited, both branches above may have
		// been ready. Do not let select choose done and consume the one final
		// push with an already-cancelled context.
		if err := stopCtx.Err(); err != nil {
			return err
		}
		// A push model has no later scrape, so whatever accumulated since the
		// last interval is delivered here or lost. The push is bounded by the
		// caller's context and runs once successfully. A Stop whose context
		// expires during the push leaves it retryable.
		select {
		case <-finalPushGate:
		case <-stopCtx.Done():
			return stopCtx.Err()
		}
		defer func() { finalPushGate <- struct{}{} }()
		if finalPushDone {
			return nil
		}
		_ = c.pushAndReport(stopCtx)
		if err := stopCtx.Err(); err != nil {
			return err
		}
		finalPushDone = true
		return nil
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
// per interval until ctx is canceled or stopLoop is closed. A failed push never
// exits the loop.
func (c *Client) runLoop(ctx context.Context, stopLoop <-chan struct{}) {
	_ = c.pushAndReport(ctx)
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopLoop:
			return
		case <-ticker.C:
			_ = c.pushAndReport(ctx)
		}
	}
}

// pushAndReport runs one guarded push and surfaces a failure through the
// panic-guarded OnError callback. It returns the failure for callers that also
// want to report it; the background loop ignores it and keeps running.
func (c *Client) pushAndReport(ctx context.Context) error {
	var err error
	if ok := safeguard.Do(func() { err = c.push(ctx) }, c.onPanic); !ok {
		err = errors.New("metrics: internal panic during push")
		c.stats.pushFailures.Add(1)
		c.stats.setLastError(err.Error())
	}
	if err != nil && c.cfg.OnError != nil {
		safeguard.Do(func() { c.cfg.OnError(err) }, func(safeguard.PanicInfo) {
			c.stats.sourcePanics.Add(1)
		})
	}
	return err
}

// push renders all sources and delivers the body. It holds pushGate for the
// full render+send so pushes never overlap.
func (c *Client) push(ctx context.Context) error {
	select {
	case <-c.pushGate:
		defer func() { c.pushGate <- struct{}{} }()
	case <-ctx.Done():
		return ctx.Err()
	}

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
	r := renderer{
		cfg:      c.cfg,
		fixed:    c.fixed,
		seen:     make(map[string]string),
		metadata: make(map[string]metadataEntry),
	}
	var outcome renderOutcome
	for _, entry := range c.sources {
		buf := newCappedBuffer(c.cfg.MaxBodyBytes)
		source := entry.source
		if ok := safeguard.Do(func() { source.WritePrometheus(buf) }, func(safeguard.PanicInfo) {
			c.stats.sourcePanics.Add(1)
		}); !ok {
			outcome.srcErrs = append(outcome.srcErrs, fmt.Errorf("metric source %q: panic while rendering", entry.name))
			outcome.failedSources++
			continue
		}
		if buf.overflow {
			outcome.malformed, outcome.duplicates, outcome.overrides = r.malformed, r.duplicates, r.overrides
			return outcome, &LimitError{Limit: limitMaxBodyBytes, Max: c.cfg.MaxBodyBytes, Actual: buf.actual}
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
	bodyExcerpt := redactResponseExcerpt(strings.TrimSpace(string(excerpt)), c.cfg.IngestionKey, c.cfg.Headers)
	return &StatusError{StatusCode: resp.StatusCode, Body: bodyExcerpt}
}

// redactResponseExcerpt removes credentials the server may have echoed. Every
// configured header value is caller-controlled and may be a secret, not only
// the SDK's apikey.
func redactResponseExcerpt(excerpt, ingestionKey string, headers http.Header) string {
	secrets := make([]string, 0, len(headers)+1)
	if ingestionKey != "" {
		secrets = append(secrets, ingestionKey)
	}
	for _, values := range headers {
		for _, value := range values {
			if value != "" {
				secrets = append(secrets, value)
			}
		}
	}
	// Replace longer overlapping values first so a shorter value cannot expose
	// the unmatched suffix of a longer credential.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		excerpt = strings.ReplaceAll(excerpt, secret, "[REDACTED]")
	}
	return excerpt
}

type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	actual   int
	overflow bool
}

func newCappedBuffer(maxBytes int) *cappedBuffer {
	return &cappedBuffer{max: maxBytes}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.actual += len(p)
	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if b.actual > b.max {
		b.overflow = true
	}
	return len(p), nil
}

func (b *cappedBuffer) Bytes() []byte { return b.buf.Bytes() }

func cloneLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
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
