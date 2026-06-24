package groundcover

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/testutil"
	"github.com/groundcover-com/groundcover-go/internal/transport"
)

// TestDeliveryMetricsOnRetryExhaustion drives the worker through retryable
// failures so the client's metrics adapter records retries and a send-exhausted
// drop.
func TestDeliveryMetricsOnRetryExhaustion(t *testing.T) {
	sender := &testutil.MockSender{Responder: func(int, []byte) error {
		return &transport.SendError{StatusCode: 503, Retryable: true, Err: errors.New("down")}
	}}
	c := mustClient(t, Config{MaxRetries: 1, RetryMax: time.Millisecond}, sender)

	c.CaptureError(context.Background(), errors.New("e"))
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	s := c.Stats()
	if s.Retries < 1 {
		t.Fatalf("expected at least one retry, got %d", s.Retries)
	}
	if s.DroppedSendExhausted != 1 {
		t.Fatalf("expected one send-exhausted drop, got %d", s.DroppedSendExhausted)
	}
	if s.Sent != 0 {
		t.Fatalf("expected zero sent, got %d", s.Sent)
	}
}

// TestDeliveryMetricsOnRateLimit drives a 429 so the rate-limited counter moves.
func TestDeliveryMetricsOnRateLimit(t *testing.T) {
	sender := &testutil.MockSender{Responder: func(int, []byte) error {
		return &transport.SendError{StatusCode: 429, RateLimited: true, Retryable: true}
	}}
	c := mustClient(t, Config{MaxRetries: 1, RateLimitBackoff: time.Millisecond}, sender)

	c.CaptureError(context.Background(), errors.New("e"))
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if c.Stats().RateLimited < 1 {
		t.Fatalf("expected a rate-limited observation, got %d", c.Stats().RateLimited)
	}
}

// TestDeliveryMetricsOnSuccess confirms the happy-path sent counter.
func TestDeliveryMetricsOnSuccess(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	c.CaptureError(context.Background(), errors.New("e"))
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if c.Stats().Sent != 1 {
		t.Fatalf("expected one sent, got %d", c.Stats().Sent)
	}
}
