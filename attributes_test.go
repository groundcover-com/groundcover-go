package groundcover

import (
	"net"
	"testing"
)

func TestAttributesCloneAndMergeIndependence(t *testing.T) {
	orig := Attributes{"a": 1}
	cp := orig.clone()
	cp["a"] = 2
	cp["b"] = 3
	if orig["a"] != 1 {
		t.Fatalf("clone mutated original: %v", orig["a"])
	}
	if _, ok := orig["b"]; ok {
		t.Fatal("clone leaked key back to original")
	}

	orig.merge(Attributes{"a": 9, "c": 4})
	if orig["a"] != 9 || orig["c"] != 4 {
		t.Fatalf("merge failed: %+v", orig)
	}

	var nilAttrs Attributes
	if nilAttrs.clone() != nil {
		t.Fatal("clone of nil attributes should be nil")
	}
}

type stringerType struct{ s string }

func (s stringerType) String() string { return s.s }

func TestSanitizeValueTypes(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"string", "x", "x"},
		{"int", 5, 5},
		{"float", 1.5, 1.5},
		{"bool", true, true},
		{"nil", nil, nil},
		{"stringer", stringerType{s: "hi"}, "hi"},
		{"error", net.ErrClosed, net.ErrClosed.Error()},
	}
	for _, tc := range cases {
		if got := sanitizeValue(tc.in, 0); got != tc.want {
			t.Fatalf("%s: got %v (%T), want %v", tc.name, got, got, tc.want)
		}
	}
}

func TestSanitizeReflectTypedCollections(t *testing.T) {
	// Typed slice -> []any.
	gotSlice, ok := sanitizeValue([]int{1, 2, 3}, 0).([]any)
	if !ok || len(gotSlice) != 3 || gotSlice[0] != 1 {
		t.Fatalf("typed slice not normalized: %#v", gotSlice)
	}

	// Typed map -> map[string]any with stringified keys.
	gotMap, ok := sanitizeValue(map[string]int{"k": 7}, 0).(map[string]any)
	if !ok || gotMap["k"] != 7 {
		t.Fatalf("typed map not normalized: %#v", gotMap)
	}

	// Non-nil pointer is dereferenced.
	n := 42
	if got := sanitizeValue(&n, 0); got != 42 {
		t.Fatalf("pointer not dereferenced: %v", got)
	}

	// Nil pointer -> nil.
	var np *int
	if got := sanitizeValue(np, 0); got != nil {
		t.Fatalf("nil pointer should be nil, got %v", got)
	}

	// Unknown kind falls back to a string.
	if got := sanitizeValue(make(chan int), 0); got == nil {
		t.Fatal("channel should stringify, got nil")
	}
}

func TestSanitizeValueDepthLimit(t *testing.T) {
	// Build a map nested beyond maxAttrDepth; the deep node collapses to a string.
	deep := map[string]any{"v": "leaf"}
	for range maxAttrDepth + 2 {
		deep = map[string]any{"next": deep}
	}
	out := sanitizeValue(deep, 0)
	if out == nil {
		t.Fatal("expected a sanitized value")
	}
}
