// Package server implements the MQ gRPC service (Produce, Consume) and
// the single dispatch goroutine that bridges the store to consumer streams.
//
// Lock discipline (enforced throughout):
//   - The store's mutex is acquired and released entirely inside TryDequeue.
//   - Channel sends to workCh happen with NO mutex held.
//     This prevents the lock-across-channel deadlock pattern documented in
//     the phase PITFALLS research.
package server

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/pkg/pb"
)

// ServerStats is a point-in-time snapshot of MQServer state. All fields are
// safe to read without a lock because they are derived from atomic loads.
type ServerStats struct {
	Produced        int64
	Consumed        int64
	ActiveConsumers int32
	Depth           int
	Capacity        int
	Dropped         int64
}

// MQServer implements pb.MQServiceServer. It bridges the Store backend to
// gRPC consumers using a single dispatch goroutine and a shared work channel,
// providing exactly-once single-dispatch delivery semantics (MQ-03).
type MQServer struct {
	pb.UnimplementedMQServiceServer

	store    queue.Store
	notify   chan struct{} // buffered(1): non-blocking signal from Produce to dispatch
	workCh   chan *pb.TelemetryMessage

	produced int64 // accessed via sync/atomic
	consumed int64 // accessed via sync/atomic
	activeC  int32 // accessed via sync/atomic

	shutdownCh chan struct{}
	once       sync.Once
}

// NewMQServer constructs a new MQServer backed by the given store and starts
// the dispatch goroutine. workChCap must be positive; values <= 0 default to 128.
func NewMQServer(s queue.Store, workChCap int) *MQServer {
	if workChCap <= 0 {
		workChCap = 128
	}
	srv := &MQServer{
		store:      s,
		notify:     make(chan struct{}, 1),
		workCh:     make(chan *pb.TelemetryMessage, workChCap),
		shutdownCh: make(chan struct{}),
	}
	go srv.dispatch()
	return srv
}

// dispatch is the single goroutine that drains the store into workCh.
// It closes workCh exactly once when it exits (via defer), signalling all
// blocked Consume calls to return nil (clean shutdown).
func (s *MQServer) dispatch() {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	defer close(s.workCh)
	for {
		select {
		case <-s.shutdownCh:
			return
		case <-s.notify:
			if done := s.drain(); done {
				return
			}
		case <-ticker.C:
			if done := s.drain(); done {
				return
			}
		}
	}
}

// drain moves all buffered messages from the store to workCh.
// Returns true if shutdown was requested during the drain; false otherwise.
// No mutex is held during the workCh send.
func (s *MQServer) drain() bool {
	for {
		msg, ok := s.store.TryDequeue()
		if !ok {
			return false
		}
		select {
		case s.workCh <- msg:
			// message forwarded; continue draining
		case <-s.shutdownCh:
			// shutdown requested while waiting to forward; exit immediately
			return true
		}
	}
}

// Produce implements MQServiceServer. It enqueues msg and signals the dispatch goroutine.
// Returns codes.InvalidArgument if req.Message is nil (T-01-03-01 mitigation).
func (s *MQServer) Produce(ctx context.Context, req *pb.ProduceRequest) (*pb.ProduceResponse, error) {
	if req.GetMessage() == nil {
		return nil, status.Error(codes.InvalidArgument, "message must not be nil")
	}
	s.store.Enqueue(req.Message)
	atomic.AddInt64(&s.produced, 1)
	// Non-blocking notify: if dispatch is already awake, skip the signal.
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return &pb.ProduceResponse{Accepted: true}, nil
}

// Consume implements MQServiceServer. It streams messages from workCh to the
// client until the client disconnects (ctx.Done) or the server shuts down
// (workCh closed). Goroutine-leak-free: both exit conditions are covered by
// a single select to prevent blocking after consumer disconnect (MQ-07).
func (s *MQServer) Consume(req *pb.ConsumeRequest, stream pb.MQService_ConsumeServer) error {
	atomic.AddInt32(&s.activeC, 1)
	defer atomic.AddInt32(&s.activeC, -1)

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-s.workCh:
			if !ok {
				// workCh was closed by dispatch during shutdown.
				return nil
			}
			if err := stream.Send(msg); err != nil {
				return err
			}
			atomic.AddInt64(&s.consumed, 1)
		}
	}
}

// Shutdown signals the dispatch goroutine to stop and close workCh. Idempotent.
func (s *MQServer) Shutdown() {
	s.once.Do(func() { close(s.shutdownCh) })
}

// Stats returns a point-in-time snapshot of server state. All reads are atomic;
// no mutex is held by Stats itself (safe to call from HTTP hot path).
func (s *MQServer) Stats() ServerStats {
	st := s.store.Inspect()
	return ServerStats{
		Produced:        atomic.LoadInt64(&s.produced),
		Consumed:        atomic.LoadInt64(&s.consumed),
		ActiveConsumers: atomic.LoadInt32(&s.activeC),
		Depth:           st.Depth,
		Capacity:        st.Capacity,
		Dropped:         st.Dropped,
	}
}
