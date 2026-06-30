// Package gateway_test provides unit tests for the gateway package.
// No build tag — these run with the standard go test ./... invocation.
// Tests assert routing wiring and Content-Type without a real database
// (handler construction is tested; a nil pool triggers the expected 500 path
// since the pool cannot be used).
package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/gateway"
)

// brokenPool returns a non-nil *pgxpool.Pool that will fail any query because
// it points at an unreachable address (port 19999, nothing listening).
// pgxpool.New is lazy — it does not connect at construction time — so this
// returns a valid, non-nil pool. Queries issued against it will fail with a
// connection-refused error, exercising the DB-error return paths in handlers.
func brokenPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(),
		"postgres://bad:bad@localhost:19999/none?sslmode=disable&connect_timeout=1")
	require.NoError(t, err, "pgxpool.New must not fail (lazy connect)")
	t.Cleanup(pool.Close)
	return pool
}

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

// TestGetTelemetry_RouteRegistered asserts that NewRouter registers
// GET /api/v1/gpus/{id}/telemetry. Uses nil pool — the nil-pool guard in the
// handler must fire before any pgxpool access and return application/json 500
// (not a text/plain panic through Recoverer).
func TestGetTelemetry_RouteRegistered(t *testing.T) {
	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(nil, cfg)
	require.NotNil(t, router)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus/GPU-test/telemetry", nil)
	router.ServeHTTP(w, r)

	// Route must be registered — not 404.
	assert.NotEqual(t, http.StatusNotFound, w.Code,
		"GET /api/v1/gpus/{id}/telemetry must be registered (not 404)")
	// Content-Type must be application/json regardless of error body.
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json",
		"telemetry route must return Content-Type: application/json")
}

// TestGetTelemetry_BadTime_Unit asserts that a malformed start_time query
// parameter returns HTTP 400 with an ErrorResponse JSON body, without any
// database interaction. The time parse happens before the nil-pool guard so
// the handler exits at the 400 path even with a nil pool.
func TestGetTelemetry_BadTime_Unit(t *testing.T) {
	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(nil, cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/api/v1/gpus/GPU-test/telemetry?start_time=not-a-valid-rfc3339", nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"malformed start_time must return 400 Bad Request")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var errResp gateway.ErrorResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.NotEmpty(t, errResp.Error, "ErrorResponse.Error must explain the bad param")
}

// TestGetTelemetry_BadEndTime_Unit asserts the same 400 behaviour for a
// malformed end_time query parameter.
func TestGetTelemetry_BadEndTime_Unit(t *testing.T) {
	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(nil, cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/api/v1/gpus/GPU-test/telemetry?end_time=2024/01/01", nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"malformed end_time must return 400 Bad Request")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var errResp gateway.ErrorResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.NotEmpty(t, errResp.Error)
}

// TestListGPUs_DBError_Unit exercises the "failed to query GPU IDs" 500 path
// by providing a non-nil but unreachable pool (brokenPool). The handler must
// return application/json 500 instead of panicking or returning text/plain.
func TestListGPUs_DBError_Unit(t *testing.T) {
	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(brokenPool(t), cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus", nil)
	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"DB error in ListGPUs must return 500")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json",
		"error response must be application/json")

	var errResp gateway.ErrorResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.NotEmpty(t, errResp.Error)
}

// TestGetTelemetry_DBError_Unit exercises the "failed to check GPU" 500 path
// (from the db.GPUExists call) by providing a non-nil but unreachable pool.
// Time params are omitted so the handler reaches the pool-access code.
func TestGetTelemetry_DBError_Unit(t *testing.T) {
	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(brokenPool(t), cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus/GPU-test/telemetry", nil)
	router.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"DB error in GetTelemetry (GPUExists) must return 500")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var errResp gateway.ErrorResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.NotEmpty(t, errResp.Error)
}
