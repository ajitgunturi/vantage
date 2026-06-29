// Package gateway_test provides unit tests for the gateway package.
// No build tag — these run with the standard go test ./... invocation.
// Tests assert routing wiring and Content-Type without a real database
// (handler construction is tested; a nil pool triggers the expected 500 path
// since the pool cannot be used).
package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/gateway"
)

// TestNewRouter_RouteRegistration asserts that NewRouter registers
// GET /api/v1/gpus and that the route returns a Content-Type of
// application/json (even if the handler returns an error body on a nil pool).
func TestNewRouter_RouteRegistration(t *testing.T) {
	// A nil pool causes DistinctGPUIDs to fail immediately with a nil dereference
	// or similar — the handler must still set Content-Type before writing the body.
	// We only care that the route is registered (not 404) and Content-Type is set.
	cfg := gateway.Config{
		Addr:    ":8080",
		MaxRows: 1000,
	}
	router := gateway.NewRouter(nil, cfg)
	require.NotNil(t, router, "NewRouter must return a non-nil handler")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus", nil)
	router.ServeHTTP(w, r)

	// Route must be registered — not 404.
	assert.NotEqual(t, http.StatusNotFound, w.Code,
		"GET /api/v1/gpus must be registered (not 404)")

	// Content-Type must be application/json regardless of body.
	ct := w.Header().Get("Content-Type")
	assert.Contains(t, ct, "application/json",
		"Content-Type must be application/json for /api/v1/gpus")
}

// TestNewRouter_SwaggerRoute asserts that /swagger/ is mounted.
func TestNewRouter_SwaggerRoute(t *testing.T) {
	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(nil, cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/swagger/", nil)
	router.ServeHTTP(w, r)

	// Swagger UI handler must be registered — not 404.
	assert.NotEqual(t, http.StatusNotFound, w.Code,
		"GET /swagger/ must be registered (not 404)")
}
