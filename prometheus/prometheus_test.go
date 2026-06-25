package prometheus_test

import (
	"context"
	"strings"
	"testing"

	groundcover "github.com/groundcover-com/groundcover-go"
	gcprom "github.com/groundcover-com/groundcover-go/prometheus"
)

func TestCollectorExposesMetrics(t *testing.T) {
	client, err := groundcover.New(groundcover.Config{Disabled: true})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	c := gcprom.NewCollector(client)
	var sb strings.Builder
	c.WritePrometheus(&sb)
	out := sb.String()

	want := []string{
		"groundcover_sdk_captured_total",
		"groundcover_sdk_sent_total",
		`groundcover_sdk_dropped_total{reason="overflow"}`,
		`groundcover_sdk_dropped_total{reason="send_exhausted"}`,
		"groundcover_sdk_retries_total",
		"groundcover_sdk_rate_limited_total",
		"groundcover_sdk_panics_recovered_total",
		`groundcover_sdk_queue_pending{unit="items"}`,
		`groundcover_sdk_queue_pending{unit="bytes"}`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("exposition missing %q\n%s", w, out)
		}
	}
	if c.Set() == nil {
		t.Fatal("Set() must not be nil")
	}
}
