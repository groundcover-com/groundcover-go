package metrics

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// capturedRequest snapshots what the fake intake received.
type capturedRequest struct {
	path        string
	apikey      string
	contentType string
	encoding    string
	userAgent   string
	custom      string
	body        string
}

// captureServer is an httptest intake that decodes gzip bodies and records
// every request.
type captureServer struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	requests []capturedRequest
	status   int
}

func newCaptureServer(t *testing.T) *captureServer {
	t.Helper()
	cs := &captureServer{t: t, status: http.StatusNoContent}
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("body is not gzip: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(zr)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		cs.mu.Lock()
		cs.requests = append(cs.requests, capturedRequest{
			path:        r.URL.Path,
			apikey:      r.Header.Get("apikey"),
			contentType: r.Header.Get("Content-Type"),
			encoding:    r.Header.Get("Content-Encoding"),
			userAgent:   r.Header.Get("User-Agent"),
			custom:      r.Header.Get("X-Custom"),
			body:        string(body),
		})
		status := cs.status
		cs.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(cs.srv.Close)
	return cs
}

func (cs *captureServer) setStatus(status int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.status = status
}

func (cs *captureServer) count() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.requests)
}

func (cs *captureServer) last() capturedRequest {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.requests) == 0 {
		cs.t.Fatal("no requests captured")
	}
	return cs.requests[len(cs.requests)-1]
}

// testConfig returns a Config pointed at the capture server with explicit
// job/instance so host environment never leaks into assertions.
func testConfig(cs *captureServer) Config {
	return Config{
		DSN:                   cs.srv.URL,
		IngestionKey:          "test-secret-key",
		Job:                   "test-job",
		Instance:              "test-instance",
		DisableRuntimeMetrics: true,
	}
}

func TestPushContract(t *testing.T) {
	cs := newCaptureServer(t)
	reg := NewRegistry()
	reg.Gauge("test_gauge", map[string]string{"path": "/x"}).Set(1.5)

	cfg := testConfig(cs)
	cfg.Headers = http.Header{"X-Custom": []string{"custom-value"}}
	client, err := New(cfg, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}

	req := cs.last()
	if req.path != "/api/v1/import/prometheus" {
		t.Errorf("path = %q, want /api/v1/import/prometheus", req.path)
	}
	if req.apikey != "test-secret-key" {
		t.Errorf("apikey header = %q, want the ingestion key", req.apikey)
	}
	if req.contentType != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("content-type = %q", req.contentType)
	}
	if req.encoding != "gzip" {
		t.Errorf("content-encoding = %q, want gzip", req.encoding)
	}
	if req.userAgent != "groundcover-go/metrics" {
		t.Errorf("user-agent = %q", req.userAgent)
	}
	if req.custom != "custom-value" {
		t.Errorf("custom header = %q, want custom-value", req.custom)
	}
	want := "# TYPE test_gauge gauge\n" +
		`test_gauge{instance="test-instance",job="test-job",path="/x"} 1.5` + "\n"
	if req.body != want {
		t.Errorf("body:\n%s\nwant:\n%s", req.body, want)
	}

	stats := client.Stats()
	if stats.Pushes != 1 || stats.PushFailures != 0 || stats.PartialPushes != 0 {
		t.Errorf("stats = %+v", stats)
	}
	if stats.SeriesLastPush != 1 {
		t.Errorf("SeriesLastPush = %d, want 1", stats.SeriesLastPush)
	}
	if stats.BytesLastPush != len(want) {
		t.Errorf("BytesLastPush = %d, want %d", stats.BytesLastPush, len(want))
	}
	if stats.LastAttempt.IsZero() || stats.LastSuccess.IsZero() {
		t.Errorf("LastAttempt/LastSuccess not set: %+v", stats)
	}
	if stats.LastError != "" {
		t.Errorf("LastError = %q, want empty", stats.LastError)
	}
	if stats.WorkerRunning {
		t.Error("WorkerRunning = true with no worker started")
	}
}

func TestPushNon2xxStatusError(t *testing.T) {
	cs := newCaptureServer(t)
	bigBody := strings.Repeat("x", 10<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, bigBody)
	}))
	t.Cleanup(srv.Close)

	cfg := testConfig(cs)
	cfg.DSN = srv.URL
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	var statusErr *StatusError
	if !errors.As(pushErr, &statusErr) {
		t.Fatalf("Push error = %v, want *StatusError", pushErr)
	}
	if statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", statusErr.StatusCode)
	}
	if len(statusErr.Body) > 4<<10 {
		t.Errorf("Body excerpt is %d bytes, want <= 4KiB", len(statusErr.Body))
	}
	if strings.Contains(pushErr.Error(), "test-secret-key") {
		t.Error("error string contains the ingestion key")
	}
	stats := client.Stats()
	if stats.PushFailures != 1 {
		t.Errorf("PushFailures = %d, want 1", stats.PushFailures)
	}
	if !stats.LastSuccess.IsZero() {
		t.Errorf("LastSuccess = %v, want zero after only a failure", stats.LastSuccess)
	}
	if stats.LastError == "" {
		t.Error("LastError not set after failure")
	}
	if strings.Contains(stats.LastError, "test-secret-key") {
		t.Error("LastError contains the ingestion key")
	}
}

func TestPushTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	client, err := New(Config{
		DSN:                   srv.URL,
		IngestionKey:          "test-secret-key",
		Timeout:               50 * time.Millisecond,
		Job:                   "j",
		Instance:              "i",
		DisableRuntimeMetrics: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	if pushErr == nil {
		t.Fatal("Push succeeded, want timeout error")
	}
	if !errors.Is(pushErr, context.DeadlineExceeded) {
		t.Errorf("Push error = %v, want context.DeadlineExceeded", pushErr)
	}
	if strings.Contains(pushErr.Error(), "test-secret-key") {
		t.Error("error string contains the ingestion key")
	}
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"missing DSN", Config{}},
		{"reserved header apikey", Config{DSN: "x", Headers: http.Header{"APIKEY": []string{"v"}}}},
		{"reserved header content-type", Config{DSN: "x", Headers: http.Header{"content-type": []string{"v"}}}},
		{"reserved header content-encoding", Config{DSN: "x", Headers: http.Header{"Content-Encoding": []string{"v"}}}},
		{"extra label job", Config{DSN: "x", ExtraLabels: map[string]string{"job": "v"}}},
		{"extra label instance", Config{DSN: "x", ExtraLabels: map[string]string{"Instance": "v"}}},
		{"extra label invalid name", Config{DSN: "x", ExtraLabels: map[string]string{"bad-name": "v"}}},
		{"extra label value too long", Config{DSN: "x", MaxLabelValueBytes: 4, ExtraLabels: map[string]string{"ok": "toolong"}}},
		{"job too long", Config{DSN: "x", Job: "toolong", MaxLabelValueBytes: 4}},
		{"instance too long", Config{DSN: "x", Instance: "toolong", MaxLabelValueBytes: 4}},
		{"too many fixed labels", Config{DSN: "x", Job: "j", Instance: "i", MaxLabelsPerSeries: 1}},
		{"reserved extra label prefix", Config{DSN: "x", ExtraLabels: map[string]string{"__name__": "x"}}},
		{"both DSN and Endpoint", Config{DSN: "x", Endpoint: "https://y.example.com/push"}},
		// Fixed labels reach every sample, so invalid bytes there would make
		// the whole exposition out of spec on every push.
		// A DSN no request can be built from must fail at construction rather
		// than as a push failure repeating every interval.
		{"dsn with a bad scheme", Config{DSN: "ftp://host.example"}},
		{"dsn with no host", Config{DSN: "https://"}},
		{"endpoint with control characters", Config{Endpoint: "https://host.example/\x7f"}},
		{"configured job not utf-8", Config{DSN: "x", Job: "svc\xff"}},
		{"configured instance not utf-8", Config{DSN: "x", Instance: "host\xff"}},
		{"extra label value not utf-8", Config{DSN: "x", ExtraLabels: map[string]string{"tag": "a\xffb"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg); err == nil {
				t.Errorf("New(%+v) succeeded, want error", tt.cfg)
			}
		})
	}
	if _, err := New(Config{Disabled: true}); err != nil {
		t.Errorf("New(Disabled) = %v, want nil", err)
	}

	// An unusable default is dropped instead of failing construction over a
	// value the caller never set.
	t.Setenv("OTEL_SERVICE_NAME", "svc\xff")
	client, err := New(Config{DSN: "x"})
	if err != nil {
		t.Fatalf("New with an invalid environment default = %v, want nil", err)
	}
	for _, l := range client.fixed {
		if l.name == labelJob {
			t.Errorf("job label kept an unusable default %q", l.value)
		}
	}
}

func TestMissingDSNSentinel(t *testing.T) {
	_, err := New(Config{})
	if !errors.Is(err, ErrMissingDSN) {
		t.Errorf("New error = %v, want ErrMissingDSN", err)
	}
}

func TestMustNewPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustNew did not panic on bad config")
		}
	}()
	MustNew(Config{})
}

func TestEndpointDerivation(t *testing.T) {
	tests := []struct {
		dsn  string
		want string
	}{
		{"https://tenant.example.com", "https://tenant.example.com/api/v1/import/prometheus"},
		{"https://tenant.example.com/", "https://tenant.example.com/api/v1/import/prometheus"},
		{"tenant.example.com", "https://tenant.example.com/api/v1/import/prometheus"},
		{"  http://local:8080/ ", "http://local:8080/api/v1/import/prometheus"},
	}
	for _, tt := range tests {
		if got := (Config{DSN: tt.dsn}).pushEndpoint(); got != tt.want {
			t.Errorf("pushEndpoint(%q) = %q, want %q", tt.dsn, got, tt.want)
		}
	}
}

func TestJobDefaultsFromEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "otel-svc")
	t.Setenv("GC_SERVICE_NAME", "gc-svc")
	if got := (Config{}).withDefaults().Job; got != "otel-svc" {
		t.Errorf("Job = %q, want otel-svc", got)
	}
	t.Setenv("OTEL_SERVICE_NAME", "")
	if got := (Config{}).withDefaults().Job; got != "gc-svc" {
		t.Errorf("Job = %q, want gc-svc", got)
	}
}

func TestDisabledAndNilClient(t *testing.T) {
	client, err := New(Config{Disabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Errorf("disabled Push = %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Errorf("disabled Start = %v", err)
	}
	if err := stop(context.Background()); err != nil {
		t.Errorf("disabled stop = %v", err)
	}

	var nilClient *Client
	if err := nilClient.Push(context.Background()); err != nil {
		t.Errorf("nil Push = %v", err)
	}
	if got := nilClient.Stats(); got != (Stats{}) {
		t.Errorf("nil Stats = %+v", got)
	}
}

func TestLimitMaxSeries(t *testing.T) {
	cs := newCaptureServer(t)
	reg := NewRegistry()
	reg.Gauge("a", nil).Set(1)
	reg.Gauge("b", nil).Set(2)
	reg.Gauge("c", nil).Set(3)

	cfg := testConfig(cs)
	cfg.MaxSeries = 2
	client, err := New(cfg, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	var limitErr *LimitError
	if !errors.As(pushErr, &limitErr) || limitErr.Limit != "MaxSeries" {
		t.Fatalf("Push error = %v, want *LimitError{MaxSeries}", pushErr)
	}
	if cs.count() != 0 {
		t.Error("a limited push must not reach the server")
	}

	// The client keeps working: shrinking below the cap succeeds.
	if !reg.Unregister("c", nil) {
		t.Fatal("Unregister(c) = false")
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push after unregister: %v", err)
	}
}

func TestLimitMaxBodyBytes(t *testing.T) {
	cs := newCaptureServer(t)
	reg := NewRegistry()
	reg.Gauge("some_long_metric_name", map[string]string{"key": strings.Repeat("v", 100)}).Set(1)

	cfg := testConfig(cs)
	cfg.MaxBodyBytes = 64
	client, err := New(cfg, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	var limitErr *LimitError
	if !errors.As(pushErr, &limitErr) || limitErr.Limit != "MaxBodyBytes" {
		t.Fatalf("Push error = %v, want *LimitError{MaxBodyBytes}", pushErr)
	}
}

func TestLimitMaxLabelsPerSeries(t *testing.T) {
	cs := newCaptureServer(t)
	reg := NewRegistry()
	reg.Gauge("m", map[string]string{"l1": "1", "l2": "2", "l3": "3"}).Set(1)

	cfg := testConfig(cs)
	cfg.MaxLabelsPerSeries = 4 // 3 source + job + instance = 5
	client, err := New(cfg, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	var limitErr *LimitError
	if !errors.As(pushErr, &limitErr) || limitErr.Limit != "MaxLabelsPerSeries" {
		t.Fatalf("Push error = %v, want *LimitError{MaxLabelsPerSeries}", pushErr)
	}
}

func TestStartPushesImmediately(t *testing.T) {
	cs := newCaptureServer(t)
	reg := NewRegistry()
	reg.Counter("c_total", nil).Inc()

	cfg := testConfig(cs)
	cfg.Interval = time.Hour // only the immediate push can happen
	client, err := New(cfg, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = stop(context.Background()) }()

	deadline := time.Now().Add(5 * time.Second)
	for cs.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if cs.count() == 0 {
		t.Fatal("no immediate push before the interval elapsed")
	}
}

func TestStartIntervalPushes(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.Interval = 20 * time.Millisecond
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for cs.count() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := cs.count(); got < 3 {
		t.Errorf("got %d pushes, want >= 3 (immediate + interval)", got)
	}
}

// There is no later scrape in a push model, so Stop delivers what accumulated
// since the last interval instead of losing it.
func TestStopPushesFinalSamples(t *testing.T) {
	cs := newCaptureServer(t)
	reg := NewRegistry()
	cfg := testConfig(cs)
	cfg.Interval = time.Hour // only the immediate push and the final one
	client, err := New(cfg, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for cs.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// Recorded after the first push: only a shutdown push can deliver it.
	reg.Counter("orders_total", nil).Add(7)
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := cs.count(); got != 2 {
		t.Fatalf("got %d pushes, want 2 (immediate + final)", got)
	}
	if body := cs.last().body; !strings.Contains(body, "orders_total{") || !strings.Contains(body, " 7\n") {
		t.Errorf("final push missing the last samples:\n%s", body)
	}

	// Stop is idempotent: a second call pushes nothing more.
	if err := stop(context.Background()); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if got := cs.count(); got != 2 {
		t.Errorf("got %d pushes after a second stop, want 2", got)
	}
}

// A failing final push is reported like any other push, not as a stop error:
// the worker did stop, and a shutdown path must not mistake a telemetry
// failure for a failure to shut down.
func TestStopReportsFinalPushFailureThroughOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	var mu sync.Mutex
	var errCount int
	cfg := Config{
		Endpoint:     srv.URL,
		IngestionKey: "k",
		Job:          "test-job",
		Instance:     "test-instance",
		Interval:     time.Hour,
		OnError: func(error) {
			mu.Lock()
			errCount++
			mu.Unlock()
		},
	}
	client, err := New(cfg, NewRegistry())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := errCount
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop = %v, want nil: the worker stopped", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if errCount < 2 {
		t.Errorf("OnError called %d times, want >= 2 (interval push and final push)", errCount)
	}
	if stats := client.Stats(); stats.PushFailures < 2 {
		t.Errorf("PushFailures = %d, want >= 2", stats.PushFailures)
	}
}

// A Stop whose deadline is spent abandons the push in flight and does not
// start another. A later Stop still flushes, so a caller that budgets too
// little time loses nothing it retries for.
func TestStopWithExpiredContextAbandonsTheInFlightPush(t *testing.T) {
	var requests atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client, err := New(Config{
		DSN:                   srv.URL,
		IngestionKey:          "k",
		Job:                   "j",
		Instance:              "i",
		Interval:              time.Hour,
		Timeout:               time.Hour,
		DisableRuntimeMetrics: true,
	}, NewRegistry())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The worker is now blocked in its first push, so Stop cannot complete.
	deadline := time.Now().Add(5 * time.Second)
	for requests.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := stop(expired); err == nil {
		t.Error("stop(expired) = nil, want the context error")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("%d requests, want 1: an expired Stop must not start a push", got)
	}

	close(release)
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("%d requests, want 2: the retried Stop must flush", got)
	}
}

func TestPushesNeverOverlap(t *testing.T) {
	var inFlight, maxInFlight atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		for {
			cur := maxInFlight.Load()
			if n <= cur || maxInFlight.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inFlight.Add(-1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client, err := New(Config{
		DSN:                   srv.URL,
		IngestionKey:          "k",
		Job:                   "j",
		Instance:              "i",
		Interval:              5 * time.Millisecond, // much shorter than the server delay
		DisableRuntimeMetrics: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Manual pushes racing the worker must serialize too.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.Push(context.Background())
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := maxInFlight.Load(); got > 1 {
		t.Errorf("max in-flight pushes = %d, want 1 (serialized)", got)
	}
}

func TestParentContextCancelStopsWorker(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.Interval = 10 * time.Millisecond
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop, err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for cs.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop after cancel: %v", err)
	}
	quiesced := cs.count()
	time.Sleep(60 * time.Millisecond)
	if got := cs.count(); got != quiesced {
		t.Errorf("pushes continued after parent cancel: %d -> %d", quiesced, got)
	}
}

func TestStopWaitsAndIsIdempotent(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.Interval = time.Hour
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	// After a stop the client may be started again.
	stop2, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("restart after stop: %v", err)
	}
	if err := stop2(context.Background()); err != nil {
		t.Fatalf("stop after restart: %v", err)
	}
}

func TestStopHonorsItsContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(func() { srv.Close() })

	client, err := New(Config{
		DSN:                   srv.URL,
		IngestionKey:          "k",
		Job:                   "j",
		Instance:              "i",
		Interval:              time.Hour,
		Timeout:               time.Hour,
		DisableRuntimeMetrics: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	// The worker is blocked in a push, so a spent deadline returns without
	// waiting for it.
	if err := stop(expired); err == nil {
		t.Error("stop(expired) = nil, want the context error")
	}
	close(release)
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestDoubleStartErrors(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.Interval = time.Hour
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = stop(context.Background()) }()
	if _, err := client.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Errorf("second Start = %v, want ErrAlreadyStarted", err)
	}
}

func TestOnErrorReceivesFailuresAndWorkerSurvives(t *testing.T) {
	cs := newCaptureServer(t)
	cs.setStatus(http.StatusInternalServerError)

	var mu sync.Mutex
	var received []error
	cfg := testConfig(cs)
	cfg.Interval = 10 * time.Millisecond
	cfg.OnError = func(err error) {
		mu.Lock()
		received = append(received, err)
		mu.Unlock()
		panic("OnError panics must not kill the worker")
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) < 2 {
		t.Fatalf("OnError called %d times, want >= 2 (worker survives failures)", len(received))
	}
	var statusErr *StatusError
	if !errors.As(received[0], &statusErr) || statusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("OnError error = %v, want *StatusError{500}", received[0])
	}
}

func TestSourceRenderingIsBounded(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.MaxBodyBytes = 128
	client, err := New(cfg, textSource(strings.Repeat("oversized_metric 1\n", 20)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	var limitErr *LimitError
	if !errors.As(pushErr, &limitErr) || limitErr.Limit != limitMaxBodyBytes {
		t.Fatalf("Push error = %v, want MaxBodyBytes LimitError", pushErr)
	}
	if cs.count() != 0 {
		t.Fatalf("requests = %d, want 0", cs.count())
	}
}

func TestConfigHeadersAreCloned(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.Headers = http.Header{"X-Custom": []string{"original"}}
	client, err := New(cfg, textSource("clone_test 1\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg.Headers.Set("X-Custom", "mutated")
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := cs.last().custom; got != "original" {
		t.Fatalf("X-Custom = %q, want original", got)
	}
}

func TestCrossOriginRedirectDoesNotForwardKey(t *testing.T) {
	var receivedKey atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		receivedKey.Store(req.Header.Get("apikey") != "")
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		http.Redirect(writer, req, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	client, err := New(Config{
		Endpoint:              origin.URL,
		IngestionKey:          "redirect-secret",
		DisableRuntimeMetrics: true,
	}, textSource("redirect_test 1\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err == nil {
		t.Fatal("Push unexpectedly followed cross-origin redirect")
	}
	if receivedKey.Load() {
		t.Fatal("redirect target received ingestion key")
	}
}

// A gateway redirecting an http endpoint to TLS on the same host is the common
// on-prem setup and must keep working.
func TestSameHostTLSUpgradeRedirectIsAllowed(t *testing.T) {
	for _, tc := range []struct {
		name        string
		from, to    string
		wantAllowed bool
	}{
		{"identical", "https://host/path", "https://host/other", true},
		{"tls upgrade", "http://host/path", "https://host/path", true},
		{"tls downgrade", "https://host/path", "http://host/path", false},
		{"other host", "https://host/path", "https://elsewhere/path", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, err := url.Parse(tc.from)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.from, err)
			}
			to, err := url.Parse(tc.to)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.to, err)
			}
			if got := sameOrigin(from, to); got != tc.wantAllowed {
				t.Fatalf("sameOrigin(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.wantAllowed)
			}
		})
	}
}

func TestStatusErrorRedactsEchoedKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte("rejected secret-key"))
	}))
	defer server.Close()
	client, err := New(Config{
		Endpoint:              server.URL,
		IngestionKey:          "secret-key",
		DisableRuntimeMetrics: true,
	}, textSource("redaction_test 1\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	if pushErr == nil || strings.Contains(pushErr.Error(), "secret-key") {
		t.Fatalf("Push error was not redacted: %v", pushErr)
	}
	if strings.Contains(client.Stats().LastError, "secret-key") {
		t.Fatalf("LastError was not redacted: %q", client.Stats().LastError)
	}
}
