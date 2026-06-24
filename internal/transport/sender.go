// Package transport owns all network I/O for the SDK: a single HTTP sender that
// POSTs gzipped JSON batches, and a single background worker that batches,
// retries, and flushes from the bounded buffer. It is the sole network owner;
// no other part of the SDK performs I/O.
package transport

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// maxErrorBodyBytes bounds how much of an error response body we read for
// diagnostics, preventing a hostile or buggy server from forcing a large read.
const maxErrorBodyBytes = 4 << 10

// Sender delivers an encoded (uncompressed JSON) batch body to the backend.
type Sender interface {
	// Send delivers body. A nil return means success. Failures are returned as
	// *SendError so the worker can decide whether and how to retry.
	Send(ctx context.Context, body []byte) error
}

// SendError categorizes a delivery failure so the worker can apply the right
// retry policy.
type SendError struct {
	// StatusCode is the HTTP status, or 0 for a transport-level error.
	StatusCode int
	// Retryable is true for transient failures (network errors, 5xx).
	Retryable bool
	// RateLimited is true for HTTP 429.
	RateLimited bool
	// RetryAfter is the server-requested backoff parsed from the Retry-After
	// header, if any.
	RetryAfter time.Duration
	// Err is the underlying cause, if any.
	Err error
}

// Error implements error.
func (e *SendError) Error() string {
	switch {
	case e.Err != nil && e.StatusCode != 0:
		return fmt.Sprintf("transport: status %d: %v", e.StatusCode, e.Err)
	case e.StatusCode != 0:
		return fmt.Sprintf("transport: status %d", e.StatusCode)
	case e.Err != nil:
		return "transport: " + e.Err.Error()
	default:
		return "transport: unknown error"
	}
}

// Unwrap exposes the underlying cause.
func (e *SendError) Unwrap() error { return e.Err }

// HTTPConfig configures an HTTPSender.
type HTTPConfig struct {
	// Endpoint is the fully-qualified URL to POST to (the SDK owns the path,
	// e.g. <DSN>/json/rum).
	Endpoint string
	// IngestionKey, when non-empty, is sent as "Authorization: Bearer <key>".
	IngestionKey string
	// UserAgent identifies the SDK on the wire.
	UserAgent string
	// Client is the HTTP client to use. Required.
	Client *http.Client
}

// HTTPSender is the default Sender. It gzips the body and POSTs it as JSON.
type HTTPSender struct {
	cfg HTTPConfig
}

// NewHTTPSender returns an HTTPSender. A nil Client is replaced with a client
// using sensible timeouts.
func NewHTTPSender(cfg HTTPConfig) *HTTPSender {
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPSender{cfg: cfg}
}

// Send gzips and POSTs body to the configured endpoint, classifying the result.
func (s *HTTPSender) Send(ctx context.Context, body []byte) error {
	gzipped, err := gzipBytes(body)
	if err != nil {
		return &SendError{Retryable: false, Err: fmt.Errorf("gzip: %w", err)}
	}

	// The endpoint is SDK-owned configuration, not attacker-controlled input.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Endpoint, bytes.NewReader(gzipped))
	if err != nil {
		return &SendError{Retryable: false, Err: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	if s.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", s.cfg.UserAgent)
	}
	if s.cfg.IngestionKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.IngestionKey)
	}

	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		// Network/transport errors are transient.
		return &SendError{Retryable: true, Err: err}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))
		_ = resp.Body.Close()
	}()

	return classify(resp)
}

// classify maps an HTTP response to nil (success) or a *SendError.
func classify(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return &SendError{
			StatusCode:  resp.StatusCode,
			RateLimited: true,
			Retryable:   true,
			RetryAfter:  parseRetryAfter(resp.Header.Get("Retry-After")),
			Err:         errors.New("rate limited"),
		}
	case resp.StatusCode >= 500:
		return &SendError{StatusCode: resp.StatusCode, Retryable: true, Err: errors.New("server error")}
	default:
		// 4xx (other than 429): permanent, do not retry.
		return &SendError{StatusCode: resp.StatusCode, Retryable: false, Err: errors.New("client error")}
	}
}

// parseRetryAfter parses a Retry-After header value in delta-seconds or HTTP
// date form. It returns zero when the value is missing or unparseable.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// gzipBytes returns the gzip-compressed form of b.
func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
