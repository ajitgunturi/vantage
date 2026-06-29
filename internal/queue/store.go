// Package queue provides the storage seam for the MQ service.
// The Store interface is shaped so a WAL-backed backend (Phase 6) can be added
// without modifying any consumer code — consumers depend only on this interface.
package queue

import "github.com/ajitg/vantage/pkg/pb"

// StoreStats is a point-in-time snapshot of buffer state. It is a plain value
// type (safe to copy); callers cannot mutate internal Store state through it.
type StoreStats struct {
	// Depth is the number of messages currently buffered.
	Depth int
	// Capacity is the maximum number of messages the buffer can hold.
	// -1 signals unbounded — reserved for future WAL backend.
	Capacity int
	// Dropped is the cumulative count of messages overwritten by drop-oldest
	// semantics since process start. This is the authoritative source for the
	// DroppedTotal field in the HTTP inspect response.
	Dropped int64
}

// Store is the storage seam for the MQ service. The in-memory ring-buffer
// backend (RingStore) is the default. A WAL-backed implementation satisfying
// this interface will be added in Phase 6 without modifying any consumer code.
//
// Contract:
//   - Enqueue never blocks; drop-oldest fires when at capacity, so Produce
//     never returns ResourceExhausted in the default in-memory mode.
//   - TryDequeue is non-blocking; returns (nil, false) when the buffer is empty.
//   - Enqueue precondition: callers (MQServer.Produce) must validate msg != nil
//     before calling. RingStore stores pointers as-is without nil-checking.
type Store interface {
	// Enqueue adds msg to the buffer. Returns true if drop-oldest fired (a slot
	// was overwritten), false if a free slot was used. Never blocks.
	Enqueue(msg *pb.TelemetryMessage) bool

	// TryDequeue removes and returns the oldest buffered message. Returns
	// (nil, false) if the buffer is empty. Never blocks.
	TryDequeue() (*pb.TelemetryMessage, bool)

	// Inspect returns a snapshot of current buffer state. The returned StoreStats
	// is a value copy — callers receive a point-in-time view that cannot race
	// with future mutations.
	Inspect() StoreStats

	// Requeue inserts msgs at the front of the buffer for oldest-first redelivery
	// (MQ-09 / D-05 at-least-once semantics). The first element of msgs is
	// delivered first on the next TryDequeue.
	//
	// When the ring is full, the newest (tail-side) entry is evicted to make room,
	// incrementing the Dropped counter. Requeue([]) and Requeue(nil) are no-ops.
	// Never blocks; does not touch any channel.
	Requeue(msgs []*pb.TelemetryMessage)

	// Close releases any resources held by the backend. The in-memory backend
	// returns nil; WAL backends may flush and close file handles here.
	Close() error
}
