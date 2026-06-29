package gateway

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajitg/vantage/pkg/db"
)

// GpuMetricResponse is the JSON representation returned by
// GET /api/v1/gpus/{id}/telemetry. Defined here to keep HTTP serialization
// separate from the domain model in pkg/models (RESEARCH Pattern 6 — do NOT
// add JSON tags to pkg/models.GpuMetric, which is a DB write contract).
type GpuMetricResponse struct {
	GpuID      string    `json:"gpu_id"`
	Timestamp  time.Time `json:"timestamp"`
	MetricName string    `json:"metric_name"`
	Value      float64   `json:"value"`
	Device     string    `json:"device,omitempty"`
	ModelName  string    `json:"model_name,omitempty"`
	Hostname   string    `json:"hostname,omitempty"`
	Container  string    `json:"container,omitempty"`
	Pod        string    `json:"pod,omitempty"`
	Namespace  string    `json:"namespace,omitempty"`
	LabelsRaw  string    `json:"labels_raw,omitempty"`
}

// ErrorResponse is the JSON envelope for 4xx/5xx gateway errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

// writeJSON sets Content-Type to application/json and encodes v into w.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// writeError writes an ErrorResponse JSON body with the given HTTP status.
// The message must not contain DSN or other secrets (ASVS V8).
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

// GetTelemetry godoc
// @Summary     Get GPU telemetry
// @Description Returns time-series metric rows for a GPU ordered newest-first (API-02).
// @Description Optional ?start_time and/or ?end_time (RFC3339) filter the window (API-03, OQ-3).
// @Description Result is capped at VANTAGE_GATEWAY_MAX_ROWS rows (OQ-1).
// @Tags        gpus
// @Produce     json
// @Param       id         path     string  true  "GPU UUID"
// @Param       start_time query    string  false "Inclusive lower bound (RFC3339); omit for unbounded"
// @Param       end_time   query    string  false "Inclusive upper bound (RFC3339); omit for unbounded"
// @Success     200  {array}   GpuMetricResponse
// @Failure     400  {object}  ErrorResponse  "malformed start_time or end_time"
// @Failure     404  {object}  ErrorResponse  "gpu_id not found"
// @Failure     500  {object}  ErrorResponse
// @Router      /gpus/{id}/telemetry [get]
func GetTelemetry(pool *pgxpool.Pool, maxRows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		// Parse optional RFC3339 time bounds before any DB access.
		// Malformed value → 400 immediately (OQ-4 / T-04-04).
		var start, end *time.Time
		if v := r.URL.Query().Get("start_time"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, http.StatusBadRequest,
					"invalid start_time: expected RFC3339 (e.g. 2006-01-02T15:04:05Z)")
				return
			}
			start = &t
		}
		if v := r.URL.Query().Get("end_time"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, http.StatusBadRequest,
					"invalid end_time: expected RFC3339 (e.g. 2006-01-02T15:04:05Z)")
				return
			}
			end = &t
		}

		// Nil pool guard — returns application/json 500 instead of panicking
		// through chi Recoverer (which would emit text/plain), preserving the
		// Content-Type contract in unit tests that pass nil pool.
		if pool == nil {
			writeError(w, http.StatusInternalServerError, "database not available")
			return
		}

		// Distinguish unknown GPU (→ 404) from known GPU with empty window (→ 200 [])
		// per OQ-2 / T-04-03. id is bound as $1; injection impossible (ASVS V5).
		exists, err := db.GPUExists(r.Context(), pool, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check GPU")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "GPU not found")
			return
		}

		// Fetch telemetry capped at maxRows (OQ-1 / T-04-05).
		metrics, err := db.Telemetry(r.Context(), pool, id, start, end, maxRows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch telemetry")
			return
		}

		// Map domain structs to HTTP response DTOs (RESEARCH Pattern 6: JSON tags
		// on the HTTP type, not on pkg/models.GpuMetric which is the DB write contract).
		resp := make([]GpuMetricResponse, 0, len(metrics))
		for _, m := range metrics {
			resp = append(resp, GpuMetricResponse{
				GpuID:      m.GpuID,
				Timestamp:  m.Timestamp,
				MetricName: m.MetricName,
				Value:      m.Value,
				Device:     m.Device,
				ModelName:  m.ModelName,
				Hostname:   m.Hostname,
				Container:  m.Container,
				Pod:        m.Pod,
				Namespace:  m.Namespace,
				LabelsRaw:  m.LabelsRaw,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// ListGPUs godoc
// @Summary     List GPU IDs
// @Description Returns the unique list of GPU UUIDs that have telemetry data.
// @Tags        gpus
// @Produce     json
// @Success     200  {array}   string
// @Failure     500  {object}  ErrorResponse
// @Router      /gpus [get]
func ListGPUs(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Guard against a nil pool (defensive check — production wiring always
		// provides a valid pool, but unit tests exercise the handler without one).
		if pool == nil {
			writeError(w, http.StatusInternalServerError, "database not available")
			return
		}
		ids, err := db.DistinctGPUIDs(r.Context(), pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to query GPU IDs")
			return
		}
		writeJSON(w, http.StatusOK, ids)
	}
}
