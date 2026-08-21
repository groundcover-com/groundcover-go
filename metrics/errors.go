package metrics

import (
	"errors"
	"fmt"
)

// ErrMissingDSN is returned by New when an enabled client has neither a DSN
// nor an Endpoint override.
var ErrMissingDSN = errors.New("metrics: DSN or Endpoint is required unless Disabled is true")

// ErrDSNAndEndpoint is returned by New when both DSN and the Endpoint
// override are set; set DSN or Endpoint, not both.
var ErrDSNAndEndpoint = errors.New("metrics: set DSN or Endpoint, not both")

// ErrAlreadyStarted is returned by Start when the background worker is
// already running.
var ErrAlreadyStarted = errors.New("metrics: client already started")

// DuplicateSeriesError reports two sources producing the same final series
// (same metric name and identical label set after fixed-label injection).
// By default the first occurrence is kept, the duplicate is dropped, and the
// error is reported through Push's aggregate error; with
// Config.FailOnDuplicateSeries the whole push fails with this error.
type DuplicateSeriesError struct {
	// Series is the final series identity (name plus sorted labels).
	Series string
	// FirstSource is the name of the source whose sample was kept.
	FirstSource string
	// DuplicateSource is the name of the source whose sample was dropped.
	DuplicateSource string
}

// Error implements error.
func (e *DuplicateSeriesError) Error() string {
	return fmt.Sprintf("metrics: duplicate series %s: first from source %q, duplicate from source %q",
		e.Series, e.FirstSource, e.DuplicateSource)
}

// StatusError is returned by Push when the backend answers with a non-2xx
// status. Body holds a bounded excerpt (at most 4KiB) of the response body.
// The ingestion key is never included.
type StatusError struct {
	// StatusCode is the HTTP response status.
	StatusCode int
	// Body is a bounded excerpt of the response body.
	Body string
}

// Error implements error.
func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("metrics: push failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("metrics: push failed with status %d: %s", e.StatusCode, e.Body)
}

// Values of LimitError.Limit, matching the Config field names.
const (
	limitMaxSeries          = "MaxSeries"
	limitMaxBodyBytes       = "MaxBodyBytes"
	limitMaxLabelsPerSeries = "MaxLabelsPerSeries"
	limitMaxLabelNameBytes  = "MaxLabelNameBytes"
	limitMaxLabelValueBytes = "MaxLabelValueBytes"
)

// LimitError is returned by Push when a configured payload limit is exceeded.
// The push is dropped; a background worker keeps running and retries on the
// next interval.
type LimitError struct {
	// Limit names the exceeded configuration field (e.g. "MaxSeries").
	Limit string
	// Max is the configured cap.
	Max int
	// Actual is the observed value that exceeded the cap.
	Actual int
}

// Error implements error.
func (e *LimitError) Error() string {
	return fmt.Sprintf("metrics: %s exceeded: %d > %d", e.Limit, e.Actual, e.Max)
}
