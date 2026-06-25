package transport

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestSendErrorFormatting(t *testing.T) {
	cause := errors.New("root")
	cases := []struct {
		err  *SendError
		want string
	}{
		{&SendError{StatusCode: 500, Err: cause}, "transport: status 500: root"},
		{&SendError{StatusCode: 404}, "transport: status 404"},
		{&SendError{Err: cause}, "transport: root"},
		{&SendError{}, "transport: unknown error"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Fatalf("Error() = %q, want %q", got, tc.want)
		}
	}
	if !errors.Is(&SendError{Err: cause}, cause) {
		t.Fatal("Unwrap must expose the cause")
	}
	if (&SendError{}).Unwrap() != nil {
		t.Fatal("Unwrap of a causeless error must be nil")
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("empty -> %v", d)
	}
	if d := parseRetryAfter("5"); d != 5*time.Second {
		t.Fatalf("delta-seconds -> %v", d)
	}
	if d := parseRetryAfter("-3"); d != 0 {
		t.Fatalf("negative seconds -> %v", d)
	}
	if d := parseRetryAfter("not-a-number"); d != 0 {
		t.Fatalf("garbage -> %v", d)
	}
	future := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(future); d <= 0 || d > 2*time.Minute+time.Second {
		t.Fatalf("future HTTP-date -> %v", d)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(past); d != 0 {
		t.Fatalf("past HTTP-date -> %v", d)
	}
}

func TestSleepCtx(t *testing.T) {
	sleepCtx(context.Background(), 0) // non-positive: immediate

	start := time.Now()
	sleepCtx(context.Background(), 20*time.Millisecond)
	if time.Since(start) < 10*time.Millisecond {
		t.Fatal("sleepCtx returned too early for a positive duration")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	sleepCtx(ctx, time.Hour)
	if time.Since(start) > time.Second {
		t.Fatal("sleepCtx should return promptly when ctx is done")
	}
}

func TestGzipBytesRoundTrip(t *testing.T) {
	in := []byte(`{"hello":"world","n":42}`)
	gz, err := gzipBytes(in)
	if err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("round-trip mismatch: %q != %q", out, in)
	}
}

func TestNewHTTPSenderDefaultsClient(t *testing.T) {
	s := NewHTTPSender(HTTPConfig{Endpoint: "https://example.invalid"})
	if s.cfg.Client == nil {
		t.Fatal("nil client should be replaced with a default")
	}
}

func TestJitterRNGBounds(t *testing.T) {
	r := newJitterRNG()
	if r.intn(0) != 0 || r.intn(-5) != 0 {
		t.Fatal("intn of non-positive n must be 0")
	}
	for range 1000 {
		if v := r.intn(10); v < 0 || v >= 10 {
			t.Fatalf("intn(10) out of range: %d", v)
		}
	}
}
