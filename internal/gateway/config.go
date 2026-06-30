// Package gateway implements the API Gateway microservice for the vantage
// telemetry pipeline. It exposes a read-only HTTP/REST API over the shared
// pkg/db pgxpool, documented via auto-generated OpenAPI annotations (swag).
//
// Service logic lives here in internal/gateway so that cmd/gateway/main.go
// is a thin composition root and the exported symbols are importable by tests.
package gateway

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds runtime configuration for the API Gateway microservice.
// Construct via FromEnv() or set fields directly for tests.
type Config struct {
	// Addr is the TCP listen address for the HTTP server.
	// Set from GATEWAY_ADDR; default ":8080".
	Addr string

	// MaxRows is a safety ceiling on the maximum number of rows returned by
	// the telemetry endpoint (GET /api/v1/gpus/{id}/telemetry). This is NOT
	// pagination — it is an operational guard against unbounded queries that
	// could return 100k+ rows per GPU (RESEARCH OQ-1 / Pitfall 3). Default 1000.
	// Set from VANTAGE_GATEWAY_MAX_ROWS (optional; non-positive or non-integer
	// values are rejected with an error, mirroring pkg/db.FromEnv).
	MaxRows int
}

// FromEnv builds a Config from environment variables.
//
// Environment variables:
//   - GATEWAY_ADDR (optional) — TCP listen address; default ":8080"
//   - VANTAGE_GATEWAY_MAX_ROWS (optional) — integer safety ceiling on telemetry
//     results; default 1000. A non-positive or non-integer value is rejected
//     with an error to prevent silent misconfiguration.
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:    ":8080",
		MaxRows: 1000,
	}

	if v := os.Getenv("GATEWAY_ADDR"); v != "" {
		cfg.Addr = v
	}

	if s := os.Getenv("VANTAGE_GATEWAY_MAX_ROWS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return Config{}, fmt.Errorf("gateway: VANTAGE_GATEWAY_MAX_ROWS: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("gateway: VANTAGE_GATEWAY_MAX_ROWS must be > 0, got %d", n)
		}
		cfg.MaxRows = n
	}

	return cfg, nil
}
