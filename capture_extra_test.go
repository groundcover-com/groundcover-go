package groundcover

import (
	"context"
	"errors"
	"testing"

	"github.com/groundcover-com/groundcover-go/internal/testutil"
)

func TestCaptureOptions(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	c.CaptureError(context.Background(), errors.New("e"),
		WithLevel(LevelWarning),
		WithUser(User{ID: "opt-user"}),
		WithFingerprint("fixed-fp"),
		WithAttributes(Attributes{"k": "v"}),
	)
	_ = c.Flush(context.Background())

	ev := decodePayload(t, sender).Events[0]
	if ev.Level != string(LevelWarning) {
		t.Fatalf("WithLevel ignored: %q", ev.Level)
	}
	if ev.Attributes.ErrorFingerprint != "fixed-fp" {
		t.Fatalf("WithFingerprint ignored: %q", ev.Attributes.ErrorFingerprint)
	}
	if ev.Attributes.ErrorMetadata["user.id"] != "opt-user" {
		t.Fatalf("WithUser ignored: %v", ev.Attributes.ErrorMetadata["user.id"])
	}
	if ev.Attributes.ErrorMetadata["k"] != "v" {
		t.Fatalf("WithAttributes ignored: %v", ev.Attributes.ErrorMetadata["k"])
	}
}

func TestWithLevelRejectsInvalid(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	c.CaptureError(context.Background(), errors.New("e"), WithLevel(Level("bogus")))
	_ = c.Flush(context.Background())
	if got := decodePayload(t, sender).Events[0].Level; got != string(LevelError) {
		t.Fatalf("invalid level should be ignored, kept default error; got %q", got)
	}
}

func TestCaptureMessageInvalidLevelDefaultsInfo(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	c.CaptureMessage(context.Background(), "msg", Level("nope"))
	_ = c.Flush(context.Background())
	if got := decodePayload(t, sender).Events[0].Level; got != string(LevelInfo) {
		t.Fatalf("invalid message level should default to info, got %q", got)
	}
}

func TestCaptureRecoveredStringPanic(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	c.CaptureRecovered(context.Background(), "string panic")
	_ = c.Flush(context.Background())

	ev := decodePayload(t, sender).Events[0]
	if ev.Attributes.ErrorType != "panic" {
		t.Fatalf("string panic type = %q, want panic", ev.Attributes.ErrorType)
	}
	if ev.Attributes.ErrorMessage != "string panic" || ev.Attributes.ErrorHandled {
		t.Fatalf("unexpected panic event: %+v", ev.Attributes)
	}
	if ev.Level != string(LevelFatal) {
		t.Fatalf("panic level = %q, want fatal", ev.Level)
	}
}

func TestCaptureRecoveredErrorPanic(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	c.CaptureRecovered(context.Background(), errors.New("err panic"))
	_ = c.Flush(context.Background())
	ev := decodePayload(t, sender).Events[0]
	if ev.Attributes.ErrorType != "*errors.errorString" || ev.Attributes.ErrorMessage != "err panic" {
		t.Fatalf("error panic not extracted: %+v", ev.Attributes)
	}
}

func TestCaptureRecoveredNilIgnored(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	c.CaptureRecovered(context.Background(), nil)
	_ = c.Flush(context.Background())
	if c.Stats().Captured != 0 {
		t.Fatalf("nil recovered value must be ignored, captured=%d", c.Stats().Captured)
	}
}

func TestBeforeSendPanicKeepsEvent(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{
		BeforeSend: func(*Event) *Event { panic("before-send blew up") },
	}, sender)
	c.CaptureError(context.Background(), errors.New("e"))
	_ = c.Flush(context.Background())
	// A panicking BeforeSend must not drop the event nor crash; it is kept.
	if len(sender.Bodies()) == 0 {
		t.Fatal("event should still be sent when BeforeSend panics")
	}
	if c.Stats().PanicsRecovered == 0 {
		t.Fatal("BeforeSend panic should be recorded")
	}
}
