// Package endpoint resolves and validates the intake URLs the signal clients
// POST to. Each signal derives its endpoint from a DSN the same way, and a DSN
// that cannot produce a usable URL must fail at construction rather than as a
// delivery failure repeating for the life of the process.
package endpoint

import (
	"fmt"
	"net/url"
	"strings"
)

// Resolve returns the URL for path under dsn, adding the default https scheme
// when the DSN has none. The result is not guaranteed usable; call Validate.
func Resolve(dsn, path string) string {
	base := strings.TrimSpace(dsn)
	if base != "" && !hasScheme(base) {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/") + path
}

// Validate reports whether target is an absolute http(s) URL a request can be
// built from.
func Validate(target string) error {
	parsed, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("endpoint %q is not a valid URL: %w", target, err)
	}
	// url.Parse lowercases the scheme, so an upper-case DSN scheme is fine.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("endpoint %q must use http or https", target)
	}
	// Hostname strips a port, so this also rejects an authority that is only a
	// port or a separator.
	if parsed.Hostname() == "" {
		return fmt.Errorf("endpoint %q has no host", target)
	}
	return nil
}

// hasScheme reports whether target starts with "<scheme>://". A DSN such as
// "localhost:8080" deliberately does not count, so it still gets the default
// scheme, while "ftp://host" keeps its own scheme and is rejected by Validate.
func hasScheme(target string) bool {
	separator := strings.Index(target, "://")
	if separator <= 0 {
		return false
	}
	for i := range separator {
		char := target[i]
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9', char == '+', char == '-', char == '.':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
