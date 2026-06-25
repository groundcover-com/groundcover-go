package transport_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/ringbuf"
	"github.com/groundcover-com/groundcover-go/internal/testutil"
	"github.com/groundcover-com/groundcover-go/internal/transport"
)

func intEncoder(items []int) ([]byte, error) {
	return []byte(strconv.Itoa(len(items))), nil
}

type countObserver struct {
	sent        atomic.Int64
	retries     atomic.Int64
	rateLimited atomic.Int64
	exhausted   atomic.Int64
	disabled    atomic.Int64
}

func (o *countObserver) OnSent(n int)          { o.sent.Add(int64(n)) }
func (o *countObserver) OnRetry()              { o.retries.Add(1) }
func (o *countObserver) OnRateLimited()        { o.rateLimited.Add(1) }
func (o *countObserver) OnSendExhausted(n int) { o.exhausted.Add(int64(n)) }
func (o *countObserver) OnSubsystemDisabled()  { o.disabled.Add(1) }
func (o *countObserver) OnQueue(_, _ int)      {}

func newWorker(t *testing.T, sender transport.Sender, obs transport.Observer, cfg transport.WorkerConfig) (*transport.Worker[int], *ringbuf.Buffer[int]) {
	t.Helper()
	ring := ringbuf.New[int](10000, 0, nil)
	w := transport.NewWorker[int](ring, sender, intEncoder, obs, nil, nil, cfg)
	return w, ring
}

func TestHTTPSenderSuccess(t *testing.T) {
	var gotAuth, gotEnc string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotEnc = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := transport.NewHTTPSender(transport.HTTPConfig{Endpoint: srv.URL, IngestionKey: "k", Client: srv.Client()})
	if err := s.Send(context.Background(), []byte(`{"a":1}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("expected Bearer auth, got %q", gotAuth)
	}
	if gotEnc != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", gotEnc)
	}
}

func TestHTTPSenderClassification(t *testing.T) {
	cases := []struct {
		status      int
		retryAfter  string
		retryable   bool
		rateLimited bool
		wantAfter   time.Duration
	}{
		{status: 500, retryable: true},
		{status: 503, retryable: true},
		{status: 400, retryable: false},
		{status: 404, retryable: false},
		{status: 429, retryAfter: "12", retryable: true, rateLimited: true, wantAfter: 12 * time.Second},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(tc.status)
		}))
		s := transport.NewHTTPSender(transport.HTTPConfig{Endpoint: srv.URL, Client: srv.Client()})
		err := s.Send(context.Background(), []byte(`{}`))
		srv.Close()

		var se *transport.SendError
		if !errors.As(err, &se) {
			t.Fatalf("status %d: expected *SendError, got %v", tc.status, err)
		}
		if se.Retryable != tc.retryable || se.RateLimited != tc.rateLimited {
			t.Fatalf("status %d: got retryable=%v rateLimited=%v", tc.status, se.Retryable, se.RateLimited)
		}
		if se.RetryAfter != tc.wantAfter {
			t.Fatalf("status %d: expected RetryAfter %v, got %v", tc.status, tc.wantAfter, se.RetryAfter)
		}
	}
}

func TestHTTPSenderNetworkError(t *testing.T) {
	s := transport.NewHTTPSender(transport.HTTPConfig{
		Endpoint: "http://127.0.0.1:1/json/rum",
		Client:   &http.Client{Timeout: 200 * time.Millisecond},
	})
	err := s.Send(context.Background(), []byte(`{}`))
	var se *transport.SendError
	if !errors.As(err, &se) || !se.Retryable {
		t.Fatalf("expected retryable network SendError, got %v", err)
	}
}

func TestWorkerBatchingAndFlush(t *testing.T) {
	sender := &testutil.MockSender{}
	obs := &countObserver{}
	w, ring := newWorker(t, sender, obs, transport.WorkerConfig{BatchSize: 10})
	w.Start()
	defer func() { _ = w.Close(context.Background()) }()

	for i := range 25 {
		ring.Push(i)
	}
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if obs.sent.Load() != 25 {
		t.Fatalf("expected 25 sent, got %d", obs.sent.Load())
	}
	if sender.Calls() != 3 { // 10 + 10 + 5
		t.Fatalf("expected 3 batches, got %d", sender.Calls())
	}
	if ring.Len() != 0 {
		t.Fatalf("expected drained ring, got %d", ring.Len())
	}
}

func TestWorkerRetryThenSuccess(t *testing.T) {
	var sleeps []time.Duration
	var mu sync.Mutex
	sender := &testutil.MockSender{Responder: func(call int, _ []byte) error {
		if call < 2 {
			return &transport.SendError{StatusCode: 500, Retryable: true, Err: errors.New("boom")}
		}
		return nil
	}}
	obs := &countObserver{}
	w, ring := newWorker(t, sender, obs, transport.WorkerConfig{
		BatchSize:  10,
		MaxRetries: 5,
		Sleep:      func(_ context.Context, d time.Duration) { mu.Lock(); sleeps = append(sleeps, d); mu.Unlock() },
	})
	w.Start()
	defer func() { _ = w.Close(context.Background()) }()

	ring.Push(1)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if obs.sent.Load() != 1 {
		t.Fatalf("expected eventual success, sent=%d", obs.sent.Load())
	}
	if obs.retries.Load() != 2 {
		t.Fatalf("expected 2 retries, got %d", obs.retries.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sleeps) != 2 {
		t.Fatalf("expected 2 backoff sleeps, got %d", len(sleeps))
	}
}

func TestWorker429UsesRateLimitBackoff(t *testing.T) {
	var sleeps []time.Duration
	var mu sync.Mutex
	calls := atomic.Int64{}
	sender := &testutil.MockSender{Responder: func(_ int, _ []byte) error {
		if calls.Add(1) == 1 {
			return &transport.SendError{StatusCode: 429, RateLimited: true, Retryable: true, RetryAfter: 7 * time.Second}
		}
		return nil
	}}
	obs := &countObserver{}
	w, ring := newWorker(t, sender, obs, transport.WorkerConfig{
		BatchSize:        10,
		MaxRetries:       3,
		RateLimitBackoff: 30 * time.Second,
		Sleep:            func(_ context.Context, d time.Duration) { mu.Lock(); sleeps = append(sleeps, d); mu.Unlock() },
	})
	w.Start()
	defer func() { _ = w.Close(context.Background()) }()

	ring.Push(1)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if obs.rateLimited.Load() != 1 {
		t.Fatalf("expected 1 rate-limited, got %d", obs.rateLimited.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sleeps) != 1 || sleeps[0] != 30*time.Second {
		t.Fatalf("expected one 30s backoff (max of RateLimitBackoff, RetryAfter), got %v", sleeps)
	}
}

func TestWorkerPermanentErrorDropsNoRetry(t *testing.T) {
	sender := &testutil.MockSender{Responder: func(_ int, _ []byte) error {
		return &transport.SendError{StatusCode: 400, Retryable: false, Err: errors.New("bad")}
	}}
	obs := &countObserver{}
	w, ring := newWorker(t, sender, obs, transport.WorkerConfig{BatchSize: 10, MaxRetries: 5})
	w.Start()
	defer func() { _ = w.Close(context.Background()) }()

	ring.Push(1)
	ring.Push(2)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if obs.exhausted.Load() != 2 {
		t.Fatalf("expected 2 dropped (send exhausted), got %d", obs.exhausted.Load())
	}
	if obs.retries.Load() != 0 {
		t.Fatalf("permanent error must not retry, got %d retries", obs.retries.Load())
	}
	if sender.Calls() != 1 {
		t.Fatalf("permanent error must be a single attempt, got %d", sender.Calls())
	}
}

func TestWorkerRetryExhausted(t *testing.T) {
	sender := &testutil.MockSender{Responder: func(_ int, _ []byte) error {
		return &transport.SendError{StatusCode: 503, Retryable: true, Err: errors.New("down")}
	}}
	obs := &countObserver{}
	w, ring := newWorker(t, sender, obs, transport.WorkerConfig{
		BatchSize:  10,
		MaxRetries: 2,
		Sleep:      func(context.Context, time.Duration) {},
	})
	w.Start()
	defer func() { _ = w.Close(context.Background()) }()

	ring.Push(1)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if obs.exhausted.Load() != 1 {
		t.Fatalf("expected 1 dropped after exhaustion, got %d", obs.exhausted.Load())
	}
	if sender.Calls() != 3 { // initial + 2 retries
		t.Fatalf("expected 3 attempts, got %d", sender.Calls())
	}
}

func TestWorkerBackoffMath(t *testing.T) {
	var sleeps []time.Duration
	var mu sync.Mutex
	sender := &testutil.MockSender{Responder: func(_ int, _ []byte) error {
		return &transport.SendError{StatusCode: 503, Retryable: true, Err: errors.New("down")}
	}}
	const retryMax = 4 * time.Second
	w, ring := newWorker(t, sender, &countObserver{}, transport.WorkerConfig{
		BatchSize:   10,
		MaxRetries:  8,
		BaseBackoff: 100 * time.Millisecond,
		RetryMax:    retryMax,
		Sleep:       func(_ context.Context, d time.Duration) { mu.Lock(); sleeps = append(sleeps, d); mu.Unlock() },
	})
	w.Start()
	defer func() { _ = w.Close(context.Background()) }()

	ring.Push(1)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sleeps) != 8 { // one sleep per retry attempt
		t.Fatalf("expected 8 backoff sleeps, got %d", len(sleeps))
	}
	for i, d := range sleeps {
		if d < 0 || d > retryMax {
			t.Fatalf("sleep %d = %v, must be within [0, %v] (full jitter, capped)", i, d, retryMax)
		}
	}
}

func TestWorkerSendPanicCountedAsExhausted(t *testing.T) {
	// A panic inside a send must be contained, counted as an exhausted drop, and
	// must not take down the worker (a later batch still sends).
	var calls atomic.Int64
	sender := &testutil.MockSender{Responder: func(int, []byte) error {
		if calls.Add(1) == 1 {
			panic("send panic")
		}
		return nil
	}}
	obs := &countObserver{}
	w, ring := newWorker(t, sender, obs, transport.WorkerConfig{BatchSize: 1, MaxRetries: 0})
	w.Start()
	defer func() { _ = w.Close(context.Background()) }()

	ring.Push(1)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if obs.exhausted.Load() != 1 {
		t.Fatalf("a panicking send should count one exhausted drop, got %d", obs.exhausted.Load())
	}

	// The worker must still be alive: a subsequent batch sends successfully.
	ring.Push(2)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if obs.sent.Load() != 1 {
		t.Fatalf("worker should survive a send panic and deliver later batches, sent=%d", obs.sent.Load())
	}
}

// loopPanicObserver panics inside OnQueue, which the worker calls from its loop
// goroutine, to deterministically exercise the loop self-disable path.
type loopPanicObserver struct{ disabled atomic.Int64 }

func (o *loopPanicObserver) OnSent(int)           {}
func (o *loopPanicObserver) OnRetry()             {}
func (o *loopPanicObserver) OnRateLimited()       {}
func (o *loopPanicObserver) OnSendExhausted(int)  {}
func (o *loopPanicObserver) OnSubsystemDisabled() { o.disabled.Add(1) }
func (o *loopPanicObserver) OnQueue(int, int)     { panic("queue panic") }

func TestWorkerLoopPanicSelfDisables(t *testing.T) {
	obs := &loopPanicObserver{}
	sender := &testutil.MockSender{}
	ring := ringbuf.New[int](100, 0, nil)
	w := transport.NewWorker[int](ring, sender, intEncoder, obs, nil, nil, transport.WorkerConfig{FlushInterval: 20 * time.Millisecond})
	w.Start()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && obs.disabled.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if obs.disabled.Load() != 1 {
		t.Fatalf("expected the loop to self-disable exactly once, got %d", obs.disabled.Load())
	}

	// After self-disable, Flush must still drain pending items rather than
	// silently succeed (the Observer keeps panicking in OnQueue; Flush guards it).
	ring.Push(1)
	ring.Push(2)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush after self-disable: %v", err)
	}
	if sender.Calls() == 0 {
		t.Fatal("Flush after self-disable must drain pending items")
	}
	if ring.Len() != 0 {
		t.Fatalf("ring should be drained after Flush, got %d", ring.Len())
	}

	// Close must not hang after a self-disabled loop.
	done := make(chan error, 1)
	go func() { done <- w.Close(context.Background()) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung after loop self-disable")
	}
}

func TestWorkerEncodeErrorDropsBatch(t *testing.T) {
	sender := &testutil.MockSender{}
	obs := &countObserver{}
	ring := ringbuf.New[int](100, 0, nil)
	failing := func([]int) ([]byte, error) { return nil, errors.New("encode boom") }
	w := transport.NewWorker[int](ring, sender, failing, obs, nil, nil, transport.WorkerConfig{BatchSize: 10})
	w.Start()
	defer func() { _ = w.Close(context.Background()) }()

	ring.Push(1)
	ring.Push(2)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sender.Calls() != 0 {
		t.Fatalf("a batch that fails to encode must not be sent, calls=%d", sender.Calls())
	}
	if obs.exhausted.Load() != 2 {
		t.Fatalf("encode failure should drop the batch as exhausted, got %d", obs.exhausted.Load())
	}
}

func TestWorkerCloseIdempotent(t *testing.T) {
	sender := &testutil.MockSender{}
	w, ring := newWorker(t, sender, &countObserver{}, transport.WorkerConfig{BatchSize: 5})
	w.Start()
	for i := range 7 {
		ring.Push(i)
	}
	w.Notify()
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("second close should be a no-op, got %v", err)
	}
	if ring.Len() != 0 {
		t.Fatalf("close should drain remaining items, got %d", ring.Len())
	}
}
