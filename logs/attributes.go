package logs

import (
	"fmt"
	"reflect"
)

// Attributes is a bag of structured fields attached to a log record.
type Attributes map[string]any

const maxAttrDepth = 10

func merge(dst, src Attributes) Attributes {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(Attributes, len(src))
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func sanitize(a Attributes) Attributes {
	if len(a) == 0 {
		return nil
	}
	out := make(Attributes, len(a))
	for k, v := range a {
		out[k] = sanitizeValue(v, 0)
	}
	return out
}

func sanitizeValue(v any, depth int) any {
	if v == nil {
		return nil
	}
	if depth >= maxAttrDepth {
		return fmt.Sprintf("%v", v)
	}
	switch value := v.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return value
	case error:
		return value.Error()
	case fmt.Stringer:
		return value.String()
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, item := range value {
			out[k] = sanitizeValue(item, depth+1)
		}
		return out
	case Attributes:
		out := make(map[string]any, len(value))
		for k, item := range value {
			out[k] = sanitizeValue(item, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = sanitizeValue(item, depth+1)
		}
		return out
	default:
		return sanitizeReflect(value, depth)
	}
}

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
