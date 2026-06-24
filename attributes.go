package groundcover

import (
	"fmt"
	"reflect"
)

// Attributes is a bag of custom, caller-supplied data attached to an event.
// Nested maps and slices are allowed. Values are kept with their natural JSON
// type so the backend can route them (strings/bools into string columns,
// numbers into numeric columns). The gc.* key namespace is reserved for the SDK.
type Attributes map[string]any

// maxAttrDepth bounds attribute nesting to avoid pathological or cyclic input.
const maxAttrDepth = 10

// clone returns a deep-ish copy of the attributes so later caller mutations do
// not affect an already-enqueued event.
func (a Attributes) clone() Attributes {
	if a == nil {
		return nil
	}
	out := make(Attributes, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}

// merge copies all entries from other into a, overwriting existing keys.
func (a Attributes) merge(other Attributes) {
	for k, v := range other {
		a[k] = v
	}
}

// sanitizeValue converts an arbitrary value into a JSON-friendly form, bounding
// recursion depth and coercing unsupported kinds to strings. It is the single
// place that decides how Go values appear on the wire.
func sanitizeValue(v any, depth int) any {
	if v == nil {
		return nil
	}
	if depth >= maxAttrDepth {
		return fmt.Sprintf("%v", v)
	}
	switch val := v.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return val
	case error:
		return val.Error()
	case fmt.Stringer:
		return val.String()
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[k] = sanitizeValue(item, depth+1)
		}
		return out
	case Attributes:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[k] = sanitizeValue(item, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = sanitizeValue(item, depth+1)
		}
		return out
	default:
		return sanitizeReflect(val, depth)
	}
}

// sanitizeReflect handles slices, arrays, and maps with non-any element types,
// falling back to a string for everything else.
func sanitizeReflect(v any, depth int) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range rv.Len() {
			out[i] = sanitizeValue(rv.Index(i).Interface(), depth+1)
		}
		return out
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		for _, key := range rv.MapKeys() {
			out[fmt.Sprintf("%v", key.Interface())] = sanitizeValue(rv.MapIndex(key).Interface(), depth+1)
		}
		return out
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return sanitizeValue(rv.Elem().Interface(), depth)
	default:
		return fmt.Sprintf("%v", v)
	}
}
