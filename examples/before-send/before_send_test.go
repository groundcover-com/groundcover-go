package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
)

// captureTransport records the (gunzipped) bodies the SDK puts on the wire.
// Overriding Config.HTTPClient is how you assert on the FINAL payload — after the
// Hasher and BeforeSend have run and the batch is encoded. This doubles as the
// "mock transport for tests" pattern.
type captureTransport struct {
	mu     sync.Mutex
	bodies []string
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	defer func() { _ = req.Body.Close() }()
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	body := raw
	if req.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = gz.Close() }()
		if body, err = io.ReadAll(gz); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, string(body))
	c.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
}

func (c *captureTransport) joined() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.bodies, "\n")
}

// End-to-end PII guarantee: the bytes that leave the process contain neither the
// scrubbed secrets/emails nor the plaintext identity.
func TestScrubAndHash_OnTheWire(t *testing.T) {
	tr := &captureTransport{}
	client, err := gc.New(gc.Config{
		DSN:           "http://ingest.example",
		HTTPClient:    &http.Client{Transport: tr},
		Hasher:        gc.NewHMACHasher([]byte("k")),
		BatchSize:     100,       // above 1 so capture doesn't auto-notify; Flush is the only trigger
		FlushInterval: time.Hour, // deliver only via explicit Flush
		BeforeSend:    scrubber(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseTimeout(time.Second)

	ctx := client.SetUser(context.Background(), gc.User{ID: "user-1", Email: "alice@secret.example"})
	client.CaptureError(ctx, errors.New("failed to notify carol@customer.com"),
		gc.WithAttributes(gc.Attributes{
			attrAuthorization: "Bearer super-secret-token",
			"gateway":         "stripe",
		}))

	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	wire := tr.joined()
	if wire == "" {
		t.Fatal("nothing delivered")
	}
	for _, leak := range []string{"super-secret-token", attrAuthorization, "user-1", "alice@secret.example", "carol@customer.com"} {
		if strings.Contains(wire, leak) {
			t.Errorf("PII LEAK: %q present on the wire", leak)
		}
	}
	if !strings.Contains(wire, "stripe") {
		t.Error("expected non-PII attribute to survive")
	}
	if !strings.Contains(wire, "[redacted-email]") {
		t.Error("expected the email in the message to be redacted")
	}
}

// A noisy fingerprint is dropped entirely by BeforeSend.
func TestDropByFingerprint(t *testing.T) {
	tr := &captureTransport{}
	client, err := gc.New(gc.Config{
		DSN:        "http://ingest.example",
		HTTPClient: &http.Client{Transport: tr},
		BeforeSend: scrubber(map[string]bool{"healthcheck-blip": true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseTimeout(time.Second)

	client.CaptureMessage(context.Background(), "blip", gc.LevelInfo, gc.WithFingerprint("healthcheck-blip"))
	_ = client.Flush(context.Background())

	if client.Stats().DroppedBeforeSend == 0 {
		t.Error("expected the noisy event to be dropped by BeforeSend")
	}
	if tr.joined() != "" {
		t.Error("dropped event must not be delivered")
	}
}
