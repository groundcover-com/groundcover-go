package groundcover_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
)

// The examples below use a disabled client so they run hermetically (no
// network). In production, configure a real DSN and ingestion key.

func ExampleInit() {
	// Zero-config in-cluster: workload/env/release/pod come from the Downward
	// API environment. Here we disable the client for a hermetic example.
	if err := groundcover.Init(groundcover.Config{Disabled: true}); err != nil {
		panic(err)
	}
	defer func() { _ = groundcover.Close(context.Background()) }()

	fmt.Println("initialized")
	// Output: initialized
}

func ExampleCaptureError() {
	_ = groundcover.Init(groundcover.Config{Disabled: true})

	if err := errors.New("charge failed"); err != nil {
		groundcover.CaptureError(context.Background(), err)
	}
	fmt.Println("captured")
	// Output: captured
}

func ExampleSetUser() {
	_ = groundcover.Init(groundcover.Config{Disabled: true})

	ctx := groundcover.SetUser(context.Background(), groundcover.User{ID: "u-123", Organization: "acme"})
	groundcover.CaptureError(ctx, errors.New("boom"), groundcover.WithAttributes(groundcover.Attributes{
		"order_id": "o-9",
		"amount":   42.5,
		"is_retry": true,
	}))
	fmt.Println("captured with user and attributes")
	// Output: captured with user and attributes
}

func ExampleCaptureMessage() {
	_ = groundcover.Init(groundcover.Config{Disabled: true})
	groundcover.CaptureMessage(context.Background(), "falling back to stale cache", groundcover.LevelWarning)
	fmt.Println("noticed")
	// Output: noticed
}

func ExampleFlush() {
	_ = groundcover.Init(groundcover.Config{Disabled: true})

	// Short-lived job: bound the flush before exit.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = groundcover.Flush(ctx)
	fmt.Println("flushed")
	// Output: flushed
}

func ExampleClient() {
	// An explicit client is useful for tests and multi-config setups.
	client, err := groundcover.New(groundcover.Config{Disabled: true})
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close(context.Background()) }()

	client.CaptureError(context.Background(), errors.New("explicit client error"))
	fmt.Println(client.Stats().Captured)
	// Output: 0
}
