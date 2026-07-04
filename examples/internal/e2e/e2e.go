// Package e2e holds the shared plumbing for the live end-to-end verifiers
// (examples/roundtrip and examples/framework-roundtrip): environment loading,
// the events-search API client, needle polling, and small helpers. It is
// internal to the examples module and is not part of the SDK surface.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// PollTimeout bounds how long a verifier waits for a needle to become
// queryable after flushing.
const PollTimeout = 90 * time.Second

// PollInterval is the pause between events-API queries while polling.
const PollInterval = 5 * time.Second

// Environment holds the resolved configuration read from the GC_* variables.
type Environment struct {
	// DSN is the ingestion origin events are POSTed to.
	DSN string
	// IngestionKey is the write key (RUM-type).
	IngestionKey string
	// APIKey is the read key for the events query API.
	APIKey string
	// APIURL is the read API base URL.
	APIURL string
	// BackendID is sent as X-Backend-Id when set (multi-backend tenants).
	BackendID string
}

// LoadEnv reads and validates the required environment variables.
func LoadEnv() (Environment, error) {
	env := Environment{
		DSN:          strings.TrimSpace(os.Getenv("GC_DSN")),
		IngestionKey: strings.TrimSpace(os.Getenv("GC_INGESTION_KEY")),
		APIKey:       strings.TrimSpace(os.Getenv("GC_API_KEY")),
		APIURL:       normalizeURL(os.Getenv("GC_API_URL")),
		BackendID:    strings.TrimSpace(os.Getenv("GC_BACKEND_ID")),
	}
	var missing []string
	if env.DSN == "" {
		missing = append(missing, "GC_DSN")
	}
	if env.IngestionKey == "" {
		missing = append(missing, "GC_INGESTION_KEY")
	}
	if env.APIKey == "" {
		missing = append(missing, "GC_API_KEY")
	}
	if env.APIURL == "" {
		missing = append(missing, "GC_API_URL")
	}
	if len(missing) > 0 {
		return Environment{}, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return env, nil
}

func normalizeURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	return u
}

type searchRequest struct {
	Query string `json:"query"`
	Start string `json:"start"`
	End   string `json:"end"`
	Limit int    `json:"limit"`
}

type searchResponse struct {
	Data []json.RawMessage `json:"data"`
}

// SearchEvents runs a gcQL query against the events search API and returns the
// matching events as raw JSON.
func SearchEvents(env Environment, gcql string) ([]json.RawMessage, error) {
	now := time.Now().UTC()
	body, err := json.Marshal(searchRequest{
		Query: gcql,
		Start: now.Add(-15 * time.Minute).Format(time.RFC3339Nano),
		End:   now.Add(5 * time.Minute).Format(time.RFC3339Nano),
		Limit: 10,
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.APIURL+"/api/k8s/v3/events/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.APIKey)
	if env.BackendID != "" {
		req.Header.Set("X-Backend-Id", env.BackendID)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("events API status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var sr searchResponse
	if err := json.Unmarshal(payload, &sr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return sr.Data, nil
}

// SearchByTestID queries the events API for the exception event carrying the
// given gc.test_id needle.
func SearchByTestID(env Environment, testID string) ([]json.RawMessage, error) {
	gcql := fmt.Sprintf(`category:rum type:exception error_metadata.gc.test_id:"%s"`, testID)
	return SearchEvents(env, gcql)
}

// PollForNeedle queries the events API until the needle appears (returning the
// first matching event) or PollTimeout elapses.
func PollForNeedle(env Environment, testID string) ([]byte, error) {
	deadline := time.Now().Add(PollTimeout)

	var lastErr error
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		events, err := SearchByTestID(env, testID)
		if err != nil {
			lastErr = err
			fmt.Printf("e2e: attempt %d query error: %v\n", attempt, err)
		} else {
			fmt.Printf("e2e: attempt %d matched %d event(s)\n", attempt, len(events))
			if len(events) > 0 {
				return events[0], nil
			}
		}
		time.Sleep(PollInterval)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("needle %s not found before timeout (last error: %w)", testID, lastErr)
	}
	return nil, fmt.Errorf("needle %s not found before timeout", testID)
}

// StoredEvent is the subset of the events-API row the verifiers assert on.
type StoredEvent struct {
	// Type is the event type (always "exception" for SDK error events).
	Type string `json:"type"`
	// Category is the event category (always "rum" for SDK error events).
	Category string `json:"category"`
	// StringAttributes holds the flattened string attribute columns.
	StringAttributes map[string]string `json:"string_attributes"`
	// FloatAttributes holds the flattened numeric attribute columns.
	FloatAttributes map[string]float64 `json:"float_attributes"`
}

// DecodeStoredEvent parses a raw events-API row into a StoredEvent.
func DecodeStoredEvent(raw []byte) (StoredEvent, error) {
	var e StoredEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return StoredEvent{}, fmt.Errorf("decode event: %w", err)
	}
	return e, nil
}

// PrettyJSON re-indents raw JSON for human-readable output.
func PrettyJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

// NewID returns a random UUIDv4 string used as the needle.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("roundtrip-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:16]))
}
