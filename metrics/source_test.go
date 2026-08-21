package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNamedAttributionInErrors(t *testing.T) {
	cs := newCaptureServer(t)
	client, err := New(testConfig(cs),
		Named("application", textSource("ok_metric 1\nbroken line!!!\n")),
		Named("cache", panicSource{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	if pushErr == nil {
		t.Fatal("Push = nil, want aggregate error")
	}
	msg := pushErr.Error()
	if !strings.Contains(msg, `metric source "application": malformed sample at line 2`) {
		t.Errorf("error missing application attribution: %v", msg)
	}
	if !strings.Contains(msg, `metric source "cache": panic while rendering`) {
		t.Errorf("error missing cache attribution: %v", msg)
	}
}

func TestNamedDelegatesRendering(t *testing.T) {
	cs := newCaptureServer(t)
	reg := NewRegistry()
	reg.Gauge("wrapped_metric", nil).Set(9)
	client, err := New(testConfig(cs), Named("application", reg))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if body := cs.last().body; !strings.Contains(body, "wrapped_metric") {
		t.Errorf("wrapped source not rendered:\n%s", body)
	}
}

func TestAggregateErrorsWithHealthyDelivery(t *testing.T) {
	cs := newCaptureServer(t)
	client, err := New(testConfig(cs),
		Named("cache", panicSource{}),
		Named("application", textSource("!!!bad\n")),
		textSource("healthy_metric 42\n"), // unnamed → source[2]
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	if pushErr == nil {
		t.Fatal("Push = nil, want aggregate error")
	}
	// errors.Join-compatible: both child errors are discoverable.
	msg := pushErr.Error()
	for _, wantSub := range []string{
		`metric source "cache": panic while rendering`,
		`metric source "application": malformed sample at line 1`,
	} {
		if !strings.Contains(msg, wantSub) {
			t.Errorf("aggregate error missing %q: %v", wantSub, msg)
		}
	}
	// The healthy source still got delivered.
	if body := cs.last().body; !strings.Contains(body, "healthy_metric") {
		t.Errorf("healthy source not delivered:\n%s", body)
	}
	stats := client.Stats()
	if stats.PartialPushes != 1 || stats.PushFailures != 0 {
		t.Errorf("partial accounting wrong: %+v", stats)
	}
	if stats.SourceFailures != 2 {
		t.Errorf("SourceFailures = %d, want 2", stats.SourceFailures)
	}
	// The delivery succeeded, so LastSuccess is set even though the push was
	// partial; the aggregate message is preserved in LastError.
	if stats.LastError == "" || stats.LastSuccess.IsZero() {
		t.Errorf("LastError/LastSuccess wrong: %+v", stats)
	}
}

func TestDuplicateSeriesDefaultFirstWins(t *testing.T) {
	cs := newCaptureServer(t)
	client, err := New(testConfig(cs),
		textSource("dup_metric 1\n"),
		textSource("dup_metric 2\n"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	var dupErr *DuplicateSeriesError
	if !errors.As(pushErr, &dupErr) {
		t.Fatalf("Push error = %v, want *DuplicateSeriesError", pushErr)
	}
	if dupErr.FirstSource != "source[0]" || dupErr.DuplicateSource != "source[1]" {
		t.Errorf("sources = %q/%q, want source[0]/source[1]", dupErr.FirstSource, dupErr.DuplicateSource)
	}
	if !strings.Contains(dupErr.Series, "dup_metric") {
		t.Errorf("Series = %q, want the series identity", dupErr.Series)
	}

	// First occurrence kept, duplicate dropped, delivery happened.
	body := cs.last().body
	if !strings.Contains(body, "} 1\n") || strings.Contains(body, "} 2\n") {
		t.Errorf("first-wins violated:\n%s", body)
	}
	stats := client.Stats()
	if stats.DuplicateSeries != 1 {
		t.Errorf("DuplicateSeries = %d, want 1", stats.DuplicateSeries)
	}
	if stats.PartialPushes != 1 || stats.PushFailures != 0 {
		t.Errorf("duplicate must be a partial push: %+v", stats)
	}
	if stats.SeriesLastPush != 1 {
		t.Errorf("SeriesLastPush = %d, want 1 (duplicate dropped)", stats.SeriesLastPush)
	}
}

func TestDuplicateSeriesIdentityIsPostInjection(t *testing.T) {
	// The two sources emit distinct label sets that collapse into the same
	// final series once the fixed job label replaces the source job label.
	cs := newCaptureServer(t)
	client, err := New(testConfig(cs),
		textSource("m{job=\"a\"} 1\n"),
		textSource("m{job=\"b\"} 2\n"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	var dupErr *DuplicateSeriesError
	if !errors.As(pushErr, &dupErr) {
		t.Fatalf("Push error = %v, want *DuplicateSeriesError after injection collapse", pushErr)
	}
	if got := client.Stats().LabelOverrides; got != 2 {
		t.Errorf("LabelOverrides = %d, want 2", got)
	}
}

func TestDuplicateSeriesStrictMode(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.FailOnDuplicateSeries = true
	client, err := New(cfg,
		Named("first", textSource("dup_metric 1\n")),
		Named("second", textSource("dup_metric 2\n")),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pushErr := client.Push(context.Background())
	var dupErr *DuplicateSeriesError
	if !errors.As(pushErr, &dupErr) {
		t.Fatalf("Push error = %v, want *DuplicateSeriesError", pushErr)
	}
	if dupErr.FirstSource != "first" || dupErr.DuplicateSource != "second" {
		t.Errorf("sources = %q/%q, want first/second", dupErr.FirstSource, dupErr.DuplicateSource)
	}
	if cs.count() != 0 {
		t.Error("strict mode must fail the whole push before delivery")
	}
	stats := client.Stats()
	if stats.PushFailures != 1 || stats.PartialPushes != 0 {
		t.Errorf("strict duplicate must be a push failure: %+v", stats)
	}
	if stats.DuplicateSeries != 1 {
		t.Errorf("DuplicateSeries = %d, want 1", stats.DuplicateSeries)
	}
}

func TestEndpointOverride(t *testing.T) {
	var gotPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client, err := New(Config{
		Endpoint:              srv.URL + "/custom/gateway/path",
		IngestionKey:          "k",
		Job:                   "j",
		Instance:              "i",
		DisableRuntimeMetrics: true,
	}, textSource("m 1\n"))
	if err != nil {
		t.Fatalf("New with Endpoint only: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got, _ := gotPath.Load().(string); got != "/custom/gateway/path" {
		t.Errorf("path = %q, want the Endpoint override path", got)
	}
}

func TestDSNAndEndpointMutuallyExclusive(t *testing.T) {
	_, err := New(Config{DSN: "https://a.example.com", Endpoint: "https://b.example.com/push"})
	if !errors.Is(err, ErrDSNAndEndpoint) {
		t.Errorf("New = %v, want ErrDSNAndEndpoint", err)
	}
	// Disabled clients still reject the ambiguous config.
	_, err = New(Config{Disabled: true, DSN: "a", Endpoint: "b"})
	if !errors.Is(err, ErrDSNAndEndpoint) {
		t.Errorf("New(Disabled) = %v, want ErrDSNAndEndpoint", err)
	}
}

func TestWorkerRunningStat(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.Interval = time.Hour
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.Stats().WorkerRunning {
		t.Error("WorkerRunning = true before Start")
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !client.Stats().WorkerRunning {
		t.Error("WorkerRunning = false while started")
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if client.Stats().WorkerRunning {
		t.Error("WorkerRunning = true after stop")
	}
}

func TestDefaultClientUsesDefaultTransport(t *testing.T) {
	cs := newCaptureServer(t)
	client, err := New(testConfig(cs))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A nil Transport means net/http falls back to http.DefaultTransport,
	// whose Proxy is ProxyFromEnvironment — proxy env vars are honored and
	// nothing (protocol, proxy) is forced by the SDK.
	if client.httpClient.Transport != nil {
		t.Errorf("default client Transport = %T, want nil (http.DefaultTransport)", client.httpClient.Transport)
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is %T", http.DefaultTransport)
	}
	if defaultTransport.Proxy == nil {
		t.Error("http.DefaultTransport.Proxy = nil; proxy env vars would be ignored")
	}
}

func TestPushThroughForwardProxy(t *testing.T) {
	// A minimal HTTP forward proxy: it receives the absolute-form request
	// (the same path a proxy env var would produce for an http:// endpoint)
	// and forwards it to the upstream intake.
	cs := newCaptureServer(t)
	var proxied atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			t.Errorf("proxy got non-absolute URL %q; not a proxy-form request", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		proxied.Add(1)
		upstream, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		upstream.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(upstream)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.WriteHeader(resp.StatusCode)
	}))
	t.Cleanup(proxy.Close)

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	cfg := testConfig(cs)
	cfg.HTTPClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}
	client, err := New(cfg, textSource("proxied_metric 1\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push through proxy: %v", err)
	}
	if proxied.Load() != 1 {
		t.Errorf("proxy handled %d requests, want 1", proxied.Load())
	}
	if body := cs.last().body; !strings.Contains(body, "proxied_metric") {
		t.Errorf("upstream did not receive the pushed body:\n%s", body)
	}
}
