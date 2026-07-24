package gateway

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter constructs the chi router for the API Gateway.
//
// Routes registered:
//   - GET /api/v1/gpus                   → ListGPUs (API-01)
//   - GET /api/v1/gpus/{id}/telemetry    → GetTelemetry (API-02, API-03)
//   - GET /swagger/*                     → Swagger UI (spec generated in Plan 03)
//
// Note: the side-effect import of pkg/docs (which registers the generated
// swagger spec on init()) lives only in cmd/gateway/main.go — NOT here.
// This keeps server.go importable in tests without requiring the generated
// docs package to exist.
func NewRouter(pool *pgxpool.Pool, cfg Config) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(MetricsMiddleware) // request-latency histogram by route pattern

	// Liveness and readiness probes — registered at router root BEFORE the API
	// group so they are always reachable even without the /api/v1 prefix.
	// These endpoints carry no swag annotations and are excluded from the OpenAPI
	// spec (Pitfall 1 — health endpoints must not appear in the documented API).
	r.Get("/healthz", HealthzHandler())
	r.Get("/readyz", ReadyzHandler(pool))
	// Prometheus exposition — operational endpoint, excluded from the OpenAPI
	// spec for the same reason as the probes.
	r.Method(http.MethodGet, "/metrics", MetricsHandler())

	r.Route("/api/v1/gpus", func(r chi.Router) {
		r.Get("/", ListGPUs(pool))
		r.Get("/{id}/telemetry", GetTelemetry(pool, cfg.MaxRows)) // API-02, API-03 (Plan 02)
	})

	// Swagger UI — served at /swagger/* after pkg/docs is generated (Plan 03).
	// The handler works here as a stub; the spec registration via init() happens
	// via the _ import in cmd/gateway/main.go.
	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	return r
}
