package groundcover

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/testutil"
)

// decodeLogPayload unmarshals the last batch body the mock sender received as a
// log payload.
func decodeLogPayload(t *testing.T, sender *testutil.MockSender) wireLogPayload {
	t.Helper()
	bodies := sender.Bodies()
	if len(bodies) == 0 {
		t.Fatal("no body was sent")
	}
	var p wireLogPayload
	if err := json.Unmarshal(bodies[len(bodies)-1], &p); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return p
}

func TestEmitLogRoundTrip(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{ServiceName: "checkout", Env: "prod", Release: "1.2.3", EnableLogs: true}, sender)

	ctx := c.SetUser(context.Background(), User{ID: "u-1", Organization: "acme"})
	c.LogInfo(ctx, "order created", Attributes{
		"order_id": "o-9",
		"amount":   42.5,
	})
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	p := decodeLogPayload(t, sender)
	if p.SessionAttributes["service.name"] != "checkout" {
		t.Fatalf("service.name = %v", p.SessionAttributes["service.name"])
	}
	if len(p.Events) != 1 {
		t.Fatalf("expected 1 log event, got %d", len(p.Events))
	}
	ev := p.Events[0]
	if ev.Type != "log" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Level != "info" {
		t.Fatalf("level = %q", ev.Level)
	}
	if ev.Attributes.Message != "order created" {
		t.Fatalf("message = %q", ev.Attributes.Message)
	}
	md := ev.Attributes.LogMetadata
	if md["order_id"] != "o-9" || md["amount"] != 42.5 {
		t.Fatalf("custom attrs wrong: %+v", md)
	}
	if md["user.id"] != "u-1" || md["user.organization"] != "acme" {
		t.Fatalf("identity not in log_metadata: %+v", md)
	}
	if md["level"] != "info" || md["severity_number"] != float64(9) {
		t.Fatalf("severity wrong: level=%v severity_number=%v", md["level"], md["severity_number"])
	}
	if md["telemetry.sdk.name"] != sdkName {
		t.Fatalf("resource attr missing: %+v", md)
	}
	if s := c.LogStats(); s.Captured != 1 || s.Sent != 1 {
		t.Fatalf("log stats wrong: %+v", s)
	}
	if s := c.Stats(); s.Captured != 0 {
		t.Fatalf("event stats must be untouched by logs: %+v", s)
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

	if got := decodeLogPayload(t, sender).Events[0].Level; got != "info" {
		t.Fatalf("level = %q, want info", got)
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

	md := decodeLogPayload(t, sender).Events[0].Attributes.LogMetadata
	if md["key"] != "from-call" {
		t.Fatalf("per-call attr must win, got %v", md["key"])
	}
	if md["only-scope"] != "x" {
		t.Fatalf("scope attribute lost: %v", md["only-scope"])
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

	p := decodeLogPayload(t, sender)
	if len(p.Events) != 1 {
		t.Fatalf("expected 1 event after sampling, got %d", len(p.Events))
	}
	if p.Events[0].Attributes.Message != "scrubbed" {
		t.Fatalf("message = %q, want scrubbed", p.Events[0].Attributes.Message)
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

	ev := decodeLogPayload(t, sender).Events[0]
	if ev.Attributes.Message != "charge ch_1 failed after 3 tries" {
		t.Fatalf("message = %q", ev.Attributes.Message)
	}
	if ev.Level != string(LevelError) {
		t.Fatalf("level = %q", ev.Level)
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

	md := decodeLogPayload(t, sender).Events[0].Attributes.LogMetadata
	if md["user.id"] == "raw-id" || md["user.email"] == "a@b.c" {
		t.Fatalf("identity not pseudonymized: %+v", md)
	}
	if md["user.id"] == "" {
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
