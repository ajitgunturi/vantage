package server_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/pkg/pb"
)

// stubServerStream is a no-op grpc.ServerStream implementation for testing.
type stubServerStream struct{}

func (stubServerStream) SetHeader(metadata.MD) error  { return nil }
func (stubServerStream) SendHeader(metadata.MD) error { return nil }
func (stubServerStream) SetTrailer(metadata.MD)       {}
func (stubServerStream) Context() context.Context     { return context.Background() }
func (stubServerStream) SendMsg(any) error            { return nil }
func (stubServerStream) RecvMsg(any) error            { return nil }

// compile-time assertion that stubServerStream satisfies grpc.ServerStream
var _ grpc.ServerStream = stubServerStream{}

// mockConsumeStream implements the bidi pb.MQService_ConsumeServer
// (= grpc.BidiStreamingServer[ConsumeClientMsg, TelemetryMessage]).
//
// The send side records every TelemetryMessage the engine emits; the recv side
// drains client→server ConsumeClientMsg values (initial credit, then acks) from
// ackCh. The two sides mirror the real bidi stream: the engine's recv goroutine
// is the sole caller of Recv(), its send loop the sole caller of Send().
type mockConsumeStream struct {
	stubServerStream
	ctx     context.Context
	mu      sync.Mutex
	msgs    []*pb.TelemetryMessage
	onSend  func()            // optional callback invoked after each successful Send
	sendErr error             // if set, Send returns this error instead of recording (error-path injection)
	ackCh   chan *pb.ConsumeClientMsg
}

func (m *mockConsumeStream) Context() context.Context { return m.ctx }

func (m *mockConsumeStream) Send(msg *pb.TelemetryMessage) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.mu.Lock()
	m.msgs = append(m.msgs, msg)
	m.mu.Unlock()
	if m.onSend != nil {
		m.onSend()
	}
	return nil
}

// Recv returns the next queued ConsumeClientMsg. It returns io.EOF when ackCh is
// closed (client half-close) and ctx.Err() when the stream context is cancelled.
func (m *mockConsumeStream) Recv() (*pb.ConsumeClientMsg, error) {
	select {
	case msg, ok := <-m.ackCh:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-m.ctx.Done():
		return nil, m.ctx.Err()
	}
}

// sentMsgs returns a copy of every message Send recorded (race-safe snapshot).
func (m *mockConsumeStream) sentMsgs() []*pb.TelemetryMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*pb.TelemetryMessage, len(m.msgs))
	copy(out, m.msgs)
	return out
}

// sendAck enqueues an ack for the given broker id onto the client→server channel.
func (m *mockConsumeStream) sendAck(id uint64) {
	m.ackCh <- &pb.ConsumeClientMsg{AckId: id}
}

// newMockStream builds a bidi mock pre-loaded with the initial credit message.
// ackChCap must be large enough to hold the initial credit plus any acks the test
// enqueues before the engine drains them.
func newMockStream(ctx context.Context, credit int32, ackChCap int) *mockConsumeStream {
	m := &mockConsumeStream{
		ctx:   ctx,
		ackCh: make(chan *pb.ConsumeClientMsg, ackChCap),
	}
	m.ackCh <- &pb.ConsumeClientMsg{Credit: credit, ConsumerId: "test"}
	return m
}

// compile-time assertion that mockConsumeStream satisfies the bidi server stream.
var _ pb.MQService_ConsumeServer = (*mockConsumeStream)(nil)

// waitConsumed blocks until srv.Stats().Consumed >= target (the ack clock) or the
// deadline elapses. Teardown is driven off acks actually landing — never off send
// count, which races ahead of acks and would tear a consumer down mid-ack.
func waitConsumed(t *testing.T, srv *server.MQServer, target int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		return srv.Stats().Consumed >= target
	}, 15*time.Second, 5*time.Millisecond, "expected Consumed >= %d", target)
}

// TestMQ_Concurrent_UniqueDelivery verifies that N messages produced to K concurrent
// consumers — all acking every message — are each delivered to exactly one consumer
// (MQ-03 unique-delivery preserved, no duplication, no loss). Buffer is oversized so
// drop-oldest cannot fire and mask loss (PITFALL-3).
func TestMQ_Concurrent_UniqueDelivery(t *testing.T) {
	const N = 2000
	const K = 3
	const bufferSize = N * 2

	s := queue.NewRingStore(bufferSize)
	srv := server.NewMQServer(s, 64)
	defer srv.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var mu sync.Mutex
	seen := make(map[string]int, N)

	var wg sync.WaitGroup
	for i := 0; i < K; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms := newMockStream(ctx, 32, N+8)
			ms.onSend = func() {
				// Record then ack the most recently delivered message.
				last := ms.sentMsgs()
				m := last[len(last)-1]
				mu.Lock()
				seen[m.GetMetricName()]++
				mu.Unlock()
				ms.sendAck(m.GetId())
			}
			srv.Consume(ms) //nolint:errcheck
		}()
	}

	for i := 0; i < N; i++ {
		_, err := srv.Produce(ctx, &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("m%d", i)},
		})
		require.NoError(t, err)
	}

	waitConsumed(t, srv, N) // all N acked exactly once across the K consumers
	cancel()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, N, "each message delivered to exactly one consumer (unique)")
	for i := 0; i < N; i++ {
		require.Equal(t, 1, seen[fmt.Sprintf("m%d", i)], "message m%d delivered exactly once", i)
	}
	require.Equal(t, int64(0), s.Inspect().Dropped, "zero drops: buffer oversized")
}

// TestMQ_AtLeastOnce_NoLoss reproduces the silent-loss defect and proves it fixed:
// 1000 messages are produced; one consumer reads and acks only 20 then disconnects.
// The remaining 980 must still be accounted for (store Depth + acked) — zero loss
// across the disconnect (MQ-09).
func TestMQ_AtLeastOnce_NoLoss(t *testing.T) {
	const N = 1000
	const ackBeforeQuit = 20
	const bufferSize = N * 2

	s := queue.NewRingStore(bufferSize)
	srv := server.NewMQServer(s, 16)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("nl-%d", i)},
		})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ack only the first ackBeforeQuit messages; ignore the rest (they stay leased).
	var sent atomic.Int64
	ms := newMockStream(ctx, 8, ackBeforeQuit+8)
	ms.onSend = func() {
		last := ms.sentMsgs()
		m := last[len(last)-1]
		if sent.Add(1) <= int64(ackBeforeQuit) {
			ms.sendAck(m.GetId())
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); srv.Consume(ms) }() //nolint:errcheck

	waitConsumed(t, srv, ackBeforeQuit) // the 20 acks have landed
	cancel()                            // now disconnect — unacked leases requeue
	wg.Wait()

	// No loss: depth + acked == produced, and nothing is left in flight.
	require.Eventually(t, func() bool {
		st := srv.Stats()
		return int64(st.Depth)+st.Consumed == int64(N) && st.InFlight == 0
	}, 5*time.Second, 10*time.Millisecond, "no loss: depth + acked == produced, nothing left in flight")

	st := srv.Stats()
	require.Equal(t, int64(ackBeforeQuit), st.Consumed, "only the acked messages were consumed")
	require.Greater(t, st.Redelivered, int64(0), "unacked leases were redelivered to the store")
	require.Equal(t, int64(0), s.Inspect().Dropped, "zero drops")
}

// TestMQ_NoOverPull verifies a consumer with credit C is never delivered more than C
// messages while it acks nothing — the in-flight window is structurally bounded by
// the credit semaphore (MQ-10, no over-pull).
func TestMQ_NoOverPull(t *testing.T) {
	const N = 500
	const credit = 5
	const bufferSize = N * 2

	s := queue.NewRingStore(bufferSize)
	srv := server.NewMQServer(s, credit)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("op-%d", i)},
		})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// This consumer NEVER acks: with credit C it can hold at most C unacked, then
	// blocks on the credit semaphore. It can never pull a (C+1)th message.
	var received atomic.Int64
	ms := newMockStream(ctx, credit, 8)
	ms.onSend = func() { received.Add(1) }

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); srv.Consume(ms) }() //nolint:errcheck

	// Wait until the window is full, then confirm it stays exactly at C (no over-pull).
	require.Eventually(t, func() bool {
		return srv.Stats().InFlight == int64(credit)
	}, 5*time.Second, 5*time.Millisecond, "the never-acking consumer fills its credit window")
	require.Never(t, func() bool {
		return received.Load() > int64(credit) || srv.Stats().InFlight > int64(credit)
	}, 500*time.Millisecond, 10*time.Millisecond,
		"a never-acking consumer must never be delivered more than credit C=%d", credit)

	require.Equal(t, int64(credit), received.Load(), "delivered exactly C with no acks")
	require.Equal(t, int64(credit), srv.Stats().InFlight)

	cancel()
	wg.Wait()
}

// TestMQ_RedeliveryOnDisconnect verifies that messages a consumer received but did
// not ack before disconnecting are redelivered to a survivor — zero loss across the
// disconnect (MQ-09, D-04).
func TestMQ_RedeliveryOnDisconnect(t *testing.T) {
	const N = 200
	const ackByA = 50
	const bufferSize = N * 4

	s := queue.NewRingStore(bufferSize)
	srv := server.NewMQServer(s, 8)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("rd-%d", i)},
		})
		require.NoError(t, err)
	}

	// Consumer A: acks only the first ackByA messages, leaves the rest leased.
	ctxA, cancelA := context.WithCancel(context.Background())
	var sentA atomic.Int64
	msA := newMockStream(ctxA, 8, ackByA+8)
	msA.onSend = func() {
		last := msA.sentMsgs()
		m := last[len(last)-1]
		if sentA.Add(1) <= int64(ackByA) {
			msA.sendAck(m.GetId())
		}
	}
	var wgA sync.WaitGroup
	wgA.Add(1)
	go func() { defer wgA.Done(); srv.Consume(msA) }() //nolint:errcheck

	waitConsumed(t, srv, ackByA) // A's 50 acks landed
	cancelA()                    // A disconnects — its unacked leases requeue
	wgA.Wait()

	require.Greater(t, srv.Stats().Redelivered, int64(0), "unacked leases from A were redelivered")

	// Consumer B: acks everything until all N are accounted for.
	ctxB, cancelB := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelB()
	msB := newMockStream(ctxB, 8, N+8)
	msB.onSend = func() {
		last := msB.sentMsgs()
		m := last[len(last)-1]
		msB.sendAck(m.GetId())
	}
	var wgB sync.WaitGroup
	wgB.Add(1)
	go func() { defer wgB.Done(); srv.Consume(msB) }() //nolint:errcheck

	require.Eventually(t, func() bool {
		st := srv.Stats()
		return st.Consumed >= int64(N) && st.Depth == 0 && st.InFlight == 0
	}, 15*time.Second, 10*time.Millisecond, "all N messages eventually acked across the disconnect — zero loss")
	cancelB()
	wgB.Wait()

	st := srv.Stats()
	require.GreaterOrEqual(t, st.Consumed, int64(N), "every produced message was ultimately acked")
	require.Greater(t, st.Redelivered, int64(0), "unacked leases from A were redelivered")
	require.Equal(t, int64(0), s.Inspect().Dropped, "zero drops")
}

// TestMQ_AckSafety verifies that acking an unknown id and double-acking a valid id
// are safe no-ops: no panic, and consumed counts only genuine first-time acks
// (MQ-09 / T-01.1-01).
func TestMQ_AckSafety(t *testing.T) {
	const N = 100
	const credit = 4
	const bufferSize = N * 2

	s := queue.NewRingStore(bufferSize)
	srv := server.NewMQServer(s, credit)
	defer srv.Shutdown()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("as-%d", i)},
		})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ms := newMockStream(ctx, credit, N*3)
	ms.onSend = func() {
		last := ms.sentMsgs()
		m := last[len(last)-1]
		ms.sendAck(9_000_000 + m.GetId()) // unknown id — must be ignored
		ms.sendAck(m.GetId())             // genuine ack
		ms.sendAck(m.GetId())             // double ack — must be ignored
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); srv.Consume(ms) }() //nolint:errcheck

	waitConsumed(t, srv, N) // exactly N genuine acks land despite unknown/double acks
	// Hold briefly: unknown/double acks must NOT push Consumed past N.
	require.Never(t, func() bool {
		return srv.Stats().Consumed > int64(N)
	}, 300*time.Millisecond, 10*time.Millisecond,
		"unknown and double acks are no-ops; consumed counts only genuine first-time acks")
	cancel()
	wg.Wait()

	require.Equal(t, int64(N), srv.Stats().Consumed)
}

// TestMQServer_Produce_NilMessage verifies that Produce returns codes.InvalidArgument
// when the request carries a nil TelemetryMessage (T-01-03-01 mitigation).
func TestMQServer_Produce_NilMessage(t *testing.T) {
	s := queue.NewRingStore(10)
	srv := server.NewMQServer(s, 10)
	defer srv.Shutdown()

	resp, err := srv.Produce(context.Background(), &pb.ProduceRequest{Message: nil})
	require.Nil(t, resp)
	require.Error(t, err)
	require.Contains(t, err.Error(), "InvalidArgument")
	require.Equal(t, 0, s.Inspect().Depth)
}

// TestMQServer_Stats verifies Stats reports Consumed == acks and Delivered == sends
// after a consumer acks a known number of messages.
func TestMQServer_Stats(t *testing.T) {
	const N = 50
	s := queue.NewRingStore(N * 2)
	srv := server.NewMQServer(s, 16)
	defer srv.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ms := newMockStream(ctx, 8, N+8)
		ms.onSend = func() {
			last := ms.sentMsgs()
			m := last[len(last)-1]
			ms.sendAck(m.GetId())
		}
		srv.Consume(ms) //nolint:errcheck
	}()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(ctx, &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("s%d", i)},
		})
		require.NoError(t, err)
	}

	waitConsumed(t, srv, N)
	cancel()
	wg.Wait()

	st := srv.Stats()
	require.Equal(t, int64(N), st.Produced)
	require.Equal(t, int64(N), st.Consumed, "Consumed == acks")
	require.GreaterOrEqual(t, st.Delivered, int64(N), "Delivered == sends (>= acks)")
	require.Equal(t, int32(0), st.ActiveConsumers, "consumer exited")
	require.Greater(t, st.Capacity, 0)
	require.Equal(t, int64(0), st.Dropped)
}

// TestMQServer_Shutdown_ReturnsOnDisconnect verifies that a consumer's Consume call
// returns promptly when its stream context is cancelled (the gRPC disconnect /
// GracefulStop signal) and that Shutdown() is idempotent.
func TestMQServer_Shutdown_ReturnsOnDisconnect(t *testing.T) {
	s := queue.NewRingStore(10)
	srv := server.NewMQServer(s, 10)

	ctx, cancel := context.WithCancel(context.Background())
	var consumeErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ms := newMockStream(ctx, 4, 8)
		consumeErr = srv.Consume(ms)
	}()

	// Let Consume enter its send loop, then disconnect.
	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()

	require.True(t, consumeErr == nil || errors.Is(consumeErr, context.Canceled),
		"Consume must return cleanly on disconnect, got %v", consumeErr)

	// Shutdown is idempotent — safe to call twice.
	srv.Shutdown()
	srv.Shutdown()
}

// TestMQ_GoroutineLeak verifies the per-consumer recv goroutine is joined on
// disconnect, so goroutine count returns to baseline after K bidi consumers exit.
func TestMQ_GoroutineLeak(t *testing.T) {
	s := queue.NewRingStore(1000)
	srv := server.NewMQServer(s, 16)
	defer srv.Shutdown()

	runtime.Gosched()
	baseline := runtime.NumGoroutine()

	const K = 10
	var wg sync.WaitGroup
	for i := 0; i < K; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, ccancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(10 * time.Millisecond)
				ccancel()
			}()
			ms := newMockStream(cctx, 8, 16)
			srv.Consume(ms) //nolint:errcheck
		}()
	}

	wg.Wait()

	require.Eventually(t,
		func() bool { return runtime.NumGoroutine() <= baseline+2 },
		5*time.Second,
		50*time.Millisecond,
		"goroutine count must return to baseline after all consumers disconnect (recv goroutine joined)",
	)
}
