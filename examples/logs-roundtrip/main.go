// Command logs-roundtrip is the live end-to-end verifier for the log signal.
// It initializes the SDK with EnableLogs, emits records carrying a unique
// needle through both layers — the direct emission API and the gcslog slog
// bridge — then queries the logs API (where the RUM pipeline routes log
// events) until the records are found, and prints what it fetched back. It
// exits 0 on success and 1 on failure.
//
// It uses the same GC_* environment as examples/roundtrip.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/groundcover-com/groundcover-go/examples/internal/e2e"
	"github.com/groundcover-com/groundcover-go/gcslog"
	"github.com/groundcover-com/groundcover-go/logs"
)

const flushTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "logs-roundtrip: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("logs-roundtrip: PASS")
}

func run() error {
	env, err := e2e.LoadEnv()
	if err != nil {
		return err
	}

	testID := e2e.NewID()
	fmt.Printf("logs-roundtrip: needle=%s dsn=%s api=%s\n", testID, env.DSN, env.APIURL)
	logsKey := os.Getenv("GC_LOGS_INGESTION_KEY")
	if logsKey == "" {
		logsKey = env.IngestionKey
	}

	client, err := logs.New(logs.Config{
		DSN:           env.DSN,
		IngestionKey:  logsKey,
		ServiceName:   "groundcover-go-logs-roundtrip",
		Env:           "examples",
		Cluster:       "examples",
		Source:        "groundcover-go",
		FlushInterval: time.Second,
	})
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	defer func() { _ = client.CloseTimeout(flushTimeout) }()

	ctx := context.Background()

	// Layer 1: the direct emission API.
	client.Error(ctx, "synthetic emission log "+testID, logs.Attributes{
		"gc.test_id":     testID,
		"example.layer":  "emission",
		"example.int":    7,
		"example.float":  3.14,
		"example.bool":   true,
		"example.string": "hello",
	})

	// Layer 2: the slog bridge, with group flattening.
	logger := slog.New(gcslog.NewHandler(gcslog.Options{Client: client}))
	logger.WarnContext(ctx, "synthetic bridge log "+testID,
		"gc.test_id", testID,
		"example.layer", "slog",
		slog.Group("http", slog.Int("status", 429)),
	)

	if err := client.FlushTimeout(flushTimeout); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	stats := client.Stats()
	fmt.Printf("logs-roundtrip: submitted (captured=%d sent=%d), polling logs API for read-back...\n", stats.Captured, stats.Sent)
	if stats.Sent != 2 {
		return fmt.Errorf("expected 2 sent log records, stats=%+v", stats)
	}

	rows, err := pollForLogs(env, testID)
	if err != nil {
		return err
	}
	fmt.Printf("logs-roundtrip: fetched %d log record(s) from the logs API:\n", len(rows))
	for _, row := range rows {
		fmt.Println(e2e.PrettyJSON(row))
	}
	if err := verifyLogs(rows, testID); err != nil {
		return fmt.Errorf("read-back content mismatch: %w", err)
	}
	fmt.Println("logs-roundtrip: read-back content verified")
	return nil
}

// queryLogs searches by the service header against the logs API. The endpoint
// streams SSE-style lines; each data: line carries a JSON object with a "logs"
// array, the last one marked done.
func queryLogs(env e2e.Environment) ([]json.RawMessage, error) {
	now := time.Now().UTC()
	body, err := json.Marshal(map[string]any{
		"start": now.Add(-15 * time.Minute).Format("2006-01-02T15:04:05.000Z"),
		"end":   now.Add(5 * time.Minute).Format("2006-01-02T15:04:05.000Z"),
		"group": map[string]any{
			"operator": "and",
			"conditions": []map[string]any{{
				"filters": []map[string]any{{"op": "match", "value": "groundcover-go-logs-roundtrip"}},
				"key":     "service",
				"origin":  "root",
				"type":    "string",
			}},
		},
		"limit": 20, "skip": 0,
		"sortBy": "timestamp", "sortOrder": "desc",
		"selectors": []any{}, "optimizeSearch": true,
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.APIURL+"/api/logs/v2/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("logs API status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var logs []json.RawMessage
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var chunk struct {
			Logs []json.RawMessage `json:"logs"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &chunk); err != nil {
			continue
		}
		logs = append(logs, chunk.Logs...)
	}
	return logs, scanner.Err()
}

// pollForLogs queries the logs API until both records appear or the poll
// timeout elapses.
func pollForLogs(env e2e.Environment, needle string) ([]json.RawMessage, error) {
	deadline := time.Now().Add(e2e.PollTimeout)
	var lastErr error
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		rows, err := queryLogs(env)
		if err != nil {
			lastErr = err
			fmt.Printf("e2e: attempt %d query error: %v\n", attempt, err)
		} else {
			matched := make([]json.RawMessage, 0, 2)
			for _, row := range rows {
				if bytes.Contains(row, []byte(needle)) {
					matched = append(matched, row)
				}
			}
			fmt.Printf("e2e: attempt %d matched %d record(s)\n", attempt, len(matched))
			if len(matched) >= 2 {
				return matched, nil
			}
		}
		time.Sleep(e2e.PollInterval)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("needle %s not found before timeout (last error: %w)", needle, lastErr)
	}
	return nil, fmt.Errorf("needle %s not found before timeout", needle)
}

// verifyLogs checks that both records came back with the content the SDK sent.
// Stored rows carry the message in a body/content field and attributes as
// flattened string columns; the checks below are tolerant to either name.
func verifyLogs(rows []json.RawMessage, testID string) error {
	var emission, bridge map[string]any
	for _, raw := range rows {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			return fmt.Errorf("decode row: %w", err)
		}
		text := rowText(row)
		switch {
		case strings.Contains(text, "synthetic emission log "+testID):
			emission = row
		case strings.Contains(text, "synthetic bridge log "+testID):
			bridge = row
		}
	}
	if emission == nil {
		return fmt.Errorf("emission-API record not found among %d rows", len(rows))
	}
	if bridge == nil {
		return fmt.Errorf("slog-bridge record not found among %d rows", len(rows))
	}
	if lvl := rowString(emission, "level"); lvl != "error" {
		return fmt.Errorf("emission level = %q, want error", lvl)
	}
	if lvl := rowString(bridge, "level"); lvl != "warning" {
		return fmt.Errorf("bridge level = %q, want warning", lvl)
	}
	for key, want := range map[string]string{
		"service": "groundcover-go-logs-roundtrip",
		"cluster": "examples",
		"env":     "examples",
		"source":  "groundcover-go",
	} {
		if got := rowString(emission, key); got != want {
			return fmt.Errorf("emission %s = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(string(mustJSON(emission)), testID) ||
		!strings.Contains(string(mustJSON(bridge)), testID) {
		return fmt.Errorf("stored records missing test needle")
	}
	return nil
}

// rowText extracts the log body from a stored row, tolerating either a body or
// content field name.
func rowText(row map[string]any) string {
	for _, k := range []string{"body", "content", "message"} {
		if s, ok := row[k].(string); ok && s != "" {
			return s
		}
	}
	b, _ := json.Marshal(row)
	return string(b)
}

func rowString(row map[string]any, key string) string {
	if s, ok := row[key].(string); ok {
		return s
	}
	return ""
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
