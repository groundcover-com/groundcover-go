// Package metrics is a standalone push client that delivers Prometheus
// metrics directly to groundcover — no agent, sidecar, or scrape endpoint.
//
// The client renders one or more Exposition sources into Prometheus text
// exposition format (0.0.4), injects fixed labels (job, instance, and any
// configured extra labels) into every sample, gzip-compresses the payload,
// and POSTs it to the groundcover Prometheus import endpoint. A background
// worker started with Start pushes immediately and then on a fixed interval;
// Push performs a single synchronous render-and-push.
//
// The package follows the SDK-wide safety guarantees: it is stdlib-only,
// every public entry point and the background worker are panic-guarded, a
// misbehaving Exposition source or OnError callback never crashes the host
// application, and all payloads are bounded by configurable limits.
//
// Most programs need only the built-in Registry plus the runtime metrics
// that are collected by default:
//
//	reg := metrics.NewRegistry()
//	client, err := metrics.New(metrics.Config{
//		DSN:          "https://<ingestion-origin>",
//		IngestionKey: "<key>",
//		Job:          "checkout",
//	}, reg)
//	if err != nil {
//		return err
//	}
//	stop, err := client.Start(context.Background())
//	if err != nil {
//		return err
//	}
//	defer stop(context.Background())
//
//	reg.Counter("orders_created_total", nil).Inc()
package metrics
