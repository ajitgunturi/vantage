// Package server implements the MQ gRPC service (Produce, Consume) with
// broker-side at-least-once delivery (MQ-09) and client-driven credit flow
// control (MQ-10), per ADR-001.
//
// Delivery model: each Consume is a bidirectional stream. The client sends an
// initial ConsumeClientMsg carrying its credit window, then one ack per message
// it has durably handled. The broker assigns a monotonic id to every message,
// leases it to the consumer on Send, and removes it from custody only when the
// consumer acks that id. Unacked leases are re-enqueued at the front of the ring
// on disconnect and redelivered to a survivor — so a consumer that dies mid-batch
// loses nothing (duplicates are possible and absorbed by the idempotent Collector).
//
// Lock discipline (enforced throughout):
//   - The store's mutex is acquired and released entirely inside the store
//     methods (TryDequeue / Enqueue / Requeue).
//   - Channel operations (sem, ackCh, notifyCh) happen with NO mutex held —
//     this prevents the lock-across-channel deadlock documented in the phase
//     PITFALLS research.
//   - The per-consumer lease table is owned exclusively by that consumer's send
//     loop goroutine; no mutex guards it. The recv goroutine only forwards ack
//     ids over ackCh.
package server

import (
	"context"
	"sort"
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
// safe to read without a lock because they are derived from atomic loads.
type ServerStats struct {
	Produced        int64 // messages accepted by Produce
	Consumed        int64 // acks received (confirmed deliveries) — at-least-once removal point
	Delivered       int64 // messages Sent to consumers (>= Consumed; the gap is in-flight + redelivered)
	Redelivered     int64 // messages re-enqueued on consumer disconnect
	InFlight        int64 // messages Sent but not yet acked (sum of all consumers' lease tables)
	ActiveConsumers int32
	Depth           int
	Capacity        int
	Dropped         int64
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

	nextID      uint64 // atomic: monotonic broker-assigned message id
	produced    int64  // atomic
	consumed    int64  // atomic: acks received
	delivered   int64  // atomic: messages Sent
	redelivered int64  // atomic: re-enqueued on disconnect
	inFlight    int64  // atomic: Sent-but-unacked across all consumers
	activeC     int32  // atomic: active Consume handlers

	shutdownCh chan struct{}
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
	return &MQServer{
		store:         s,
		defaultCredit: defaultCredit,
		notifyCh:      make(chan struct{}),
		shutdownCh:    make(chan struct{}),
	}
}

// notifyAll wakes every goroutine parked in notifyChan() by closing the current
// notify channel and installing a fresh one. Closing a channel broadcasts to all
// readers simultaneously — unlike the old buffered-1 notify which woke only one.
func (s *MQServer) notifyAll() {
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

// Produce implements MQServiceServer. It enqueues msg and broadcasts to all
// waiting consumers. Returns codes.InvalidArgument if req.Message is nil.
func (s *MQServer) Produce(ctx context.Context, req *pb.ProduceRequest) (*pb.ProduceResponse, error) {
	if req.GetMessage() == nil {
		return nil, status.Error(codes.InvalidArgument, "message must not be nil")
	}
	s.store.Enqueue(req.Message)
	atomic.AddInt64(&s.produced, 1)
	s.notifyAll()
	return &pb.ProduceResponse{Accepted: true}, nil
}

// Consume implements the bidirectional at-least-once delivery RPC. The first
// client message sets the credit window; subsequent client messages are acks.
// Exactly one goroutine calls stream.Recv() (the recv goroutine) and exactly one
// calls stream.Send() (this handler's send loop) — the only safe gRPC bidi shape.
func (s *MQServer) Consume(stream pb.MQService_ConsumeServer) error {
	atomic.AddInt32(&s.activeC, 1)
	defer atomic.AddInt32(&s.activeC, -1)

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

	// Step 3: per-consumer lease table, owned solely by this send loop.
	leases := make(map[uint64]*pb.TelemetryMessage, credit)

	// Step 4: ack channel from the recv goroutine to the send loop.
	ackCh := make(chan uint64, credit)

	// Step 5: recv goroutine — the only caller of stream.Recv().
	var wg sync.WaitGroup
	var recvFinalErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(ackCh) // closing ackCh tells the send loop the client is gone
		for {
			msg, rerr := stream.Recv()
			if rerr != nil {
				recvFinalErr = rerr
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

	// Step 6: deferred cleanup — join the recv goroutine (no leak), then requeue
	// unacked leases oldest-first for redelivery to a survivor (at-least-once).
	defer func() {
		wg.Wait()
		if len(leases) > 0 {
			msgs := sortedByID(leases)
			s.store.Requeue(msgs)
			atomic.AddInt64(&s.redelivered, int64(len(msgs)))
			atomic.AddInt64(&s.inFlight, -int64(len(msgs)))
			for _, m := range msgs {
				delete(leases, m.GetId())
			}
			s.notifyAll() // wake survivors to pick up the redelivered messages
		}
		// Drain any acks that arrived after the send loop exited.
		for id := range ackCh {
			if _, ok := leases[id]; ok {
				delete(leases, id)
				atomic.AddInt64(&s.consumed, 1)
				atomic.AddInt64(&s.inFlight, -1)
			}
		}
	}()

	// ack applies a single ack id to the lease table: a no-op unless the id is a
	// live lease held by THIS consumer (guards unknown/double acks and ack
	// spoofing — T-01.1-01). Returns true when a lease was actually removed.
	ack := func(id uint64) bool {
		if _, ok := leases[id]; !ok {
			return false
		}
		delete(leases, id)
		atomic.AddInt64(&s.consumed, 1)
		atomic.AddInt64(&s.inFlight, -1)
		return true
	}

	// Step 7: send loop — the only caller of stream.Send().
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
		}

		// Fetch the next message; park if the store is empty.
		msg, ok := s.store.TryDequeue()
		if !ok {
			sem <- struct{}{} // return the unused token
			select {
			case <-s.notifyChan(): // Produce signalled a new message
			case id, ok := <-ackCh: // an ack arrived — process it (receiving consumes it)
				if !ok {
					return recvFinalErr
				}
				if ack(id) {
					sem <- struct{}{}
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		// Assign a broker id, lease the message, and deliver it.
		id := atomic.AddUint64(&s.nextID, 1)
		msg.Id = id
		leases[id] = msg
		if err := stream.Send(msg); err != nil {
			// Delivery failed — undo the lease and re-enqueue this one message.
			delete(leases, id)
			sem <- struct{}{}
			s.store.Requeue([]*pb.TelemetryMessage{msg})
			return err
		}
		atomic.AddInt64(&s.delivered, 1)
		atomic.AddInt64(&s.inFlight, 1)
	}
}

// creditCeiling bounds a consumer's credit window. It is the ring capacity when
// bounded (and >= creditCeiling), otherwise the fixed creditCeiling constant.
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

// sortedByID returns the lease values ordered by ascending broker id, so
// redelivery preserves original production order (oldest-first).
func sortedByID(leases map[uint64]*pb.TelemetryMessage) []*pb.TelemetryMessage {
	ids := make([]uint64, 0, len(leases))
	for id := range leases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	msgs := make([]*pb.TelemetryMessage, 0, len(ids))
	for _, id := range ids {
		msgs = append(msgs, leases[id])
	}
	return msgs
}

// Shutdown signals server-wide shutdown. Idempotent. Consumers terminate when
// their stream contexts are cancelled (gRPC GracefulStop); this signal is kept
// for cmd/mq's shutdown ordering and future backends.
func (s *MQServer) Shutdown() {
	s.once.Do(func() { close(s.shutdownCh) })
}

// Stats returns a point-in-time snapshot of server state. All reads are atomic;
// no mutex is held by Stats itself (safe to call from the HTTP hot path).
func (s *MQServer) Stats() ServerStats {
	st := s.store.Inspect()
	return ServerStats{
		Produced:        atomic.LoadInt64(&s.produced),
		Consumed:        atomic.LoadInt64(&s.consumed),
		Delivered:       atomic.LoadInt64(&s.delivered),
		Redelivered:     atomic.LoadInt64(&s.redelivered),
		InFlight:        atomic.LoadInt64(&s.inFlight),
		ActiveConsumers: atomic.LoadInt32(&s.activeC),
		Depth:           st.Depth,
		Capacity:        st.Capacity,
		Dropped:         st.Dropped,
	}
}
