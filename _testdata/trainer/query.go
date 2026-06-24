package main

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

// environment holds the resolved trainer configuration.
type environment struct {
	dsn          string
	ingestionKey string
	apiKey       string
	apiURL       string
	backendID    string
}

// loadEnv reads and validates the required environment variables.
func loadEnv() (environment, error) {
	env := environment{
		dsn:          strings.TrimSpace(os.Getenv("GC_DSN")),
		ingestionKey: strings.TrimSpace(os.Getenv("GC_INGESTION_KEY")),
		apiKey:       strings.TrimSpace(os.Getenv("GC_API_KEY")),
		apiURL:       normalizeURL(os.Getenv("GC_API_URL")),
		backendID:    strings.TrimSpace(os.Getenv("GC_BACKEND_ID")),
	}
	var missing []string
	if env.dsn == "" {
		missing = append(missing, "GC_DSN")
	}
	if env.ingestionKey == "" {
		missing = append(missing, "GC_INGESTION_KEY")
	}
	if env.apiKey == "" {
		missing = append(missing, "GC_API_KEY")
	}
	if env.apiURL == "" {
		missing = append(missing, "GC_API_URL")
	}
	if len(missing) > 0 {
		return environment{}, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
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

// searchRequest is the events search request body.
type searchRequest struct {
	Query string `json:"query"`
	Start string `json:"start"`
	End   string `json:"end"`
	Limit int    `json:"limit"`
}

// searchResponse is the subset of the events search response we care about.
type searchResponse struct {
	Data []json.RawMessage `json:"data"`
}

// searchEvents runs a gcQL query against the events search API and returns the
// number of matching events.
func searchEvents(env environment, gcql string) (int, error) {
	now := time.Now().UTC()
	body, err := json.Marshal(searchRequest{
		Query: gcql,
		Start: now.Add(-15 * time.Minute).Format(time.RFC3339Nano),
		End:   now.Add(5 * time.Minute).Format(time.RFC3339Nano),
		Limit: 10,
	})
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.apiURL+"/api/k8s/v3/events/search", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.apiKey)
	if env.backendID != "" {
		req.Header.Set("X-Backend-Id", env.backendID)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("events API status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var sr searchResponse
	if err := json.Unmarshal(payload, &sr); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	return len(sr.Data), nil
}

// newID returns a random UUIDv4 string used as the needle.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("trainer-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:16]))
}
