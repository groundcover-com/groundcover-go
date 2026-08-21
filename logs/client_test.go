package logs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/logs"
)

type request struct {
	path        string
	contentType string
	encoding    string
	auth        string
	body        []byte
}

func TestStandaloneClientRoundTrip(t *testing.T) {
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		requests <- request{
			path:        req.URL.Path,
			contentType: req.Header.Get("Content-Type"),
			encoding:    req.Header.Get("Content-Encoding"),
			auth:        req.Header.Get("Authorization"),
			body:        body,
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := logs.New(logs.Config{
		DSN:          server.URL,
		IngestionKey: "logs-key",
		ServiceName:  "checkout",
		Env:          "production",
		Cluster:      "cluster-a",
		Source:       "my-source",
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close(context.Background()) }()

	client.Info(context.Background(), "order created", logs.Attributes{
		"order_id": "o-9",
		"nested":   map[string]any{"count": 2},
	})
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	req := <-requests
	if req.path != "/json/logs" || req.contentType != "application/x-ndjson" || req.encoding != "" {
		t.Fatalf("request path=%q content-type=%q encoding=%q", req.path, req.contentType, req.encoding)
	}
	if req.auth != "Bearer logs-key" {
		t.Fatalf("authorization = %q", req.auth)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(req.body), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	for key, want := range map[string]any{
		"msg": "order created", "level": "info", "order_id": "o-9",
		"nested.count": float64(2), "service": "checkout", "env": "production",
		"cluster": "cluster-a", "source": "my-source",
	} {
		if got := record[key]; got != want {
			t.Fatalf("record[%q] = %#v, want %#v", key, got, want)
		}
	}
	if stats := client.Stats(); stats.Captured != 1 || stats.Sent != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestStandaloneBeforeSendAndDisabled(t *testing.T) {
	disabled, err := logs.New(logs.Config{Disabled: true})
	if err != nil {
		t.Fatalf("disabled New: %v", err)
	}
	disabled.Info(context.Background(), "ignored")
	if stats := disabled.Stats(); stats.Captured != 0 {
		t.Fatalf("disabled stats = %+v", stats)
	}

	client, err := logs.New(logs.Config{
		DSN: "https://example.invalid",
		BeforeSend: func(record *logs.Record) *logs.Record {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	client.Error(context.Background(), "dropped")
	if stats := client.Stats(); stats.DroppedBeforeSend != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestStandaloneSanitizesNonFiniteAndRejectsOversizedRecords(t *testing.T) {
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		bodies = append(bodies, body)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := logs.New(logs.Config{
		DSN:           server.URL,
		HTTPClient:    server.Client(),
		MaxBytes:      1024,
		MaxBatchBytes: 1024,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.Info(context.Background(), "finite batch", logs.Attributes{"value": math.NaN()})
	client.Info(context.Background(), strings.Repeat(`"`, 2000))
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(bodies) != 1 {
		t.Fatalf("requests = %d, want 1", len(bodies))
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(bodies[0]), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record["value"] != "NaN" {
		t.Fatalf("value = %#v, want NaN string", record["value"])
	}
	stats := client.Stats()
	if stats.Captured != 1 || stats.Sent != 1 || stats.DroppedOverflow != 1 {
		t.Fatalf("stats = %+v", stats)
	}

	client.Info(context.Background(), "after close")
	if stats := client.Stats(); stats.Captured != 1 || stats.QueuePendingItems != 0 {
		t.Fatalf("post-close stats = %+v", stats)
	}
}

// A DSN that cannot produce a usable URL is a configuration error, reported at
// construction rather than as a delivery failure for the life of the process.
func TestStandaloneRejectsUnusableDSN(t *testing.T) {
	if _, err := logs.New(logs.Config{}); !errors.Is(err, logs.ErrMissingDSN) {
		t.Fatalf("New(no DSN) = %v, want ErrMissingDSN", err)
	}
	for _, dsn := range []string{"ftp://host.example", "https://", "://host.example"} {
		if _, err := logs.New(logs.Config{DSN: dsn}); err == nil {
			t.Errorf("New(DSN=%q) = nil error, want a configuration error", dsn)
		}
	}
	for _, dsn := range []string{"host.example", "localhost:8080", "HTTPS://host.example"} {
		client, err := logs.New(logs.Config{DSN: dsn})
		if err != nil {
			t.Errorf("New(DSN=%q) = %v, want nil", dsn, err)
			continue
		}
		_ = client.Close(context.Background())
	}
	// A disabled client needs no endpoint at all.
	if _, err := logs.New(logs.Config{Disabled: true}); err != nil {
		t.Errorf("New(Disabled) = %v, want nil", err)
	}
}

// A flush that gives up early must leave its records on the way to the intake,
// not destroy them: a too-short shutdown budget is a common mistake and it must
// not cost telemetry.
func TestStandaloneShortFlushDoesNotLoseRecords(t *testing.T) {
	var delivered atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		time.Sleep(300 * time.Millisecond)
		delivered.Add(int64(bytes.Count(bytes.TrimSpace(body), []byte("\n")) + 1))
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := logs.New(logs.Config{
		DSN:           server.URL,
		HTTPClient:    server.Client(),
		FlushInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for range 5 {
		client.Info(context.Background(), "record")
	}
	if err := client.FlushTimeout(20 * time.Millisecond); err == nil {
		t.Fatal("expected the short flush to report its deadline")
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := delivered.Load(); got != 5 {
		t.Fatalf("delivered = %d, want 5", got)
	}
	if stats := client.Stats(); stats.Sent != 5 || stats.DroppedSendExhausted != 0 {
		t.Fatalf("stats = %+v, want 5 sent and nothing dropped", stats)
	}
}

// A cyclic attribute must not take the host process down, and the rest of the
// record must still be delivered.
func TestStandaloneCyclicAttributeIsDeliveredSafely(t *testing.T) {
	var bodies [][]byte
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := logs.New(logs.Config{DSN: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cyclic := map[string]any{"name": "root"}
	cyclic["self"] = cyclic
	client.Info(context.Background(), "cyclic", logs.Attributes{"loop": cyclic, "ok": "kept"})
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("requests = %d, want the record delivered", len(bodies))
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(bodies[0]), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record["ok"] != "kept" {
		t.Fatalf("healthy attribute missing: %#v", record)
	}
}

// A BeforeSend callback that re-enters the client (a common accident when the
// callback logs) must not deadlock against a concurrent Close.
func TestStandaloneBeforeSendReentrancyDoesNotDeadlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var client *logs.Client
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	newClient, err := logs.New(logs.Config{
		DSN:        server.URL,
		HTTPClient: server.Client(),
		BeforeSend: func(record *logs.Record) *logs.Record {
			once.Do(func() {
				close(entered)
				<-release
				// Give Close time to block on the lifecycle barrier before
				// re-entering, so the deadlock is deterministic if the barrier
				// is ever held across this callback again.
				time.Sleep(100 * time.Millisecond)
				client.Info(context.Background(), "re-entrant")
			})
			return record
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client = newClient

	go client.Info(context.Background(), "outer")
	<-entered

	closed := make(chan error, 1)
	go func() {
		// Close blocks on the lifecycle barrier while BeforeSend is running.
		close(release)
		closed <- client.Close(context.Background())
	}()

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close deadlocked against a re-entrant BeforeSend")
	}
}
