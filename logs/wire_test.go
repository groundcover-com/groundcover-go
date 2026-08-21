package logs

import "testing"

func TestFlattenExplicitDottedKeyWins(t *testing.T) {
	attributes := Attributes{
		"a":   map[string]any{"b": 1},
		"a.b": 2,
	}
	for range 100 {
		record := buildWireRecord(&Record{Attributes: attributes}, resource{})
		if got := record["a.b"]; got != 2 {
			t.Fatalf("a.b = %#v, want explicit value 2", got)
		}
	}
}
