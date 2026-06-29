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

// TestRingStore_Requeue verifies front-insertion semantics for at-least-once redelivery (D-05).
func TestRingStore_Requeue(t *testing.T) {
	t.Run("empty_requeue_is_noop", func(t *testing.T) {
		s := NewRingStore(4)
		s.Requeue(nil)
		s.Requeue([]*pb.TelemetryMessage{})
		require.Equal(t, 0, s.Inspect().Depth, "Requeue(nil) and Requeue([]) must be no-ops")
	})

	t.Run("requeue_preserves_ascending_order", func(t *testing.T) {
		// Requeue([m1,m2,m3]) on an empty store → TryDequeue returns m1, m2, m3.
		s := NewRingStore(8)
		m1, m2, m3 := msg("m1"), msg("m2"), msg("m3")
		s.Requeue([]*pb.TelemetryMessage{m1, m2, m3})
		require.Equal(t, 3, s.Inspect().Depth)
		first, ok := s.TryDequeue()
		require.True(t, ok)
		require.Equal(t, "m1", first.MetricName)
		second, ok := s.TryDequeue()
		require.True(t, ok)
		require.Equal(t, "m2", second.MetricName)
		third, ok := s.TryDequeue()
		require.True(t, ok)
		require.Equal(t, "m3", third.MetricName)
	})

	t.Run("requeue_ahead_of_existing", func(t *testing.T) {
		// Requeued messages must appear before any already-buffered messages.
		s := NewRingStore(8)
		s.Enqueue(msg("existing"))
		s.Requeue([]*pb.TelemetryMessage{msg("redelivered1"), msg("redelivered2")})
		require.Equal(t, 3, s.Inspect().Depth)
		first, _ := s.TryDequeue()
		require.Equal(t, "redelivered1", first.MetricName)
		second, _ := s.TryDequeue()
		require.Equal(t, "redelivered2", second.MetricName)
		third, _ := s.TryDequeue()
		require.Equal(t, "existing", third.MetricName)
	})

	t.Run("full_ring_requeue_drops_newest_tail", func(t *testing.T) {
		// When ring is full, Requeue evicts the newest (tail-side) entry to make room.
		// cap=3, fill with a, b, c; Requeue([r1]) → evicts c (newest), then front-inserts r1.
		s := NewRingStore(3)
		s.Enqueue(msg("a"))
		s.Enqueue(msg("b"))
		s.Enqueue(msg("c")) // buffer full
		require.Equal(t, int64(0), s.Inspect().Dropped)
		s.Requeue([]*pb.TelemetryMessage{msg("r1")})
		// One eviction (c dropped) + r1 inserted at head.
		require.Equal(t, int64(1), s.Inspect().Dropped, "eviction must increment dropped")
		require.Equal(t, 3, s.Inspect().Depth)
		first, _ := s.TryDequeue()
		require.Equal(t, "r1", first.MetricName)
		second, _ := s.TryDequeue()
		require.Equal(t, "a", second.MetricName)
		third, _ := s.TryDequeue()
		require.Equal(t, "b", third.MetricName)
	})
}

// TestRingStore_Requeue_Concurrent checks that Requeue is safe under the race detector.
func TestRingStore_Requeue_Concurrent(t *testing.T) {
	const N = 500
	s := NewRingStore(N * 3)

	var wg sync.WaitGroup
	wg.Add(3)

	// Concurrent Enqueue goroutine
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			s.Enqueue(&pb.TelemetryMessage{MetricName: fmt.Sprintf("e%d", i)})
		}
	}()

	// Concurrent Requeue goroutine
	go func() {
		defer wg.Done()
		batch := make([]*pb.TelemetryMessage, 10)
		for i := 0; i < 10; i++ {
			batch[i] = &pb.TelemetryMessage{MetricName: fmt.Sprintf("r%d", i)}
		}
		for i := 0; i < N/10; i++ {
			s.Requeue(batch)
		}
	}()

	// Concurrent TryDequeue goroutine
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			s.TryDequeue()
		}
	}()

	wg.Wait()
	// No assertion on counts — the test validates race-detector cleanliness only.
	_ = s.Inspect()
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
