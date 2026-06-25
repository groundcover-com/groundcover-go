package groundcover

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/testutil"
)

// BenchmarkCaptureErrorDisabled measures the overhead of a disabled client,
// which must be ~zero (the no-op path).
func BenchmarkCaptureErrorDisabled(b *testing.B) {
	c, err := New(Config{Disabled: true})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	err = errors.New("benchmark error")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.CaptureError(ctx, err)
	}
}

// BenchmarkCaptureErrorEnabled measures the cost of capturing (enrich + stack +
// non-blocking hand-off) against a mock sender, without real network I/O.
func BenchmarkCaptureErrorEnabled(b *testing.B) {
	c, err := newClient(Config{
		DSN:           "https://example.invalid",
		MaxQueue:      100000,
		FlushInterval: time.Hour, // keep the worker out of the hot path
	}, &testutil.MockSender{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = c.Close(context.Background()) })
	ctx := context.Background()
	err = errors.New("benchmark error")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.CaptureError(ctx, err)
	}
}
