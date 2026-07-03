package collector

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/ajitg/vantage/pkg/models"
	"github.com/ajitg/vantage/pkg/pb"
)

// Runner holds Collector runtime state including atomic readiness. It wraps the
// dial+reconnect orchestration previously in the package-level Run function.
// Use NewRunner to construct; call Run to start the consume loop.
type Runner struct {
	cfg   Config
	pool  *pgxpool.Pool
	ready atomic.Bool
}

// NewRunner creates a new Runner with the given config and DB pool. The runner
// is not ready until Run has successfully opened a Consume stream to the MQ.
func NewRunner(cfg Config, pool *pgxpool.Pool) *Runner {
	return &Runner{cfg: cfg, pool: pool}
}

// IsReady reports whether the runner currently has an active Consume stream.
// Returns false on a freshly created Runner and transitions to true once the
// MQ Consume RPC succeeds. Resets to false when the stream ends (reconnecting).
// Safe to call from any goroutine (backed by atomic.Bool).
func (r *Runner) IsReady() bool {
	return r.ready.Load()
}

// Run is the outer reconnect loop for the Collector microservice (COLL-02).
// It dials the MQ, calls consumeStream for one stream attempt (signalling
// readiness via r.ready when the stream opens / closes), and retries on
// failure with exponential backoff — base 100ms, cap 5s.
//
// The loop exits only when ctx is cancelled (graceful shutdown).
// Error classification mirrors the package-level Run: context.Canceled / ctx.Err() non-nil → clean exit;
// any other error → log and retry.
func (r *Runner) Run(ctx context.Context) error {
	backoff := 100 * time.Millisecond
	const maxBackoff = 5 * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		conn, err := dialMQ(r.cfg.MQAddr)
		if err != nil {
			slog.Warn("dial failed, retrying", "error", err, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		// Do NOT reset backoff here: grpc.NewClient is lazy (G-1 reasoning).

		consumeStart := time.Now()
		err = consumeStream(ctx, pb.NewMQServiceClient(conn), r.pool, r.cfg, r.ready.Store)
		conn.Close() //nolint:errcheck

		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Reset backoff only after a long-lived stream (>5s) — proves a genuine connection (G-1).
		if time.Since(consumeStart) > 5*time.Second {
			backoff = 100 * time.Millisecond
		}

		slog.Info("stream ended, reconnecting", "error", err, "backoff", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// dialMQ opens a gRPC client connection to the MQ server at addr.
// Uses insecure transport (internal service mesh) and keepalive parameters
// that match the MQ server's enforcement policy (MinTime=15s):
// Client Time=30s, Timeout=10s, PermitWithoutStream=true.
//
// grpc.NewClient is lazy — no TCP connection is made until the first RPC.
func dialMQ(addr string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("collector: dial %s: %w", addr, err)
	}
	return conn, nil
}

// persistBatch upserts msgs into gpu_metrics via pgx.Batch + SendBatch.
//
// Security (T-03-03a): SQL parameters are passed positionally ($1..$11) via
// pgx.Batch.Queue and models.InsertSQL — no string interpolation of proto field
// values, fully preventing SQL injection from malicious payloads.
//
// Idempotency (COLL-05): models.InsertSQL carries ON CONFLICT (gpu_id,
// metric_name, timestamp) DO NOTHING; re-delivered duplicates are silently
// discarded without disrupting the batch.
//
// pgx.Batch drain contract: br.Exec is called exactly b.Len() times (T-03-03d /
// PITFALL 4). Under/over-draining corrupts the pool connection.
//
// Transaction semantics: pgx v5 SendBatch runs the entire batch in ONE implicit
// transaction. An Exec error aborts the transaction and rolls back all earlier
// rows in the same batch — "the rest of the batch always lands" is therefore
// INCORRECT for pgx v5. persistBatch captures the first Exec error and returns
// it wrapped so the caller (flush) can decide not to ack any of the batch.
//
// Bad proto messages (unparseable timestamp etc.) are skipped with a log entry
// and do NOT abort the batch (T-03-03c). Their slot is simply not queued; the
// surrounding batch of valid messages still proceeds normally.
func persistBatch(ctx context.Context, pool *pgxpool.Pool, msgs []*pb.TelemetryMessage) error {
	b := &pgx.Batch{}
	for _, msg := range msgs {
		m, err := models.FromProto(msg)
		if err != nil {
			slog.Warn("skip bad proto", "id", msg.GetId(), "error", err)
			continue
		}
		b.Queue(models.InsertSQL,
			m.GpuID, m.Timestamp, m.MetricName, m.Value,
			m.Device, m.ModelName, m.Hostname, m.Container,
			m.Pod, m.Namespace, m.LabelsRaw,
		)
	}
	if b.Len() == 0 {
		return nil
	}
	br := pool.SendBatch(ctx, b)
	// Drain ALL b.Len() Exec calls unconditionally — PITFALL 4 (pgx v5): under- or
	// over-draining leaves the pool connection in an undefined state that silently
	// corrupts subsequent queries. Capture the first genuine error for return.
	var firstErr error
	for i := 0; i < b.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			// pgx v5 SendBatch runs the whole batch in ONE implicit transaction;
			// a single Exec error aborts the transaction and rolls back all rows
			// already executed in this batch. Log each error for observability but
			// only capture the first to return to the caller (C-1 fix).
			if firstErr == nil {
				firstErr = err
			}
			slog.Error("batch exec failed", "row", i, "error", err)
		}
	}
	// br.Close flushes any remaining round-trips and releases the connection back
	// to the pool. If Close fails and no Exec error was seen, surface the Close error.
	if cerr := br.Close(); cerr != nil && firstErr == nil {
		firstErr = cerr
	}
	if firstErr != nil {
		return fmt.Errorf("collector: persist batch: %w", firstErr)
	}
	return nil
}

// consumeStream is the internal implementation shared by Consume and Runner.Run.
// markReady is an optional callback invoked with true after the MQ Consume stream
// opens and with false (via defer) when it closes. Pass nil from Consume (no
// readiness tracking needed); Runner.Run passes r.ready.Store to expose
// stream connectivity via IsReady() without altering Consume's exported signature.
//
// Two-goroutine bidi split (ADR-001, must-have truth):
//   - recv goroutine: sole caller of stream.Recv(). Forwards messages over
//     msgCh and reports its terminal error over recvDone.
//   - batch goroutine (this function): sole caller of stream.Send().
//     Sends initial credit then acks after each successful persistBatch.
//
// Concurrent stream.Send from two goroutines is undefined behaviour in gRPC-Go
// (documented race; the transport may panic). This split is the only safe shape.
//
// Flush triggers:
//   - size: when len(batch) >= cfg.BatchSize
//   - time: every cfg.FlushMS milliseconds (ticker)
//
// Ack ordering: each ack is sent per-message after the whole batch persists,
// replenishing exactly one credit slot per ack in the broker's sliding window.
func consumeStream(ctx context.Context, client pb.MQServiceClient, pool *pgxpool.Pool, cfg Config, markReady func(bool)) error {
	// Derive a child context so that an early error return (e.g. persistBatch failure)
	// cancels the recv goroutine and prevents it from leaking (Collector MINOR).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.Consume(ctx)
	if err != nil {
		return fmt.Errorf("collector: open stream: %w", err)
	}

	// Stream is now established — signal readiness if a callback was provided.
	// Defer the false signal so IsReady() resets when consumeStream returns
	// (stream ended or error), signalling "not connected" during reconnect backoff.
	if markReady != nil {
		markReady(true)
		defer markReady(false)
	}

	// Buffer sized to cfg.Credit so the recv goroutine can always accept from
	// the server without blocking (the server never sends more than Credit
	// messages ahead of acks, so msgCh can never overflow).
	msgCh := make(chan *pb.TelemetryMessage, cfg.Credit)
	recvDone := make(chan error, 1) // buffered so recv goroutine never blocks

	// Recv goroutine — sole caller of stream.Recv().
	go func() {
		defer close(msgCh) // signals batch goroutine when the stream is done
		for {
			msg, err := stream.Recv()
			if err != nil {
				recvDone <- err
				return
			}
			select {
			case msgCh <- msg:
			case <-ctx.Done():
				recvDone <- ctx.Err()
				return
			}
		}
	}()

	// Send initial credit handshake (batch goroutine is the SOLE caller of Send).
	if err := stream.Send(&pb.ConsumeClientMsg{Credit: int32(cfg.Credit)}); err != nil {
		return fmt.Errorf("collector: send credit handshake: %w", err)
	}

	ticker := time.NewTicker(time.Duration(cfg.FlushMS) * time.Millisecond)
	defer ticker.Stop()

	var batch []*pb.TelemetryMessage

	// flush persists the current batch and acks each message.
	// Only the batch goroutine calls flush — the only safe caller of stream.Send.
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := persistBatch(ctx, pool, batch); err != nil {
			// persistBatch failed — do NOT ack any message. The unacked messages
			// remain in the broker's lease table and are redelivered on disconnect
			// (at-least-once, ADR-001). Returning the error causes Consume to exit
			// and Run's reconnect loop to re-establish the stream (C-1 fix).
			return err
		}
		// Ack every message in the batch, including any that persistBatch skipped
		// due to bad proto (unparseable timestamp etc.). Acking poison messages is
		// DELIBERATE: redelivering them forever would stall the pipeline. They are
		// logged in persistBatch and do not land in the DB (T-03-03c).
		for _, m := range batch {
			if err := stream.Send(&pb.ConsumeClientMsg{AckId: m.GetId()}); err != nil {
				return fmt.Errorf("collector: send ack (id=%d): %w", m.GetId(), err)
			}
		}
		batch = batch[:0]
		return nil
	}

	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				// recv goroutine exited; do a final flush (errors ignored — stream
				// is already terminating; unacked messages will be redelivered by
				// the broker's at-least-once requeue on disconnect).
				_ = flush()
				return <-recvDone
			}
			batch = append(batch, msg)
			if len(batch) >= cfg.BatchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Consume opens a single bidi Consume stream to the MQ and processes messages
// until ctx is cancelled or the stream ends. It is the exported testing seam:
// callers pass a pre-dialled pb.MQServiceClient (e.g., over bufconn in tests or
// a real grpc.ClientConn in production via Run).
//
// Consume delegates to consumeStream with no readiness tracking (markReady=nil).
// Runner.Run uses consumeStream directly to gate IsReady() on stream connectivity.
func Consume(ctx context.Context, client pb.MQServiceClient, pool *pgxpool.Pool, cfg Config) error {
	return consumeStream(ctx, client, pool, cfg, nil)
}

// Run is the outer reconnect loop for the Collector microservice (COLL-02).
// It is a backward-compatible one-liner that delegates to NewRunner(cfg, pool).Run(ctx)
// so existing call sites and tests stay valid without change.
//
// For readiness-aware operation (e.g. cmd/collector), use NewRunner directly.
func Run(ctx context.Context, cfg Config, pool *pgxpool.Pool) error {
	return NewRunner(cfg, pool).Run(ctx)
}
