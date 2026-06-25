package groundcover

import (
	"reflect"
	"strings"
)

// errorTyper allows an error to advertise its semantic type, taking precedence
// over the reflected concrete type. This mirrors OTel semconv's ErrorType logic.
type errorTyper interface {
	ErrorType() string
}

// innermostError unwraps err following both single (Unwrap() error) and multi
// (Unwrap() []error, e.g. errors.Join) wrapping, returning the innermost
// meaningful error. For a multi-wrap it follows the first branch.
func innermostError(err error) error {
	for {
		switch u := err.(type) { //nolint:errorlint // intentional single-level type switch for unwrap
		case interface{ Unwrap() error }:
			next := u.Unwrap()
			if next == nil {
				return err
			}
			err = next
		case interface{ Unwrap() []error }:
			branches := u.Unwrap()
			if len(branches) == 0 {
				return err
			}
			err = branches[0]
		default:
			return err
		}
	}
}

// errorType returns the type string for err, using the innermost wrapped error:
//  1. an ErrorType() string method if present;
//  2. otherwise the reflected concrete type (e.g. "*net.OpError").
//
// This ports the OTel semconv ErrorType behavior (Apache-2.0).
func errorType(err error) string {
	if err == nil {
		return ""
	}
	inner := innermostError(err)

	// Both the ErrorType() override and the reflected type are taken from the
	// innermost error, so an outer wrapper cannot relabel the type.
	if typer, ok := inner.(errorTyper); ok {
		if t := typer.ErrorType(); t != "" {
			return t
		}
	}

	t := reflect.TypeOf(inner)
	if t == nil {
		return "error"
	}
	return typeName(t)
}

// typeName renders a reflect.Type as a pointer-qualified, package-qualified name
// such as "*net.OpError" or "errors.errorString".
func typeName(t reflect.Type) string {
	stars := 0
	for t.Kind() == reflect.Pointer {
		stars++
		t = t.Elem()
	}
	prefix := strings.Repeat("*", stars)
	name := t.Name()
	if name == "" {
		// Anonymous type: fall back to the full string form.
		return prefix + strings.TrimPrefix(t.String(), "*")
	}
	if pkg := t.PkgPath(); pkg != "" {
		return prefix + lastPathSegment(pkg) + "." + name
	}
	return prefix + name
}

// lastPathSegment returns the final element of a slash-separated import path.
func lastPathSegment(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
