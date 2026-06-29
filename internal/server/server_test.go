package server_test

import (
	"context"
	"fmt"
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

// mockConsumeStream implements pb.MQService_ConsumeServer (= grpc.ServerStreamingServer[TelemetryMessage]).
type mockConsumeStream struct {
	stubServerStream
	ctx    context.Context
	mu     sync.Mutex
	msgs   []*pb.TelemetryMessage
	onSend func() // optional callback invoked after each successful Send
}

func (m *mockConsumeStream) Context() context.Context { return m.ctx }

func (m *mockConsumeStream) Send(msg *pb.TelemetryMessage) error {
	m.mu.Lock()
	m.msgs = append(m.msgs, msg)
	m.mu.Unlock()
	if m.onSend != nil {
		m.onSend()
	}
	return nil
}

// compile-time assertion that mockConsumeStream satisfies pb.MQService_ConsumeServer.
var _ pb.MQService_ConsumeServer = (*mockConsumeStream)(nil)

// TestMQ_Concurrent_UniqueDelivery verifies that N=2000 messages produced to K=3 concurrent
// consumers are each delivered to exactly one consumer (no duplication, no loss).
// Buffer is sized 2×N to prevent drop-oldest from firing during the test (per PITFALL-3).
func TestMQ_Concurrent_UniqueDelivery(t *testing.T) {
	const N = 2000
	const K = 3
	const bufferSize = N * 2

	s := queue.NewRingStore(bufferSize)
	srv := server.NewMQServer(s, N)
	defer srv.Shutdown()

	var received atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < K; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms := &mockConsumeStream{
				ctx: ctx,
				onSend: func() {
					// Cancel shared ctx when the N-th message is received across all consumers.
					if received.Add(1) >= int64(N) {
						cancel()
					}
				},
			}
			srv.Consume(&pb.ConsumeRequest{}, ms) //nolint:errcheck
		}()
	}

	// Produce N messages. Produce does not block.
	for i := 0; i < N; i++ {
		_, err := srv.Produce(ctx, &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("m%d", i)},
		})
		if err != nil {
			// ctx may have been cancelled once N messages are received; that's fine.
			break
		}
	}

	wg.Wait()

	require.Equal(t, int64(N), received.Load(), "each message must be delivered to exactly one consumer")
	require.Equal(t, int64(0), s.Inspect().Dropped, "zero drops: buffer was oversized to prevent non-bug loss")
}

// TestMQ_LateJoin_BufferedDelivery verifies the late-join contract: messages
// produced while NO consumer is attached are buffered by the MQ and delivered
// in full — no loss, no duplication — to a consumer that attaches afterwards.
//
// This is the network-decoupled path the smoke check models with separate
// produce/consume invocations. Here it is enforced rather than demonstrated:
// the producer fully completes and the messages drain out of the ring into the
// work channel (Depth == 0) BEFORE Consume is ever called.
//
// workChCap is sized >= N so dispatch can park every message in the work channel
// with no consumer draining it; bufferSize is oversized so drop-oldest cannot
// fire and mask loss (per PITFALL-3).
func TestMQ_LateJoin_BufferedDelivery(t *testing.T) {
	const N = 500
	const bufferSize = N * 2
	const workChCap = N * 2

	s := queue.NewRingStore(bufferSize)
	srv := server.NewMQServer(s, workChCap)
	defer srv.Shutdown()

	// Produce all N with NO consumer attached. Produce never blocks.
	for i := 0; i < N; i++ {
		_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("late-%d", i)},
		})
		require.NoError(t, err)
	}

	// Barrier: wait until dispatch has emptied the ring into workCh. This proves
	// the messages are buffered and waiting for a consumer, not racing produce.
	require.Eventually(t,
		func() bool { return srv.Stats().Depth == 0 },
		5*time.Second, 5*time.Millisecond,
		"dispatch must drain the ring into workCh while no consumer is attached",
	)
	require.Equal(t, int64(0), s.Inspect().Dropped, "no drops before any consumer attached")

	// NOW a consumer joins, well after the producer is done.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var received atomic.Int64
	ms := &mockConsumeStream{
		ctx: ctx,
		onSend: func() {
			if received.Add(1) >= int64(N) {
				cancel() // stop the consumer once all N are drained
			}
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		srv.Consume(&pb.ConsumeRequest{ConsumerId: "late"}, ms) //nolint:errcheck
	}()
	wg.Wait()

	require.Equal(t, int64(N), received.Load(), "late-joining consumer must receive every buffered message")

	// Content integrity: exactly the N produced messages, each once — no loss, no dup.
	ms.mu.Lock()
	defer ms.mu.Unlock()
	require.Len(t, ms.msgs, N)
	seen := make(map[string]int, N)
	for _, m := range ms.msgs {
		seen[m.GetMetricName()]++
	}
	require.Len(t, seen, N, "every delivered message must be unique (no duplication)")
	for i := 0; i < N; i++ {
		require.Equal(t, 1, seen[fmt.Sprintf("late-%d", i)], "message late-%d delivered exactly once", i)
	}
	require.Equal(t, int64(0), s.Inspect().Dropped, "zero drops across the whole late-join flow")
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
	// Store must not have been touched.
	require.Equal(t, 0, s.Inspect().Depth)
}

// TestMQServer_Stats verifies that Stats returns accurate atomic values after
// producing and consuming a known number of messages.
func TestMQServer_Stats(t *testing.T) {
	const N = 50
	s := queue.NewRingStore(N * 2)
	srv := server.NewMQServer(s, N)
	defer srv.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var received atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ms := &mockConsumeStream{
			ctx: ctx,
			onSend: func() {
				if received.Add(1) >= int64(N) {
					cancel()
				}
			},
		}
		srv.Consume(&pb.ConsumeRequest{}, ms) //nolint:errcheck
	}()

	for i := 0; i < N; i++ {
		_, err := srv.Produce(ctx, &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("s%d", i)},
		})
		if err != nil {
			break
		}
	}

	wg.Wait()

	st := srv.Stats()
	require.Equal(t, int64(N), st.Produced)
	require.Equal(t, int64(N), st.Consumed)
	require.Equal(t, int32(0), st.ActiveConsumers) // consumer exited
	require.Greater(t, st.Capacity, 0)
	require.Equal(t, int64(0), st.Dropped)
}

// TestMQServer_DefaultWorkChCap verifies that NewMQServer with a non-positive
// workChCap does not panic and creates a functional server.
func TestMQServer_DefaultWorkChCap(t *testing.T) {
	s := queue.NewRingStore(10)
	srv := server.NewMQServer(s, 0) // should default to 128
	defer srv.Shutdown()

	_, err := srv.Produce(context.Background(), &pb.ProduceRequest{
		Message: &pb.TelemetryMessage{MetricName: "cap-test"},
	})
	require.NoError(t, err)
}

// TestMQServer_Shutdown_ClosesConsume verifies that Shutdown() causes all
// blocked Consume calls to return nil (clean server-side termination, MQ-07).
func TestMQServer_Shutdown_ClosesConsume(t *testing.T) {
	s := queue.NewRingStore(10)
	srv := server.NewMQServer(s, 10)

	ctx := context.Background()
	var wg sync.WaitGroup
	var consumeErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		ms := &mockConsumeStream{ctx: ctx}
		consumeErr = srv.Consume(&pb.ConsumeRequest{}, ms)
	}()

	// Give Consume time to enter the select loop.
	time.Sleep(20 * time.Millisecond)
	srv.Shutdown()

	wg.Wait()
	require.NoError(t, consumeErr, "Consume must return nil when workCh is closed by Shutdown")
}

// TestMQ_GoroutineLeak verifies that goroutines spawned by Consume exit promptly
// when the stream context is cancelled, leaving no goroutine leaks.
func TestMQ_GoroutineLeak(t *testing.T) {
	s := queue.NewRingStore(1000)
	srv := server.NewMQServer(s, 100)
	defer srv.Shutdown()

	// Measure baseline after NewMQServer so the dispatch goroutine is already counted.
	runtime.Gosched()
	baseline := runtime.NumGoroutine()

	const K = 10
	var wg sync.WaitGroup
	for i := 0; i < K; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, ccancel := context.WithCancel(context.Background())
			// Cancel this consumer's context after a short delay.
			go func() {
				time.Sleep(10 * time.Millisecond)
				ccancel()
			}()
			ms := &mockConsumeStream{ctx: cctx}
			srv.Consume(&pb.ConsumeRequest{}, ms) //nolint:errcheck
		}()
	}

	wg.Wait()

	require.Eventually(t,
		func() bool { return runtime.NumGoroutine() <= baseline+2 },
		5*time.Second,
		50*time.Millisecond,
		"goroutine count must return to baseline after all consumers disconnect",
	)
}
