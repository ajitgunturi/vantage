package server_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/pkg/pb"
)

// TestMQ_InitialRecvError covers the Step-1 path: if the very first stream.Recv()
// (the initial-credit read) errors, Consume returns that error before delivering.
func TestMQ_InitialRecvError(t *testing.T) {
	s := queue.NewRingStore(10)
	srv := server.NewMQServer(s, 10)
	defer srv.Shutdown()

	// An ackCh closed before any credit message makes the first Recv return io.EOF.
	ms := &mockConsumeStream{ctx: context.Background(), ackCh: make(chan *pb.ConsumeClientMsg)}
	close(ms.ackCh)

	err := srv.Consume(ms)
	require.Error(t, err, "Consume must return the initial-Recv error")
}

// TestMQ_DefaultCreditWhenZero covers NewMQServer's default-credit branch and the
// Consume credit clamp: a consumer that sends Credit=0 gets the server default and
// still delivers normally.
func TestMQ_DefaultCreditWhenZero(t *testing.T) {
	const N = 30
	s := queue.NewRingStore(N * 2)
	srv := server.NewMQServer(s, 0) // defaultCredit <= 0 -> 20 (NewMQServer branch)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: "z"},
		})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ms := newMockStream(ctx, 0, N+8) // Credit=0 -> Consume clamps to defaultCredit
	ms.onSend = func() {
		last := ms.sentMsgs()
		ms.sendAck(last[len(last)-1].GetId())
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); srv.Consume(ms) }() //nolint:errcheck

	waitConsumed(t, srv, N)
	cancel()
	wg.Wait()
	require.Equal(t, int64(N), srv.Stats().Consumed)
}

// TestMQ_CreditCeilingClamp covers the ceiling clamp: a consumer requesting more
// credit than max(Capacity, 1000) is clamped, and delivery still works.
func TestMQ_CreditCeilingClamp(t *testing.T) {
	const N = 20
	s := queue.NewRingStore(50) // Capacity 50 -> ceiling = max(50,1000) = 1000
	srv := server.NewMQServer(s, 8)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: "c"},
		})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ms := newMockStream(ctx, 1_000_000, N+8) // huge credit -> clamped to 1000
	ms.onSend = func() {
		last := ms.sentMsgs()
		ms.sendAck(last[len(last)-1].GetId())
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); srv.Consume(ms) }() //nolint:errcheck

	waitConsumed(t, srv, N)
	cancel()
	wg.Wait()
	require.Equal(t, int64(N), srv.Stats().Consumed)
}

// TestMQ_SendError covers the Send-error path: when stream.Send fails, the leased
// message is re-enqueued (no loss) and Consume returns the error.
func TestMQ_SendError(t *testing.T) {
	s := queue.NewRingStore(10)
	srv := server.NewMQServer(s, 4)
	defer srv.Shutdown()

	_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
		Message: &pb.TelemetryMessage{MetricName: "boom"},
	})
	require.NoError(t, err)

	wantErr := errors.New("send failed")
	ms := newMockStream(context.Background(), 4, 8)
	ms.sendErr = wantErr
	// Half-close the client→server direction (after the buffered initial credit) so
	// the recv goroutine sees io.EOF and exits — otherwise the deferred wg.Wait()
	// in Consume would block forever on a background context.
	close(ms.ackCh)

	got := srv.Consume(ms)
	require.ErrorIs(t, got, wantErr, "Consume must return the Send error")

	// The message was re-enqueued on Send failure — still retrievable, zero loss.
	require.Eventually(t, func() bool {
		return srv.Stats().Depth == 1 && srv.Stats().InFlight == 0
	}, 2*time.Second, 10*time.Millisecond, "failed-Send message must be requeued (no loss)")
}

// unboundedStore is a minimal Store whose Inspect reports Capacity == -1, to cover
// the creditCeiling unbounded-backend branch (WAL backend stand-in).
type unboundedStore struct {
	mu   sync.Mutex
	msgs []*pb.TelemetryMessage
}

func (u *unboundedStore) Enqueue(m *pb.TelemetryMessage) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.msgs = append(u.msgs, m)
	return false
}
func (u *unboundedStore) TryDequeue() (*pb.TelemetryMessage, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.msgs) == 0 {
		return nil, false
	}
	m := u.msgs[0]
	u.msgs = u.msgs[1:]
	return m, true
}
func (u *unboundedStore) Inspect() queue.StoreStats {
	u.mu.Lock()
	defer u.mu.Unlock()
	return queue.StoreStats{Depth: len(u.msgs), Capacity: -1, Dropped: 0}
}
func (u *unboundedStore) Requeue(msgs []*pb.TelemetryMessage) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.msgs = append(append([]*pb.TelemetryMessage{}, msgs...), u.msgs...)
}
func (u *unboundedStore) Close() error { return nil }

var _ queue.Store = (*unboundedStore)(nil)

// TestMQ_CreditCeilingUnbounded covers creditCeiling's Capacity<0 branch: with an
// unbounded backend, a huge credit request is clamped to the fixed creditCeiling.
func TestMQ_CreditCeilingUnbounded(t *testing.T) {
	const N = 15
	u := &unboundedStore{}
	srv := server.NewMQServer(u, 8)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: "u"},
		})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ms := newMockStream(ctx, 5_000, N+8) // > creditCeiling (1000) with Capacity == -1
	ms.onSend = func() {
		last := ms.sentMsgs()
		ms.sendAck(last[len(last)-1].GetId())
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); srv.Consume(ms) }() //nolint:errcheck

	waitConsumed(t, srv, N)
	cancel()
	wg.Wait()
	require.Equal(t, int64(N), srv.Stats().Consumed)
}
