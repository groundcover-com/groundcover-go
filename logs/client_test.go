package logs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/groundcover-com/groundcover-go/logs"
)

type request struct {
	path        string
	contentType string
	encoding    string
	auth        string
	body        []byte
}

func TestStandaloneClientRoundTrip(t *testing.T) {
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		requests <- request{
			path:        req.URL.Path,
			contentType: req.Header.Get("Content-Type"),
			encoding:    req.Header.Get("Content-Encoding"),
			auth:        req.Header.Get("Authorization"),
			body:        body,
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := logs.New(logs.Config{
		DSN:          server.URL,
		IngestionKey: "logs-key",
		ServiceName:  "checkout",
		Env:          "production",
		Cluster:      "cluster-a",
		Source:       "my-source",
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close(context.Background()) }()

	client.Info(context.Background(), "order created", logs.Attributes{
		"order_id": "o-9",
		"nested":   map[string]any{"count": 2},
	})
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	req := <-requests
	if req.path != "/json/logs" || req.contentType != "application/x-ndjson" || req.encoding != "" {
		t.Fatalf("request path=%q content-type=%q encoding=%q", req.path, req.contentType, req.encoding)
	}
	if req.auth != "Bearer logs-key" {
		t.Fatalf("authorization = %q", req.auth)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(req.body), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	for key, want := range map[string]any{
		"msg": "order created", "level": "info", "order_id": "o-9",
		"nested.count": float64(2), "service": "checkout", "env": "production",
		"cluster": "cluster-a", "source": "my-source",
	} {
		if got := record[key]; got != want {
			t.Fatalf("record[%q] = %#v, want %#v", key, got, want)
		}
	}
	if stats := client.Stats(); stats.Captured != 1 || stats.Sent != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestStandaloneBeforeSendAndDisabled(t *testing.T) {
	disabled, err := logs.New(logs.Config{Disabled: true})
	if err != nil {
		t.Fatalf("disabled New: %v", err)
	}
	disabled.Info(context.Background(), "ignored")
	if stats := disabled.Stats(); stats.Captured != 0 {
		t.Fatalf("disabled stats = %+v", stats)
	}

	client, err := logs.New(logs.Config{
		DSN: "https://example.invalid",
		BeforeSend: func(record *logs.Record) *logs.Record {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	client.Error(context.Background(), "dropped")
	if stats := client.Stats(); stats.DroppedBeforeSend != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}
