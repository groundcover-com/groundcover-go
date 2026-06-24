package groundcover_test

import (
	"context"
	"errors"
	"testing"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
)

// initDropGlobal points the package-level client at an unreachable DSN with a
// BeforeSend that drops everything, so global captures are observable via
// GlobalStats without any network I/O.
func initDropGlobal(t *testing.T) {
	t.Helper()
	err := groundcover.Init(groundcover.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    func(*groundcover.Event) *groundcover.Event { return nil },
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = groundcover.Close(ctx)
		// Restore a hermetic no-op global for any subsequent tests/examples.
		_ = groundcover.Init(groundcover.Config{Disabled: true})
	})
}

func TestGlobalCaptureError(t *testing.T) {
	initDropGlobal(t)
	before := groundcover.GlobalStats().DroppedBeforeSend

	ctx := groundcover.SetUser(context.Background(), groundcover.User{ID: "u-1"})
	groundcover.CaptureError(ctx, errors.New("global error"))

	if got := groundcover.GlobalStats().DroppedBeforeSend; got != before+1 {
		t.Fatalf("expected one global capture, delta=%d", got-before)
	}
}

func TestGlobalCaptureMessageWithScope(t *testing.T) {
	initDropGlobal(t)
	before := groundcover.GlobalStats().DroppedBeforeSend

	ctx := groundcover.WithScope(context.Background(), func(s *groundcover.Scope) {
		s.SetLevel(groundcover.LevelWarning)
	})
	groundcover.CaptureMessage(ctx, "noticed", groundcover.LevelInfo)

	if got := groundcover.GlobalStats().DroppedBeforeSend; got != before+1 {
		t.Fatalf("expected one global message capture, delta=%d", got-before)
	}
}

func TestGlobalRecoverReRaises(t *testing.T) {
	initDropGlobal(t)
	before := groundcover.GlobalStats().DroppedBeforeSend

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("global Recover must re-raise")
			}
		}()
		defer groundcover.Recover(context.Background())
		panic(errors.New("global boom"))
	}()

	if got := groundcover.GlobalStats().DroppedBeforeSend; got != before+1 {
		t.Fatalf("expected the panic to be captured, delta=%d", got-before)
	}
}

func TestGlobalRecoverNoPanicIsNoOp(t *testing.T) {
	initDropGlobal(t)
	// Recover with no active panic must return cleanly.
	func() { defer groundcover.Recover(context.Background()) }()
}

func TestGlobalCaptureRecovered(t *testing.T) {
	initDropGlobal(t)
	before := groundcover.GlobalStats().DroppedBeforeSend
	groundcover.CaptureRecovered(context.Background(), "oops")
	if got := groundcover.GlobalStats().DroppedBeforeSend; got != before+1 {
		t.Fatalf("expected CaptureRecovered to capture, delta=%d", got-before)
	}
}

func TestGlobalFlush(t *testing.T) {
	initDropGlobal(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := groundcover.Flush(ctx); err != nil {
		t.Fatalf("global flush: %v", err)
	}
}
