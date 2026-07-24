package mqhttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mqhttp "github.com/ajitg/vantage/internal/http"
	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
)

// TestHealthz verifies that HealthzHandler returns HTTP 200 with a JSON body
// containing {"status":"ok"} and the correct Content-Type header.
func TestHealthz(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	mqhttp.HealthzHandler()(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
}

// TestReadyz_Serving verifies that ReadyzHandler returns 200 while the server
// is running (i.e., before Shutdown() is called).
func TestReadyz_Serving(t *testing.T) {
	s := queue.NewBroker(queue.BrokerConfig{Capacity: 100})
	srv := server.NewMQServer(s, 10)
	defer srv.Shutdown()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	mqhttp.ReadyzHandler(srv)(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
}

// TestReadyz_ShuttingDown verifies that ReadyzHandler returns 503 once
// Shutdown() has been called — the readiness gate for Kubernetes pod eviction.
func TestReadyz_ShuttingDown(t *testing.T) {
	s := queue.NewBroker(queue.BrokerConfig{Capacity: 100})
	srv := server.NewMQServer(s, 10)

	srv.Shutdown() // signal shutdown; handler must now return 503

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	mqhttp.ReadyzHandler(srv)(w, r)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "shutting_down", body["status"])
}

// TestIsShuttingDown verifies the MQServer.IsShuttingDown method: false before
// Shutdown(), true after — non-blocking channel select, no lock.
func TestIsShuttingDown(t *testing.T) {
	s := queue.NewBroker(queue.BrokerConfig{Capacity: 100})
	srv := server.NewMQServer(s, 10)

	assert.False(t, srv.IsShuttingDown(), "must report false before Shutdown()")
	srv.Shutdown()
	assert.True(t, srv.IsShuttingDown(), "must report true after Shutdown()")

	// Idempotent: calling Shutdown again must not panic.
	srv.Shutdown()
	assert.True(t, srv.IsShuttingDown())
}
