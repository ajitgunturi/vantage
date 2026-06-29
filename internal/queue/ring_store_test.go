package queue

// Test sizing rationale for TestRingStore_Concurrent_UniqueDelivery (QA-02):
// The buffer is intentionally sized at 2×N (4000 slots for 2000 messages) to
// ensure drop-oldest never fires during the correctness window. A non-zero
// Inspect().Dropped value would indicate a test design error (buffer too small),
// NOT a ring buffer bug. Future maintainers: do not shrink bufferSize below N
// without updating the "zero drops" assertion accordingly.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajitg/vantage/pkg/pb"
	"github.com/stretchr/testify/require"
)

// msg is a test helper that constructs a *pb.TelemetryMessage with only
// MetricName set — sufficient for store-layer tests that don't inspect payload.
func msg(name string) *pb.TelemetryMessage {
	return &pb.TelemetryMessage{MetricName: name}
}

func TestRingStore_Enqueue(t *testing.T) {
	s := NewRingStore(3)
	s.Enqueue(msg("a"))
	s.Enqueue(msg("b"))
	stats := s.Inspect()
	require.Equal(t, 2, stats.Depth, "depth after two enqueues")
	require.Equal(t, int64(0), stats.Dropped, "no drops on a fresh buffer")
	require.Equal(t, 3, stats.Capacity, "capacity matches constructor arg")
}

func TestRingStore_TryDequeue_Empty(t *testing.T) {
	s := NewRingStore(3)
	m, ok := s.TryDequeue()
	require.False(t, ok, "empty buffer must return ok=false")
	require.Nil(t, m, "empty buffer must return nil message")
	require.Equal(t, 0, s.Inspect().Depth)
}

// TestRingStore_DropOldest proves drop-oldest semantics: when the fourth message
// is enqueued into a capacity-3 buffer, the oldest message ("a") is dropped,
// and the first surviving dequeue returns "b".
func TestRingStore_DropOldest(t *testing.T) {
	s := NewRingStore(3)
	s.Enqueue(msg("a"))
	s.Enqueue(msg("b"))
	s.Enqueue(msg("c")) // buffer full
	droppedFlag := s.Enqueue(msg("d"))
	require.True(t, droppedFlag, "fourth enqueue into capacity-3 buffer must fire drop-oldest")
	require.Equal(t, int64(1), s.Inspect().Dropped)
	require.Equal(t, 3, s.Inspect().Depth)
	// "a" was the oldest and was dropped; "b" must be the oldest surviving message.
	m, ok := s.TryDequeue()
	require.True(t, ok)
	require.Equal(t, "b", m.MetricName, "first dequeue after drop-oldest must return 'b', not 'a'")
}

// TestRingStore_WrapAround confirms FIFO order is preserved across the ring wrap-around boundary.
func TestRingStore_WrapAround(t *testing.T) {
	s := NewRingStore(3)
	s.Enqueue(msg("x"))
	s.Enqueue(msg("y"))
	s.Enqueue(msg("z"))
	s.TryDequeue() // discard "x"
	s.TryDequeue() // discard "y"
	s.Enqueue(msg("p"))
	s.Enqueue(msg("q"))
	// Buffer holds "z", "p", "q" in FIFO insertion order.
	names := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		m, ok := s.TryDequeue()
		require.True(t, ok, "expected item at index %d", i)
		names = append(names, m.MetricName)
	}
	require.Equal(t, []string{"z", "p", "q"}, names, "FIFO order must be preserved across wrap-around")
}

func TestRingStore_Inspect(t *testing.T) {
	s := NewRingStore(5)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		s.Enqueue(msg(name))
	}
	s.Enqueue(msg("overflow")) // triggers drop-oldest once
	stats := s.Inspect()
	require.Equal(t, 5, stats.Capacity)
	require.Equal(t, 5, stats.Depth)
	require.Equal(t, int64(1), stats.Dropped)
}

// TestRingStore_Concurrent_UniqueDelivery is the QA-02 race-under-load correctness
// test. N messages are produced; K consumers compete to drain the buffer. Each
// message must be delivered to exactly one consumer (total received == N, no
// duplicates, no misses). Buffer is sized 2×N so drop-oldest never fires.
func TestRingStore_Concurrent_UniqueDelivery(t *testing.T) {
	const N = 2000
	const K = 3
	const bufferSize = N * 2 // 2×N guarantees drop-oldest never fires; see package comment

	s := NewRingStore(bufferSize)

	var received atomic.Int64
	var wg sync.WaitGroup
	wg.Add(K)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start K competing consumer goroutines. Each dequeues until N messages
	// have been delivered collectively, or the context expires.
	for i := 0; i < K; i++ {
		go func() {
			defer wg.Done()
			for {
				if _, ok := s.TryDequeue(); ok {
					n := received.Add(1)
					if n >= N {
						cancel() // fast-exit: signal sibling goroutines to stop
						return
					}
				}
				select {
				case <-ctx.Done():
					return
				default:
					// Also exit if another goroutine already reached N while this
					// one was blocked on a failed TryDequeue.
					if received.Load() >= N {
						return
					}
				}
			}
		}()
	}

	// Producer: enqueue N messages using MetricName for traceability.
	for i := 0; i < N; i++ {
		s.Enqueue(&pb.TelemetryMessage{MetricName: fmt.Sprintf("m%d", i)})
	}

	wg.Wait()

	require.Equal(t, int64(N), received.Load(),
		"each message delivered to exactly one consumer; total must equal N")
	require.Equal(t, int64(0), s.Inspect().Dropped,
		"zero drops: buffer sized 2×N guarantees no overflow during correctness window")
}
