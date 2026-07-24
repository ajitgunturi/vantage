// Package server implements the MQ gRPC service (Produce, Consume) with
// broker-side at-least-once delivery (MQ-09) and client-driven credit flow
// control (MQ-10), per ADR-001.
//
// Delivery model: each Consume is a bidirectional stream. The client sends an
// initial ConsumeClientMsg carrying its credit window, then one ack per message
// it has durably handled. The broker assigns a stable monotonic id to every
// message at Enqueue, leases it to a consumer on Send, and removes it from
// custody only when that consumer acks the id. Unacked leases are requeued
// oldest-first on disconnect and redelivered to a survivor — so a consumer that
// dies mid-batch loses nothing (duplicates possible, absorbed by the idempotent
// Collector).
//
// Backpressure: Produce surfaces queue.ErrFull as codes.ResourceExhausted so
// producers back off instead of the broker silently evicting. Capacity accounting: the store
// counts in-flight leases against capacity, so disconnect requeues never evict.
//
// Lock discipline (enforced throughout):
//   - All queue/lease state lives behind the store's own mutex (queue.Broker).
//   - Channel operations (sem, ackCh, notifyCh) happen with NO mutex held.
//   - Each stream has a unique broker-assigned consumer id; ack ownership is
//     enforced by the store (T-01.1-01), not by per-handler state.
package server

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/pkg/pb"
)

// creditCeiling bounds the per-consumer in-flight window when the ring capacity
// is unbounded (WAL backend, Capacity == -1) or very small. It caps the semaphore
// allocation so a malicious/huge initial credit cannot exhaust memory (T-01.1-03).
const creditCeiling = 1000

// ServerStats is a point-in-time snapshot of MQServer state. All fields are
// safe to read without a lock (atomic loads + a store snapshot).
type ServerStats struct {
	Produced        int64 // messages accepted by Produce
	Rejected        int64 // Produce refusals under backpressure — not loss
	Consumed        int64 // acks received (confirmed deliveries) — at-least-once removal point
	Delivered       int64 // messages Sent to consumers (>= Consumed; the gap is in-flight + redelivered)
	Redelivered     int64 // messages requeued for redelivery (disconnect / failed send)
	InFlight        int64 // messages Sent but not yet acked (broker lease table gauge)
	ActiveConsumers int32
	Depth           int
	RetryDepth      int // messages awaiting redelivery (retry lane)
	DLQDepth        int // dead-lettered messages currently held
	Capacity        int
	DroppedOverflow int64 // drop-oldest evictions (opt-in policy only)
	DroppedRequeue  int64 // requeue evictions — structurally 0 under capacity accounting (tripwire)
	Dropped         int64 // sum of drop causes (back-compat)
	DeadLettered    int64 // messages routed to the DLQ
	LeaseExpired    int64 // leases reclaimed by the TTL sweeper
}

// MQServer implements pb.MQServiceServer with broker-side at-least-once delivery.
// There is no dispatch goroutine and no work channel: each Consume handler polls
// the store directly under its credit window and parks on a broadcast notify
// channel when the store is empty.
type MQServer struct {
	pb.UnimplementedMQServiceServer

	store         queue.Store
	defaultCredit int // applied when a consumer's initial credit is <= 0

	notifyMu sync.RWMutex  // guards notifyCh
	notifyCh chan struct{} // closed-and-reopened on each Produce to broadcast "new message"

	consumerSeq uint64 // atomic: unique per-stream consumer id for lease ownership
	produced    int64  // atomic
	rejected    int64  // atomic: Produce backpressure refusals
	consumed    int64  // atomic: acks accepted by the store
	delivered   int64  // atomic: messages Sent
	redelivered int64  // atomic: leases requeued for redelivery
	activeC     int32  // atomic: active Consume handlers

	drainCh    chan struct{} // closed by BeginDrain: refuse Produce, keep consumers
	shutdownCh chan struct{} // closed by Shutdown: consumers terminate
	drainOnce  sync.Once
	once       sync.Once
}

// NewMQServer constructs an MQServer backed by the given store. defaultCredit is
// the in-flight window granted to a consumer whose initial ConsumeClientMsg
// carries a non-positive credit; values <= 0 default to 20. No goroutine is
// started — consumers poll the store directly.
func NewMQServer(s queue.Store, defaultCredit int) *MQServer {
	if defaultCredit <= 0 {
		defaultCredit = 20
	}
	srv := &MQServer{
		store:         s,
		defaultCredit: defaultCredit,
		notifyCh:      make(chan struct{}),
		drainCh:       make(chan struct{}),
		shutdownCh:    make(chan struct{}),
	}
	// Register the wakeup hook so the store's sweeper can rouse parked
	// consumers when retry-lane messages become visible or TTL-expired leases
	// are reclaimed. Optional: fakes without a sweeper skip this.
	if ns, ok := s.(interface{ SetNotify(func()) }); ok {
		ns.SetNotify(srv.NotifyAll)
	}
	return srv
}

// DLQList returns up to limit dead-lettered messages, oldest first.
func (s *MQServer) DLQList(limit int) []queue.DLQEntry {
	return s.store.DLQList(limit)
}

// DLQReplay drains the DLQ back into the main lane and wakes consumers.
// Returns the number of replayed messages (operator path).
func (s *MQServer) DLQReplay() int {
	n := s.store.DLQReplay()
	if n > 0 {
		s.NotifyAll()
	}
	return n
}

// NotifyAll wakes every goroutine parked in notifyChan by closing the current
// notify channel and installing a fresh one. Closing a channel broadcasts to all
// readers simultaneously. Exported for the store's background sweeper to
// wake parked consumers when redeliveries become visible.
func (s *MQServer) NotifyAll() {
	s.notifyMu.Lock()
	old := s.notifyCh
	s.notifyCh = make(chan struct{})
	s.notifyMu.Unlock()
	close(old)
}

// notifyChan returns the current notify channel. Consume handlers read it under
// the RLock so they never observe a torn pointer mid-swap.
func (s *MQServer) notifyChan() <-chan struct{} {
	s.notifyMu.RLock()
	defer s.notifyMu.RUnlock()
	return s.notifyCh
}

// Produce implements MQServiceServer. It admits msg under the store's capacity
// budget and broadcasts to waiting consumers.
//
// Errors: codes.InvalidArgument for a nil message; codes.ResourceExhausted when
// the budget is full under the reject/block policies (backpressure — the
// producer should back off and retry); codes.Unavailable during shutdown (drain:
// a draining broker refuses new work so in-flight messages can clear).
func (s *MQServer) Produce(ctx context.Context, req *pb.ProduceRequest) (*pb.ProduceResponse, error) {
	if req.GetMessage() == nil {
		return nil, status.Error(codes.InvalidArgument, "message must not be nil")
	}
	if s.IsShuttingDown() {
		return nil, status.Error(codes.Unavailable, "shutting down — draining")
	}
	if err := s.store.Enqueue(ctx, req.Message); err != nil {
		switch {
		case errors.Is(err, queue.ErrFull):
			atomic.AddInt64(&s.rejected, 1)
			return nil, status.Error(codes.ResourceExhausted, "queue full — retry with backoff")
		case errors.Is(err, queue.ErrClosed):
			return nil, status.Error(codes.Unavailable, "shutting down")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil, status.FromContextError(err).Err()
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	atomic.AddInt64(&s.produced, 1)
	s.NotifyAll()
	return &pb.ProduceResponse{Accepted: true}, nil
}

// Consume implements the bidirectional at-least-once delivery RPC. The first
// client message sets the credit window; subsequent client messages are acks.
// Exactly one goroutine calls stream.Recv() (the recv goroutine) and exactly one
// calls stream.Send() (this handler's send loop) — the only safe gRPC bidi shape.
func (s *MQServer) Consume(stream pb.MQService_ConsumeServer) error {
	atomic.AddInt32(&s.activeC, 1)
	defer atomic.AddInt32(&s.activeC, -1)

	// Broker-assigned stream identity — the lease-ownership key. Never derived
	// from the client-supplied ConsumerId (spoofable, logging-only).
	cid := atomic.AddUint64(&s.consumerSeq, 1)

	ctx := stream.Context()

	// Step 1: read the initial credit message.
	initMsg, err := stream.Recv()
	if err != nil {
		return err
	}
	credit := int(initMsg.GetCredit())
	if credit <= 0 {
		credit = s.defaultCredit
	}
	if ceiling := s.creditCeiling(); credit > ceiling {
		credit = ceiling
	}

	// Step 2: credit semaphore — a buffered channel as a token bucket. Holding a
	// token is permission for one in-flight (unacked) message; this structurally
	// bounds in-flight to credit with no counter and no over-pull.
	sem := make(chan struct{}, credit)
	for i := 0; i < credit; i++ {
		sem <- struct{}{}
	}

	// Step 3: ack channel from the recv goroutine to the send loop.
	ackCh := make(chan uint64, credit)

	// Step 4: recv goroutine — the only caller of stream.Recv().
	var wg sync.WaitGroup
	var recvFinalErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ackCh) // closing ackCh tells the send loop the client is gone
		for {
			msg, rerr := stream.Recv()
			if rerr != nil {
				// Translate io.EOF to nil: the client called CloseSend(), which is
				// a clean half-close — not an error. Anything else (transport failure,
				// context cancel) is surfaced as-is (MQ MINOR: io.EOF translation).
				if errors.Is(rerr, io.EOF) {
					recvFinalErr = nil
				} else {
					recvFinalErr = rerr
				}
				return
			}
			if id := msg.GetAckId(); id != 0 {
				select {
				case ackCh <- id:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Step 5: deferred cleanup — join the recv goroutine (no leak), drain any
	// remaining acks FIRST (so confirmed messages are not needlessly requeued),
	// then release this consumer's unacked leases for redelivery to a survivor.
	// Capacity accounting guarantees the release has headroom — it never evicts.
	defer func() {
		wg.Wait()
		// After wg.Wait(), the recv goroutine has exited and closed ackCh — this
		// range loop always terminates.
		for id := range ackCh {
			if s.store.Ack(id, cid) {
				atomic.AddInt64(&s.consumed, 1)
			}
		}
		if n := s.store.ReleaseConsumer(cid); n > 0 {
			atomic.AddInt64(&s.redelivered, int64(n))
			s.NotifyAll() // wake survivors to pick up the redelivered messages
		}
	}()

	// ack applies a single ack id via the store's ownership guard: a no-op
	// unless the id is a live lease held by THIS stream (unknown/double/foreign
	// acks and ack spoofing — T-01.1-01). Returns true when a lease was removed;
	// false also covers leases revoked by the TTL sweeper — in that case
	// the credit token is deliberately NOT replenished (credit revocation).
	ack := func(id uint64) bool {
		if !s.store.Ack(id, cid) {
			return false
		}
		atomic.AddInt64(&s.consumed, 1)
		return true
	}

	// Step 6: send loop — the only caller of stream.Send().
	for {
		// Drain pending acks first, replenishing credit for each genuine ack.
	ackDrain:
		for {
			select {
			case id, ok := <-ackCh:
				if !ok {
					return recvFinalErr
				}
				if ack(id) {
					sem <- struct{}{}
				}
			default:
				break ackDrain
			}
		}

		// Acquire a credit token (block until the window has room).
		select {
		case <-sem:
			// have a token — proceed to fetch a message
		case id, ok := <-ackCh:
			if !ok {
				return recvFinalErr
			}
			if ack(id) {
				sem <- struct{}{}
			}
			continue
		case <-ctx.Done():
			return ctx.Err()
		case <-s.shutdownCh:
			// Server is shutting down — wake parked consumers so GracefulStop
			// is not blocked indefinitely (M-3 fix).
			return status.Error(codes.Unavailable, "shutting down")
		}

		// Capture the notify channel BEFORE Lease to close the missed-wakeup
		// window (M-2 fix): if Produce+NotifyAll fires between capture and lease,
		// we either see the message in Lease or we hold the pre-close handle
		// (immediate wakeup). Reading notifyChan AFTER Lease risks sleeping
		// forever on a missed signal.
		ch := s.notifyChan()

		// Lease the next message; park if nothing is deliverable.
		msg, ok := s.store.Lease(cid)
		if !ok {
			sem <- struct{}{} // return the unused token
			select {
			case <-ch: // Produce/redelivery signalled (M-2: captured before Lease)
			case id, ok := <-ackCh: // an ack arrived — process it (receiving consumes it)
				if !ok {
					return recvFinalErr
				}
				if ack(id) {
					sem <- struct{}{}
				}
			case <-ctx.Done():
				return ctx.Err()
			case <-s.shutdownCh:
				// Server is shutting down — wake parked consumers (M-3 fix).
				return status.Error(codes.Unavailable, "shutting down")
			}
			continue
		}

		if err := stream.Send(msg); err != nil {
			// Delivery failed — return the lease for immediate redelivery. The
			// message keeps its stable id; the deferred ReleaseConsumer
			// skips it because the lease is already gone.
			if s.store.ReleaseLease(msg.GetId(), cid) {
				atomic.AddInt64(&s.redelivered, 1)
				s.NotifyAll()
			}
			return err
		}
		atomic.AddInt64(&s.delivered, 1)
	}
}

// creditCeiling bounds a consumer's credit window to max(ring capacity,
// creditCeiling constant). It guards small-cap rings (capacity < constant) and
// unbounded backends (Capacity == -1) where a malicious/huge initial credit
// would otherwise exhaust memory (T-01.1-03, MQ MINOR: creditCeiling doc).
func (s *MQServer) creditCeiling() int {
	cap := s.store.Inspect().Capacity
	if cap < 0 {
		return creditCeiling // unbounded backend — fall back to the fixed cap
	}
	if cap > creditCeiling {
		return cap
	}
	return creditCeiling
}

// BeginDrain enters the drain phase: Produce refuses new work
// (Unavailable) and the readiness gate flips, but Consume streams stay alive
// so healthy consumers keep acking the backlog. Idempotent. Call Shutdown
// after the backlog clears (or the drain deadline expires).
func (s *MQServer) BeginDrain() {
	s.drainOnce.Do(func() { close(s.drainCh) })
}

// Drained reports whether no deliverable or in-flight work remains: main and
// retry lanes empty, no unacked leases. (Dead-lettered messages are terminal
// and do not block a drain.) Used by cmd/mq's preStop drain loop.
func (s *MQServer) Drained() bool {
	st := s.store.Inspect()
	return st.Depth == 0 && st.RetryDepth == 0 && st.InFlight == 0
}

// Shutdown signals server-wide shutdown. Idempotent. Implies BeginDrain;
// consumers terminate when they observe shutdownCh or their stream contexts
// are cancelled (gRPC GracefulStop).
func (s *MQServer) Shutdown() {
	s.BeginDrain()
	s.once.Do(func() { close(s.shutdownCh) })
}

// IsShuttingDown reports whether the server is draining or shutting down.
// Uses a non-blocking channel select — no mutex needed and no allocation.
// Safe to call on the HTTP hot path (used by ReadyzHandler for Kubernetes
// readiness probes — a draining broker is NOT ready).
func (s *MQServer) IsShuttingDown() bool {
	select {
	case <-s.drainCh:
		return true
	default:
	}
	select {
	case <-s.shutdownCh:
		return true
	default:
		return false
	}
}

// Stats returns a point-in-time snapshot of server state. All reads are atomic
// or store-snapshot; no server mutex is held (safe for the HTTP hot path).
func (s *MQServer) Stats() ServerStats {
	st := s.store.Inspect()
	return ServerStats{
		Produced:        atomic.LoadInt64(&s.produced),
		Rejected:        atomic.LoadInt64(&s.rejected),
		Consumed:        atomic.LoadInt64(&s.consumed),
		Delivered:       atomic.LoadInt64(&s.delivered),
		Redelivered:     atomic.LoadInt64(&s.redelivered),
		InFlight:        st.InFlight,
		ActiveConsumers: atomic.LoadInt32(&s.activeC),
		Depth:           st.Depth,
		RetryDepth:      st.RetryDepth,
		DLQDepth:        st.DLQDepth,
		Capacity:        st.Capacity,
		DroppedOverflow: st.DroppedOverflow,
		DroppedRequeue:  st.DroppedRequeue,
		Dropped:         st.Dropped,
		DeadLettered:    st.DeadLetteredTotal,
		LeaseExpired:    st.LeaseExpiredTotal,
	}
}
