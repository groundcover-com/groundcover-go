package gcslog_test

import (
	"compress/gzip"
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

// capture is an httptest handler that decompresses and stores ingestion bodies.
type capture struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(zr)
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

type wirePayload struct {
	Events []struct {
		Type       string `json:"type"`
		Level      string `json:"level"`
		Attributes struct {
			Message     string         `json:"message"`
			LogMetadata map[string]any `json:"log_metadata"`
		} `json:"attributes"`
	} `json:"events"`
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

func decodeAll(t *testing.T, sink *capture) wirePayload {
	t.Helper()
	bodies := sink.all()
	if len(bodies) == 0 {
		t.Fatal("no body was delivered")
	}
	var merged wirePayload
	for _, b := range bodies {
		var p wirePayload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		merged.Events = append(merged.Events, p.Events...)
	}
	return merged
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

	p := decodeAll(t, sink)
	if len(p.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(p.Events))
	}
	info := p.Events[0]
	if info.Type != "log" || info.Level != "info" || info.Attributes.Message != "order created" {
		t.Fatalf("unexpected info record: %+v", info)
	}
	md := info.Attributes.LogMetadata
	if md["order_id"] != "o-9" || md["qty"] != float64(3) {
		t.Fatalf("attrs wrong: %+v", md)
	}
	warn := p.Events[1]
	if warn.Level != "warning" {
		t.Fatalf("slog warn must map to warning, got %q", warn.Level)
	}
	if warn.Attributes.LogMetadata["http.status"] != float64(429) {
		t.Fatalf("group attr not dotted: %+v", warn.Attributes.LogMetadata)
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

	p := decodeAll(t, sink)
	md := p.Events[0].Attributes.LogMetadata
	if md["service_tier"] != "gold" {
		t.Fatalf("pre-group attr lost: %+v", md)
	}
	if md["req.method"] != "GET" || md["req.path"] != "/checkout" {
		t.Fatalf("grouped attrs wrong: %+v", md)
	}
	if p.Events[0].Level != "error" {
		t.Fatalf("level = %q", p.Events[0].Level)
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

	p := decodeAll(t, sink)
	if len(p.Events) != 1 || p.Events[0].Attributes.Message != "kept" {
		t.Fatalf("expected only the warn record, got %+v", p.Events)
	}
}

func TestHandlerGlobalClientBeforeInitIsNoOp(t *testing.T) {
	// A handler with no explicit client uses the package-level client, which is
	// a no-op before Init: logging must be safe and silent.
	logger := slog.New(gcslog.NewHandler(gcslog.Options{}))
	logger.Info("goes nowhere")
}
