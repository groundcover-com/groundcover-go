// Command trainer is a live, end-to-end round-trip check for the groundcover Go
// SDK. It submits a synthetic error carrying a unique needle (gc.test_id) plus
// one custom attribute of each supported type, then polls the events API until
// the needle is found. It exits 0 on success and 1 on failure.
//
// It lives under _testdata with its own go.mod so its verification-only
// dependencies never enter the library's go.sum.
//
// Required environment:
//
//	GC_DSN            base ingestion origin we POST /json/rum to (the BYOC
//	                  ingestion endpoint, e.g. https://<tenant>.platform.grcv.io)
//	GC_INGESTION_KEY  write key (RUM-type)
//	GC_API_KEY        read key for the events query API
//	GC_API_URL        read API base (e.g. https://api.groundcover.com)
//	GC_BACKEND_ID     optional; sent as X-Backend-Id when set
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
)

const (
	flushTimeout = 10 * time.Second
	pollTimeout  = 90 * time.Second
	pollInterval = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "trainer: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("trainer: PASS")
}

func run() error {
	env, err := loadEnv()
	if err != nil {
		return err
	}

	testID := newID()
	fmt.Printf("trainer: gc.test_id=%s dsn=%s api=%s\n", testID, env.dsn, env.apiURL)

	if err := groundcover.Init(groundcover.Config{
		DSN:           env.dsn,
		IngestionKey:  env.ingestionKey,
		Workload:      "groundcover-go-trainer",
		Env:           "trainer",
		Release:       groundcover.Version,
		FlushInterval: time.Second,
	}); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	ctx := context.Background()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), flushTimeout)
		defer cancel()
		_ = groundcover.Close(closeCtx)
	}()

	ctx = groundcover.SetUser(ctx, groundcover.User{ID: "trainer-user", Organization: "groundcover"})
	groundcover.CaptureError(ctx, errors.New("synthetic trainer error "+testID), groundcover.WithAttributes(groundcover.Attributes{
		"gc.test_id":     testID,
		"trainer.string": "hello",
		"trainer.int":    7,
		"trainer.float":  3.14,
		"trainer.bool":   true,
	}))

	flushCtx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	if err := groundcover.Flush(flushCtx); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	fmt.Println("trainer: submitted, polling for read-back...")

	return pollForNeedle(env, testID)
}

// pollForNeedle queries the events API until the needle appears or the timeout
// elapses.
func pollForNeedle(env environment, testID string) error {
	deadline := time.Now().Add(pollTimeout)
	gcql := fmt.Sprintf(`category:rum type:exception error_metadata.gc.test_id:"%s"`, testID)

	var lastErr error
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		count, err := searchEvents(env, gcql)
		if err != nil {
			lastErr = err
			fmt.Printf("trainer: attempt %d query error: %v\n", attempt, err)
		} else {
			fmt.Printf("trainer: attempt %d matched %d event(s)\n", attempt, count)
			if count > 0 {
				return nil
			}
		}
		time.Sleep(pollInterval)
	}
	if lastErr != nil {
		return fmt.Errorf("needle not found before timeout (last error: %w)", lastErr)
	}
	return errors.New("needle not found before timeout")
}
