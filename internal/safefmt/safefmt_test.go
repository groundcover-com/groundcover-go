package safefmt_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/groundcover-com/groundcover-go/internal/safefmt"
)

type selfPointer struct{ next *selfPointer }

type holdsMap struct{ data map[string]any }

func TestAcyclicValuesFormatLikeFmt(t *testing.T) {
	loop := &selfPointer{}
	loop.next = loop
	for _, value := range []any{
		42,
		"text",
		3.5,
		[]int{1, 2, 3},
		map[string]int{"a": 1},
		struct {
			A int
			B string
		}{1, "b"},
		// fmt prints a nested pointer as an address, so this cannot loop.
		*loop,
	} {
		if got, want := safefmt.String(value), fmt.Sprintf("%v", value); got != want {
			t.Errorf("String(%T) = %q, want %q", value, got, want)
		}
	}
}

func TestCyclicMapIsReportedByType(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	got := safefmt.String(cyclic)
	if !strings.HasPrefix(got, "<cyclic ") {
		t.Fatalf("String(cyclic map) = %q", got)
	}
}

func TestCycleReachableThroughStructAndSliceIsReported(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic

	for name, value := range map[string]any{
		"struct field": holdsMap{data: cyclic},
		"slice":        []any{cyclic},
		"nested map":   map[string]any{"outer": map[string]any{"inner": cyclic}},
		"pointer":      &cyclic,
	} {
		if got := safefmt.String(value); !strings.HasPrefix(got, "<cyclic ") {
			t.Errorf("%s: String = %q, want a cyclic report", name, got)
		}
	}
}

func TestExcessivelyDeepValueIsReported(t *testing.T) {
	nested := map[string]any{"leaf": 1}
	for range 100 {
		nested = map[string]any{"outer": nested}
	}
	if got := safefmt.String(nested); !strings.HasPrefix(got, "<cyclic ") {
		t.Fatalf("String(deeply nested map) = %q, want a bounded report", got)
	}
}

// A struct holding a pointer to itself is what fmt handles by printing an
// address, so it must keep formatting normally rather than being reported.
func TestSelfPointerStructIsNotReported(t *testing.T) {
	loop := &selfPointer{}
	loop.next = loop
	if got := safefmt.String(loop); strings.HasPrefix(got, "<cyclic ") {
		t.Fatalf("String(self-pointer) = %q, want an address", got)
	}
}

func TestNilIsFormatted(t *testing.T) {
	if got := safefmt.String(nil); got != "<nil>" {
		t.Fatalf("String(nil) = %q", got)
	}
}
