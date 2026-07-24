package collector

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector metrics live on a package registry (one Collector process per
// binary) and are exposed by MetricsHandler on the health server.
var (
	metricsRegistry = prometheus.NewRegistry()

	batchPersistSeconds = promauto.With(metricsRegistry).NewHistogram(prometheus.HistogramOpts{
		Name:    "collector_batch_persist_seconds",
		Help:    "Latency of persisting one batch to Postgres (bisect included).",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 14), // 1ms .. ~8s
	})
	batchRows = promauto.With(metricsRegistry).NewHistogram(prometheus.HistogramOpts{
		Name:    "collector_batch_rows",
		Help:    "Rows per flushed batch.",
		Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1 .. 2048
	})
	batchesTotal = promauto.With(metricsRegistry).NewCounter(prometheus.CounterOpts{
		Name: "collector_batches_total",
		Help: "Batches flushed successfully.",
	})
	dbErrorsTotal = promauto.With(metricsRegistry).NewCounter(prometheus.CounterOpts{
		Name: "collector_db_errors_total",
		Help: "Batch persist attempts that failed with a transient DB error (redelivered, not acked).",
	})
	deadLetteredTotal = promauto.With(metricsRegistry).NewCounter(prometheus.CounterOpts{
		Name: "collector_rows_dead_lettered_total",
		Help: "Poison rows isolated into gpu_metrics_dlq.",
	})
)

// observeFlush records the outcome of one flush: duration, size, and whether
// it succeeded, dead-lettered poison rows, or failed transiently.
func observeFlush(start time.Time, rows, deadLettered int, err error) {
	if err != nil {
		dbErrorsTotal.Inc()
		return
	}
	batchPersistSeconds.Observe(time.Since(start).Seconds())
	batchRows.Observe(float64(rows))
	batchesTotal.Inc()
	if deadLettered > 0 {
		deadLetteredTotal.Add(float64(deadLettered))
	}
}

// MetricsHandler exposes the Collector's Prometheus registry.
//
// Route: GET /metrics (on COLLECTOR_HEALTH_ADDR)
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
}
