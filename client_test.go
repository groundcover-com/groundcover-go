package groundcover

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/testutil"
)

func mustClient(t *testing.T, cfg Config, sender *testutil.MockSender) *Client {
	t.Helper()
	cfg.DSN = "https://example.invalid"
	if cfg.EnableLogs && cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			if err := sender.Send(req.Context(), body); err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(http.NoBody),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})}
	}
	c, err := newClient(cfg, sender)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// decodePayload unmarshals the single batch body the mock sender received.
func decodePayload(t *testing.T, sender *testutil.MockSender) wirePayload {
	t.Helper()
	bodies := sender.Bodies()
	if len(bodies) == 0 {
		t.Fatal("no body was sent")
	}
	var p wirePayload
	if err := json.Unmarshal(bodies[len(bodies)-1], &p); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return p
}

func TestCaptureErrorRoundTrip(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{ServiceName: "checkout", Env: "prod", Release: "1.2.3"}, sender)

	ctx := c.SetUser(context.Background(), User{ID: "u-1", Organization: "acme"})
	c.CaptureError(ctx, errors.New("charge failed"), WithAttributes(Attributes{
		"gc.test_id": "abc-123",
		"order_id":   "o-9",
		"amount":     42.5,
		"is_retry":   true,
	}))
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	p := decodePayload(t, sender)
	if p.SessionAttributes["service.name"] != "checkout" {
		t.Fatalf("service.name = %v", p.SessionAttributes["service.name"])
	}
	if p.SessionAttributes["env"] != "prod" || p.SessionAttributes["releaseId"] != "1.2.3" {
		t.Fatalf("session spine wrong: %+v", p.SessionAttributes)
	}
	if len(p.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(p.Events))
	}
	ev := p.Events[0]
	if ev.Type != "exception" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Attributes.ErrorMessage != "charge failed" {
		t.Fatalf("message = %q", ev.Attributes.ErrorMessage)
	}
	if !ev.Attributes.ErrorHandled {
		t.Fatal("expected handled=true for CaptureError")
	}
	md := ev.Attributes.ErrorMetadata
	if md["gc.test_id"] != "abc-123" {
		t.Fatalf("gc.test_id missing in error_metadata: %+v", md)
	}
	if md["user.id"] != "u-1" || md["user.organization"] != "acme" {
		t.Fatalf("identity not in error_metadata: %+v", md)
	}
	if md["amount"] != 42.5 || md["is_retry"] != true {
		t.Fatalf("typed attrs wrong: amount=%v is_retry=%v", md["amount"], md["is_retry"])
	}
	if md["telemetry.sdk.name"] != sdkName {
		t.Fatalf("resource attr missing: %+v", md)
	}
	if c.Stats().Sent != 1 {
		t.Fatalf("expected 1 sent, stats=%+v", c.Stats())
	}
}

func TestMergePrecedenceScopeThenOption(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	ctx := c.WithScope(context.Background(), func(s *Scope) {
		s.SetAttribute("key", "from-scope")
		s.SetAttribute("only-scope", "x")
	})
	c.CaptureError(ctx, errors.New("e"), WithAttributes(Attributes{"key": "from-option"}))
	_ = c.Flush(context.Background())

	md := decodePayload(t, sender).Events[0].Attributes.ErrorMetadata
	if md["key"] != "from-option" {
		t.Fatalf("per-call option must win, got %v", md["key"])
	}
	if md["only-scope"] != "x" {
		t.Fatalf("scope attribute lost: %v", md["only-scope"])
	}
}

func TestDropOldestOverflow(t *testing.T) {
	var dropped int
	sender := &testutil.MockSender{}
	// Prevent the worker from draining so the buffer overflows deterministically.
	c := mustClient(t, Config{
		MaxQueue:      3,
		FlushInterval: time.Hour,
		OnDrop:        func(n int) { dropped += n },
	}, sender)

	for range 10 {
		c.CaptureError(context.Background(), errors.New("e"))
	}
	if c.Stats().DroppedOverflow == 0 {
		t.Fatal("expected overflow drops")
	}
	if dropped == 0 {
		t.Fatal("OnDrop should have been called")
	}
	if c.ring.Len() > 3 {
		t.Fatalf("ring exceeded MaxQueue: %d", c.ring.Len())
	}
}

func TestBeforeSendDrop(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{
		BeforeSend: func(e *Event) *Event {
			if e.ErrorMessage == "drop me" {
				return nil
			}
			return e
		},
	}, sender)

	c.CaptureError(context.Background(), errors.New("drop me"))
	c.CaptureError(context.Background(), errors.New("keep me"))
	_ = c.Flush(context.Background())

	if c.Stats().DroppedBeforeSend != 1 {
		t.Fatalf("expected 1 BeforeSend drop, got %d", c.Stats().DroppedBeforeSend)
	}
	p := decodePayload(t, sender)
	for _, ev := range p.Events {
		if ev.Attributes.ErrorMessage == "drop me" {
			t.Fatal("dropped event must not be sent")
		}
	}
}

func TestBeforeSendScrub(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{
		BeforeSend: func(e *Event) *Event {
			e.ErrorMessage = "[scrubbed]"
			return e
		},
	}, sender)
	c.CaptureError(context.Background(), errors.New("secret token=xyz"))
	_ = c.Flush(context.Background())
	if got := decodePayload(t, sender).Events[0].Attributes.ErrorMessage; got != "[scrubbed]" {
		t.Fatalf("BeforeSend scrub failed: %q", got)
	}
}

func TestHasherPseudonymizesIdentity(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{Hasher: NewHMACHasher([]byte("key"))}, sender)
	ctx := c.SetUser(context.Background(), User{ID: "u-1", Email: "a@b.com"})
	c.CaptureError(ctx, errors.New("e"))
	_ = c.Flush(context.Background())

	md := decodePayload(t, sender).Events[0].Attributes.ErrorMetadata
	if md["user.id"] == "u-1" || md["user.id"] == "" {
		t.Fatalf("user.id should be hashed, got %v", md["user.id"])
	}
	if md["user.email"] == "a@b.com" || md["user.email"] == "" {
		t.Fatalf("user.email should be hashed, got %v", md["user.email"])
	}
}

func TestDisabledClientIsNoOp(t *testing.T) {
	c, err := New(Config{Disabled: true})
	if err != nil {
		t.Fatalf("New disabled: %v", err)
	}
	c.CaptureError(context.Background(), errors.New("e"))
	c.CaptureMessage(context.Background(), "m", LevelInfo)
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if (c.Stats() != Stats{}) {
		t.Fatalf("disabled client should have zero stats, got %+v", c.Stats())
	}
}

func TestCaptureNilErrorIgnored(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	c.CaptureError(context.Background(), nil)
	_ = c.Flush(context.Background())
	if c.Stats().Captured != 0 {
		t.Fatalf("nil error must be ignored, captured=%d", c.Stats().Captured)
	}
}

func TestRecoverReRaises(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("Recover must re-raise the panic")
			}
		}()
		defer c.Recover(context.Background())
		panic(errors.New("kaboom"))
	}()

	p := decodePayload(t, sender)
	ev := p.Events[0]
	if ev.Attributes.ErrorHandled {
		t.Fatal("panic event must be unhandled")
	}
	if ev.Attributes.ErrorMessage != "kaboom" {
		t.Fatalf("panic message = %q", ev.Attributes.ErrorMessage)
	}
}

func TestCaptureMessage(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	c.CaptureMessage(context.Background(), "stale cache", LevelWarning)
	_ = c.Flush(context.Background())
	ev := decodePayload(t, sender).Events[0]
	if ev.Attributes.ErrorMessage != "stale cache" || ev.Level != string(LevelWarning) {
		t.Fatalf("unexpected message event: %+v", ev)
	}
}

// After Close there is nothing left to drain, so a late capture must be
// discarded rather than retained in the queue forever.
func TestCaptureAfterCloseIsDiscarded(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{EnableLogs: true}, sender)
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for range 100 {
		c.CaptureError(context.Background(), errors.New("late"))
		c.EmitLog(context.Background(), LevelError, "late")
	}
	if got := c.Stats(); got.Captured != 0 || got.QueuePendingItems != 0 || got.DroppedShutdown != 100 {
		t.Fatalf("event stats after Close = %+v, want 100 shutdown drops and nothing queued", got)
	}
	if got := c.LogStats(); got.Captured != 0 || got.QueuePendingItems != 0 || got.DroppedShutdown != 100 {
		t.Fatalf("log stats after Close = %+v, want 100 shutdown drops and nothing queued", got)
	}
}

func TestNewRequiresDSN(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrMissingDSN) {
		t.Fatalf("expected ErrMissingDSN, got %v", err)
	}
}

// A DSN that cannot produce a usable URL is a configuration error, reported at
// construction rather than as a delivery failure for the life of the process.
func TestNewRejectsUnusableDSN(t *testing.T) {
	for _, dsn := range []string{"ftp://host.example", "https://", "://host.example"} {
		if _, err := New(Config{DSN: dsn}); err == nil {
			t.Errorf("New(DSN=%q) = nil error, want a configuration error", dsn)
		}
	}
	// A host without a scheme, and a host:port, still get the default scheme.
	for _, dsn := range []string{"host.example", "localhost:8080", "HTTPS://host.example"} {
		client, err := New(Config{DSN: dsn})
		if err != nil {
			t.Errorf("New(DSN=%q) = %v, want nil", dsn, err)
			continue
		}
		_ = client.Close(context.Background())
	}
}
