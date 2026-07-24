// Package queue provides the storage engine and seam for the MQ service.
// The Store interface is shaped so a WAL-backed backend (ADR-009, Phase 7) can
// be added without modifying the gRPC layer — the server depends only on this
// interface. The in-memory Broker is the default implementation.
package queue

import (
	"context"

	"github.com/ajitg/vantage/pkg/pb"
)

// StoreStats is a point-in-time snapshot of broker state. It is a plain value
// type (safe to copy); callers cannot mutate internal state through it.
type StoreStats struct {
	// Depth is the number of messages queued for delivery (main lane).
	Depth int
	// RetryDepth is the number of messages awaiting redelivery in the retry
	// lane — inside or past their visibility backoff window.
	RetryDepth int
	// DLQDepth is the number of dead-lettered messages currently held.
	DLQDepth int
	// Capacity is the total budget: queued + in-flight never exceeds it.
	// -1 signals unbounded — reserved for a future WAL backend.
	Capacity int
	// InFlight is the number of leased (delivered-but-unacked) messages.
	InFlight int64
	// RejectedTotal counts Enqueue refusals (backpressure surfaced to the
	// producer — NOT message loss; the producer retries).
	RejectedTotal int64
	// DroppedOverflow counts drop-oldest evictions on Enqueue — non-zero only
	// under the opt-in drop-oldest policy (drop counters are split by cause).
	DroppedOverflow int64
	// DroppedRequeue counts requeue-path evictions. Structurally zero under
	// capacity accounting; kept as a tripwire — alert if it ever moves.
	DroppedRequeue int64
	// Dropped is the sum of all drop causes (back-compat with dropped_total).
	Dropped int64
	// DeadLetteredTotal counts messages routed to the DLQ after exhausting
	// MaxDeliveries.
	DeadLetteredTotal int64
	// DLQEvictedTotal counts dead-lettered entries lost to DLQ overflow
	// (bounded DLQ, drop-oldest within the DLQ only).
	DLQEvictedTotal int64
	// LeaseExpiredTotal counts leases reclaimed by the TTL sweeper.
	LeaseExpiredTotal int64
}

// Store is the storage seam for the MQ service (ADR-009). The in-memory Broker
// is the default backend; a WAL-backed implementation would persist the lanes
// and lease table behind the same contract.
//
// Contract (delivery hardening):
//   - Enqueue assigns the message's stable broker id and admits it under the
//     capacity budget (queued + in-flight <= capacity). At a full budget the
//     configured overflow policy applies; ErrFull signals backpressure.
//     Only PolicyBlock may block, bounded by BlockTimeout and ctx.
//   - Lease/Ack/ReleaseLease/ReleaseConsumer manage broker-owned leases with
//     per-consumer ownership guards; all are non-blocking.
//   - Enqueue precondition: callers (MQServer.Produce) must validate msg != nil.
type Store interface {
	// Enqueue admits msg under the capacity budget and assigns its broker id.
	// Returns ErrFull under backpressure, ErrClosed after Close, or a ctx error.
	Enqueue(ctx context.Context, msg *pb.TelemetryMessage) error

	// Lease removes the next deliverable message and records it in-flight for
	// consumer. Returns (nil, false) when nothing is deliverable. Never blocks.
	Lease(consumer uint64) (*pb.TelemetryMessage, bool)

	// Ack removes the lease for id if held by consumer. Unknown/foreign/double
	// acks return false (no-op).
	Ack(id, consumer uint64) bool

	// ReleaseLease returns one lease (failed Send) for immediate redelivery.
	ReleaseLease(id, consumer uint64) bool

	// ReleaseConsumer requeues all of consumer's unacked leases oldest-first
	// (disconnect path) and returns how many were requeued. Never evicts.
	ReleaseConsumer(consumer uint64) int

	// DLQList returns up to limit dead-lettered entries, oldest first.
	// limit <= 0 returns all.
	DLQList(limit int) []DLQEntry

	// DLQReplay drains the DLQ back into the main lane as fresh work (attempts
	// reset, id preserved), stopping early if the budget fills. Returns the
	// number of replayed messages.
	DLQReplay() int

	// Inspect returns a snapshot of current broker state.
	Inspect() StoreStats

	// Close releases backend resources and wakes blocked producers.
	Close() error
}
