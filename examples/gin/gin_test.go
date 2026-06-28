package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	gc "github.com/groundcover-com/groundcover-go"
	gcgin "github.com/groundcover-com/groundcover-go/contrib/gin"
)

// These tests show how to system-test a Gin service that uses groundcover without
// a live backend. The seam is a BeforeSend callback that snapshots every event
// in-process and drops delivery, so assertions are synchronous and hermetic. Each
// test creates its own recorder and client (no shared global state).

type recorder struct {
	mu     sync.Mutex
	events []gc.Event
}

func (r *recorder) before(e *gc.Event) *gc.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, *e) // snapshot (post-scope, post-options; pre-Hasher)
	return nil                      // drop delivery: hermetic test
}

func (r *recorder) all() []gc.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]gc.Event, len(r.events))
	copy(out, r.events)
	return out
}

// newRecorderClient builds an isolated client whose every capture is recorded.
func newRecorderClient(t *testing.T) (*recorder, *gc.Client) {
	t.Helper()
	rec := &recorder{}
	client, err := gc.New(gc.Config{
		DSN:         "http://127.0.0.1:0", // unused: before() drops delivery
		ServiceName: "examples-gin-test",
		BeforeSend:  rec.before,
		Hasher:      gc.NewHMACHasher([]byte("test-key")),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.CloseTimeout(0) })
	return rec, client
}

// newTestRouter wires gin.Recovery() before the groundcover middleware so a panic
// is captured, re-raised, and turned into a 500. Captures are routed to the given
// client via WithClient. Identity/scope is set INSIDE the handler: the middleware
// re-reads c.Request.Context() at capture time, so handler enrichment is reflected
// in the captured error.
func newTestRouter(client *gc.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gcgin.Middleware(gcgin.WithClient(client), gcgin.WithErrorCapture(true)))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	r.GET("/checkout", func(c *gin.Context) {
		ctx := gc.SetUser(c.Request.Context(), gc.User{
			ID: "user-77", Email: "buyer@example.com", Organization: "acme",
		})
		ctx = gc.WithScope(ctx, func(s *gc.Scope) { s.SetSessionID(c.Query("sid")) })
		c.Request = c.Request.WithContext(ctx)

		_ = c.Error(errPaymentDeclined)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "charge failed"})
	})

	r.GET("/panic", func(c *gin.Context) { panic("unexpected nil pointer") })
	return r
}

func do(engine http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// Passing path: a healthy request returns 200 and captures nothing.
func TestHealthz_Passing(t *testing.T) {
	rec, client := newRecorderClient(t)
	if w := do(newTestRouter(client), "/healthz"); w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("want 0 captured events, got %d", n)
	}
}

// Failing path: c.Error → 500, exactly one HANDLED error, carrying the identity and
// session set in the handler (verifies the middleware reflects handler-set scope).
func TestCheckout_Failing_CapturesHandledErrorWithScope(t *testing.T) {
	rec, client := newRecorderClient(t)
	w := do(newTestRouter(client), "/checkout?sid=sess-1")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
	events := rec.all()
	if len(events) != 1 {
		t.Fatalf("want exactly 1 captured event, got %d", len(events))
	}
	e := events[0]
	if !e.ErrorHandled {
		t.Errorf("want handled error")
	}
	if e.SessionID != "sess-1" {
		t.Errorf("want session sess-1 set in handler, got %q (handler-set scope not reflected?)", e.SessionID)
	}
	if e.User.ID == "" {
		t.Errorf("want user set in handler to be captured")
	}
}

// Panic path: handler panics; the middleware captures ONE unhandled error,
// re-raises, gin.Recovery returns 500 — the panic never escapes the test.
func TestPanic_CapturedReRaisedRecovered(t *testing.T) {
	rec, client := newRecorderClient(t)
	w := do(newTestRouter(client), "/panic")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 from gin.Recovery, got %d", w.Code)
	}
	events := rec.all()
	if len(events) != 1 {
		t.Fatalf("want exactly 1 captured panic (no double-capture), got %d", len(events))
	}
	if events[0].ErrorHandled {
		t.Errorf("want unhandled (panic)")
	}
}

// NOTE on PII: the SDK applies the Hasher *after* BeforeSend, so a BeforeSend
// recorder observes the RAW identity — pseudonymization is only guaranteed on the
// delivered payload. Asserting PII redaction therefore belongs at the wire level;
// see examples/before-send/before_send_test.go (TestScrubAndHash_OnTheWire), which
// inspects the actual bytes via a custom HTTPClient transport.

var errPaymentDeclined = &paymentError{}

type paymentError struct{}

func (*paymentError) Error() string { return "payment declined" }
