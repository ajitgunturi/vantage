// Package mqhttp provides the HTTP control-plane handler for the MQ service.
// Package name is mqhttp (not http) to avoid shadowing the stdlib http package
// within this file. Import path: github.com/ajitg/vantage/internal/http.
package mqhttp

import (
	"encoding/json"
	"net/http"

	"github.com/ajitg/vantage/internal/server"
)

// InspectResponse is the JSON body returned by GET /api/v1/queue/inspect.
//
// As of Phase 01.1 (at-least-once delivery, D-09) the counters distinguish sends
// from confirmed deliveries:
//   - ProducedTotal  — messages accepted by Produce.
//   - DeliveredTotal — messages sent to consumers (may exceed ConsumedTotal by the
//     in-flight window plus any redeliveries).
//   - ConsumedTotal  — acks received (confirmed deliveries) — the at-least-once
//     removal point. This is NO LONGER a count of sends.
//   - RedeliveredTotal — messages re-enqueued and redelivered after a consumer
//     disconnected with unacked leases.
//   - InFlight — current gauge of messages sent but not yet acked (sum of all
//     per-consumer lease tables).
type InspectResponse struct {
	Capacity         int   `json:"capacity"`
	Depth            int   `json:"depth"`
	ProducedTotal    int64 `json:"produced_total"`
	DeliveredTotal   int64 `json:"delivered_total"`
	ConsumedTotal    int64 `json:"consumed_total"`
	RedeliveredTotal int64 `json:"redelivered_total"`
	DroppedTotal     int64 `json:"dropped_total"`
	ActiveConsumers  int32 `json:"active_consumers"`
	InFlight         int64 `json:"in_flight"`
}

// InspectHandler returns an http.HandlerFunc that responds with a JSON snapshot
// of the MQServer state. Stats() uses atomic reads; no mutex is held in this path.
//
// Route: GET /api/v1/queue/inspect (method-scoped via Go 1.22+ ServeMux)
func InspectHandler(srv *server.MQServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := srv.Stats()
		resp := InspectResponse{
			Capacity:         st.Capacity,
			Depth:            st.Depth,
			ProducedTotal:    st.Produced,
			DeliveredTotal:   st.Delivered,
			ConsumedTotal:    st.Consumed,
			RedeliveredTotal: st.Redelivered,
			DroppedTotal:     st.Dropped,
			ActiveConsumers:  st.ActiveConsumers,
			InFlight:         st.InFlight,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}
