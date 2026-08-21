package groundcover

import (
	"encoding/json"
	"runtime/debug"
	"testing"
	"time"
)

func testResource() resource {
	return resource{
		serviceName: "checkout",
		env:         "prod",
		namespace:   "shop",
		cluster:     "c1",
		release:     "1.0.0",
		userAgent:   sdkName + "/1.0.0",
		startTime:   time.Unix(0, 0),
		attrs: map[string]string{
			"telemetry.sdk.name": sdkName,
			"host.name":          "host-1",
			"service.name":       "checkout", // session-level: must be excluded from metadata
			"k8s.namespace.name": "shop",     // session-level: must be excluded from metadata
		},
	}
}

func TestEncodeBatchShape(t *testing.T) {
	e := &Event{
		ID:           "evt-1",
		Timestamp:    time.Unix(1, 0),
		Type:         "exception",
		Level:        LevelError,
		ErrorHandled: true,
		ErrorType:    "*errors.errorString",
		ErrorMessage: "boom",
		Fingerprint:  "fp",
		User:         User{ID: "u-1", Organization: "acme"},
		Stacktrace:   []Frame{{Function: "main.run", File: "/app/main.go", Line: 10, InApp: true}},
		Attributes:   Attributes{"gc.test_id": "needle", "amount": 9.5},
	}

	body, err := encodeBatch([]*Event{e}, testResource())
	if err != nil {
		t.Fatalf("encodeBatch: %v", err)
	}
	var p wirePayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if p.SessionAttributes["service.name"] != "checkout" || p.SessionAttributes["env"] != "prod" {
		t.Fatalf("session spine wrong: %+v", p.SessionAttributes)
	}
	if _, ok := p.SessionAttributes["session_start_time"]; !ok {
		t.Fatal("session_start_time missing")
	}

	ev := p.Events[0]
	if ev.Timestamp != time.Unix(1, 0).UnixNano() {
		t.Fatalf("timestamp not nanoseconds: %d", ev.Timestamp)
	}
	if len(ev.Attributes.ErrorStacktrace) != 1 || ev.Attributes.ErrorStacktrace[0].Filename != "/app/main.go" {
		t.Fatalf("frames wrong: %+v", ev.Attributes.ErrorStacktrace)
	}

	md := ev.Attributes.ErrorMetadata
	if md["gc.test_id"] != "needle" || md["amount"] != 9.5 {
		t.Fatalf("custom attrs missing from metadata: %+v", md)
	}
	if md["user.id"] != "u-1" || md["user.organization"] != "acme" {
		t.Fatalf("identity missing from metadata: %+v", md)
	}
	if md["telemetry.sdk.name"] != sdkName || md["host.name"] != "host-1" {
		t.Fatalf("detailed resource attrs missing from metadata: %+v", md)
	}
	if _, present := md["service.name"]; present {
		t.Fatal("session-level key service.name must not be duplicated into metadata")
	}
	if _, present := md["k8s.namespace.name"]; present {
		t.Fatal("session-level key k8s.namespace.name must not be duplicated into metadata")
	}
	if md["level"] != string(LevelError) {
		t.Fatalf("level missing from metadata: %+v", md["level"])
	}
}

func TestEncodeBatchIncludesTitle(t *testing.T) {
	e := &Event{
		Type:         "exception",
		Timestamp:    time.Unix(1, 0),
		Level:        LevelError,
		ErrorType:    "*net.OpError",
		ErrorMessage: "connection refused",
		Title:        "*net.OpError: connection refused",
	}
	body, err := encodeBatch([]*Event{e}, testResource())
	if err != nil {
		t.Fatalf("encodeBatch: %v", err)
	}
	var p wirePayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := p.Events[0].Attributes.ErrorMetadata["gc.title"]; got != "*net.OpError: connection refused" {
		t.Fatalf("gc.title not on the wire: %v", got)
	}
}

func TestEncodedEventSizeAccountsForNestedAttributes(t *testing.T) {
	plain := &Event{Type: "exception", ErrorMessage: "x"}
	rich := &Event{
		Type:         "exception",
		ErrorMessage: "x",
		Stacktrace:   []Frame{{Function: "f", File: "a.go"}},
		Attributes: Attributes{
			"nested": map[string]any{"a": "value", "b": []any{1, 2, "three"}},
			"list":   []any{"x", "y"},
			"scalar": 12345,
			"empty":  nil,
		},
	}
	res := resource{}
	if encodedEventSize(rich, res) <= encodedEventSize(plain, res) {
		t.Fatalf("nested attributes should increase the encoded size (%d vs %d)",
			encodedEventSize(rich, res), encodedEventSize(plain, res))
	}
}

func TestEncodedEventSizeIsExactAndIncludesJSONEscaping(t *testing.T) {
	event := &Event{
		Type:         "exception",
		ErrorMessage: "contains\x00control",
		Attributes:   Attributes{"value": "\x00\x01\x02"},
	}
	res := resource{}
	body, err := encodeBatch([]*Event{event}, res)
	if err != nil {
		t.Fatalf("encodeBatch: %v", err)
	}
	if got := encodedEventSize(event, res); got != len(body) {
		t.Fatalf("encodedEventSize = %d, want exact body size %d", got, len(body))
	}
}

func TestVersionNonEmpty(t *testing.T) {
	if Version() == "" {
		t.Fatal("Version() must not be empty")
	}
}

func TestModulePathMatchesMainModule(t *testing.T) {
	// version.go lives in the module root package, so modulePath must equal
	// the module path exactly. Compare against the test binary's main module
	// from build info rather than a hardcoded string so the test keeps
	// working after a module rename or fork.
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Path == "" {
		t.Skip("no build info available")
	}
	if got := modulePath(); got != bi.Main.Path {
		t.Errorf("modulePath() = %q, want main module path %q", got, bi.Main.Path)
	}
}

func TestNormalizeModuleVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"v0.1.1", "0.1.1"},
		{"0.1.1", "0.1.1"},
		{"(devel)", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := normalizeModuleVersion(tc.in); got != tc.want {
			t.Errorf("normalizeModuleVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
