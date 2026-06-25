package groundcover

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/groundcover-com/groundcover-go/internal/testutil"
)

// TestReservedKeysNotOverridableByAttributes verifies that caller attributes
// cannot overwrite SDK-managed identity/severity keys (which would bypass the
// IdentityHasher or break numeric type stability).
func TestReservedKeysNotOverridableByAttributes(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{Hasher: NewHMACHasher([]byte("k"))}, sender)

	ctx := c.SetUser(context.Background(), User{ID: "u-1", Email: "a@b.com"})
	c.CaptureError(ctx, errors.New("e"), WithAttributes(Attributes{
		"user.id":         "plaintext-bypass",
		"user.email":      "raw@evil.com",
		"severity_number": "not-a-number",
		"level":           "definitely-not-a-level",
		"order_id":        "kept", // a normal custom attr survives
	}))
	_ = c.Flush(context.Background())

	md := decodePayload(t, sender).Events[0].Attributes.ErrorMetadata
	if md["user.id"] == "plaintext-bypass" {
		t.Fatal("caller attribute overrode hashed user.id")
	}
	if md["user.id"] == "u-1" {
		t.Fatal("user.id should be hashed, not raw")
	}
	if md["user.email"] == "raw@evil.com" {
		t.Fatal("caller attribute overrode user.email")
	}
	if _, ok := md["severity_number"].(float64); !ok {
		t.Fatalf("severity_number must stay numeric, got %T (%v)", md["severity_number"], md["severity_number"])
	}
	if md["level"] != string(LevelError) {
		t.Fatalf("level must be SDK-managed, got %v", md["level"])
	}
	if md["order_id"] != "kept" {
		t.Fatalf("normal custom attribute should survive, got %v", md["order_id"])
	}
}

// TestTitleAndFingerprintComputedAfterBeforeSend verifies a scrubber that
// rewrites the message is reflected in the title (no pre-scrub data leaks).
func TestTitleAndFingerprintComputedAfterBeforeSend(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{
		BeforeSend: func(e *Event) *Event {
			e.ErrorMessage = "[redacted]"
			return e
		},
	}, sender)

	c.CaptureError(context.Background(), errors.New("secret-token-12345"))
	_ = c.Flush(context.Background())

	ev := decodePayload(t, sender).Events[0]
	title, _ := ev.Attributes.ErrorMetadata["gc.title"].(string)
	if strings.Contains(title, "secret-token") {
		t.Fatalf("pre-scrub message leaked into title: %q", title)
	}
	if !strings.Contains(title, "[redacted]") {
		t.Fatalf("title should reflect the scrubbed message: %q", title)
	}
	if strings.Contains(ev.Attributes.ErrorMessage, "secret-token") {
		t.Fatalf("scrubbed message should not be sent: %q", ev.Attributes.ErrorMessage)
	}
}

// TestBeforeSendCanOverrideTitleAndFingerprint verifies explicit values set in
// BeforeSend are preserved (not recomputed over).
func TestBeforeSendCanOverrideTitleAndFingerprint(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{
		BeforeSend: func(e *Event) *Event {
			e.Title = "Custom Title"
			e.Fingerprint = "custom-fp"
			return e
		},
	}, sender)
	c.CaptureError(context.Background(), errors.New("x"))
	_ = c.Flush(context.Background())

	ev := decodePayload(t, sender).Events[0]
	if ev.Attributes.ErrorFingerprint != "custom-fp" {
		t.Fatalf("BeforeSend fingerprint override lost: %q", ev.Attributes.ErrorFingerprint)
	}
	if ev.Attributes.ErrorMetadata["gc.title"] != "Custom Title" {
		t.Fatalf("BeforeSend title override lost: %v", ev.Attributes.ErrorMetadata["gc.title"])
	}
}

// TestAttributesSnapshotOnCapture verifies that mutating a nested attribute
// value after capture does not change the already-queued event.
func TestAttributesSnapshotOnCapture(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	nested := map[string]any{"k": "original"}
	list := []any{"a", "b"}
	c.CaptureError(context.Background(), errors.New("e"), WithAttributes(Attributes{
		"data": nested,
		"list": list,
	}))

	// Mutate the caller's structures after capture but before flush/encode.
	nested["k"] = "mutated"
	list[0] = "z"

	_ = c.Flush(context.Background())

	md := decodePayload(t, sender).Events[0].Attributes.ErrorMetadata
	gotMap, ok := md["data"].(map[string]any)
	if !ok || gotMap["k"] != "original" {
		t.Fatalf("nested map was not snapshotted at capture: %v", md["data"])
	}
	gotList, ok := md["list"].([]any)
	if !ok || gotList[0] != "a" {
		t.Fatalf("nested slice was not snapshotted at capture: %v", md["list"])
	}
}

// TestHasherAppliesAfterBeforeSend verifies identity set (or left raw) by
// BeforeSend is still pseudonymized — the hash runs after BeforeSend.
func TestHasherAppliesAfterBeforeSend(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{
		Hasher: NewHMACHasher([]byte("k")),
		BeforeSend: func(e *Event) *Event {
			e.User.ID = "raw-from-beforesend"
			e.User.Email = "raw@evil.com"
			return e
		},
	}, sender)

	c.CaptureError(context.Background(), errors.New("e"))
	_ = c.Flush(context.Background())

	md := decodePayload(t, sender).Events[0].Attributes.ErrorMetadata
	if md["user.id"] == "raw-from-beforesend" {
		t.Fatal("BeforeSend-set raw user.id bypassed the hasher")
	}
	if md["user.email"] == "raw@evil.com" {
		t.Fatal("BeforeSend-set raw user.email bypassed the hasher")
	}
	if md["user.id"] == "" {
		t.Fatal("expected a hashed user.id")
	}
}

// TestCaptureMessageLevelBeatsScope verifies the per-call level argument wins
// over a ctx-scope level (global < ctx < per-call).
func TestCaptureMessageLevelBeatsScope(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	ctx := c.WithScope(context.Background(), func(s *Scope) { s.SetLevel(LevelWarning) })
	c.CaptureMessage(ctx, "m", LevelError)
	_ = c.Flush(context.Background())

	if got := decodePayload(t, sender).Events[0].Level; got != string(LevelError) {
		t.Fatalf("per-call level must beat scope, got %q", got)
	}
}

// TestScopeDoesNotDowngradePanicLevel verifies a recovered panic stays Fatal
// even when the request scope sets a lower level.
func TestScopeDoesNotDowngradePanicLevel(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	ctx := c.WithScope(context.Background(), func(s *Scope) { s.SetLevel(LevelWarning) })
	c.CaptureRecovered(ctx, errors.New("kaboom"))
	_ = c.Flush(context.Background())

	if got := decodePayload(t, sender).Events[0].Level; got != string(LevelFatal) {
		t.Fatalf("scope must not downgrade a panic from fatal, got %q", got)
	}
}

// TestScopeStillSetsLevelForNonPanic confirms scope level still applies to a
// normal (non-locked) event.
func TestScopeStillSetsLevelForNonPanic(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)
	ctx := c.WithScope(context.Background(), func(s *Scope) { s.SetLevel(LevelWarning) })
	c.CaptureError(ctx, errors.New("e"))
	_ = c.Flush(context.Background())
	if got := decodePayload(t, sender).Events[0].Level; got != string(LevelWarning) {
		t.Fatalf("scope level should apply to a normal error, got %q", got)
	}
}

// TestSharedScopeMutationVisibleToCapture verifies that mutating the scope on a
// context after deriving it (as middleware + handler do) is seen at capture,
// without threading the returned context back.
func TestSharedScopeMutationVisibleToCapture(t *testing.T) {
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{}, sender)

	reqCtx := c.WithIsolatedScope(context.Background()) // middleware seeds
	c.SetUser(reqCtx, User{ID: "handler-user"})         // handler mutates; return ignored
	c.CaptureError(reqCtx, errors.New("e"))             // capture uses the same ctx
	_ = c.Flush(context.Background())

	if got := decodePayload(t, sender).Events[0].Attributes.ErrorMetadata["user.id"]; got != "handler-user" {
		t.Fatalf("scope mutation should be visible at capture without re-threading, got %v", got)
	}
}

func TestDebugModeHonorsScrubbing(t *testing.T) {
	var buf bytes.Buffer
	sender := &testutil.MockSender{}
	c := mustClient(t, Config{Debug: true, Hasher: NewHMACHasher([]byte("k"))}, sender)
	c.debugOut = &buf

	ctx := c.SetUser(context.Background(), User{ID: "secret-user-id"})
	c.CaptureError(ctx, errors.New("boom"))
	_ = c.Flush(context.Background())

	out := buf.String()
	if out == "" {
		t.Fatal("debug mode should write the event")
	}
	if strings.Contains(out, "secret-user-id") {
		t.Fatalf("debug output must honor identity hashing, leaked raw id:\n%s", out)
	}
	if !strings.Contains(out, "[groundcover]") || !strings.Contains(out, "boom") {
		t.Fatalf("debug output missing expected content:\n%s", out)
	}
}

func TestRenderDebug(t *testing.T) {
	e := &Event{
		Level: LevelError, Type: "exception", Title: "*x.E: boom",
		Fingerprint: "fp", ErrorHandled: true,
		User:       User{ID: "u-1", Organization: "acme"},
		Attributes: Attributes{"order_id": "o-9"},
		Stacktrace: []Frame{{Function: "main.run", File: "/app/main.go", Line: 10}},
	}
	out := renderDebug(e)
	for _, want := range []string{"[groundcover] error exception", "*x.E: boom", "fingerprint=fp", "id=u-1", "order_id=o-9", "main.run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderDebug missing %q in:\n%s", want, out)
		}
	}
}

// TestSanitizeAttributesExpandsTypedCollections verifies the capture-time
// snapshot expands typed collections so the byte estimate sees real structure.
func TestSanitizeAttributesExpandsTypedCollections(t *testing.T) {
	in := Attributes{"ids": []int{1, 2, 3}, "m": map[string]int{"a": 1}}
	out := sanitizeAttributes(in)
	if _, ok := out["ids"].([]any); !ok {
		t.Fatalf("typed slice not expanded: %T", out["ids"])
	}
	if _, ok := out["m"].(map[string]any); !ok {
		t.Fatalf("typed map not expanded: %T", out["m"])
	}
	if sanitizeAttributes(nil) != nil {
		t.Fatal("nil attributes should sanitize to nil")
	}
}
