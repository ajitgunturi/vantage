package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/gateway"
)

// TestMetricsEndpoint verifies /metrics serves the gateway registry and that
// the request-latency histogram records by chi route PATTERN (bounded
// cardinality) after real requests flow through the middleware.
func TestMetricsEndpoint(t *testing.T) {
	router := gateway.NewRouter(nil, gateway.Config{Addr: ":0", MaxRows: 100})

	// Drive a request through the middleware first so a labelled sample exists.
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()

	assert.Contains(t, body, "gateway_http_request_duration_seconds")
	assert.Contains(t, body, `route="/healthz"`, "samples must be labelled by route pattern")
	assert.Contains(t, body, `status="200"`)
}
