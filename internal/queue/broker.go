package queue

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ajitg/vantage/pkg/pb"
)

// Compile-time assertion: Broker must satisfy the Store interface.
var _ Store = (*Broker)(nil)

// ErrFull is returned by Enqueue when the broker's capacity budget
// (queued + retrying + in-flight) is exhausted and the overflow policy does not
// admit the message. The gRPC layer maps it to codes.ResourceExhausted so
// producers back off and retry (backpressure contract).
var ErrFull = errors.New("queue: capacity budget exhausted")

// ErrClosed is returned by Enqueue after Close.
var ErrClosed = errors.New("queue: broker closed")

// OverflowPolicy selects what Enqueue does when the capacity budget is full.
type OverflowPolicy string

const (
	// PolicyReject refuses the new message with ErrFull — visible
	// backpressure; the producer keeps the message and retries with backoff.
	// Opt-in for workloads where every record matters more than freshness.
	PolicyReject OverflowPolicy = "reject"
	// PolicyDropOldest (default) evicts the oldest queued message to admit the
	// new one. This is a telemetry pipeline: under overload the oldest reading
	// is the least valuable, so freshness wins — and every eviction is counted
	// (DroppedOverflow) so operators can see the pressure and scale instead.
	PolicyDropOldest OverflowPolicy = "drop-oldest"
	// PolicyBlock parks the producer until space frees, the configured
	// BlockTimeout elapses (→ ErrFull), or its context is cancelled.
	PolicyBlock OverflowPolicy = "block"
)

// ParseOverflowPolicy validates a policy string (env MQ_OVERFLOW_POLICY).
func ParseOverflowPolicy(s string) (OverflowPolicy, error) {
	switch OverflowPolicy(s) {
	case PolicyReject, PolicyDropOldest, PolicyBlock:
		return OverflowPolicy(s), nil
	}
	return "", fmt.Errorf("queue: unknown overflow policy %q (want reject|drop-oldest|block)", s)
}

// BrokerConfig configures a Broker. Zero values get defaults in NewBroker.
type BrokerConfig struct {
	// Capacity is the total message budget: queued (main + retry) plus leased
	// (in-flight) messages together never exceed it (capacity accounting).
	Capacity int
	// Policy is the Enqueue overflow policy. Default: PolicyDropOldest
	// (telemetry freshness-first; see the policy constants).
	Policy OverflowPolicy
	// BlockTimeout bounds how long PolicyBlock waits for space. Default: 1s.
	BlockTimeout time.Duration
	// RetryBackoffBase is the redelivery delay after the first failed delivery;
	// it doubles per attempt up to RetryBackoffMax (no more hot-loop
	// redelivery). Defaults: 500ms base, 30s max.
	RetryBackoffBase time.Duration
	RetryBackoffMax  time.Duration
	// MaxDeliveries dead-letters a message after this many deliveries.
	// Default: 5.
	MaxDeliveries uint32
	// DLQCapacity bounds the dead-letter lane (own budget, separate from
	// Capacity). At DLQ overflow the oldest dead-lettered entry is evicted and
	// counted. Default: 1000.
	DLQCapacity int
	// LeaseTTL is how long a delivery may stay unacked before the sweeper
	// reclaims it for redelivery. Default: 30s.
	LeaseTTL time.Duration
	// SweepInterval is the background sweeper tick (retry-visibility wakeups
	// and lease-TTL reclaim granularity). Default: 100ms.
	SweepInterval time.Duration
	// Clock overrides time.Now for deterministic tests.
	Clock func() time.Time
}

func (cfg *BrokerConfig) applyDefaults() {
	if cfg.Policy == "" {
		cfg.Policy = PolicyDropOldest
	}
	if cfg.BlockTimeout <= 0 {
		cfg.BlockTimeout = time.Second
	}
	if cfg.RetryBackoffBase <= 0 {
		cfg.RetryBackoffBase = 500 * time.Millisecond
	}
	if cfg.RetryBackoffMax <= 0 {
		cfg.RetryBackoffMax = 30 * time.Second
	}
	if cfg.MaxDeliveries == 0 {
		cfg.MaxDeliveries = 5
	}
	if cfg.DLQCapacity <= 0 {
		cfg.DLQCapacity = 1000
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 30 * time.Second
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = 100 * time.Millisecond
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
}

// entry is a queued or leased message with its broker-side delivery state.
// attempts counts leases (deliveries) of this entry — the DLQ-routing input.
type entry struct {
	msg      *pb.TelemetryMessage
	attempts uint32
}

// leaseRec tracks one in-flight entry: who holds it and until when.
type leaseRec struct {
	e        *entry
	consumer uint64
	deadline time.Time
}

// retryItem is a redelivery-pending entry with its earliest-visible time.
type retryItem struct {
	e         *entry
	visibleAt time.Time
}

// DLQEntry is an inspection copy of one dead-lettered message.
type DLQEntry struct {
	Msg            *pb.TelemetryMessage `json:"-"`
	ID             uint64               `json:"id"`
	GpuID          string               `json:"gpu_id"`
	MetricName     string               `json:"metric_name"`
	Timestamp      string               `json:"timestamp"`
	Attempts       uint32               `json:"attempts"`
	Reason         string               `json:"reason"`
	DeadLetteredAt time.Time            `json:"dead_lettered_at"`
}

// Broker is the in-memory MQ storage engine: a bounded FIFO main lane, a
// retry lane with per-message visibility backoff, a dead-letter lane, and a
// broker-owned lease table — all under one mutex. Hardening invariants:
//
//   - Admission control: main + retry + in-flight <= Capacity. Enqueue at a
//     full budget applies the overflow policy — drop-oldest by default
//     (telemetry freshness-first) with every eviction counted; reject/block
//     opt-ins give lossless backpressure instead.
//   - Because leases count against the budget, requeuing a consumer's unacked
//     leases ALWAYS has headroom — the requeue path can never evict unrelated
//     messages. DroppedRequeue is a tripwire counter and must stay 0.
//   - Broker ids are assigned once, at Enqueue; delivery_attempts increments
//     per lease.
//   - Redeliveries land in the retry lane with exponential visibility backoff
//     (no hot-loop), and a message delivered MaxDeliveries times is routed to
//     the DLQ instead of back into circulation.
//   - A background sweeper reclaims leases older than LeaseTTL and wakes
//     consumers when retry-lane messages become visible.
//
// Lock discipline: all state behind one sync.Mutex; notify/space channels are
// swapped under the mutex but waited on with no lock held.
type Broker struct {
	mu     sync.Mutex
	main   deque
	retry  retryHeap
	dlq    dlqRing
	leases map[uint64]leaseRec
	nextID uint64

	cfg BrokerConfig
	now func() time.Time

	rejected        int64 // Enqueue refusals (reject policy / block timeout / nothing evictable)
	droppedOverflow int64 // drop-oldest evictions on Enqueue (opt-in policy only)
	droppedRequeue  int64 // requeue-path evictions — structurally 0 (tripwire)
	deadLettered    int64 // messages routed to the DLQ (max deliveries)
	dlqEvicted      int64 // DLQ-overflow evictions (oldest dead-lettered entry lost)
	leaseExpired    int64 // leases reclaimed by the TTL sweeper

	notify  func()        // wakes parked consumers (server's NotifyAll); may be nil
	spaceCh chan struct{} // closed-and-swapped broadcast: "budget slot freed"

	sweepStop chan struct{}
	sweepDone chan struct{}
	closed    bool
}

// NewBroker constructs a Broker and starts its background sweeper. Panics on
// non-positive capacity (programming error, mirrors the former RingStore
// contract). Call Close to stop the sweeper.
func NewBroker(cfg BrokerConfig) *Broker {
	if cfg.Capacity <= 0 {
		panic(fmt.Sprintf("queue: Broker capacity must be positive, got %d", cfg.Capacity))
	}
	cfg.applyDefaults()
	b := &Broker{
		main:      newDeque(cfg.Capacity),
		leases:    make(map[uint64]leaseRec),
		dlq:       newDLQRing(cfg.DLQCapacity),
		cfg:       cfg,
		now:       cfg.Clock,
		spaceCh:   make(chan struct{}),
		sweepStop: make(chan struct{}),
		sweepDone: make(chan struct{}),
	}
	go b.sweeper()
	return b
}

// SetNotify registers fn to be called (outside the broker mutex) whenever
// parked consumers should re-poll: retry-lane messages became visible or
// expired leases were reclaimed. The MQ server registers its NotifyAll here.
func (b *Broker) SetNotify(fn func()) {
	b.mu.Lock()
	b.notify = fn
	b.mu.Unlock()
}

// used is the current budget consumption. Caller must hold b.mu.
func (b *Broker) used() int { return b.main.len() + b.retry.Len() + len(b.leases) }

// signalSpace wakes all producers parked in Enqueue (block policy).
// Caller must hold b.mu.
func (b *Broker) signalSpace() {
	close(b.spaceCh)
	b.spaceCh = make(chan struct{})
}

// backoff returns the visibility delay before redelivering a message that has
// been delivered `attempts` times: base * 2^(attempts-1), capped at max.
func (b *Broker) backoff(attempts uint32) time.Duration {
	d := b.cfg.RetryBackoffBase
	for i := uint32(1); i < attempts; i++ {
		d *= 2
		if d >= b.cfg.RetryBackoffMax {
			return b.cfg.RetryBackoffMax
		}
	}
	if d > b.cfg.RetryBackoffMax {
		d = b.cfg.RetryBackoffMax
	}
	return d
}

// requeue routes a no-longer-leased entry: DLQ once it has exhausted
// MaxDeliveries, otherwise the retry lane — immediately visible for transport
// failures (delay=0) or after exponential backoff for disconnect/TTL requeues.
// Caller must hold b.mu. Budget headroom is guaranteed: the entry just
// left the lease table, so used() < capacity here by invariant.
func (b *Broker) requeue(e *entry, delay time.Duration) {
	if e.attempts >= b.cfg.MaxDeliveries {
		if b.dlq.push(dlqItem{e: e, reason: fmt.Sprintf("max delivery attempts exceeded (%d)", b.cfg.MaxDeliveries), at: b.now()}) {
			b.dlqEvicted++
		}
		b.deadLettered++
		// Dead-lettering frees main-budget space (DLQ has its own budget).
		b.signalSpace()
		return
	}
	heap.Push(&b.retry, retryItem{e: e, visibleAt: b.now().Add(delay)})
}

// Enqueue admits msg into the main lane under the capacity budget, assigning
// its stable broker id. At a full budget the overflow policy decides:
// drop-oldest (default) → evict the oldest QUEUED entry (never a lease);
// reject → ErrFull; block → wait for space up to BlockTimeout / ctx
// cancellation.
func (b *Broker) Enqueue(ctx context.Context, msg *pb.TelemetryMessage) error {
	var timeout <-chan time.Time
	b.mu.Lock()
	for {
		if b.closed {
			b.mu.Unlock()
			return ErrClosed
		}
		if b.used() < b.cfg.Capacity {
			b.nextID++
			msg.Id = b.nextID
			b.main.pushBack(&entry{msg: msg})
			b.mu.Unlock()
			return nil
		}
		switch b.cfg.Policy {
		case PolicyDropOldest:
			if e, ok := b.main.popFront(); ok {
				_ = e // evicted oldest queued entry
				b.droppedOverflow++
				continue // budget now has room; loop admits msg
			}
			// Whole budget is leased/retrying — nothing evictable. Degrade to reject.
			b.rejected++
			b.mu.Unlock()
			return ErrFull
		case PolicyBlock:
			if timeout == nil {
				t := time.NewTimer(b.cfg.BlockTimeout)
				defer t.Stop()
				timeout = t.C
			}
			ch := b.spaceCh
			b.mu.Unlock()
			select {
			case <-ch:
				b.mu.Lock()
				continue
			case <-timeout:
				b.mu.Lock()
				b.rejected++
				b.mu.Unlock()
				return ErrFull
			case <-ctx.Done():
				return ctx.Err()
			}
		default: // PolicyReject
			b.rejected++
			b.mu.Unlock()
			return ErrFull
		}
	}
}

// Lease returns the next deliverable message and records it in-flight for
// consumer. Visible retry-lane messages win over fresh main-lane traffic
// (they are the oldest work); retry entries still inside their backoff window
// are not deliverable. Attempts increment on every lease; the lease
// deadline seeds the TTL sweeper. Non-blocking.
func (b *Broker) Lease(consumer uint64) (*pb.TelemetryMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var e *entry
	if b.retry.Len() > 0 && !b.now().Before(b.retry[0].visibleAt) {
		e = heap.Pop(&b.retry).(retryItem).e
	} else if me, ok := b.main.popFront(); ok {
		e = me
	} else {
		return nil, false
	}
	e.attempts++
	e.msg.DeliveryAttempts = e.attempts // expose the attempt count on the wire
	b.leases[e.msg.Id] = leaseRec{e: e, consumer: consumer, deadline: b.now().Add(b.cfg.LeaseTTL)}
	return e.msg, true
}

// Ack removes the lease for id if — and only if — it is held by consumer
// (T-01.1-01 ownership guard; unknown, double, foreign, and TTL-revoked acks
// are no-ops). Returns true when a lease was removed; a budget slot frees and
// blocked producers wake.
func (b *Broker) Ack(id, consumer uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, ok := b.leases[id]
	if !ok || rec.consumer != consumer {
		return false
	}
	delete(b.leases, id)
	b.signalSpace()
	return true
}

// ReleaseLease requeues a single lease (failed Send) with immediate
// visibility — the transport died; a survivor should get it right away.
// No-op unless consumer holds the lease.
func (b *Broker) ReleaseLease(id, consumer uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, ok := b.leases[id]
	if !ok || rec.consumer != consumer {
		return false
	}
	delete(b.leases, id)
	b.requeue(rec.e, 0)
	return true
}

// ReleaseConsumer requeues ALL of consumer's unacked leases (disconnect path)
// into the retry lane with per-message exponential backoff, in ascending-id
// order, and reports how many were requeued or dead-lettered. Because leases
// count against the budget, this always has headroom and never evicts.
func (b *Broker) ReleaseConsumer(consumer uint64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ids []uint64
	for id, rec := range b.leases {
		if rec.consumer == consumer {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		rec := b.leases[id]
		delete(b.leases, id)
		b.requeue(rec.e, b.backoff(rec.e.attempts))
	}
	return len(ids)
}

// DLQList returns up to limit dead-lettered entries, oldest first.
// limit <= 0 returns all. The returned slice is a snapshot copy.
func (b *Broker) DLQList(limit int) []DLQEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dlq.list(limit)
}

// DLQReplay drains the DLQ back into the main lane as fresh work: attempts
// reset to zero, stable id preserved. Replay stops early if the main budget
// fills; remaining entries stay dead-lettered. Returns how many were replayed.
func (b *Broker) DLQReplay() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for b.used() < b.cfg.Capacity {
		item, ok := b.dlq.pop()
		if !ok {
			break
		}
		item.e.attempts = 0 // operator-directed fresh start
		b.main.pushBack(item.e)
		n++
	}
	return n
}

// SweepExpired reclaims leases whose deadline passed: each is requeued
// with backoff or dead-lettered per MaxDeliveries. The stuck consumer's later
// ack becomes a no-op — its credit token is NOT replenished (credit
// revocation). Returns how many leases were reclaimed. Called by the
// background sweeper; exported for deterministic tests.
func (b *Broker) SweepExpired() int {
	b.mu.Lock()
	n := 0
	nowT := b.now()
	var ids []uint64
	for id, rec := range b.leases {
		if !nowT.Before(rec.deadline) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		rec := b.leases[id]
		delete(b.leases, id)
		b.requeue(rec.e, b.backoff(rec.e.attempts))
		b.leaseExpired++
		n++
	}
	notify := b.notify
	b.mu.Unlock()
	if n > 0 && notify != nil {
		notify()
	}
	return n
}

// sweeper is the background loop: every SweepInterval it reclaims expired
// leases and wakes parked consumers when retry-lane work is visible.
func (b *Broker) sweeper() {
	defer close(b.sweepDone)
	t := time.NewTicker(b.cfg.SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-b.sweepStop:
			return
		case <-t.C:
			b.SweepExpired()
			b.mu.Lock()
			visible := b.retry.Len() > 0 && !b.now().Before(b.retry[0].visibleAt)
			notify := b.notify
			b.mu.Unlock()
			if visible && notify != nil {
				notify()
			}
		}
	}
}

// Inspect returns a point-in-time snapshot (value copy — cannot race).
func (b *Broker) Inspect() StoreStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return StoreStats{
		Depth:             b.main.len(),
		RetryDepth:        b.retry.Len(),
		DLQDepth:          b.dlq.len(),
		Capacity:          b.cfg.Capacity,
		InFlight:          int64(len(b.leases)),
		RejectedTotal:     b.rejected,
		DroppedOverflow:   b.droppedOverflow,
		DroppedRequeue:    b.droppedRequeue,
		Dropped:           b.droppedOverflow + b.droppedRequeue,
		DeadLetteredTotal: b.deadLettered,
		DLQEvictedTotal:   b.dlqEvicted,
		LeaseExpiredTotal: b.leaseExpired,
	}
}

// Close stops the sweeper, marks the broker closed, and wakes blocked
// producers. Idempotent.
func (b *Broker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.signalSpace()
	b.mu.Unlock()
	close(b.sweepStop)
	<-b.sweepDone
	return nil
}

// deque is a fixed-capacity FIFO ring of entries. Capacity equals the broker
// budget, and the budget invariant (main + retry + leased <= budget) guarantees
// pushBack is never called on a full ring — enforced by the panic guard.
type deque struct {
	buf   []*entry
	head  int
	count int
}

func newDeque(capacity int) deque { return deque{buf: make([]*entry, capacity)} }

func (d *deque) len() int { return d.count }

func (d *deque) pushBack(e *entry) {
	if d.count == len(d.buf) {
		panic("queue: deque overflow — budget invariant violated")
	}
	d.buf[(d.head+d.count)%len(d.buf)] = e
	d.count++
}

func (d *deque) popFront() (*entry, bool) {
	if d.count == 0 {
		return nil, false
	}
	e := d.buf[d.head]
	d.buf[d.head] = nil // release for GC
	d.head = (d.head + 1) % len(d.buf)
	d.count--
	return e, true
}

// retryHeap is a min-heap of retryItems ordered by visibleAt (ties: id order,
// so equal-visibility redeliveries keep production order).
type retryHeap []retryItem

func (h retryHeap) Len() int { return len(h) }
func (h retryHeap) Less(i, j int) bool {
	if h[i].visibleAt.Equal(h[j].visibleAt) {
		return h[i].e.msg.Id < h[j].e.msg.Id
	}
	return h[i].visibleAt.Before(h[j].visibleAt)
}
func (h retryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *retryHeap) Push(x any)   { *h = append(*h, x.(retryItem)) }
func (h *retryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = retryItem{}
	*h = old[:n-1]
	return it
}

// dlqItem is one dead-lettered entry with its routing metadata.
type dlqItem struct {
	e      *entry
	reason string
	at     time.Time
}

// dlqRing is a fixed-capacity FIFO of dead-lettered entries with drop-oldest
// at overflow (the DLQ must be bounded; overflow is counted by the caller).
type dlqRing struct {
	buf   []dlqItem
	head  int
	count int
}

func newDLQRing(capacity int) dlqRing { return dlqRing{buf: make([]dlqItem, capacity)} }

func (r *dlqRing) len() int { return r.count }

// push appends item; if the ring is full the oldest entry is evicted first.
// Returns true when an eviction occurred.
func (r *dlqRing) push(item dlqItem) bool {
	evicted := false
	if r.count == len(r.buf) {
		r.buf[r.head] = dlqItem{}
		r.head = (r.head + 1) % len(r.buf)
		r.count--
		evicted = true
	}
	r.buf[(r.head+r.count)%len(r.buf)] = item
	r.count++
	return evicted
}

func (r *dlqRing) pop() (dlqItem, bool) {
	if r.count == 0 {
		return dlqItem{}, false
	}
	item := r.buf[r.head]
	r.buf[r.head] = dlqItem{}
	r.head = (r.head + 1) % len(r.buf)
	r.count--
	return item, true
}

func (r *dlqRing) list(limit int) []DLQEntry {
	n := r.count
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]DLQEntry, 0, n)
	for i := 0; i < n; i++ {
		it := r.buf[(r.head+i)%len(r.buf)]
		out = append(out, DLQEntry{
			Msg:            it.e.msg,
			ID:             it.e.msg.GetId(),
			GpuID:          it.e.msg.GetGpuId(),
			MetricName:     it.e.msg.GetMetricName(),
			Timestamp:      it.e.msg.GetTimestamp(),
			Attempts:       it.e.attempts,
			Reason:         it.reason,
			DeadLetteredAt: it.at,
		})
	}
	return out
}
