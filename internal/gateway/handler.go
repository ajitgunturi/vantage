package gateway

import (
	"encoding/json"
	"net/http"
	"time"

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
