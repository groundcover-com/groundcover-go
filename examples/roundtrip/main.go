// Command roundtrip is the end-to-end example and CI verifier for the
// groundcover Go SDK. It initializes the SDK, captures a synthetic error
// carrying a unique needle (gc.test_id) plus one custom attribute of each
// supported type, then queries the events API until the event is found and
// prints what it fetched back. It exits 0 on success and 1 on failure.
//
// Run it with the standard environment:
//
//	GC_DSN            base ingestion origin we POST /json/rum to (the BYOC
//	                  ingestion endpoint, e.g. https://<tenant>.platform.grcv.io)
//	GC_INGESTION_KEY  write key (RUM-type)
//	GC_API_KEY        read key for the events query API
//	GC_API_URL        read API base (e.g. https://api.groundcover.com)
//	GC_BACKEND_ID     optional; sent as X-Backend-Id when set
//
//	go run .
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
	"github.com/groundcover-com/groundcover-go/examples/internal/e2e"
)

const flushTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("roundtrip: PASS")
}

func run() error {
	env, err := e2e.LoadEnv()
	if err != nil {
		return err
	}

	testID := e2e.NewID()
	fmt.Printf("roundtrip: gc.test_id=%s dsn=%s api=%s\n", testID, env.DSN, env.APIURL)

	if err := gc.Init(gc.Config{
		DSN:          env.DSN,
		IngestionKey: env.IngestionKey,
		ServiceName:  "groundcover-go-roundtrip", // pragma: allowlist secret
		Env:          "examples",
		// Release is the application's version (releaseId / service.version);
		// the SDK version travels separately in telemetry.sdk.version.
		Release:       "1.0.0",
		FlushInterval: time.Second,
	}); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	defer func() { _ = gc.CloseTimeout(flushTimeout) }()

	ctx := gc.SetUser(context.Background(), gc.User{ID: "roundtrip-user", Organization: "groundcover"})
	gc.CaptureError(ctx, errors.New("synthetic roundtrip error "+testID), gc.WithAttributes(gc.Attributes{
		"gc.test_id":     testID,
		"example.string": "hello",
		"example.int":    7,
		"example.float":  3.14,
		"example.bool":   true,
	}))

	if err := gc.FlushTimeout(flushTimeout); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	fmt.Println("roundtrip: submitted, polling for read-back...")

	event, err := e2e.PollForNeedle(env, testID)
	if err != nil {
		return err
	}
	fmt.Println("roundtrip: fetched event from the events API:")
	fmt.Println(e2e.PrettyJSON(event))

	if err := verifyEvent(event, testID); err != nil {
		return fmt.Errorf("read-back content mismatch: %w", err)
	}
	fmt.Println("roundtrip: read-back content verified")
	return nil
}

// verifyEvent checks that the fetched event carries the fields the SDK sent:
// type/category, the needle, the readable title, handled flag, identity, and one
// custom attribute of each type. It validates the wire contract end-to-end, not
// just that an event exists.
func verifyEvent(raw []byte, testID string) error {
	e, err := e2e.DecodeStoredEvent(raw)
	if err != nil {
		return err
	}
	if e.Type != "exception" || e.Category != "rum" {
		return fmt.Errorf("type/category = %q/%q, want exception/rum", e.Type, e.Category)
	}
	checks := map[string]string{
		"error_metadata.gc.test_id":     testID,
		"error_handled":                 "true",
		"error_metadata.user.id":        "roundtrip-user",
		"error_metadata.example.string": "hello",
		"error_metadata.example.bool":   "true",
	}
	for k, want := range checks {
		if got := e.StringAttributes[k]; got != want {
			return fmt.Errorf("string_attributes[%q] = %q, want %q", k, got, want)
		}
	}
	if title := e.StringAttributes["error_metadata.gc.title"]; !strings.Contains(title, "synthetic roundtrip error") {
		return fmt.Errorf("error_metadata.gc.title = %q, missing message", title)
	}
	// Numbers are also routed to the float bucket.
	for k, want := range map[string]float64{
		"error_metadata.example.float": 3.14,
		"error_metadata.example.int":   7,
	} {
		if got := e.FloatAttributes[k]; got != want {
			return fmt.Errorf("float_attributes[%q] = %v, want %v", k, got, want)
		}
	}
	return nil
}
