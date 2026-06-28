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
	"time"

	gc "github.com/groundcover-com/groundcover-go"
)

const (
	flushTimeout = 10 * time.Second
	pollTimeout  = 90 * time.Second
	pollInterval = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "roundtrip: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("roundtrip: PASS")
}

func run() error {
	env, err := loadEnv()
	if err != nil {
		return err
	}

	testID := newID()
	fmt.Printf("roundtrip: gc.test_id=%s dsn=%s api=%s\n", testID, env.dsn, env.apiURL)

	if err := gc.Init(gc.Config{
		DSN:           env.dsn,
		IngestionKey:  env.ingestionKey,
		ServiceName:   "groundcover-go-roundtrip",
		Env:           "examples",
		Release:       gc.Version,
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

	event, err := pollForNeedle(env, testID)
	if err != nil {
		return err
	}
	fmt.Println("roundtrip: fetched event from the events API:")
	fmt.Println(prettyJSON(event))

	if err := verifyEvent(event, testID); err != nil {
		return fmt.Errorf("read-back content mismatch: %w", err)
	}
	fmt.Println("roundtrip: read-back content verified")
	return nil
}

// pollForNeedle queries the events API until the needle appears (returning the
// first matching event) or the timeout elapses.
func pollForNeedle(env environment, testID string) ([]byte, error) {
	deadline := time.Now().Add(pollTimeout)
	gcql := fmt.Sprintf(`category:rum type:exception error_metadata.gc.test_id:"%s"`, testID)

	var lastErr error
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		events, err := searchEvents(env, gcql)
		if err != nil {
			lastErr = err
			fmt.Printf("roundtrip: attempt %d query error: %v\n", attempt, err)
		} else {
			fmt.Printf("roundtrip: attempt %d matched %d event(s)\n", attempt, len(events))
			if len(events) > 0 {
				return events[0], nil
			}
		}
		time.Sleep(pollInterval)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("needle not found before timeout (last error: %w)", lastErr)
	}
	return nil, errors.New("needle not found before timeout")
}
