package groundcover

import (
	"encoding/json"
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

func TestEstimateSizeAccountsForNestedAttributes(t *testing.T) {
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
	if estimateSize(rich) <= estimateSize(plain) {
		t.Fatalf("nested attributes should increase the estimate (%d vs %d)", estimateSize(rich), estimateSize(plain))
	}
}

func TestEstimateValueSize(t *testing.T) {
	if estimateValueSize("hello") != len("hello") {
		t.Fatal("string size should be its length")
	}
	if estimateValueSize(true) != scalarSizeEstimate {
		t.Fatal("scalar size should be the scalar estimate")
	}
	nested := map[string]any{"k": "vv"}
	if got := estimateValueSize(nested); got <= len("k") {
		t.Fatalf("map size should include keys and values, got %d", got)
	}
	if got := estimateValueSize(Attributes{"k": "vv"}); got <= len("k") {
		t.Fatalf("Attributes size should include keys and values, got %d", got)
	}
	if estimateValueSize([]any{"a", "bb"}) < 2+len("a")+len("bb") {
		t.Fatal("slice size should sum element sizes")
	}
}

func TestVersionNonEmpty(t *testing.T) {
	if Version() == "" {
		t.Fatal("Version() must not be empty")
	}
}

func TestModulePathFromGoMod(t *testing.T) {
	if got := modulePathFromGoMod(); got == "" {
		t.Fatal("module path must not be empty")
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

func TestStripLineComment(t *testing.T) {
	tests := []struct{ in, want string }{
		{"module example.com/foo", "module example.com/foo"},
		{"module example.com/foo // comment", "module example.com/foo "},
		{"// only comment", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := stripLineComment(tc.in); got != tc.want {
			t.Errorf("stripLineComment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
