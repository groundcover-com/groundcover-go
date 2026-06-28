package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
)

// countingTransport counts delivery attempts so a test can assert that a Disabled
// client performs no I/O.
type countingTransport struct {
	mu    sync.Mutex
	calls int
}

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
}

// An enabled client requires a DSN; the error is a sentinel callers can detect.
func TestErrMissingDSN(t *testing.T) {
	if _, err := gc.New(gc.Config{}); !errors.Is(err, gc.ErrMissingDSN) {
		t.Fatalf("want ErrMissingDSN, got %v", err)
	}
	// Disabled is the explicit opt-out and needs no DSN.
	if _, err := gc.New(gc.Config{Disabled: true}); err != nil {
		t.Fatalf("disabled client should be valid, got %v", err)
	}
}

// A Disabled client performs zero network I/O and never errors.
func TestDisabled_NoNetwork(t *testing.T) {
	tr := &countingTransport{}
	client, err := gc.New(gc.Config{Disabled: true, HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		t.Fatal(err)
	}
	client.CaptureError(context.Background(), errors.New("ignored"))
	_ = client.Flush(context.Background())
	_ = client.Close(context.Background())
	if tr.calls != 0 {
		t.Fatalf("disabled client made %d requests, want 0", tr.calls)
	}
}

// Capture must never block or panic even when delivery always fails — the SDK's
// prime directive.
func TestDeadBackend_NeverBlocksOrPanics(t *testing.T) {
	client, err := gc.New(gc.Config{
		DSN:        "http://ingest.example",
		HTTPClient: &http.Client{Transport: failingTransport{}},
		MaxRetries: 1,
		RetryMax:   10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseTimeout(time.Second)

	done := make(chan struct{})
	go func() {
		for range 100 {
			client.CaptureError(context.Background(), errors.New("will not deliver"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CaptureError blocked — violates the non-blocking guarantee")
	}
	if client.Stats().Captured == 0 {
		t.Errorf("expected captured > 0")
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline delivery failure")
}
