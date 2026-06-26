// Command before-send shows how to keep sensitive data out of groundcover and
// control volume, using the two configuration hooks that run on every event:
//
//   - BeforeSend: the single chokepoint to scrub/redact, drop, or sample events.
//     Return nil to drop an event; mutate and return it to scrub.
//   - Hasher: pseudonymizes user.id / user.email with a keyed HMAC so identities
//     are correlatable but not reversible.
//
// Run with no backend to see the redacted, sampled output via Debug:
//
//	go run ./before-send
//
// Or against groundcover (the HMAC key should come from your secret store):
//
//	GC_DSN=https://<tenant>.platform.grcv.io GC_INGESTION_KEY=<key> GC_PII_KEY=$SECRET go run ./before-send
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
)

// attrAuthorization is the bearer-token attribute key; centralized so the example
// and its scrubber stay in sync.
const attrAuthorization = "authorization"

func main() {
	piiKey := os.Getenv("GC_PII_KEY")
	if piiKey == "" {
		piiKey = "dev-only-not-a-secret"
	}

	cfg := gc.Config{
		DSN:          os.Getenv("GC_DSN"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "before-send-example",
		Env:          "examples",

		// Pseudonymize identity fields. With a stable key, the same user maps to
		// the same opaque value across events without exposing the raw id/email.
		Hasher: gc.NewHMACHasher([]byte(piiKey)),

		// The scrub/sample chokepoint, applied to every event. We drop one known
		// high-volume notice by its (caller-set) fingerprint.
		BeforeSend: scrubber(map[string]bool{"healthcheck-blip": true}),
	}
	if cfg.DSN == "" {
		cfg.DSN = "https://local.invalid"
		cfg.Debug = true                                 // print the post-scrub, post-hash event to stderr
		cfg.HTTPClient = &http.Client{Transport: drop{}} // truly offline: no network attempts
		fmt.Fprintln(os.Stderr, "no GC_DSN set: running in Debug mode (events printed to stderr, not delivered)")
	}
	if err := gc.Init(cfg); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	ctx := gc.SetUser(context.Background(), gc.User{
		ID:    "user-9007",
		Email: "alice@example.com", // hashed by the Hasher before it leaves the process
	})

	// 1) An error whose message contains an email — redacted by BeforeSend.
	gc.CaptureError(ctx, errors.New("failed to email receipt to bob@customer.com"))

	// 2) An error carrying secret attributes — the secrets are removed.
	gc.CaptureError(ctx, errors.New("payment gateway rejected charge"),
		gc.WithAttributes(gc.Attributes{
			attrAuthorization: "Bearer sk_live_supersecret",
			"card_number":     "4242 4242 4242 4242",
			"gateway":         "stripe", // kept
		}))

	// 3) A high-volume, low-value notice — dropped by its caller-set fingerprint.
	gc.CaptureMessage(ctx, "healthcheck blip", gc.LevelInfo, gc.WithFingerprint("healthcheck-blip"))

	_ = gc.FlushTimeout(5 * time.Second)

	s := gc.GlobalStats()
	fmt.Printf("done — captured=%d dropped(before_send=%d)\n", s.Captured, s.DroppedBeforeSend)
}

// scrubber returns a BeforeSend hook that:
//   - drops events whose (caller-set) fingerprint is in noisy (returns nil),
//   - redacts emails from the error message,
//   - removes secret attributes.
//
// Ordering matters: BeforeSend runs BEFORE the Hasher, so e.User here is the RAW
// identity — the Hasher pseudonymizes it afterward for the delivered/debug
// payload. Don't log e.User from inside a hook expecting it to be pseudonymized.
// Likewise the SDK computes a fingerprint only after BeforeSend, so the noisy
// check matches only fingerprints the caller set explicitly via WithFingerprint.
func scrubber(noisy map[string]bool) func(*gc.Event) *gc.Event {
	emailRE := regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	return func(e *gc.Event) *gc.Event {
		if e.Fingerprint != "" && noisy[e.Fingerprint] {
			return nil // drop entirely
		}
		e.ErrorMessage = emailRE.ReplaceAllString(e.ErrorMessage, "[redacted-email]")
		for _, k := range []string{attrAuthorization, "card_number", "password", "ssn"} {
			delete(e.Attributes, k)
		}
		return e
	}
}

// drop is an HTTP transport that accepts and discards everything, so the no-DSN
// demo is genuinely offline (no network attempts, no shutdown retries).
type drop struct{}

func (drop) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody, Header: make(http.Header)}, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
