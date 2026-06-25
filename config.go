package groundcover

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// Default configuration values.
const (
	defaultMaxQueue         = 10_000
	defaultMaxBytes         = 32 << 20 // 32 MiB
	defaultBatchSize        = 250
	defaultFlushInterval    = 5 * time.Second
	defaultMaxBatchBytes    = 512 << 10 // 512 KiB
	defaultMaxRetries       = 3
	defaultRetryMax         = 30 * time.Second
	defaultRateLimitBackoff = 30 * time.Second
	defaultStackDepthMax    = 128
	defaultHTTPTimeout      = 30 * time.Second

	// ingestPath is the SDK-owned path appended to the DSN. v1 targets the RUM
	// ingestion endpoint; a dedicated errors endpoint will replace it with no
	// user-facing change.
	ingestPath = "/json/rum"
)

// ErrMissingDSN is returned by Init/New when no DSN is configured on an enabled
// client.
var ErrMissingDSN = errors.New("groundcover: DSN is required (set Config.DSN) unless Disabled is true")

// Config configures a Client. The zero value is only valid when Disabled is true.
//
// Most callers set only DSN and IngestionKey; every other field has a sensible
// default and is a tuning knob you can ignore until you need it.
type Config struct {
	// DSN is the base ingestion origin (e.g. https://<tenant>.platform.grcv.io
	// for BYOC). The SDK owns the path and appends /json/rum. A missing scheme
	// defaults to https. Find it in the groundcover UI under
	// Settings -> Access -> Ingestion Keys.
	DSN string
	// IngestionKey is the write key sent as "Authorization: Bearer <key>".
	// It is REQUIRED when posting directly to a cloud/BYOC ingestion origin
	// (omitting it yields silent 401s, since capture never errors at the call
	// site). It is optional ONLY when DSN points at a local in-cluster sensor,
	// which needs no auth. Use a RUM-type ingestion key.
	IngestionKey string

	// ServiceName sets the service identity (OpenTelemetry service.name; the
	// "service" in Datadog/OTel terms). Auto-detected from OTEL_SERVICE_NAME /
	// GC_SERVICE_NAME when unset. In Kubernetes you can usually leave this empty
	// and let the groundcover sensor enrich pod -> workload server-side.
	ServiceName string
	// Env overrides deployment.environment.name (env: GC_ENV / DEPLOYMENT_ENVIRONMENT).
	Env string
	// Release overrides service.version (env: GC_RELEASE).
	Release string

	// MaxQueue bounds the pending buffer by item count (default 10000).
	MaxQueue int
	// MaxBytes bounds the pending buffer by estimated bytes (default 32 MiB).
	MaxBytes int
	// BatchSize is the maximum number of events per request (default 250).
	BatchSize int
	// FlushInterval is the periodic flush cadence (default 5s).
	FlushInterval time.Duration
	// MaxBatchBytes is the maximum estimated request size (default 512 KiB).
	MaxBatchBytes int
	// MaxRetries is the retry attempt cap after the first try (default 3).
	MaxRetries int
	// RetryMax caps exponential backoff (default 30s).
	RetryMax time.Duration
	// RateLimitBackoff is the minimum 429 backoff (default 30s).
	RateLimitBackoff time.Duration
	// StackDepthMax caps captured stack frames (default 128).
	StackDepthMax int

	// OnDrop observes dropped events (also recorded as a self-metric).
	OnDrop func(n int)
	// BeforeSend can scrub or sample an event; returning nil drops it.
	BeforeSend func(*Event) *Event
	// Hasher optionally pseudonymizes user.id / user.email via keyed HMAC.
	Hasher IdentityHasher
	// Logger receives throttled SDK-internal logs.
	Logger Logger
	// Disabled makes the client a no-op with near-zero overhead.
	Disabled bool
	// Debug prints each captured event to stderr in a compact, readable form
	// (after scrubbing/hashing), for local development. It does not affect
	// delivery. Leave off in production.
	Debug bool

	// HTTPClient overrides the HTTP client used for delivery. Primarily a test
	// seam; nil uses a client with sensible timeouts.
	HTTPClient *http.Client
}

// withDefaults returns a copy of cfg with zero fields replaced by defaults.
func (c Config) withDefaults() Config {
	if c.MaxQueue <= 0 {
		c.MaxQueue = defaultMaxQueue
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = defaultMaxBytes
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultBatchSize
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = defaultFlushInterval
	}
	if c.MaxBatchBytes <= 0 {
		c.MaxBatchBytes = defaultMaxBatchBytes
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	} else if c.MaxRetries == 0 {
		c.MaxRetries = defaultMaxRetries
	}
	if c.RetryMax <= 0 {
		c.RetryMax = defaultRetryMax
	}
	if c.RateLimitBackoff <= 0 {
		c.RateLimitBackoff = defaultRateLimitBackoff
	}
	if c.StackDepthMax <= 0 {
		c.StackDepthMax = defaultStackDepthMax
	}
	return c
}

// validate checks the configuration and returns an error if it is unusable.
func (c Config) validate() error {
	if c.Disabled {
		return nil
	}
	if strings.TrimSpace(c.DSN) == "" {
		return ErrMissingDSN
	}
	return nil
}

// endpoint returns the fully-qualified ingestion URL (DSN + ingestPath), adding
// a default https scheme when the DSN omits one.
func (c Config) endpoint() string {
	dsn := strings.TrimRight(strings.TrimSpace(c.DSN), "/")
	if dsn != "" && !strings.HasPrefix(dsn, "http://") && !strings.HasPrefix(dsn, "https://") {
		dsn = "https://" + dsn
	}
	return dsn + ingestPath
}
