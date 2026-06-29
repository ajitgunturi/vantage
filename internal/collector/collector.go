package collector

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/ajitg/vantage/pkg/models"
	"github.com/ajitg/vantage/pkg/pb"
)

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
// pgx.Batch drain contract: br.Close is deferred immediately after SendBatch,
// and br.Exec is called exactly b.Len() times (T-03-03d / PITFALL 4).
// Under/over-draining corrupts the pool connection; partial row errors are
// logged-and-continued (not returned) so the rest of the batch always lands.
//
// Bad proto messages (unparseable timestamp etc.) are skipped with a log entry
// and do NOT abort the batch (T-03-03c).
func persistBatch(ctx context.Context, pool *pgxpool.Pool, msgs []*pb.TelemetryMessage) error {
	b := &pgx.Batch{}
	for _, msg := range msgs {
		m, err := models.FromProto(msg)
		if err != nil {
			log.Printf("collector: skip bad proto (id=%d): %v", msg.GetId(), err)
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
	defer br.Close() // MUST be deferred before the Exec loop (PITFALL 4)
	for i := 0; i < b.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			// ON CONFLICT DO NOTHING means this only fires on genuine errors.
			// Log-and-continue so the rest of the batch is not aborted.
			log.Printf("collector: batch row %d exec: %v", i, err)
		}
	}
	return nil
}

// Consume opens a single bidi Consume stream to the MQ and processes messages
// until ctx is cancelled or the stream ends. It is the exported testing seam:
// callers pass a pre-dialled pb.MQServiceClient (e.g., over bufconn in tests or
// a real grpc.ClientConn in production via Run).
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
func Consume(ctx context.Context, client pb.MQServiceClient, pool *pgxpool.Pool, cfg Config) error {
	stream, err := client.Consume(ctx)
	if err != nil {
		return fmt.Errorf("collector: open stream: %w", err)
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
			return err
		}
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

// Run is the outer reconnect loop for the Collector microservice (COLL-02).
// It dials the MQ, calls Consume for one stream attempt, and retries on failure
// with exponential backoff — base 100ms, cap 5s (T-03-03b / DoS mitigation).
//
// The loop exits only when ctx is cancelled (graceful shutdown) or when ctx.Err()
// is non-nil on entry (already cancelled before first attempt).
//
// Error classification: context.Canceled / ctx.Err() non-nil → clean exit;
// any other Consume error → log "stream ended — reconnecting" and retry.
// DSN and sensitive data are never included in logged or returned errors (T-03-03c).
func Run(ctx context.Context, cfg Config, pool *pgxpool.Pool) error {
	backoff := 100 * time.Millisecond
	const maxBackoff = 5 * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		conn, err := dialMQ(cfg.MQAddr)
		if err != nil {
			log.Printf("collector: dial: %v — retrying in %v", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = 100 * time.Millisecond // reset on successful dial

		err = Consume(ctx, pb.NewMQServiceClient(conn), pool, cfg)
		conn.Close() //nolint:errcheck

		// Only exit if OUR context was canceled (graceful shutdown signal).
		// A server-side GracefulStop translates to codes.Canceled on the client,
		// which gRPC-go may surface as stdlib context.Canceled — but OUR ctx is
		// still valid in that case, so we must NOT exit: we should reconnect.
		// Checking ctx.Err() exclusively is the only reliable discriminant.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("collector: stream ended (%v) — reconnecting in %v", err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
