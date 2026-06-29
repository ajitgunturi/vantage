package mqhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/pkg/pb"

	mqhttp "github.com/ajitg/vantage/internal/http"
)

// TestInspect_JSON verifies that GET /api/v1/queue/inspect returns HTTP 200
// with Content-Type application/json and a valid body containing all six
// required fields, with ProducedTotal reflecting the number of produced messages.
func TestInspect_JSON(t *testing.T) {
	s := queue.NewRingStore(100)
	srv := server.NewMQServer(s, 10)
	defer srv.Shutdown()

	// Produce one message so ProducedTotal == 1.
	_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
		Message: &pb.TelemetryMessage{MetricName: "test"},
	})
	require.NoError(t, err)

	// Call the handler via httptest — no real port needed.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/queue/inspect", nil)
	mqhttp.InspectHandler(srv)(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp mqhttp.InspectResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err, "response body must be valid JSON")

	require.Greater(t, resp.Capacity, 0, "capacity must be positive")
	require.Equal(t, int64(1), resp.ProducedTotal, "ProducedTotal must reflect one produced message")
}
