// Package metrics is a standalone push client that delivers Prometheus
// metrics directly to groundcover — no agent, sidecar, or scrape endpoint.
//
// The client renders one or more MetricSource values into Prometheus text
// exposition format (0.0.4), injects fixed labels (job, instance, and any
// configured extra labels) into every sample, gzip-compresses the payload,
// and POSTs it to the groundcover Prometheus import endpoint. A background
// worker started with Start pushes immediately and then on a fixed interval;
// Push performs a single synchronous render-and-push.
//
// The package follows the SDK-wide safety guarantees: it is stdlib-only,
// every public entry point and the background worker are panic-guarded, a
// misbehaving MetricSource or OnError callback never crashes the host
// application, and all payloads are bounded by configurable limits.
//
// Sources are rendered independently: a panicking or malformed source is
// reported through Push's aggregate error while healthy sources are still
// delivered. Wrap a source with Named to attribute its failures by name.
// Duplicate final series across sources keep the first occurrence by
// default and are reported as a *DuplicateSeriesError;
// Config.FailOnDuplicateSeries makes them fail the push instead. Delivery
// goes to the DSN-derived import path, or to Config.Endpoint verbatim when
// set. Push counters, per-source failure counters, and worker state are
// observable via Stats.
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
