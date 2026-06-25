package groundcover

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type typedError struct{ msg string }

func (e *typedError) Error() string     { return e.msg }
func (e *typedError) ErrorType() string { return "custom.DomainError" }

func TestErrorTypeExtraction(t *testing.T) {
	plain := errors.New("boom")
	if got := errorType(plain); got != "*errors.errorString" {
		t.Fatalf("plain error type = %q", got)
	}

	wrapped := fmt.Errorf("context: %w", plain)
	if got := errorType(wrapped); got != "*errors.errorString" {
		t.Fatalf("wrapped should resolve to innermost: %q", got)
	}

	typed := fmt.Errorf("outer: %w", &typedError{msg: "inner"})
	if got := errorType(typed); got != "custom.DomainError" {
		t.Fatalf("ErrorType() method should win: %q", got)
	}

	joined := errors.Join(errors.New("a"), errors.New("b"))
	if got := errorType(joined); got == "" {
		t.Fatalf("joined error type should be non-empty")
	}

	if errorType(nil) != "" {
		t.Fatal("nil error must yield empty type")
	}
}

type outerError struct{ inner error }

func (o *outerError) Error() string     { return "outer: " + o.inner.Error() }
func (o *outerError) Unwrap() error     { return o.inner }
func (o *outerError) ErrorType() string { return "OuterType" }

func TestErrorTypeIgnoresOuterErrorTypeMethod(t *testing.T) {
	// An ErrorType() on an outer wrapper must not relabel the innermost type.
	err := &outerError{inner: errors.New("inner")}
	if got := errorType(err); got != "*errors.errorString" {
		t.Fatalf("outer ErrorType() should not win over innermost type, got %q", got)
	}
	// But ErrorType() on the innermost error is honored.
	innerTyped := fmt.Errorf("wrap: %w", &typedError{msg: "x"})
	if got := errorType(innerTyped); got != "custom.DomainError" {
		t.Fatalf("innermost ErrorType() should win, got %q", got)
	}
}

func TestFingerprintStableAcrossLineChanges(t *testing.T) {
	e1 := &Event{
		ErrorType: "*errors.errorString",
		Stacktrace: []Frame{
			{Function: "main.run", File: "/app/main.go", Line: 10, InApp: true},
			{Function: "main.helper", File: "/app/util.go", Line: 20, InApp: true},
		},
	}
	e2 := &Event{
		ErrorType: "*errors.errorString",
		Stacktrace: []Frame{
			{Function: "main.run", File: "/app/main.go", Line: 99, InApp: true}, // different line
			{Function: "main.helper", File: "/app/util.go", Line: 200, InApp: true},
		},
	}
	if fingerprint(e1) != fingerprint(e2) {
		t.Fatal("fingerprint must ignore line numbers")
	}

	e3 := &Event{ErrorType: "other", Stacktrace: e1.Stacktrace}
	if fingerprint(e1) == fingerprint(e3) {
		t.Fatal("different error types should not share a fingerprint")
	}
}

func TestFingerprintMessageFallback(t *testing.T) {
	a := &Event{ErrorMessage: "user 12345 not found"}
	b := &Event{ErrorMessage: "user 67890 not found"}
	if fingerprint(a) != fingerprint(b) {
		t.Fatal("messages differing only by numbers must group together")
	}
	c := &Event{ErrorMessage: "totally different"}
	if fingerprint(a) == fingerprint(c) {
		t.Fatal("unrelated messages must not collide")
	}
}

func TestNormalizeMessage(t *testing.T) {
	if got := normalizeMessage("id=42 retries=7"); got != "id=0 retries=0" {
		t.Fatalf("normalizeMessage = %q", got)
	}
}

func TestStacktraceCaptureAndInApp(t *testing.T) {
	frames := nestedCapture()
	if len(frames) == 0 {
		t.Fatal("expected captured frames")
	}
	var sawInApp bool
	for _, f := range frames {
		if f.InApp && strings.Contains(f.Function, "groundcover") {
			sawInApp = true
		}
	}
	if !sawInApp {
		t.Fatalf("expected an in-app frame under the module path, frames=%+v", frames)
	}
}

func nestedCapture() []Frame {
	return captureStack(0, 32, "github.com/groundcover-com/groundcover-go")
}

func TestIsInApp(t *testing.T) {
	const mod = "github.com/groundcover-com/groundcover-go"
	if !isInApp(mod+"/pkg.Func", "/src/pkg/file.go", mod) {
		t.Fatal("module frame should be in-app")
	}
	if isInApp(mod+"/vendor/x/y.Func", "/src/vendor/x/file.go", mod) {
		t.Fatal("vendored frame must not be in-app")
	}
	if isInApp("runtime.goexit", "/usr/local/go/src/runtime/asm.s", mod) {
		t.Fatal("runtime frame must not be in-app")
	}
}

func TestSanitizeValue(t *testing.T) {
	in := map[string]any{
		"s": "x",
		"n": 5,
		"f": 1.5,
		"b": true,
		"nested": map[string]any{
			"inner": []any{1, "two", false},
		},
	}
	out, ok := sanitizeValue(in, 0).(map[string]any)
	if !ok {
		t.Fatal("expected map output")
	}
	if out["s"] != "x" || out["n"] != 5 || out["f"] != 1.5 || out["b"] != true {
		t.Fatalf("scalars altered: %+v", out)
	}
	nested := out["nested"].(map[string]any)
	arr := nested["inner"].([]any)
	if len(arr) != 3 || arr[1] != "two" {
		t.Fatalf("nested slice wrong: %+v", arr)
	}
}

func TestSanitizeStringerAndError(t *testing.T) {
	if got := sanitizeValue(errors.New("e"), 0); got != "e" {
		t.Fatalf("error should stringify, got %v", got)
	}
}

func TestConfigDefaultsAndEndpoint(t *testing.T) {
	c := Config{DSN: "app.example.com"}.withDefaults()
	if c.MaxQueue != defaultMaxQueue || c.BatchSize != defaultBatchSize {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if got := (Config{DSN: "app.example.com"}).endpoint(); got != "https://app.example.com/json/rum" {
		t.Fatalf("endpoint scheme defaulting failed: %q", got)
	}
	if got := (Config{DSN: "http://h:8080/"}).endpoint(); got != "http://h:8080/json/rum" {
		t.Fatalf("endpoint trailing slash handling failed: %q", got)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{}).validate(); !errors.Is(err, ErrMissingDSN) {
		t.Fatalf("expected ErrMissingDSN, got %v", err)
	}
	if err := (Config{Disabled: true}).validate(); err != nil {
		t.Fatalf("disabled config should validate, got %v", err)
	}
}

func TestLastPathSegment(t *testing.T) {
	if got := lastPathSegment("github.com/foo/bar"); got != "bar" {
		t.Fatalf("got %q", got)
	}
	if got := lastPathSegment("main"); got != "main" {
		t.Fatalf("no-slash path should be returned as-is, got %q", got)
	}
}

func TestContextWithScopeNilContext(t *testing.T) {
	//nolint:staticcheck // explicitly exercising the nil-context fallback
	ctx := contextWithScope(nil, &Scope{})
	if ctx == nil {
		t.Fatal("contextWithScope(nil, ...) must return a usable context")
	}
}

func TestLevelSeverity(t *testing.T) {
	if LevelError.severityNumber() != 17 || LevelDebug.severityNumber() != 5 {
		t.Fatal("unexpected severity numbers")
	}
	if !LevelWarning.valid() || Level("bogus").valid() {
		t.Fatal("level validity wrong")
	}
}

func TestHMACHasher(t *testing.T) {
	h := NewHMACHasher([]byte("k"))
	if h.HashIdentity("") != "" {
		t.Fatal("empty input must map to empty output")
	}
	a, b := h.HashIdentity("user"), h.HashIdentity("user")
	if a != b {
		t.Fatal("hash must be deterministic")
	}
	if a == "user" {
		t.Fatal("hash must differ from input")
	}
}
