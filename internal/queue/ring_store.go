package queue

import (
	"fmt"
	"sync"

	"github.com/ajitg/vantage/pkg/pb"
)

// Compile-time assertion: RingStore must satisfy the Store interface.
// A compile failure here means a method signature has drifted from the interface.
var _ Store = (*RingStore)(nil)

// RingStore is a thread-safe, bounded circular FIFO buffer with drop-oldest
// semantics when the buffer is full.
//
// A single sync.Mutex guards all state. sync.RWMutex would be semantically
// incorrect here: TryDequeue mutates head and count on every call, so there is
// no safe read-only path — every operation is effectively a write.
//
// Fields are unexported; external code accesses state only through the Store
// interface methods. The struct layout is intentionally tight: buf is pre-allocated
// in NewRingStore and never grown or shrunk after construction.
type RingStore struct {
	mu      sync.Mutex              // guards all fields below
	buf     []*pb.TelemetryMessage  // pre-allocated ring; fixed length == cap
	head    int                     // index of the oldest valid item
	tail    int                     // index where the next Enqueue writes
	count   int                     // number of valid items; invariant: 0 <= count <= cap
	cap     int                     // maximum capacity, set once at construction
	dropped int64                   // cumulative drop-oldest events, guarded by mu
}

// NewRingStore creates a new RingStore with the given capacity.
//
// Panics if capacity <= 0 — a non-positive capacity is a programming error,
// not a runtime condition (equivalent to calling make([]T, 0)).
func NewRingStore(capacity int) *RingStore {
	if capacity <= 0 {
		panic(fmt.Sprintf("queue: RingStore capacity must be positive, got %d", capacity))
	}
	return &RingStore{
		buf: make([]*pb.TelemetryMessage, capacity),
		cap: capacity,
	}
}

// Enqueue adds msg to the ring buffer. If the buffer is at capacity, the oldest
// item is discarded (drop-oldest semantics) before writing msg. Returns true if
// drop-oldest fired, false if a free slot was used. Never blocks.
//
// Precondition: msg must not be nil. Validation is the caller's responsibility
// (MQServer.Produce returns codes.InvalidArgument on nil input).
func (r *RingStore) Enqueue(msg *pb.TelemetryMessage) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	dropped := false
	if r.count == r.cap {
		// Buffer full: evict the oldest slot to make room.
		r.buf[r.head] = nil               // release pointer for GC
		r.head = (r.head + 1) % r.cap
		r.count--
		r.dropped++
		dropped = true
	}
	r.buf[r.tail] = msg
	r.tail = (r.tail + 1) % r.cap
	r.count++
	return dropped
}

// TryDequeue removes and returns the oldest message. Returns (nil, false) if the
// buffer is empty. Never blocks. The returned pointer is safe to use after the
// call — the mutex is released before returning.
func (r *RingStore) TryDequeue() (*pb.TelemetryMessage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.count == 0 {
		return nil, false
	}
	msg := r.buf[r.head]
	r.buf[r.head] = nil                  // release GC reference to the evicted slot
	r.head = (r.head + 1) % r.cap
	r.count--
	return msg, true
}

// Inspect returns a snapshot of the current buffer state. The returned StoreStats
// is a value copy — callers receive a point-in-time view that cannot race with
// future mutations.
func (r *RingStore) Inspect() StoreStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return StoreStats{
		Depth:    r.count,
		Capacity: r.cap,
		Dropped:  r.dropped,
	}
}

// Requeue inserts msgs at the front of the ring buffer so they are the next
// messages returned by TryDequeue (oldest-first redelivery, MQ-09 / D-05).
//
// Algorithm: iterate msgs in reverse order so that msgs[0] lands at the head
// after all insertions, giving msgs[0] the earliest dequeue priority.
//
// For each message, if the ring is full, evict the newest (tail-side) entry
// first (drop-newest semantics during requeue — inverse of Enqueue's drop-oldest),
// increment dropped, then decrement head and store the message.
//
// The whole method holds r.mu.Lock() to preserve the single-lock discipline;
// no channel is touched under the lock (prevents deadlock with the dispatch loop).
//
// Requeue(nil) and Requeue([]) are no-ops.
func (r *RingStore) Requeue(msgs []*pb.TelemetryMessage) {
	if len(msgs) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Insert in reverse so msgs[0] ends up at head.
	for i := len(msgs) - 1; i >= 0; i-- {
		if r.count == r.cap {
			// Ring full: evict the newest (tail-side) entry to prioritize redelivery.
			r.tail = (r.tail - 1 + r.cap) % r.cap
			r.buf[r.tail] = nil // release pointer for GC
			r.count--
			r.dropped++
		}
		r.head = (r.head - 1 + r.cap) % r.cap
		r.buf[r.head] = msgs[i]
		r.count++
	}
}

// Close is a no-op for the in-memory backend. Returns nil to satisfy the Store
// interface. WAL backends may flush pending writes and close file handles here.
func (r *RingStore) Close() error {
	return nil
}
