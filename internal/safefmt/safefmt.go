// Package safefmt formats arbitrary caller values for telemetry without the
// risk of an unrecoverable crash.
//
// The SDK coerces unsupported attribute values to strings with fmt, which has
// no cycle detection: a value that can reach itself — most commonly a
// map[string]any holding itself, directly or inside a struct — makes fmt
// recurse until the process dies of a stack overflow. A stack overflow is a
// runtime fatal error, so recover cannot contain it and the SDK would take the
// host application down with it. This package screens a value first and
// reports cyclic input by type instead of formatting it.
package safefmt

import (
	"fmt"
	"reflect"
)

// maxScanDepth bounds the cycle scan. A value nested more deeply than this is
// treated as unsafe rather than scanned further; legitimate telemetry values
// are nowhere near the limit.
const maxScanDepth = 32

// String renders v the way fmt would, except that a value which can reach
// itself is reported as its type. Acyclic values format exactly as "%v".
func String(v any) string {
	if v == nil {
		return "<nil>"
	}
	if unsafeToFormat(reflect.ValueOf(v), make(map[reference]struct{}), 0) {
		return fmt.Sprintf("<cyclic %T>", v)
	}
	return fmt.Sprintf("%v", v)
}

// reference identifies one container instance during a scan.
type reference struct {
	pointer uintptr
	typ     reflect.Type
}

// unsafeToFormat reports whether rv is a cycle or is too deeply nested to
// prove otherwise. It walks the same kinds fmt descends into, including
// unexported struct fields, which fmt also prints. Values are only inspected,
// never converted with Interface, so unexported fields are safe to visit.
func unsafeToFormat(rv reflect.Value, seen map[reference]struct{}, depth int) bool {
	if depth > maxScanDepth {
		return true
	}
	switch rv.Kind() {
	case reflect.Map, reflect.Slice:
		if rv.IsNil() {
			return false
		}
		ref := reference{pointer: rv.Pointer(), typ: rv.Type()}
		if _, repeated := seen[ref]; repeated {
			return true
		}
		seen[ref] = struct{}{}
		defer delete(seen, ref)
	}

	switch rv.Kind() {
	case reflect.Map:
		for _, key := range rv.MapKeys() {
			if unsafeToFormat(key, seen, depth+1) || unsafeToFormat(rv.MapIndex(key), seen, depth+1) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range rv.Len() {
			if unsafeToFormat(rv.Index(i), seen, depth+1) {
				return true
			}
		}
	case reflect.Interface:
		if rv.IsNil() {
			return false
		}
		return unsafeToFormat(rv.Elem(), seen, depth+1)
	case reflect.Pointer:
		// fmt only dereferences a pointer at the top level, and only into a
		// container; below that it prints an address, so it cannot loop.
		if depth > 0 || rv.IsNil() {
			return false
		}
		switch elem := rv.Elem(); elem.Kind() {
		case reflect.Array, reflect.Slice, reflect.Struct, reflect.Map:
			return unsafeToFormat(elem, seen, depth+1)
		}
	case reflect.Struct:
		for i := range rv.NumField() {
			if unsafeToFormat(rv.Field(i), seen, depth+1) {
				return true
			}
		}
	}
	return false
}
