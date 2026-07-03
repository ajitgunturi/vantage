package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

// TelemetryPage is the paginated response envelope for GET /api/v1/gpus/{id}/telemetry.
// Data holds the metric rows for the current page; Pagination carries cursor metadata.
type TelemetryPage struct {
	Data       []GpuMetricResponse `json:"data"`
	Pagination PaginationMeta      `json:"pagination"`
}

// PaginationMeta carries the limit/offset applied to the query and a sentinel
// indicating whether another page exists (API-05, RESEARCH Pitfall 5 — off-by-one).
type PaginationMeta struct {
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasNext bool `json:"has_next"`
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

// HealthzHandler returns a liveness probe handler that always responds 200 OK.
// No database access required — if the process is alive, the handler fires.
// Not annotated with swag — health endpoints are excluded from the documented API (Pitfall 1).
func HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ReadyzHandler returns a readiness probe handler that pings the Postgres pool
// with a 2-second timeout. Returns 503 Service Unavailable if the pool is nil
// or if the ping fails. Responses contain only generic status strings — no DSN,
// driver text, or version is included (T-06-05 / ASVS V8).
// Not annotated with swag — health endpoints are excluded from the documented API (Pitfall 1).
func ReadyzHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			writeError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// GetTelemetry godoc
// @Summary     Get GPU telemetry
// @Description Returns time-series metric rows for a GPU ordered newest-first (API-02).
// @Description Optional ?start_time and/or ?end_time (RFC3339) filter the window (API-03, OQ-3).
// @Description Use limit and offset for pagination; result is wrapped in a TelemetryPage envelope (API-05).
// @Tags        gpus
// @Produce     json
// @Param       id         path     string  true  "GPU UUID"
// @Param       start_time query    string  false "Inclusive lower bound (RFC3339); omit for unbounded"
// @Param       end_time   query    string  false "Inclusive upper bound (RFC3339); omit for unbounded"
// @Param       limit      query    int     false "Max rows to return (default/ceiling: VANTAGE_GATEWAY_MAX_ROWS)" minimum(1)
// @Param       offset     query    int     false "Row offset for pagination (default: 0)" minimum(0)
// @Success     200  {object}  TelemetryPage
// @Failure     400  {object}  ErrorResponse  "malformed start_time, end_time, limit, or offset"
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

		// Parse pagination params: limit (default maxRows, ceiling maxRows, ≥1)
		// and offset (default 0, ≥0). Both are validated before pool access so
		// that unit tests with a nil pool exercise the 400 path (T-06-03/T-06-04).
		limit := maxRows
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				writeError(w, http.StatusBadRequest, "limit must be a positive integer")
				return
			}
			if n > maxRows {
				n = maxRows
			}
			limit = n
		}
		offset := 0
		if v := r.URL.Query().Get("offset"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
				return
			}
			offset = n
		}

		// Nil pool guard — returns application/json 500 instead of panicking
		// through chi Recoverer (which would emit text/plain), preserving the
		// Content-Type contract in unit tests that pass nil pool.
		if pool == nil {
			writeError(w, http.StatusInternalServerError, "database not available")
			return
		}

		// Bound DB round-trips to 10s so a slow Postgres query cannot hold
		// the handler goroutine open indefinitely (gateway MINOR).
		dbCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// Distinguish unknown GPU (→ 404) from known GPU with empty window (→ 200 [])
		// per OQ-2 / T-04-03. id is bound as $1; injection impossible (ASVS V5).
		exists, err := db.GPUExists(dbCtx, pool, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check GPU")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "GPU not found")
			return
		}

		// Fetch limit+1 rows to detect has_next without a COUNT query (API-05,
		// RESEARCH Pitfall 5). limit and offset are pgx $N placeholders (T-06-03).
		metrics, err := db.Telemetry(dbCtx, pool, id, start, end, limit+1, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch telemetry")
			return
		}

		// has_next is true when the extra sentinel row was returned.
		// Trim back to limit rows before mapping to the response DTO.
		hasNext := len(metrics) > limit
		if hasNext {
			metrics = metrics[:limit]
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
		writeJSON(w, http.StatusOK, TelemetryPage{
			Data:       resp,
			Pagination: PaginationMeta{Limit: limit, Offset: offset, HasNext: hasNext},
		})
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
		// Bound DB round-trip to 10s (gateway MINOR: DB timeout).
		dbCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		ids, err := db.DistinctGPUIDs(dbCtx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to query GPU IDs")
			return
		}
		writeJSON(w, http.StatusOK, ids)
	}
}
