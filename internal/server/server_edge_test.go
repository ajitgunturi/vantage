package server_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/pkg/pb"
)

// TestMQ_InitialRecvError covers the Step-1 path: if the very first stream.Recv()
// (the initial-credit read) errors, Consume returns that error before delivering.
func TestMQ_InitialRecvError(t *testing.T) {
	s := queue.NewBroker(queue.BrokerConfig{Capacity: 10})
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
	s := queue.NewBroker(queue.BrokerConfig{Capacity: N * 2})
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
	s := queue.NewBroker(queue.BrokerConfig{Capacity: 50}) // Capacity 50 -> ceiling = max(50,1000) = 1000
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
	s := queue.NewBroker(queue.BrokerConfig{Capacity: 10})
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

	// The message was requeued on Send failure (retry lane, immediately
	// visible) — still retrievable, zero loss.
	require.Eventually(t, func() bool {
		st := srv.Stats()
		return st.Depth+st.RetryDepth == 1 && st.InFlight == 0
	}, 2*time.Second, 10*time.Millisecond, "failed-Send message must be requeued (no loss)")
}

// unboundedStore is a minimal Store whose Inspect reports Capacity == -1, to cover
// the creditCeiling unbounded-backend branch (WAL backend stand-in).
type unboundedStore struct {
	mu     sync.Mutex
	msgs   []*pb.TelemetryMessage
	leases map[uint64]unboundedLease
	nextID uint64
}

type unboundedLease struct {
	msg      *pb.TelemetryMessage
	consumer uint64
}

func (u *unboundedStore) Enqueue(_ context.Context, m *pb.TelemetryMessage) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.nextID++
	m.Id = u.nextID
	u.msgs = append(u.msgs, m)
	return nil
}
func (u *unboundedStore) Lease(consumer uint64) (*pb.TelemetryMessage, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.msgs) == 0 {
		return nil, false
	}
	m := u.msgs[0]
	u.msgs = u.msgs[1:]
	if u.leases == nil {
		u.leases = make(map[uint64]unboundedLease)
	}
	u.leases[m.GetId()] = unboundedLease{msg: m, consumer: consumer}
	return m, true
}
func (u *unboundedStore) Ack(id, consumer uint64) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	rec, ok := u.leases[id]
	if !ok || rec.consumer != consumer {
		return false
	}
	delete(u.leases, id)
	return true
}
func (u *unboundedStore) ReleaseLease(id, consumer uint64) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	rec, ok := u.leases[id]
	if !ok || rec.consumer != consumer {
		return false
	}
	delete(u.leases, id)
	u.msgs = append([]*pb.TelemetryMessage{rec.msg}, u.msgs...)
	return true
}
func (u *unboundedStore) ReleaseConsumer(consumer uint64) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for id, rec := range u.leases {
		if rec.consumer == consumer {
			delete(u.leases, id)
			u.msgs = append([]*pb.TelemetryMessage{rec.msg}, u.msgs...)
			n++
		}
	}
	return n
}
func (u *unboundedStore) Inspect() queue.StoreStats {
	u.mu.Lock()
	defer u.mu.Unlock()
	return queue.StoreStats{Depth: len(u.msgs), Capacity: -1, InFlight: int64(len(u.leases))}
}
func (u *unboundedStore) DLQList(int) []queue.DLQEntry { return nil }
func (u *unboundedStore) DLQReplay() int               { return 0 }
func (u *unboundedStore) Close() error                 { return nil }

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

// TestMQ_ProduceBackpressure covers the backpressure contract: at a full
// capacity budget under the reject policy (opt-in for lossless workloads),
// Produce returns codes.ResourceExhausted and the rejection is counted.
func TestMQ_ProduceBackpressure(t *testing.T) {
	s := queue.NewBroker(queue.BrokerConfig{Capacity: 2, Policy: queue.PolicyReject})
	srv := server.NewMQServer(s, 4)
	defer srv.Shutdown()

	for i := 0; i < 2; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: "bp"},
		})
		require.NoError(t, err)
	}
	_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
		Message: &pb.TelemetryMessage{MetricName: "bp-overflow"},
	})
	require.Error(t, err)
	require.Equal(t, codes.ResourceExhausted, status.Code(err), "full budget must surface as ResourceExhausted")

	st := srv.Stats()
	require.Equal(t, int64(2), st.Produced)
	require.Equal(t, int64(1), st.Rejected)
	require.Equal(t, int64(0), st.Dropped, "reject policy must not lose messages")
	require.Equal(t, 2, st.Depth)
}

// TestMQ_ProduceDuringShutdown covers the shutdown Produce gate: after Shutdown,
// Produce refuses new work with codes.Unavailable so in-flight can clear.
func TestMQ_ProduceDuringShutdown(t *testing.T) {
	s := queue.NewBroker(queue.BrokerConfig{Capacity: 4})
	srv := server.NewMQServer(s, 4)
	srv.Shutdown()

	_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
		Message: &pb.TelemetryMessage{MetricName: "late"},
	})
	require.Error(t, err)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, int64(0), srv.Stats().Produced)
}

// TestMQ_LeaseTTLReclaimsStuckConsumer covers the lease TTL end-to-end: a consumer that
// keeps its stream open but never acks (deadlocked flush, hung DB) holds its
// leases only until LeaseTTL. The sweeper reclaims them for a healthy survivor,
// and the pipeline drains despite the wedged consumer staying connected.
func TestMQ_LeaseTTLReclaimsStuckConsumer(t *testing.T) {
	const N = 10
	b := queue.NewBroker(queue.BrokerConfig{
		Capacity:         N * 2,
		LeaseTTL:         150 * time.Millisecond,
		RetryBackoffBase: time.Millisecond,
		RetryBackoffMax:  time.Millisecond,
		SweepInterval:    20 * time.Millisecond,
	})
	defer b.Close() //nolint:errcheck
	srv := server.NewMQServer(b, 4)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: "ttl"},
		})
		require.NoError(t, err)
	}

	// Stuck consumer: takes deliveries, never acks, never disconnects.
	stuckCtx, stuckCancel := context.WithCancel(context.Background())
	defer stuckCancel()
	stuck := newMockStream(stuckCtx, N, N+8) // credit N: can lease everything
	var wgStuck sync.WaitGroup
	wgStuck.Add(1)
	go func() { defer wgStuck.Done(); srv.Consume(stuck) }() //nolint:errcheck

	// Wait until the stuck consumer holds leases.
	require.Eventually(t, func() bool { return srv.Stats().InFlight > 0 },
		2*time.Second, 5*time.Millisecond, "stuck consumer must lease messages")

	// Healthy survivor: acks everything it receives.
	okCtx, okCancel := context.WithCancel(context.Background())
	defer okCancel()
	okStream := newMockStream(okCtx, 4, N*3)
	okStream.onSend = func() {
		last := okStream.sentMsgs()
		okStream.sendAck(last[len(last)-1].GetId())
	}
	var wgOK sync.WaitGroup
	wgOK.Add(1)
	go func() { defer wgOK.Done(); srv.Consume(okStream) }() //nolint:errcheck

	// All N messages drain through the survivor despite the wedged consumer
	// never disconnecting — the TTL sweeper reclaimed its leases.
	waitConsumed(t, srv, N)

	st := srv.Stats()
	require.GreaterOrEqual(t, st.LeaseExpired, int64(1), "sweeper must have reclaimed expired leases")
	require.Equal(t, int64(0), st.Dropped, "TTL reclaim must not lose messages")

	stuckCancel()
	okCancel()
	wgStuck.Wait()
	wgOK.Wait()
}

// TestMQ_DrainPhase covers the drain phase: BeginDrain refuses producers (Unavailable,
// readiness gate flips) while consumers stay connected and clear the backlog;
// Drained() flips true once nothing is queued or in flight.
func TestMQ_DrainPhase(t *testing.T) {
	const N = 20
	b := queue.NewBroker(queue.BrokerConfig{Capacity: N * 2})
	defer b.Close() //nolint:errcheck
	srv := server.NewMQServer(b, 8)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: "drain"},
		})
		require.NoError(t, err)
	}

	srv.BeginDrain()
	require.True(t, srv.IsShuttingDown(), "drain must flip the readiness gate")
	require.False(t, srv.Drained(), "backlog still queued")

	_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
		Message: &pb.TelemetryMessage{MetricName: "late"},
	})
	require.Equal(t, codes.Unavailable, status.Code(err), "draining broker must refuse producers")

	// A consumer attached DURING the drain still gets served and acks everything.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ms := newMockStream(ctx, 8, N*2)
	ms.onSend = func() {
		last := ms.sentMsgs()
		ms.sendAck(last[len(last)-1].GetId())
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); srv.Consume(ms) }() //nolint:errcheck

	waitConsumed(t, srv, N)
	require.Eventually(t, func() bool { return srv.Drained() },
		2*time.Second, 10*time.Millisecond, "Drained must flip once the backlog clears")
	require.Equal(t, int32(1), srv.Stats().ActiveConsumers, "consumer must survive the drain phase")

	cancel()
	wg.Wait()
}
