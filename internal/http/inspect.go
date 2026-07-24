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
//
// The delivery hardening adds cause-split loss/backpressure counters:
//   - RejectedTotal — Produce refusals under backpressure (NOT loss; the
//     producer retries with backoff).
//   - DroppedOverflowTotal — drop-oldest evictions (opt-in policy only).
//   - DroppedRequeueTotal — requeue evictions; structurally 0 under
//     capacity accounting — requeue never evicts (tripwire — alert if it ever moves).
//   - DroppedTotal — sum of drop causes (back-compat).
type InspectResponse struct {
	Capacity             int   `json:"capacity"`
	Depth                int   `json:"depth"`
	RetryDepth           int   `json:"retry_depth"`
	DLQDepth             int   `json:"dlq_depth"`
	ProducedTotal        int64 `json:"produced_total"`
	RejectedTotal        int64 `json:"rejected_total"`
	DeliveredTotal       int64 `json:"delivered_total"`
	ConsumedTotal        int64 `json:"consumed_total"`
	RedeliveredTotal     int64 `json:"redelivered_total"`
	DroppedTotal         int64 `json:"dropped_total"`
	DroppedOverflowTotal int64 `json:"dropped_overflow_total"`
	DroppedRequeueTotal  int64 `json:"dropped_requeue_total"`
	DeadLetteredTotal    int64 `json:"dead_lettered_total"`
	LeaseExpiredTotal    int64 `json:"lease_expired_total"`
	ActiveConsumers      int32 `json:"active_consumers"`
	InFlight             int64 `json:"in_flight"`
}

// InspectHandler returns an http.HandlerFunc that responds with a JSON snapshot
// of the MQServer state. Stats() uses atomic reads; no mutex is held in this path.
//
// Route: GET /api/v1/queue/inspect (method-scoped via Go 1.22+ ServeMux)
func InspectHandler(srv *server.MQServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := srv.Stats()
		resp := InspectResponse{
			Capacity:             st.Capacity,
			Depth:                st.Depth,
			RetryDepth:           st.RetryDepth,
			DLQDepth:             st.DLQDepth,
			ProducedTotal:        st.Produced,
			RejectedTotal:        st.Rejected,
			DeliveredTotal:       st.Delivered,
			ConsumedTotal:        st.Consumed,
			RedeliveredTotal:     st.Redelivered,
			DroppedTotal:         st.Dropped,
			DroppedOverflowTotal: st.DroppedOverflow,
			DroppedRequeueTotal:  st.DroppedRequeue,
			DeadLetteredTotal:    st.DeadLettered,
			LeaseExpiredTotal:    st.LeaseExpired,
			ActiveConsumers:      st.ActiveConsumers,
			InFlight:             st.InFlight,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}
