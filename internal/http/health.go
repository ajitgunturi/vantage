// Package mqhttp provides the HTTP control-plane handlers for the MQ service.
// Package name is mqhttp (not http) to avoid shadowing the stdlib http package.
// Import path: github.com/ajitg/vantage/internal/http.
package mqhttp

import (
	"net/http"

	"github.com/ajitg/vantage/internal/server"
)

// HealthzHandler returns an http.HandlerFunc that always responds with 200 OK —
// the process is alive (liveness probe). Bodies contain only a status field to
// prevent information disclosure (T-06-06).
//
// Route: GET /healthz
func HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	}
}

// ReadyzHandler returns an http.HandlerFunc that signals Kubernetes readiness.
// Returns 200 while the server is up; returns 503 after Shutdown() is called,
// signalling that the pod should stop receiving traffic before gRPC drain.
// MQServer.IsShuttingDown uses a non-blocking channel select — O(1) and lock-free.
//
// Route: GET /readyz
func ReadyzHandler(srv *server.MQServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if srv.IsShuttingDown() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"shutting_down"}`)) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	}
}
