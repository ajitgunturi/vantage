package mqhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mqhttp "github.com/ajitg/vantage/internal/http"
	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/pkg/pb"
)

// deadLetterOne pushes one message through MaxDeliveries=1 so it dead-letters.
func deadLetterServer(t *testing.T) (*server.MQServer, *queue.Broker) {
	t.Helper()
	b := queue.NewBroker(queue.BrokerConfig{
		Capacity:         8,
		MaxDeliveries:    1,
		RetryBackoffBase: time.Millisecond,
		RetryBackoffMax:  time.Millisecond,
	})
	t.Cleanup(func() { _ = b.Close() })
	srv := server.NewMQServer(b, 4)
	t.Cleanup(srv.Shutdown)

	_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
		Message: &pb.TelemetryMessage{MetricName: "poison", GpuId: "0"},
	})
	require.NoError(t, err)
	_, ok := b.Lease(1)
	require.True(t, ok)
	require.Equal(t, 1, b.ReleaseConsumer(1)) // attempts >= MaxDeliveries → DLQ
	require.Equal(t, 1, srv.Stats().DLQDepth)
	return srv, b
}

func TestDLQHandler_ListsEntries(t *testing.T) {
	srv, _ := deadLetterServer(t)

	rr := httptest.NewRecorder()
	mqhttp.DLQHandler(srv)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/queue/dlq", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	var resp mqhttp.DLQResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Depth)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "poison", resp.Entries[0].MetricName)
	assert.Equal(t, uint32(1), resp.Entries[0].Attempts)
	assert.Contains(t, resp.Entries[0].Reason, "max delivery attempts exceeded")
}

func TestDLQHandler_LimitValidation(t *testing.T) {
	srv, _ := deadLetterServer(t)

	rr := httptest.NewRecorder()
	mqhttp.DLQHandler(srv)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/queue/dlq?limit=abc", nil))
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	rr = httptest.NewRecorder()
	mqhttp.DLQHandler(srv)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/queue/dlq?limit=1", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestDLQHandler_EmptyDLQ(t *testing.T) {
	b := queue.NewBroker(queue.BrokerConfig{Capacity: 4})
	t.Cleanup(func() { _ = b.Close() })
	srv := server.NewMQServer(b, 4)
	t.Cleanup(srv.Shutdown)

	rr := httptest.NewRecorder()
	mqhttp.DLQHandler(srv)(rr, httptest.NewRequest(http.MethodGet, "/api/v1/queue/dlq", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	var resp mqhttp.DLQResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Depth)
	assert.NotNil(t, resp.Entries)
	assert.Empty(t, resp.Entries)
}

func TestDLQReplayHandler_ReplaysAndReports(t *testing.T) {
	srv, b := deadLetterServer(t)

	rr := httptest.NewRecorder()
	mqhttp.DLQReplayHandler(srv)(rr, httptest.NewRequest(http.MethodPost, "/api/v1/queue/dlq/replay", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	var resp mqhttp.DLQReplayResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Replayed)
	assert.Equal(t, 0, resp.Remaining)

	// The replayed message is back in circulation as fresh work.
	m, ok := b.Lease(2)
	require.True(t, ok)
	assert.Equal(t, uint32(1), m.GetDeliveryAttempts())
}
