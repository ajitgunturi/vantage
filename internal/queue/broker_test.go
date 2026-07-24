package queue

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/pkg/pb"
)

func msg(name string) *pb.TelemetryMessage {
	return &pb.TelemetryMessage{MetricName: name, GpuId: "0", Timestamp: "2026-07-24T00:00:00Z"}
}

func newTestBroker(t *testing.T, cfg BrokerConfig) *Broker {
	t.Helper()
	b := NewBroker(cfg)
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// The reject policy (opt-in for lossless workloads) surfaces backpressure to
// the producer instead of evicting the oldest message.
func TestEnqueueRejectWhenFull(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 2, Policy: PolicyReject})
	ctx := context.Background()

	require.NoError(t, b.Enqueue(ctx, msg("m1")))
	require.NoError(t, b.Enqueue(ctx, msg("m2")))
	err := b.Enqueue(ctx, msg("m3"))
	require.ErrorIs(t, err, ErrFull)

	st := b.Inspect()
	assert.Equal(t, 2, st.Depth)
	assert.Equal(t, int64(1), st.RejectedTotal, "rejection must be counted")
	assert.Equal(t, int64(0), st.DroppedOverflow, "reject policy must not drop")
	assert.Equal(t, int64(0), st.Dropped)
}

// leased (in-flight) messages count against capacity — admission uses
// depth + inFlight, so requeue always has guaranteed headroom.
func TestCapacityAccountingIncludesInFlight(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 4, Policy: PolicyReject})
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		require.NoError(t, b.Enqueue(ctx, msg(fmt.Sprintf("m%d", i))))
	}
	// Lease two: depth drops to 2, in-flight rises to 2 — budget still full.
	m1, ok := b.Lease(1)
	require.True(t, ok)
	_, ok = b.Lease(1)
	require.True(t, ok)

	st := b.Inspect()
	assert.Equal(t, 2, st.Depth)
	assert.Equal(t, int64(2), st.InFlight)

	require.ErrorIs(t, b.Enqueue(ctx, msg("m5")), ErrFull,
		"depth+inFlight == capacity must reject new production")

	// Ack frees one budget slot — the next Enqueue is admitted.
	require.True(t, b.Ack(m1.GetId(), 1))
	require.NoError(t, b.Enqueue(ctx, msg("m5")))
}

// with in-flight counted against capacity, a consumer disconnect at a full
// budget requeues every unacked lease without evicting anything.
func TestReleaseConsumerNeverEvicts(t *testing.T) {
	// 1ns backoff: redeliveries become visible immediately for this test.
	b := newTestBroker(t, BrokerConfig{Capacity: 4, RetryBackoffBase: time.Nanosecond, RetryBackoffMax: time.Nanosecond})
	ctx := context.Background()

	ids := make([]uint64, 0, 4)
	for i := 0; i < 4; i++ {
		require.NoError(t, b.Enqueue(ctx, msg(fmt.Sprintf("m%d", i))))
	}
	for i := 0; i < 4; i++ {
		m, ok := b.Lease(7)
		require.True(t, ok)
		ids = append(ids, m.GetId())
	}
	require.Equal(t, 0, b.Inspect().Depth)

	n := b.ReleaseConsumer(7)
	assert.Equal(t, 4, n)

	st := b.Inspect()
	assert.Equal(t, 4, st.RetryDepth, "all leases must return to the retry lane")
	assert.Equal(t, int64(0), st.InFlight)
	assert.Equal(t, int64(0), st.DroppedRequeue, "requeue must never evict")
	assert.Equal(t, int64(0), st.Dropped)

	// Redelivery preserves original production order (oldest id first).
	for _, want := range ids {
		m, ok := b.Lease(8)
		require.True(t, ok)
		assert.Equal(t, want, m.GetId(), "stable id, oldest-first redelivery")
	}
}

// Drop-oldest is the default for this telemetry pipeline: under overload the
// oldest reading is the least valuable, so the freshest data is retained and
// every eviction is counted.
func TestDropOldestPolicy(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 3, Policy: PolicyDropOldest})
	ctx := context.Background()

	for i := 1; i <= 4; i++ {
		require.NoError(t, b.Enqueue(ctx, msg(fmt.Sprintf("m%d", i))))
	}
	st := b.Inspect()
	assert.Equal(t, 3, st.Depth)
	assert.Equal(t, int64(1), st.DroppedOverflow)
	assert.Equal(t, int64(1), st.Dropped)

	m, ok := b.Lease(1)
	require.True(t, ok)
	assert.Equal(t, "m2", m.GetMetricName(), "m1 must have been evicted")
}

// Drop-oldest has nothing to evict when the whole budget is leased out —
// it degrades to reject rather than corrupting in-flight state.
func TestDropOldestAllLeasedRejects(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 2, Policy: PolicyDropOldest})
	ctx := context.Background()

	require.NoError(t, b.Enqueue(ctx, msg("m1")))
	require.NoError(t, b.Enqueue(ctx, msg("m2")))
	_, ok := b.Lease(1)
	require.True(t, ok)
	_, ok = b.Lease(1)
	require.True(t, ok)

	require.ErrorIs(t, b.Enqueue(ctx, msg("m3")), ErrFull)
	st := b.Inspect()
	assert.Equal(t, int64(1), st.RejectedTotal)
	assert.Equal(t, int64(0), st.DroppedOverflow)
}

// block policy parks the producer until space frees (ack) or times out.
func TestBlockPolicyWaitsForSpace(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 1, Policy: PolicyBlock, BlockTimeout: 5 * time.Second})
	ctx := context.Background()

	require.NoError(t, b.Enqueue(ctx, msg("m1")))
	m, ok := b.Lease(1)
	require.True(t, ok)

	done := make(chan error, 1)
	go func() { done <- b.Enqueue(ctx, msg("m2")) }()

	select {
	case <-done:
		t.Fatal("Enqueue must block while the budget is full")
	case <-time.After(50 * time.Millisecond):
	}

	require.True(t, b.Ack(m.GetId(), 1)) // frees one budget slot
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue must unblock after Ack frees space")
	}
}

func TestBlockPolicyTimeout(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 1, Policy: PolicyBlock, BlockTimeout: 50 * time.Millisecond})
	ctx := context.Background()

	require.NoError(t, b.Enqueue(ctx, msg("m1")))
	start := time.Now()
	err := b.Enqueue(ctx, msg("m2"))
	require.ErrorIs(t, err, ErrFull)
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	assert.Equal(t, int64(1), b.Inspect().RejectedTotal)
}

func TestBlockPolicyCtxCancel(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 1, Policy: PolicyBlock, BlockTimeout: 5 * time.Second})
	require.NoError(t, b.Enqueue(context.Background(), msg("m1")))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Enqueue(ctx, msg("m2")) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue must return on ctx cancel")
	}
}

// Stable identity: the broker id is assigned once at Enqueue and never changes —
// redelivered messages keep their identity.
func TestStableIDAcrossRedelivery(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 4, RetryBackoffBase: time.Nanosecond, RetryBackoffMax: time.Nanosecond})
	require.NoError(t, b.Enqueue(context.Background(), msg("m1")))

	m1, ok := b.Lease(1)
	require.True(t, ok)
	id := m1.GetId()
	require.NotZero(t, id)

	b.ReleaseConsumer(1)
	m2, ok := b.Lease(2)
	require.True(t, ok)
	assert.Equal(t, id, m2.GetId(), "redelivery must not mint a new id")
}

// T-01.1-01 guard preserved: an ack for a lease owned by another consumer is a no-op.
func TestAckOwnershipGuard(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 2})
	require.NoError(t, b.Enqueue(context.Background(), msg("m1")))
	m, ok := b.Lease(1)
	require.True(t, ok)

	assert.False(t, b.Ack(m.GetId(), 99), "foreign consumer must not ack")
	assert.False(t, b.Ack(12345, 1), "unknown id must be a no-op")
	assert.True(t, b.Ack(m.GetId(), 1))
	assert.False(t, b.Ack(m.GetId(), 1), "double ack must be a no-op")
}

// ReleaseLease returns a single failed-send message to the front for the next lease.
func TestReleaseLeaseFrontPriority(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 4})
	ctx := context.Background()
	require.NoError(t, b.Enqueue(ctx, msg("m1")))
	require.NoError(t, b.Enqueue(ctx, msg("m2")))

	m1, ok := b.Lease(1)
	require.True(t, ok)
	require.True(t, b.ReleaseLease(m1.GetId(), 1))

	next, ok := b.Lease(2)
	require.True(t, ok)
	assert.Equal(t, m1.GetId(), next.GetId(), "released lease must be redelivered first")
	assert.False(t, b.ReleaseLease(m1.GetId(), 1), "release after re-lease by another consumer is a no-op")
}

// Race test: concurrent producers, leasers, ackers. Invariants: budget never
// exceeded, every admitted message is acked exactly once, nothing is lost.
func TestBrokerConcurrencyRace(t *testing.T) {
	const capacity = 64
	const producers = 8
	const perProducer = 200
	b := newTestBroker(t, BrokerConfig{Capacity: capacity, Policy: PolicyBlock, BlockTimeout: 10 * time.Second})
	ctx := context.Background()

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				if err := b.Enqueue(ctx, msg(fmt.Sprintf("p%d-%d", p, i))); err != nil {
					t.Errorf("enqueue: %v", err)
					return
				}
			}
		}(p)
	}

	total := producers * perProducer
	var mu sync.Mutex
	seen := make(map[uint64]int, total)
	var consumers sync.WaitGroup
	for c := uint64(1); c <= 4; c++ {
		consumers.Add(1)
		go func(cid uint64) {
			defer consumers.Done()
			for {
				mu.Lock()
				if len(seen) >= total {
					mu.Unlock()
					return
				}
				mu.Unlock()
				m, ok := b.Lease(cid)
				if !ok {
					time.Sleep(time.Millisecond)
					continue
				}
				if !b.Ack(m.GetId(), cid) {
					t.Errorf("own ack refused for id %d", m.GetId())
					return
				}
				mu.Lock()
				seen[m.GetId()]++
				mu.Unlock()
			}
		}(c)
	}

	wg.Wait()
	consumers.Wait()

	require.Len(t, seen, total, "every message delivered")
	for id, n := range seen {
		require.Equal(t, 1, n, "id %d delivered %d times in steady state", id, n)
	}
	st := b.Inspect()
	assert.Equal(t, 0, st.Depth)
	assert.Equal(t, int64(0), st.InFlight)
	assert.Equal(t, int64(0), st.Dropped)
}

// delivery_attempts counts leases — 1 on first delivery, incrementing on
// each redelivery of the same (stable-id) message.
func TestDeliveryAttemptsIncrement(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 4, RetryBackoffBase: time.Nanosecond, RetryBackoffMax: time.Nanosecond})
	require.NoError(t, b.Enqueue(context.Background(), msg("m1")))

	m, ok := b.Lease(1)
	require.True(t, ok)
	assert.Equal(t, uint32(1), m.GetDeliveryAttempts())

	b.ReleaseConsumer(1)
	m, ok = b.Lease(2)
	require.True(t, ok)
	assert.Equal(t, uint32(2), m.GetDeliveryAttempts(), "redelivery must increment attempts")

	b.ReleaseConsumer(2)
	m, ok = b.Lease(3)
	require.True(t, ok)
	assert.Equal(t, uint32(3), m.GetDeliveryAttempts())
	assert.Equal(t, m.GetId(), m.GetId(), "id stays stable while attempts increment")
}

// fakeClock is a thread-safe manually-advanced clock for deterministic
// visibility/TTL tests (the background sweeper reads it concurrently).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_753_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// a disconnect-requeued message is invisible during its backoff window and
// becomes leasable only after base*2^(attempts-1) elapses — no hot-loop redelivery.
func TestRetryBackoffVisibility(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 4, RetryBackoffBase: 500 * time.Millisecond, RetryBackoffMax: 30 * time.Second, Clock: clk.Now,
	})
	require.NoError(t, b.Enqueue(context.Background(), msg("m1")))

	_, ok := b.Lease(1)
	require.True(t, ok)
	require.Equal(t, 1, b.ReleaseConsumer(1)) // attempts=1 → backoff 500ms

	_, ok = b.Lease(2)
	assert.False(t, ok, "message must be invisible inside its backoff window")
	assert.Equal(t, 1, b.Inspect().RetryDepth)

	clk.Advance(499 * time.Millisecond)
	_, ok = b.Lease(2)
	assert.False(t, ok, "still invisible 1ms before the deadline")

	clk.Advance(2 * time.Millisecond)
	m, ok := b.Lease(2)
	require.True(t, ok, "visible after the backoff window")
	assert.Equal(t, uint32(2), m.GetDeliveryAttempts())

	// Second requeue doubles the delay: attempts=2 → 1s.
	require.Equal(t, 1, b.ReleaseConsumer(2))
	clk.Advance(999 * time.Millisecond)
	_, ok = b.Lease(3)
	assert.False(t, ok, "second backoff must be doubled (1s)")
	clk.Advance(2 * time.Millisecond)
	_, ok = b.Lease(3)
	assert.True(t, ok)
}

// visible retry-lane work wins over fresh main-lane traffic — aged
// redeliveries are not starved behind new production.
func TestRetryPreferredOverMain(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 4, RetryBackoffBase: 100 * time.Millisecond, RetryBackoffMax: time.Second, Clock: clk.Now,
	})
	ctx := context.Background()
	require.NoError(t, b.Enqueue(ctx, msg("old")))
	m, ok := b.Lease(1)
	require.True(t, ok)
	oldID := m.GetId()
	b.ReleaseConsumer(1)

	require.NoError(t, b.Enqueue(ctx, msg("fresh")))
	clk.Advance(101 * time.Millisecond)

	next, ok := b.Lease(2)
	require.True(t, ok)
	assert.Equal(t, oldID, next.GetId(), "visible retry work must be served before fresh main traffic")
}

// after MaxDeliveries the message routes to the DLQ instead of circulating.
func TestMaxDeliveriesDeadLetters(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 4, MaxDeliveries: 2, RetryBackoffBase: time.Millisecond, RetryBackoffMax: time.Millisecond, Clock: clk.Now,
	})
	require.NoError(t, b.Enqueue(context.Background(), msg("poison")))

	for attempt := 1; attempt <= 2; attempt++ {
		clk.Advance(2 * time.Millisecond)
		m, ok := b.Lease(1)
		require.True(t, ok, "attempt %d must deliver", attempt)
		require.Equal(t, uint32(attempt), m.GetDeliveryAttempts())
		require.Equal(t, 1, b.ReleaseConsumer(1))
	}

	clk.Advance(time.Hour)
	_, ok := b.Lease(1)
	assert.False(t, ok, "dead-lettered message must leave circulation")

	st := b.Inspect()
	assert.Equal(t, 1, st.DLQDepth)
	assert.Equal(t, int64(1), st.DeadLetteredTotal)
	assert.Equal(t, 0, st.RetryDepth)

	entries := b.DLQList(0)
	require.Len(t, entries, 1)
	assert.Equal(t, "poison", entries[0].MetricName)
	assert.Equal(t, uint32(2), entries[0].Attempts)
	assert.Contains(t, entries[0].Reason, "max delivery attempts exceeded (2)")
}

// replay drains the DLQ back into circulation as fresh work (attempts
// reset, id preserved) and respects the capacity budget.
func TestDLQReplay(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 4, MaxDeliveries: 1, RetryBackoffBase: time.Millisecond, RetryBackoffMax: time.Millisecond, Clock: clk.Now,
	})
	require.NoError(t, b.Enqueue(context.Background(), msg("dl")))
	m, ok := b.Lease(1)
	require.True(t, ok)
	id := m.GetId()
	b.ReleaseConsumer(1) // attempts=1 >= MaxDeliveries=1 → DLQ
	require.Equal(t, 1, b.Inspect().DLQDepth)

	require.Equal(t, 1, b.DLQReplay())
	assert.Equal(t, 0, b.Inspect().DLQDepth)

	replayed, ok := b.Lease(2)
	require.True(t, ok)
	assert.Equal(t, id, replayed.GetId(), "replay preserves the stable id")
	assert.Equal(t, uint32(1), replayed.GetDeliveryAttempts(), "replay resets the attempt count")
}

// replay stops early when the main budget is full — partial replay, rest
// stays dead-lettered.
func TestDLQReplayRespectsBudget(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 2, MaxDeliveries: 1, RetryBackoffBase: time.Millisecond, RetryBackoffMax: time.Millisecond, Clock: clk.Now,
	})
	ctx := context.Background()
	require.NoError(t, b.Enqueue(ctx, msg("dl")))
	m, ok := b.Lease(1)
	require.True(t, ok)
	_ = m
	b.ReleaseConsumer(1) // → DLQ (frees budget)
	require.Equal(t, 1, b.Inspect().DLQDepth)

	// Fill the whole budget with fresh work.
	require.NoError(t, b.Enqueue(ctx, msg("f1")))
	require.NoError(t, b.Enqueue(ctx, msg("f2")))

	assert.Equal(t, 0, b.DLQReplay(), "no budget → nothing replayed")
	assert.Equal(t, 1, b.Inspect().DLQDepth, "entry must stay dead-lettered")
}

// the DLQ itself is bounded — overflow evicts the OLDEST dead-lettered
// entry and counts it.
func TestDLQOverflowEvictsOldest(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 8, MaxDeliveries: 1, DLQCapacity: 2,
		RetryBackoffBase: time.Millisecond, RetryBackoffMax: time.Millisecond, Clock: clk.Now,
	})
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		require.NoError(t, b.Enqueue(ctx, msg(fmt.Sprintf("dl%d", i))))
		_, ok := b.Lease(1)
		require.True(t, ok)
		b.ReleaseConsumer(1) // → DLQ
	}
	st := b.Inspect()
	assert.Equal(t, 2, st.DLQDepth)
	assert.Equal(t, int64(3), st.DeadLetteredTotal)
	assert.Equal(t, int64(1), st.DLQEvictedTotal)

	entries := b.DLQList(0)
	require.Len(t, entries, 2)
	assert.Equal(t, "dl2", entries[0].MetricName, "dl1 (oldest) must have been evicted")
}

// retry-lane messages count against the capacity budget.
func TestRetryCountsAgainstBudget(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 2, Policy: PolicyReject,
		RetryBackoffBase: time.Second, RetryBackoffMax: time.Second, Clock: clk.Now,
	})
	ctx := context.Background()
	require.NoError(t, b.Enqueue(ctx, msg("m1")))
	require.NoError(t, b.Enqueue(ctx, msg("m2")))
	_, ok := b.Lease(1)
	require.True(t, ok)
	_, ok = b.Lease(1)
	require.True(t, ok)
	require.Equal(t, 2, b.ReleaseConsumer(1)) // both → retry lane

	require.ErrorIs(t, b.Enqueue(ctx, msg("m3")), ErrFull,
		"retry-lane occupancy must reject new production")
}

// the background sweeper wakes parked consumers when a retry-lane message
// becomes visible — without a fresh Produce event.
func TestSweeperNotifiesOnVisibility(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{
		Capacity: 4, RetryBackoffBase: 20 * time.Millisecond, RetryBackoffMax: time.Second,
		SweepInterval: 5 * time.Millisecond,
	})
	notified := make(chan struct{}, 1)
	b.SetNotify(func() {
		select {
		case notified <- struct{}{}:
		default:
		}
	})
	require.NoError(t, b.Enqueue(context.Background(), msg("m1")))
	_, ok := b.Lease(1)
	require.True(t, ok)
	b.ReleaseConsumer(1)

	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("sweeper must notify once the retry entry is visible")
	}
	m, ok := b.Lease(2)
	require.True(t, ok)
	assert.Equal(t, uint32(2), m.GetDeliveryAttempts())
}

func TestParseOverflowPolicy(t *testing.T) {
	for _, s := range []string{"reject", "drop-oldest", "block"} {
		p, err := ParseOverflowPolicy(s)
		require.NoError(t, err)
		assert.Equal(t, OverflowPolicy(s), p)
	}
	_, err := ParseOverflowPolicy("evict-random")
	require.Error(t, err)
}

func TestEnqueueAfterClose(t *testing.T) {
	b := NewBroker(BrokerConfig{Capacity: 2})
	require.NoError(t, b.Close())
	require.NoError(t, b.Close(), "Close must be idempotent")
	err := b.Enqueue(context.Background(), msg("late"))
	require.ErrorIs(t, err, ErrClosed)
}

// a lease older than LeaseTTL is reclaimed by the sweeper — requeued with
// backoff — and the stuck consumer's late ack becomes a no-op (credit revoked).
func TestSweepExpiredReclaimsLease(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 4, LeaseTTL: 30 * time.Second,
		RetryBackoffBase: time.Millisecond, RetryBackoffMax: time.Millisecond,
		Clock: clk.Now,
	})
	require.NoError(t, b.Enqueue(context.Background(), msg("stuck")))
	m, ok := b.Lease(1)
	require.True(t, ok)

	require.Equal(t, 0, b.SweepExpired(), "lease inside its TTL must not be swept")

	clk.Advance(31 * time.Second)
	require.Equal(t, 1, b.SweepExpired(), "expired lease must be reclaimed")

	st := b.Inspect()
	assert.Equal(t, int64(0), st.InFlight)
	assert.Equal(t, 1, st.RetryDepth)
	assert.Equal(t, int64(1), st.LeaseExpiredTotal)

	assert.False(t, b.Ack(m.GetId(), 1), "late ack of a swept lease is a no-op (credit revocation)")

	clk.Advance(time.Second)
	m2, ok := b.Lease(2)
	require.True(t, ok, "reclaimed message must be redeliverable")
	assert.Equal(t, m.GetId(), m2.GetId())
	assert.Equal(t, uint32(2), m2.GetDeliveryAttempts())
}

// a lease that expires past MaxDeliveries dead-letters instead of retrying.
func TestSweepExpiredRoutesToDLQ(t *testing.T) {
	clk := newFakeClock()
	b := newTestBroker(t, BrokerConfig{
		Capacity: 4, LeaseTTL: time.Second, MaxDeliveries: 1,
		RetryBackoffBase: time.Millisecond, RetryBackoffMax: time.Millisecond,
		Clock: clk.Now,
	})
	require.NoError(t, b.Enqueue(context.Background(), msg("stuck-poison")))
	_, ok := b.Lease(1)
	require.True(t, ok)
	clk.Advance(2 * time.Second)
	require.Equal(t, 1, b.SweepExpired())

	st := b.Inspect()
	assert.Equal(t, 1, st.DLQDepth)
	assert.Equal(t, 0, st.RetryDepth)
	assert.Equal(t, int64(1), st.DeadLetteredTotal)
}

// Timestamps are producer-owned: across enqueue, lease, requeue, and
// redelivery the broker mutates ONLY id and delivery_attempts — the payload
// (timestamp above all) passes through verbatim. Broker-side restamping would
// corrupt the telemetry timeline.
func TestBrokerNeverMutatesPayload(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 4, RetryBackoffBase: time.Nanosecond, RetryBackoffMax: time.Nanosecond})
	const producerTS = "2026-07-24T01:02:03.123456789Z"
	in := &pb.TelemetryMessage{
		Timestamp: producerTS, MetricName: "DCGM_FI_DEV_GPU_UTIL",
		GpuId: "0", Uuid: "GPU-ts-owned", Value: 42.5,
	}
	require.NoError(t, b.Enqueue(context.Background(), in))

	m, ok := b.Lease(1)
	require.True(t, ok)
	assert.Equal(t, producerTS, m.GetTimestamp(), "lease must not restamp")
	b.ReleaseConsumer(1)

	m, ok = b.Lease(2)
	require.True(t, ok)
	assert.Equal(t, producerTS, m.GetTimestamp(), "redelivery must not restamp")
	assert.Equal(t, 42.5, m.GetValue())
	assert.Equal(t, "GPU-ts-owned", m.GetUuid())
}

// The DEFAULT overflow policy is drop-oldest: telemetry freshness-first — a
// full budget admits new production by evicting the oldest queued reading,
// counted in DroppedOverflow, with no producer error.
func TestDefaultPolicyIsDropOldest(t *testing.T) {
	b := newTestBroker(t, BrokerConfig{Capacity: 2})
	ctx := context.Background()

	require.NoError(t, b.Enqueue(ctx, msg("old-1")))
	require.NoError(t, b.Enqueue(ctx, msg("old-2")))
	require.NoError(t, b.Enqueue(ctx, msg("fresh")), "default policy must admit fresh telemetry")

	st := b.Inspect()
	assert.Equal(t, int64(1), st.DroppedOverflow, "the eviction must be counted")
	assert.Equal(t, int64(0), st.RejectedTotal)
	assert.Equal(t, 2, st.Depth)

	m, ok := b.Lease(1)
	require.True(t, ok)
	assert.Equal(t, "old-2", m.GetMetricName(), "oldest reading evicted; newer ones retained")
}
