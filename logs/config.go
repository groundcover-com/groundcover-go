package logs

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	defaultMaxQueue         = 10_000
	defaultMaxBytes         = 32 << 20
	defaultBatchSize        = 250
	defaultFlushInterval    = 5 * time.Second
	defaultMaxBatchBytes    = 512 << 10
	defaultMaxRetries       = 3
	defaultRetryMax         = 30 * time.Second
	defaultRateLimitBackoff = 30 * time.Second
	defaultHTTPTimeout      = 30 * time.Second
)

// ErrMissingDSN is returned when an enabled client has no DSN.
var ErrMissingDSN = errors.New("groundcover/logs: DSN is required unless Disabled is true")

// IdentityHasher pseudonymizes identity fields before delivery.
type IdentityHasher interface {
	HashIdentity(value string) string
}

// Config configures a standalone logs Client.
type Config struct {
	// DSN is the base groundcover ingestion origin.
	DSN string
	// IngestionKey is the write key for /json/logs.
	IngestionKey string
	// ServiceName is written as service on every record.
	ServiceName string
	// Env is written as env on every record.
	Env string
	// Cluster is written as cluster on every record.
	Cluster string
	// Source is written as source on every record. It defaults to "go".
	Source string

	// MaxQueue bounds pending records by item count.
	MaxQueue int
	// MaxBytes bounds pending records by estimated bytes.
	MaxBytes int
	// BatchSize is the maximum records per request.
	BatchSize int
	// FlushInterval is the periodic flush cadence.
	FlushInterval time.Duration
	// MaxBatchBytes bounds the estimated request size.
	MaxBatchBytes int
	// MaxRetries is the retry cap after the first attempt.
	MaxRetries int
	// RetryMax caps exponential retry backoff.
	RetryMax time.Duration
	// RateLimitBackoff is the minimum 429 backoff. A longer server-supplied
	// Retry-After is honoured up to RetryMax.
	RateLimitBackoff time.Duration

	// BeforeSend can scrub or sample a record; returning nil drops it.
	BeforeSend func(*Record) *Record
	// Hasher pseudonymizes user.id and user.email.
	Hasher IdentityHasher
	// Disabled makes the client a no-op.
	Disabled bool
	// HTTPClient overrides the delivery client.
	HTTPClient *http.Client
}

func (c Config) validate() error {
	if !c.Disabled && strings.TrimSpace(c.DSN) == "" {
		return ErrMissingDSN
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.Source == "" {
		c.Source = "go"
	}
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
	return c
}

func (c Config) endpoint() string {
	dsn := strings.TrimRight(strings.TrimSpace(c.DSN), "/")
	if dsn != "" && !strings.HasPrefix(dsn, "http://") && !strings.HasPrefix(dsn, "https://") {
		dsn = "https://" + dsn
	}
	return dsn + "/json/logs"
}
