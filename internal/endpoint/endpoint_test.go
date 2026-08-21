package endpoint_test

import (
	"errors"
	"net/http"
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
		{"HTTPS://host.example", "https://host.example/json/logs"},
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
	rejected := []string{
		"ftp://host.example", "https://", "://host", "http://",
		"https://host.example?tenant=x",
		"https://host.example#fragment",
		"https://user:password@host.example",
	}
	for _, dsn := range rejected {
		if err := endpoint.ValidateDSN(dsn); err == nil {
			t.Errorf("ValidateDSN(%q) = nil, want an error", dsn)
		}
	}
	accepted := []string{"host.example", "https://host.example", "localhost:8080", "HTTP://host.example"}
	for _, dsn := range accepted {
		if err := endpoint.ValidateDSN(dsn); err != nil {
			t.Errorf("ValidateDSN(%q) = %v", dsn, err)
			continue
		}
		resolved := endpoint.Resolve(dsn, "/api/v1/import/prometheus")
		if err := endpoint.Validate(resolved); err != nil {
			t.Errorf("DSN %q resolved to %q: %v", dsn, resolved, err)
		}
	}
}

func TestSafeRedirectPolicy(t *testing.T) {
	for _, tc := range []struct {
		name        string
		from, to    string
		wantAllowed bool
	}{
		{"identical origin", "https://host/path", "https://host/other", true},
		{"TLS upgrade", "http://host/path", "https://host/path", true},
		{"TLS downgrade", "https://host/path", "http://host/path", false},
		{"other host", "https://host/path", "https://elsewhere/path", false},
		{"subdomain", "https://host/path", "https://child.host/path", false},
		{"other port", "https://host:443/path", "https://host:8443/path", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous, err := http.NewRequest(http.MethodGet, tc.from, nil)
			if err != nil {
				t.Fatalf("request %q: %v", tc.from, err)
			}
			next, err := http.NewRequest(http.MethodGet, tc.to, nil)
			if err != nil {
				t.Fatalf("request %q: %v", tc.to, err)
			}
			err = endpoint.SafeRedirectPolicy(nil)(next, []*http.Request{previous})
			if gotAllowed := err == nil; gotAllowed != tc.wantAllowed {
				t.Fatalf("redirect %q -> %q error = %v, allowed=%v want %v",
					tc.from, tc.to, err, gotAllowed, tc.wantAllowed)
			}
		})
	}
}

func TestSafeRedirectPolicyPreservesCallerPolicy(t *testing.T) {
	called := false
	policy := endpoint.SafeRedirectPolicy(func(*http.Request, []*http.Request) error {
		called = true
		return http.ErrUseLastResponse
	})
	previous, _ := http.NewRequest(http.MethodGet, "https://host/one", nil)
	next, _ := http.NewRequest(http.MethodGet, "https://host/two", nil)
	if err := policy(next, []*http.Request{previous}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("policy error = %v, want ErrUseLastResponse", err)
	}
	if !called {
		t.Fatal("caller policy was not invoked")
	}
}
