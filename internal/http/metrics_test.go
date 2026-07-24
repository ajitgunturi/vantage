package mqhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mqhttp "github.com/ajitg/vantage/internal/http"
	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/pkg/pb"
)

// TestMetricsHandler_ExposesBrokerState verifies the Prometheus exposition
// derives from live broker state: gauges and counters move with traffic and
// agree with Stats().
func TestMetricsHandler_ExposesBrokerState(t *testing.T) {
	b := queue.NewBroker(queue.BrokerConfig{Capacity: 8})
	t.Cleanup(func() { _ = b.Close() })
	srv := server.NewMQServer(b, 4)
	t.Cleanup(srv.Shutdown)

	for i := 0; i < 3; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: "m", GpuId: "0"},
		})
		require.NoError(t, err)
	}
	// Lease and ack one so consumed/in-flight related series move.
	m, ok := b.Lease(1)
	require.True(t, ok)
	require.True(t, b.Ack(m.GetId(), 1))

	rr := httptest.NewRecorder()
	mqhttp.MetricsHandler(srv)(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()

	assert.Contains(t, body, "mq_produced_total 3")
	assert.Contains(t, body, "mq_queue_depth 2")
	assert.Contains(t, body, "mq_capacity 8")
	assert.Contains(t, body, `mq_dropped_total{cause="overflow"} 0`)
	assert.Contains(t, body, `mq_dropped_total{cause="requeue"} 0`)
	assert.Contains(t, body, "mq_rejected_total 0")
	assert.Contains(t, body, "mq_dead_lettered_total 0")
	assert.Contains(t, body, "mq_lease_expired_total 0")
	assert.Contains(t, body, "mq_in_flight 0")
	assert.Contains(t, body, "mq_active_consumers 0")
	assert.Contains(t, body, "# HELP mq_queue_depth", "exposition must carry HELP text")
}
