package streamer

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Streamer metrics live on a package registry (one Streamer process per
// binary) and are exposed by MetricsHandler on the health server.
var (
	metricsRegistry = prometheus.NewRegistry()

	rowsProducedTotal = promauto.With(metricsRegistry).NewCounter(prometheus.CounterOpts{
		Name: "streamer_rows_produced_total",
		Help: "CSV rows successfully published to the MQ.",
	})
	rowsSkippedTotal = promauto.With(metricsRegistry).NewCounter(prometheus.CounterOpts{
		Name: "streamer_rows_skipped_total",
		Help: "Malformed CSV rows skipped.",
	})
	produceRetriesTotal = promauto.With(metricsRegistry).NewCounter(prometheus.CounterOpts{
		Name: "streamer_produce_retries_total",
		Help: "Produce attempts retried after a transient failure or broker backpressure.",
	})
)

// MetricsHandler exposes the Streamer's Prometheus registry.
//
// Route: GET /metrics (on STREAMER_HEALTH_ADDR)
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
}
