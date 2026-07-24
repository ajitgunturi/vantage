package mqhttp

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
)

// DLQResponse is the JSON body returned by GET /api/v1/queue/dlq.
type DLQResponse struct {
	Depth   int              `json:"depth"`
	Entries []queue.DLQEntry `json:"entries"`
}

// DLQHandler lists dead-lettered messages, oldest first.
//
// Route: GET /api/v1/queue/dlq[?limit=N]  (default limit 100; limit=0 → all)
func DLQHandler(srv *server.MQServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				http.Error(w, `{"error":"limit must be a non-negative integer"}`, http.StatusBadRequest)
				return
			}
			limit = n
		}
		entries := srv.DLQList(limit)
		if entries == nil {
			entries = []queue.DLQEntry{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(DLQResponse{ //nolint:errcheck
			Depth:   srv.Stats().DLQDepth,
			Entries: entries,
		})
	}
}

// DLQReplayResponse is the JSON body returned by POST /api/v1/queue/dlq/replay.
type DLQReplayResponse struct {
	Replayed int `json:"replayed"`
	// Remaining is the DLQ depth after the replay — non-zero when the main
	// budget filled before the DLQ drained (partial replay).
	Remaining int `json:"remaining"`
}

// DLQReplayHandler re-enqueues dead-lettered messages as fresh work (attempts
// reset, stable id preserved). POST-only: replay mutates broker state.
//
// Route: POST /api/v1/queue/dlq/replay
func DLQReplayHandler(srv *server.MQServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := srv.DLQReplay()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(DLQReplayResponse{ //nolint:errcheck
			Replayed:  n,
			Remaining: srv.Stats().DLQDepth,
		})
	}
}
