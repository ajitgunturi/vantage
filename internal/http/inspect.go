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
// All six fields are required by the MQ-06 spec.
type InspectResponse struct {
	Capacity        int   `json:"capacity"`
	Depth           int   `json:"depth"`
	ProducedTotal   int64 `json:"produced_total"`
	ConsumedTotal   int64 `json:"consumed_total"`
	DroppedTotal    int64 `json:"dropped_total"`
	ActiveConsumers int32 `json:"active_consumers"`
}

// InspectHandler returns an http.HandlerFunc that responds with a JSON snapshot
// of the MQServer state. Stats() uses atomic reads; no mutex is held in this path.
//
// Route: GET /api/v1/queue/inspect (method-scoped via Go 1.22+ ServeMux)
func InspectHandler(srv *server.MQServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := srv.Stats()
		resp := InspectResponse{
			Capacity:        st.Capacity,
			Depth:           st.Depth,
			ProducedTotal:   st.Produced,
			ConsumedTotal:   st.Consumed,
			DroppedTotal:    st.Dropped,
			ActiveConsumers: st.ActiveConsumers,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}
