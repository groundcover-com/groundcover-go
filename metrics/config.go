package metrics

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/groundcover-com/groundcover-go/internal/endpoint"
)

const (
	defaultInterval           = 30 * time.Second
	defaultTimeout            = 10 * time.Second
	defaultMaxSeries          = 10_000
	defaultMaxBodyBytes       = 8 << 20
	defaultMaxLabelsPerSeries = 64
	defaultMaxLabelNameBytes  = 256
	defaultMaxLabelValueBytes = 4096
)

// Config configures a standalone metrics Client.
type Config struct {
	// DSN is the base groundcover ingestion origin. The SDK owns the path
	// (<DSN>/api/v1/import/prometheus). A missing scheme defaults to https.
	// Set either DSN (normal) or Endpoint (advanced), not both.
	DSN string
	// Endpoint is an advanced override: the full push URL, for on-prem or
	// custom gateways where the standard DSN-derived path does not apply.
	// When set it is used verbatim. Mutually exclusive with DSN.
	Endpoint string
	// IngestionKey is the write key sent as the apikey header on every push.
	IngestionKey string

	// Interval is the cadence of the background worker started with Start.
	// It defaults to 30s.
	Interval time.Duration
	// Timeout bounds each individual push. It defaults to 10s.
	Timeout time.Duration

	// Job is written as the job label on every sample. It defaults to the
	// OTEL_SERVICE_NAME then GC_SERVICE_NAME environment variables; when all
	// are empty the label is omitted.
	Job string
	// Instance is written as the instance label on every sample. It defaults
	// to os.Hostname(); when empty the label is omitted.
	Instance string
	// ExtraLabels are fixed labels added to every sample. Keys must be valid
	// Prometheus label names and must not be "job" or "instance"; New returns
	// an error otherwise. Fixed labels replace same-named source labels.
	ExtraLabels map[string]string

	// Headers are additional HTTP headers sent on every push. The reserved
	// keys apikey, Content-Type, and Content-Encoding (case-insensitive) are
	// rejected by New.
	Headers http.Header

	// DisableRuntimeMetrics turns off the built-in Go runtime and process
	// metrics source that is otherwise collected on every push.
	DisableRuntimeMetrics bool

	// FailOnDuplicateSeries makes a duplicate final series (same metric name
	// and identical label set after fixed-label injection, across all
	// sources) fail the whole push with a *DuplicateSeriesError instead of
	// keeping the first occurrence and dropping the duplicate. The default
	// (false) favors availability; the strict mode exists for migrations —
	// when consolidating several metric pipelines onto this client, enable
	// it first in staging so silently colliding series are surfaced as hard
	// errors before the drop-the-duplicate behavior can mask them, then turn
	// it off once the sources are known to be disjoint.
	FailOnDuplicateSeries bool

	// OnError observes periodic push failures from the background worker. It
	// is panic-guarded; a panicking callback never crashes the worker.
	OnError func(error)

	// MaxSeries caps sample lines per push. It defaults to 10000. Exceeding a
	// limit fails that push with a *LimitError; the worker keeps running.
	MaxSeries int
	// MaxBodyBytes caps the uncompressed exposition body per push. It
	// defaults to 8MiB.
	MaxBodyBytes int
	// MaxLabelsPerSeries caps labels on a single sample after fixed-label
	// injection. It defaults to 64.
	MaxLabelsPerSeries int
	// MaxLabelNameBytes caps a single label name. It defaults to 256.
	MaxLabelNameBytes int
	// MaxLabelValueBytes caps a single (unescaped) label value. It defaults
	// to 4096.
	MaxLabelValueBytes int

	// Disabled makes the client a no-op.
	Disabled bool
	// HTTPClient overrides the delivery client. Intended as a test seam.
	HTTPClient *http.Client
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = defaultInterval
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	// Defaults come from the environment and the hostname, which can hold
	// arbitrary bytes. An unusable default is dropped rather than failing
	// construction over a value the caller never set; an explicitly configured
	// value is rejected by validate instead.
	if c.Job == "" {
		c.Job = usableLabelValue(firstNonEmpty(os.Getenv("OTEL_SERVICE_NAME"), os.Getenv("GC_SERVICE_NAME")))
	}
	if c.Instance == "" {
		hostname, _ := os.Hostname()
		c.Instance = usableLabelValue(hostname)
	}
	if c.MaxSeries <= 0 {
		c.MaxSeries = defaultMaxSeries
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = defaultMaxBodyBytes
	}
	if c.MaxLabelsPerSeries <= 0 {
		c.MaxLabelsPerSeries = defaultMaxLabelsPerSeries
	}
	if c.MaxLabelNameBytes <= 0 {
		c.MaxLabelNameBytes = defaultMaxLabelNameBytes
	}
	if c.MaxLabelValueBytes <= 0 {
		c.MaxLabelValueBytes = defaultMaxLabelValueBytes
	}
	return c
}

// validate checks a defaulted Config. It never mutates c.
func (c Config) validate() error {
	dsn := strings.TrimSpace(c.DSN)
	override := strings.TrimSpace(c.Endpoint)
	if dsn != "" && override != "" {
		return ErrDSNAndEndpoint
	}
	if !c.Disabled && dsn == "" && override == "" {
		return ErrMissingDSN
	}
	for key := range c.Headers {
		switch {
		case strings.EqualFold(key, "apikey"),
			strings.EqualFold(key, "Content-Type"),
			strings.EqualFold(key, "Content-Encoding"):
			return fmt.Errorf("metrics: header %q is reserved and set by the SDK", key)
		}
	}
	for name, value := range c.ExtraLabels {
		if strings.EqualFold(name, labelJob) || strings.EqualFold(name, labelInstance) {
			return fmt.Errorf("metrics: ExtraLabels key %q is reserved; use Config.Job / Config.Instance", name)
		}
		if !isValidLabelName(name) {
			return fmt.Errorf("metrics: ExtraLabels key %q is not a valid Prometheus label name", name)
		}
		if strings.HasPrefix(name, "__") {
			return fmt.Errorf("metrics: ExtraLabels key %q is reserved", name)
		}
		if len(name) > c.MaxLabelNameBytes {
			return fmt.Errorf("metrics: ExtraLabels key %q exceeds MaxLabelNameBytes (%d)", name, c.MaxLabelNameBytes)
		}
		if len(value) > c.MaxLabelValueBytes {
			return fmt.Errorf("metrics: ExtraLabels value for %q exceeds MaxLabelValueBytes (%d)", name, c.MaxLabelValueBytes)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("metrics: ExtraLabels value for %q is not valid UTF-8", name)
		}
	}
	// Job and Instance default from the environment and the hostname, so they
	// can carry arbitrary bytes too. A fixed label is injected into every
	// sample, so one bad value would make every push out of spec.
	for name, value := range map[string]string{
		labelJob:      c.Job,
		labelInstance: c.Instance,
	} {
		if len(value) > c.MaxLabelValueBytes {
			return fmt.Errorf("metrics: %s value exceeds MaxLabelValueBytes (%d)", name, c.MaxLabelValueBytes)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("metrics: %s value is not valid UTF-8", name)
		}
	}
	fixedLabelCount := len(c.ExtraLabels)
	if c.Job != "" {
		fixedLabelCount++
	}
	if c.Instance != "" {
		fixedLabelCount++
	}
	if fixedLabelCount > c.MaxLabelsPerSeries {
		return fmt.Errorf("metrics: %d fixed labels exceed MaxLabelsPerSeries (%d)", fixedLabelCount, c.MaxLabelsPerSeries)
	}
	// Catch an endpoint no request can be built from here, at construction,
	// rather than as a push failure repeating every interval. A disabled
	// client never pushes, so it needs no endpoint.
	if dsn != "" || override != "" {
		if err := endpoint.Validate(c.pushEndpoint()); err != nil {
			return fmt.Errorf("metrics: %w", err)
		}
	}
	return nil
}

// pushEndpoint returns the URL pushes are POSTed to: the Endpoint override
// verbatim when set, otherwise the import path derived from the DSN.
func (c Config) pushEndpoint() string {
	if override := strings.TrimSpace(c.Endpoint); override != "" {
		return override
	}
	return endpoint.Resolve(c.DSN, "/api/v1/import/prometheus")
}

// fixedLabels returns the job/instance/extra labels, sorted by name.
func (c Config) fixedLabels() []label {
	labels := make([]label, 0, len(c.ExtraLabels)+2)
	if c.Job != "" {
		labels = append(labels, label{name: labelJob, value: c.Job})
	}
	if c.Instance != "" {
		labels = append(labels, label{name: labelInstance, value: c.Instance})
	}
	for name, value := range c.ExtraLabels {
		labels = append(labels, label{name: name, value: value})
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].name < labels[j].name })
	return labels
}

// usableLabelValue returns value if it can be rendered in the exposition
// format, and the empty string otherwise, which omits the label.
func usableLabelValue(value string) string {
	if !utf8.ValidString(value) {
		return ""
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
