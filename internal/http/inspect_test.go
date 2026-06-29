package mqhttp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/pkg/pb"

	mqhttp "github.com/ajitg/vantage/internal/http"
)

// ackingStream is a minimal bidi pb.MQService_ConsumeServer that grants an initial
// credit window and acks every message it receives — enough to drive the engine's
// at-least-once counters (delivered/consumed/in_flight) for the inspect test.
type ackingStream struct {
	ctx   context.Context
	ackCh chan *pb.ConsumeClientMsg
}

func newAckingStream(ctx context.Context, credit int32) *ackingStream {
	s := &ackingStream{ctx: ctx, ackCh: make(chan *pb.ConsumeClientMsg, 1024)}
	s.ackCh <- &pb.ConsumeClientMsg{Credit: credit, ConsumerId: "inspect-test"}
	return s
}

func (s *ackingStream) SetHeader(metadata.MD) error  { return nil }
func (s *ackingStream) SendHeader(metadata.MD) error { return nil }
func (s *ackingStream) SetTrailer(metadata.MD)       {}
func (s *ackingStream) Context() context.Context     { return s.ctx }
func (s *ackingStream) SendMsg(any) error            { return nil }
func (s *ackingStream) RecvMsg(any) error            { return nil }

func (s *ackingStream) Send(msg *pb.TelemetryMessage) error {
	s.ackCh <- &pb.ConsumeClientMsg{AckId: msg.GetId()} // ack every delivered message
	return nil
}

func (s *ackingStream) Recv() (*pb.ConsumeClientMsg, error) {
	select {
	case m, ok := <-s.ackCh:
		if !ok {
			return nil, io.EOF
		}
		return m, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

var _ pb.MQService_ConsumeServer = (*ackingStream)(nil)

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

// TestInspect_AtLeastOnceCounters verifies the inspect JSON carries the at-least-once
// counter set (D-09 / MQ-09): consumed_total reflects acks, delivered_total reflects
// sends, redelivered_total and in_flight are present, and all fields map from
// ServerStats. After a consumer delivers and acks N messages, delivered_total ==
// consumed_total == N and in_flight == 0.
func TestInspect_AtLeastOnceCounters(t *testing.T) {
	const N = 25
	s := queue.NewRingStore(N * 2)
	srv := server.NewMQServer(s, 8)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: "alo"},
		})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newAckingStream(ctx, 8)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); srv.Consume(stream) }() //nolint:errcheck

	// Wait until all N are acked (consumed == acks), then snapshot via the handler.
	require.Eventually(t, func() bool {
		return srv.Stats().Consumed >= int64(N)
	}, 10*time.Second, 5*time.Millisecond, "consumer acks all N messages")
	cancel()
	wg.Wait()

	// Raw JSON: every at-least-once key must be present.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/queue/inspect", nil)
	mqhttp.InspectHandler(srv)(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	for _, key := range []string{
		"capacity", "depth", "produced_total", "delivered_total",
		"consumed_total", "redelivered_total", "dropped_total",
		"active_consumers", "in_flight",
	} {
		_, ok := raw[key]
		require.Truef(t, ok, "inspect JSON must contain key %q", key)
	}

	// Typed mapping: values match a fresh ServerStats snapshot.
	var resp mqhttp.InspectResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	st := srv.Stats()
	require.Equal(t, st.Delivered, resp.DeliveredTotal)
	require.Equal(t, st.Consumed, resp.ConsumedTotal)
	require.Equal(t, st.Redelivered, resp.RedeliveredTotal)
	require.Equal(t, st.InFlight, resp.InFlight)

	require.Equal(t, int64(N), resp.ConsumedTotal, "consumed_total == acks == N")
	require.GreaterOrEqual(t, resp.DeliveredTotal, int64(N), "delivered_total == sends >= N")
	require.Equal(t, int64(0), resp.InFlight, "all messages acked — nothing in flight")
}
