package endpoint_test

import (
	"testing"

	"github.com/groundcover-com/groundcover-go/internal/endpoint"
)

func TestResolve(t *testing.T) {
	for _, tt := range []struct {
		dsn  string
		want string
	}{
		{"https://host.example", "https://host.example/json/logs"},
		{"https://host.example/", "https://host.example/json/logs"},
		{"host.example", "https://host.example/json/logs"},
		// A host:port DSN is not a scheme, so it still gets the default one.
		{"localhost:8080", "https://localhost:8080/json/logs"},
		{"http://host.example", "http://host.example/json/logs"},
		// A scheme is case-insensitive and must not be prepended twice.
		{"HTTPS://host.example", "HTTPS://host.example/json/logs"},
		{"  https://host.example  ", "https://host.example/json/logs"},
		{"", "/json/logs"},
	} {
		if got := endpoint.Resolve(tt.dsn, "/json/logs"); got != tt.want {
			t.Errorf("Resolve(%q) = %q, want %q", tt.dsn, got, tt.want)
		}
	}
}

func TestValidate(t *testing.T) {
	valid := []string{
		"https://host.example/json/logs",
		"http://host.example:8080/json/logs",
		"HTTPS://host.example/json/logs",
	}
	for _, target := range valid {
		if err := endpoint.Validate(target); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", target, err)
		}
	}

	invalid := []string{
		"",
		"/json/logs",
		"ftp://host.example/json/logs",
		"https:/json/logs",
		"host.example/json/logs",
		"https://host.example/\x7f",
	}
	for _, target := range invalid {
		if err := endpoint.Validate(target); err == nil {
			t.Errorf("Validate(%q) = nil, want an error", target)
		}
	}
}

// The two together are what the clients rely on: a DSN either resolves to a
// usable URL or is reported at construction.
func TestResolveThenValidate(t *testing.T) {
	rejected := []string{"ftp://host.example", "https://", "://host", "http://"}
	for _, dsn := range rejected {
		resolved := endpoint.Resolve(dsn, "/api/v1/import/prometheus")
		if err := endpoint.Validate(resolved); err == nil {
			t.Errorf("DSN %q resolved to %q and passed validation", dsn, resolved)
		}
	}
	accepted := []string{"host.example", "https://host.example", "localhost:8080", "HTTP://host.example"}
	for _, dsn := range accepted {
		resolved := endpoint.Resolve(dsn, "/api/v1/import/prometheus")
		if err := endpoint.Validate(resolved); err != nil {
			t.Errorf("DSN %q resolved to %q: %v", dsn, resolved, err)
		}
	}
}
