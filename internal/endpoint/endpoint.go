// Package endpoint resolves and validates the intake URLs the signal clients
// POST to. Each signal derives its endpoint from a DSN the same way, and a DSN
// that cannot produce a usable URL must fail at construction rather than as a
// delivery failure repeating for the life of the process.
package endpoint

import (
	"errors"
	"fmt"
	"net/http"
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
	parsed, err := url.Parse(base)
	if err != nil {
		// Validation reports the useful error before callers resolve a DSN.
		return strings.TrimRight(base, "/") + path
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	return parsed.String()
}

// ValidateDSN reports whether dsn is a usable base URL. Query parameters,
// fragments, and userinfo are refused: the SDK owns the intake path, appending
// it to a raw DSN with any of those components silently targets the wrong URL,
// and credentials in userinfo must not be propagated.
func ValidateDSN(dsn string) error {
	base := strings.TrimSpace(dsn)
	if base != "" && !hasScheme(base) {
		base = "https://" + base
	}
	if err := Validate(base); err != nil {
		return err
	}
	parsed, _ := url.Parse(base)
	switch {
	case parsed.User != nil:
		return fmt.Errorf("DSN %q must not contain userinfo", dsn)
	case parsed.RawQuery != "":
		return fmt.Errorf("DSN %q must not contain a query", dsn)
	case parsed.Fragment != "":
		return fmt.Errorf("DSN %q must not contain a fragment", dsn)
	default:
		return nil
	}
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

// SafeRedirectPolicy returns an HTTP redirect policy that preserves a caller's
// policy while refusing to forward credentials across hosts or from HTTPS to
// HTTP. Identical origins and same-host HTTP-to-HTTPS upgrades are allowed.
func SafeRedirectPolicy(previous func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && !sameOrigin(via[0].URL, req.URL) {
			return errors.New("refusing unsafe redirect")
		}
		if previous != nil {
			return previous(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

func sameOrigin(left, right *url.URL) bool {
	if !strings.EqualFold(left.Host, right.Host) {
		return false
	}
	if strings.EqualFold(left.Scheme, right.Scheme) {
		return true
	}
	return strings.EqualFold(left.Scheme, "http") && strings.EqualFold(right.Scheme, "https")
}
