package gcslog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	groundcover "github.com/groundcover-com/groundcover-go"
	"github.com/groundcover-com/groundcover-go/gcslog"
)

// capture is an httptest handler that stores ingestion bodies.
type capture struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, body)
	c.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (c *capture) all() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.bodies))
	copy(out, c.bodies)
	return out
}

func newTestClient(t *testing.T, sink *capture) *groundcover.Client {
	t.Helper()
	srv := httptest.NewServer(sink)
	t.Cleanup(srv.Close)
	c, err := groundcover.New(groundcover.Config{
		DSN:        srv.URL,
		EnableLogs: true,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func decodeAll(t *testing.T, sink *capture) []map[string]any {
	t.Helper()
	bodies := sink.all()
	if len(bodies) == 0 {
		t.Fatal("no body was delivered")
	}
	var records []map[string]any
	for _, b := range bodies {
		for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatalf("unmarshal NDJSON line: %v", err)
			}
			records = append(records, record)
		}
	}
	return records
}

func TestHandlerForwardsRecords(t *testing.T) {
	sink := &capture{}
	client := newTestClient(t, sink)

	logger := slog.New(gcslog.NewHandler(gcslog.Options{Client: client}))
	logger.Info("order created", "order_id", "o-9", slog.Int("qty", 3))
	logger.Warn("rate limit near", slog.Group("http", slog.Int("status", 429)))

	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	records := decodeAll(t, sink)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	info := records[0]
	if info["level"] != "info" || info["msg"] != "order created" {
		t.Fatalf("unexpected info record: %+v", info)
	}
	if info["order_id"] != "o-9" || info["qty"] != float64(3) {
		t.Fatalf("attrs wrong: %+v", info)
	}
	warn := records[1]
	if warn["level"] != "warn" {
		t.Fatalf("slog warn must map to warn, got %v", warn["level"])
	}
	if warn["http.status"] != float64(429) {
		t.Fatalf("group attr not dotted: %+v", warn)
	}
}

func TestHandlerWithAttrsAndGroups(t *testing.T) {
	sink := &capture{}
	client := newTestClient(t, sink)

	logger := slog.New(gcslog.NewHandler(gcslog.Options{Client: client})).
		With("service_tier", "gold").
		WithGroup("req").
		With("method", "GET")
	logger.Error("boom", "path", "/checkout")

	_ = client.Flush(context.Background())

	records := decodeAll(t, sink)
	attrs := records[0]
	if attrs["service_tier"] != "gold" {
		t.Fatalf("pre-group attr lost: %+v", attrs)
	}
	if attrs["req.method"] != "GET" || attrs["req.path"] != "/checkout" {
		t.Fatalf("grouped attrs wrong: %+v", attrs)
	}
	if attrs["level"] != "error" {
		t.Fatalf("level = %v", attrs["level"])
	}
}

func TestHandlerMinimumLevel(t *testing.T) {
	sink := &capture{}
	client := newTestClient(t, sink)

	logger := slog.New(gcslog.NewHandler(gcslog.Options{Client: client, Level: slog.LevelWarn}))
	logger.Info("filtered out")
	logger.Debug("also filtered")
	logger.Warn("kept")

	_ = client.Flush(context.Background())

	records := decodeAll(t, sink)
	if len(records) != 1 || records[0]["msg"] != "kept" {
		t.Fatalf("expected only the warn record, got %+v", records)
	}
}

func TestHandlerGlobalClientBeforeInitIsNoOp(t *testing.T) {
	// A handler with no explicit client uses the package-level client, which is
	// a no-op before Init: logging must be safe and silent.
	logger := slog.New(gcslog.NewHandler(gcslog.Options{}))
	logger.Info("goes nowhere")
}
