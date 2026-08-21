package groundcover

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/testutil"
)

// decodeLogPayload unmarshals the last NDJSON batch body the mock sender
// received.
func decodeLogPayload(t *testing.T, sender *testutil.MockSender) []map[string]any {
	t.Helper()
	bodies := sender.Bodies()
	if len(bodies) == 0 {
		t.Fatal("no body was sent")
	}
	lines := bytes.Split(bytes.TrimSpace(bodies[len(bodies)-1]), []byte{'\n'})
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("unmarshal NDJSON line: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func TestEmitLogRoundTrip(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{ServiceName: "checkout", Env: "prod", Release: "1.2.3", EnableLogs: true}, sender)

	ctx := c.SetUser(context.Background(), User{ID: "u-1", Organization: "acme"})
	c.LogInfo(ctx, "order created", Attributes{
		"order_id": "o-9",
		"amount":   42.5,
		"nested":   map[string]any{"a": map[string]any{"b": 1}},
	})
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	records := decodeLogPayload(t, sender)
	if len(records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(records))
	}
	record := records[0]
	if record["msg"] != "order created" {
		t.Fatalf("msg = %v", record["msg"])
	}
	if record["level"] != "info" || record["severity_number"] != float64(9) {
		t.Fatalf("severity wrong: level=%v severity_number=%v", record["level"], record["severity_number"])
	}
	if record["order_id"] != "o-9" || record["amount"] != 42.5 {
		t.Fatalf("custom attrs wrong: %+v", record)
	}
	if record["nested.a.b"] != float64(1) {
		t.Fatalf("nested map not flattened to dotted key: %+v", record)
	}
	if record["user.id"] != "u-1" || record["user.organization"] != "acme" {
		t.Fatalf("identity not in attributes: %+v", record)
	}
	if record["telemetry.sdk.name"] != sdkName {
		t.Fatalf("resource attr missing: %+v", record)
	}
	if _, ok := record["timestamp"].(string); !ok {
		t.Fatalf("timestamp must be an RFC3339 string: %+v", record)
	}
	if s := c.LogStats(); s.Captured != 1 || s.Sent != 1 {
		t.Fatalf("log stats wrong: %+v", s)
	}
	if s := c.Stats(); s.Captured != 0 {
		t.Fatalf("event stats must be untouched by logs: %+v", s)
	}
}

func TestLogIntakeRequestContract(t *testing.T) {
	type capturedRequest struct {
		path          string
		contentType   string
		encoding      string
		auth          string
		serviceHeader string
		envHeader     string
		clusterHeader string
		sourceHeader  string
		body          []byte
	}
	got := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- capturedRequest{
			path:          r.URL.Path,
			contentType:   r.Header.Get("Content-Type"),
			encoding:      r.Header.Get("Content-Encoding"),
			auth:          r.Header.Get("Authorization"),
			serviceHeader: r.Header.Get("x-groundcover-service-name"),
			envHeader:     r.Header.Get("x-groundcover-env-name"),
			clusterHeader: r.Header.Get("x-groundcover-cluster"),
			sourceHeader:  r.Header.Get("x-groundcover-source"),
			body:          body,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{
		DSN:              srv.URL,
		IngestionKey:     "rum-key",
		LogsIngestionKey: "logs-key",
		ServiceName:      "checkout",
		Env:              "production",
		Cluster:          "cluster-a",
		LogSource:        "my-source",
		EnableLogs:       true,
		HTTPClient:       srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close(context.Background()) }()

	c.LogInfo(context.Background(), "log message", Attributes{"order_id": "o-9"})
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	req := <-got
	if req.path != "/json/logs" {
		t.Fatalf("path = %q", req.path)
	}
	if req.contentType != "application/x-ndjson" || req.encoding != "" {
		t.Fatalf("content-type=%q content-encoding=%q", req.contentType, req.encoding)
	}
	if req.auth != "Bearer logs-key" {
		t.Fatalf("authorization = %q", req.auth)
	}
	if req.serviceHeader != "" || req.envHeader != "" || req.clusterHeader != "" || req.sourceHeader != "" {
		t.Fatalf("resource metadata must not use headers: service=%q env=%q cluster=%q source=%q",
			req.serviceHeader, req.envHeader, req.clusterHeader, req.sourceHeader)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(req.body), &record); err != nil {
		t.Fatalf("decode NDJSON: %v", err)
	}
	if record["msg"] != "log message" || record["order_id"] != "o-9" {
		t.Fatalf("record = %+v", record)
	}
	if record["service"] != "checkout" || record["env"] != "production" ||
		record["cluster"] != "cluster-a" || record["source"] != "my-source" {
		t.Fatalf("resource fields = %+v", record)
	}
}

func TestLogsDisabledByDefault(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	c.LogInfo(context.Background(), "should be dropped")
	c.EmitLog(context.Background(), LevelError, "also dropped")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if sender.Calls() != 0 {
		t.Fatalf("expected no delivery, got %d calls", sender.Calls())
	}
	if s := c.LogStats(); s.Captured != 0 {
		t.Fatalf("expected zero captured, stats=%+v", s)
	}
}

func TestEmitLogInvalidLevelDefaultsToInfo(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{EnableLogs: true}, sender)

	c.EmitLog(context.Background(), Level("bogus"), "msg")
	_ = c.Flush(context.Background())

	if got := decodeLogPayload(t, sender)[0]["level"]; got != "info" {
		t.Fatalf("level = %v, want info", got)
	}
}

func TestLogWarnMapsToWireWarn(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{EnableLogs: true}, sender)

	c.LogWarn(context.Background(), "w")
	_ = c.Flush(context.Background())

	attrs := decodeLogPayload(t, sender)[0]
	// The RUM log contract uses "warn"; the OTel-style severity number stays 13.
	if attrs["level"] != "warn" || attrs["severity_number"] != float64(13) {
		t.Fatalf("level=%v severity_number=%v", attrs["level"], attrs["severity_number"])
	}
}

func TestLogReservedKeysNotOverridable(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{EnableLogs: true}, sender)

	c.LogInfo(context.Background(), "real message", Attributes{
		"message": "spoofed",
		"level":   "fatal",
		"user.id": "spoofed-id",
	})
	_ = c.Flush(context.Background())

	attrs := decodeLogPayload(t, sender)[0]
	if attrs["msg"] != "real message" || attrs["level"] != "info" {
		t.Fatalf("reserved keys overridden: %+v", attrs)
	}
	if _, ok := attrs["user.id"]; ok {
		t.Fatalf("caller-injected identity must be dropped: %+v", attrs)
	}
}

func TestLogScopeAndPerCallPrecedence(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{EnableLogs: true}, sender)

	ctx := c.WithScope(context.Background(), func(s *Scope) {
		s.SetAttribute("key", "from-scope")
		s.SetAttribute("only-scope", "x")
	})
	c.LogWarn(ctx, "w", Attributes{"key": "from-call"})
	_ = c.Flush(context.Background())

	attrs := decodeLogPayload(t, sender)[0]
	if attrs["key"] != "from-call" {
		t.Fatalf("per-call attr must win, got %v", attrs["key"])
	}
	if attrs["only-scope"] != "x" {
		t.Fatalf("scope attribute lost: %v", attrs["only-scope"])
	}
}

func TestBeforeSendLog(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{
		EnableLogs: true,
		BeforeSendLog: func(l *Log) *Log {
			if l.Level == LevelDebug {
				return nil // sample out debug records
			}
			l.Message = "scrubbed"
			return l
		},
	}, sender)

	c.LogDebug(context.Background(), "noisy")
	c.LogError(context.Background(), "secret payload")
	_ = c.Flush(context.Background())

	records := decodeLogPayload(t, sender)
	if len(records) != 1 {
		t.Fatalf("expected 1 record after sampling, got %d", len(records))
	}
	if records[0]["msg"] != "scrubbed" {
		t.Fatalf("msg = %v, want scrubbed", records[0]["msg"])
	}
	if s := c.LogStats(); s.DroppedBeforeSend != 1 {
		t.Fatalf("expected 1 before_send drop, stats=%+v", s)
	}
}

func TestLogOverflowDoesNotAffectEvents(t *testing.T) {
	sender := &testutil.MockSender{}
	// A tiny log queue that cannot drain (interval is huge, batch size larger
	// than the queue), so pushes overflow deterministically.
	c := mustClient(t, Config{
		EnableLogs:    true,
		MaxQueue:      2,
		FlushInterval: time.Hour,
		BatchSize:     100,
	}, sender)

	for range 5 {
		c.LogInfo(context.Background(), "spam")
	}
	if s := c.LogStats(); s.DroppedOverflow != 3 {
		t.Fatalf("expected 3 overflow drops, stats=%+v", s)
	}
	// The error pipeline has its own (equally tiny) budget, untouched by logs.
	if s := c.Stats(); s.DroppedOverflow != 0 {
		t.Fatalf("event pipeline must be unaffected, stats=%+v", s)
	}
}

func TestLogfFormatting(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{EnableLogs: true}, sender)

	c.LogErrorf(context.Background(), "charge %s failed after %d tries", "ch_1", 3)
	_ = c.Flush(context.Background())

	attrs := decodeLogPayload(t, sender)[0]
	if attrs["msg"] != "charge ch_1 failed after 3 tries" {
		t.Fatalf("msg = %v", attrs["msg"])
	}
	if attrs["level"] != string(LevelError) {
		t.Fatalf("level = %v", attrs["level"])
	}
}

func TestLogIdentityHashed(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{
		EnableLogs: true,
		Hasher:     NewHMACHasher([]byte("k")),
	}, sender)

	ctx := c.SetUser(context.Background(), User{ID: "raw-id", Email: "a@b.c"})
	c.LogInfo(ctx, "m")
	_ = c.Flush(context.Background())

	attrs := decodeLogPayload(t, sender)[0]
	if attrs["user.id"] == "raw-id" || attrs["user.email"] == "a@b.c" {
		t.Fatalf("identity not pseudonymized: %+v", attrs)
	}
	if attrs["user.id"] == "" {
		t.Fatal("hashed user.id must not be empty")
	}
}

func TestGlobalLogAPIBeforeInitIsNoOp(t *testing.T) {
	// Must not panic and must not touch the global.
	LogInfo(context.Background(), "before init")
	LogErrorf(context.Background(), "before init %d", 1)
	EmitLog(context.Background(), LevelWarning, "w")
	if s := GlobalLogStats(); s.Captured != 0 {
		t.Fatalf("no-op client must not capture, stats=%+v", s)
	}
}
