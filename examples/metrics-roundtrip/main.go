// Command metrics-roundtrip is the live end-to-end verifier for the metrics
// push client. It registers a gauge, a counter, and a histogram carrying a
// unique run_id needle, pushes them (together with the default runtime
// metrics) to the Prometheus import endpoint, then polls the Prometheus
// query API until both a custom metric and a runtime metric with that
// job/run_id are queryable. It exits 0 on success and 1 on failure.
//
// It uses the same GC_* environment as examples/roundtrip, plus an optional
// GC_METRICS_INGESTION_KEY when metrics use a different key type than RUM.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/groundcover-com/groundcover-go/examples/internal/e2e"
	"github.com/groundcover-com/groundcover-go/metrics"
)

const job = "groundcover-go-metrics-roundtrip"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "metrics-roundtrip: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("metrics-roundtrip: PASS")
}

func run() error {
	env, err := e2e.LoadEnv()
	if err != nil {
		return err
	}
	metricsKey := os.Getenv("GC_METRICS_INGESTION_KEY")
	if metricsKey == "" {
		metricsKey = env.IngestionKey
	}

	runID := e2e.NewID()
	fmt.Printf("metrics-roundtrip: needle run_id=%s dsn=%s api=%s\n", runID, env.DSN, env.APIURL)

	reg := metrics.NewRegistry()
	reg.Gauge("example_queue_depth", nil).Set(17)
	reg.Counter("example_orders_total", map[string]string{"status": "ok"}).Add(3)
	hist := reg.Histogram("example_request_seconds", nil, []float64{0.01, 0.1, 1})
	hist.Observe(0.005)
	hist.Observe(0.5)

	client, err := metrics.New(metrics.Config{
		DSN:          env.DSN,
		IngestionKey: metricsKey,
		Job:          job,
		ExtraLabels:  map[string]string{"run_id": runID},
		// Runtime metrics stay enabled (the default) so the verifier also
		// proves go_* metrics arrive.
	}, reg)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if err := client.Push(context.Background()); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	stats := client.Stats()
	fmt.Printf("metrics-roundtrip: pushed %d series (stats %+v), polling query API...\n", stats.SeriesLastPush, stats)
	if stats.PushesSucceeded != 1 {
		return fmt.Errorf("expected 1 successful push, stats=%+v", stats)
	}

	queries := []string{
		fmt.Sprintf(`example_orders_total{job=%q,run_id=%q}`, job, runID),
		fmt.Sprintf(`go_goroutines{job=%q,run_id=%q}`, job, runID),
	}
	for _, query := range queries {
		samples, err := pollForSamples(env, query)
		if err != nil {
			return err
		}
		fmt.Printf("metrics-roundtrip: query %s returned:\n", query)
		for _, sample := range samples {
			fmt.Println(e2e.PrettyJSON(sample))
		}
	}
	fmt.Println("metrics-roundtrip: custom and runtime metrics verified via the Prometheus query API")
	return nil
}

// promResult is the subset of the Prometheus query API response the verifier
// asserts on.
type promResult struct {
	Status string `json:"status"`
	Data   struct {
		Result []json.RawMessage `json:"result"`
	} `json:"data"`
}

// queryProm runs one instant query against the Prometheus-compatible query
// API and returns the raw result vector.
func queryProm(env e2e.Environment, query string) ([]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	endpoint := env.APIURL + "/api/prometheus/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+env.APIKey)
	if env.BackendID != "" {
		req.Header.Set("X-Backend-Id", env.BackendID)
	}

	client := &http.Client{Timeout: 30 * time.Second}
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
		return nil, fmt.Errorf("query API status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var result promResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if result.Status != "success" {
		return nil, fmt.Errorf("query API returned status %q: %s", result.Status, strings.TrimSpace(string(payload)))
	}
	return result.Data.Result, nil
}

// pollForSamples queries until the metric appears or the poll timeout
// elapses.
func pollForSamples(env e2e.Environment, query string) ([]json.RawMessage, error) {
	deadline := time.Now().Add(e2e.PollTimeout)
	var lastErr error
	for attempt := 1; time.Now().Before(deadline); attempt++ {
		samples, err := queryProm(env, query)
		if err != nil {
			lastErr = err
			fmt.Printf("e2e: attempt %d query error: %v\n", attempt, err)
		} else {
			fmt.Printf("e2e: attempt %d matched %d sample(s) for %s\n", attempt, len(samples), query)
			if len(samples) > 0 {
				return samples, nil
			}
		}
		time.Sleep(e2e.PollInterval)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("metric %s not queryable before timeout (last error: %w)", query, lastErr)
	}
	return nil, fmt.Errorf("metric %s not queryable before timeout", query)
}
