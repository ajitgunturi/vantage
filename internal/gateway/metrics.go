package gateway

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Gateway metrics live on a package registry (one Gateway process per binary)
// and are exposed by MetricsHandler.
var (
	metricsRegistry = prometheus.NewRegistry()

	requestDuration = promauto.With(metricsRegistry).NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_http_request_duration_seconds",
		Help:    "API request latency by route pattern, method, and status.",
		Buckets: prometheus.ExponentialBuckets(0.0005, 2, 14), // 0.5ms .. ~4s
	}, []string{"route", "method", "status"})
)

// MetricsMiddleware records a latency histogram per chi route pattern.
// Using the route PATTERN (e.g. /api/v1/gpus/{id}/telemetry), not the raw
// path, keeps label cardinality bounded regardless of how many GPU ids exist.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		requestDuration.WithLabelValues(route, r.Method, strconv.Itoa(ww.Status())).
			Observe(time.Since(start).Seconds())
	})
}

// MetricsHandler exposes the Gateway's Prometheus registry.
//
// Route: GET /metrics (kept out of the OpenAPI spec — operational, not API).
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
}
