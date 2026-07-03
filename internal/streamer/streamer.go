package streamer

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/ajitg/vantage/pkg/pb"
)

// Runner holds streamer runtime state including atomic readiness. It wraps the
// dial+Stream orchestration that was previously in the package-level Run function.
// Use NewRunner to construct; call Run to start streaming.
type Runner struct {
	cfg   Config
	ready atomic.Bool
}

// NewRunner creates a new Runner with the given config. The runner is not ready
// until Run has successfully dialed the MQ and entered the streaming loop.
func NewRunner(cfg Config) *Runner {
	return &Runner{cfg: cfg}
}

// IsReady reports whether the runner has dialed the MQ and begun streaming.
// Returns false on a freshly created Runner; transitions to true once Run
// has successfully connected to the MQ and is entering the CSV streaming loop.
// Safe to call from any goroutine (backed by atomic.Bool).
func (r *Runner) IsReady() bool {
	return r.ready.Load()
}

// Run validates the config, dials the MQ, marks readiness, and streams the CSV
// in an infinite loop until ctx is cancelled.
//
// Readiness semantics: IsReady() flips to true once the MQ connection is
// established and Stream is about to enter the loop — signalling that the
// pod is genuinely producing telemetry (OBS-01).
//
// Returns context.Canceled on clean shutdown.
// Returns a non-nil error if CSVPath is empty or if the CSV cannot be opened.
func (r *Runner) Run(ctx context.Context) error {
	if r.cfg.CSVPath == "" {
		return fmt.Errorf("streamer: CSVPath is required (set STREAMER_CSV_PATH)")
	}
	conn, err := dialMQ(r.cfg.MQAddr)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck
	client := pb.NewMQServiceClient(conn)
	// Mark ready: MQ is dialed and we are entering the streaming loop.
	r.ready.Store(true)
	return Stream(ctx, client, r.cfg.CSVPath, r.cfg.LoopDelayMS, false)
}

// dialMQ dials the MQ gRPC server with insecure transport and keepalive
// parameters matching the MQ server's enforcement policy (MinTime=15s):
//
//	Time=30s, Timeout=10s, PermitWithoutStream=true.
//
// grpc.NewClient uses lazy connect — the physical TCP connection is established
// on the first RPC call, not here. This function never blocks on network I/O.
func dialMQ(addr string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second, // matches MQ server Time=30s
			Timeout:             10 * time.Second, // matches MQ server Timeout=10s
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("streamer: dial %s: %w", addr, err)
	}
	return conn, nil
}

// recordToProto maps a 12-column DCGM CSV record to a TelemetryMessage.
//
// The CSV timestamp (col 0) is discarded and replaced by a fresh RFC3339Nano
// restamp. Using RFC3339Nano is a Phase-2 lock-in decision: the DCGM CSV has
// 2470 rows that all share the same second-granularity timestamp; RFC3339 would
// collapse same-GPU/metric readings onto the same natural key and only the first
// row would survive the ON CONFLICT DO NOTHING upsert.
//
// Column indices (from DCGM CSV analysis):
//
//	0 = timestamp  (ignored — discarded by restamp)
//	1 = metric_name
//	2 = gpu_id     (ordinal "0","1","2" — passed through to proto; NOT stored in DB)
//	3 = device
//	4 = uuid       (GPU UUID — maps to db.gpu_id in the Collector)
//	5 = model_name
//	6 = hostname
//	7 = container
//	8 = pod
//	9 = namespace
//	10 = value     (numeric; ParseFloat error → skip this row)
//	11 = labels_raw
func recordToProto(record []string) (*pb.TelemetryMessage, error) {
	value, err := strconv.ParseFloat(record[10], 64)
	if err != nil {
		return nil, fmt.Errorf("streamer: parse value %q: %w", record[10], err)
	}
	return &pb.TelemetryMessage{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano), // MUST be RFC3339Nano (STREAM-02)
		MetricName: record[1],
		GpuId:      record[2], // ordinal — passed through; Collector uses proto.Uuid for db.gpu_id
		Device:     record[3],
		Uuid:       record[4], // GPU UUID — maps to db.gpu_id in Collector
		ModelName:  record[5],
		Hostname:   record[6],
		Container:  record[7],
		Pod:        record[8],
		Namespace:  record[9],
		Value:      value,
		LabelsRaw:  record[11],
	}, nil
}

// Stream reads the DCGM CSV at csvPath and publishes each record to the MQ via
// the provided client. It is the exported test seam for the Streamer service.
//
// Behaviour:
//   - FieldsPerRecord=12 enforces the strict 12-column DCGM format; rows with a
//     wrong column count are skipped and logged, never panic (STREAM-04, T-03-02a).
//   - Each record is restamped at RFC3339Nano precision before publishing (STREAM-02).
//   - If once==true, makes exactly one full pass through the CSV then returns nil.
//     This enables fast unit and bufconn tests without the production infinite loop.
//   - If once==false, loops indefinitely — seeking back to the start after each EOF —
//     until ctx is cancelled (STREAM-01).
//   - loopDelayMS controls the inter-row sleep; 0 disables it (STREAM-05 / T-03-02b).
//
// Stream is stateless: each invocation opens its own file descriptor and maintains
// no shared mutable state. Up to 10 concurrent calls over the same path are safe
// under -race (STREAM-05).
// produceWithRetry publishes msg to the MQ with exponential backoff on transient
// Produce failures (M-6 fix). Backoff: base 100ms, cap 5s, ctx-aware sleep.
// Returns nil when the message is accepted, or ctx.Err() if the context is
// cancelled before a successful Produce. One MQ blip must NOT kill the instance.
func produceWithRetry(ctx context.Context, client pb.MQServiceClient, msg *pb.TelemetryMessage) error {
	backoff := 100 * time.Millisecond
	const maxBackoff = 5 * time.Second
	for {
		_, err := client.Produce(ctx, &pb.ProduceRequest{Message: msg})
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("produce failed, retrying", "backoff", backoff, "error", err)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func Stream(ctx context.Context, client pb.MQServiceClient, csvPath string, loopDelayMS int, once bool) error {
	f, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("streamer: open csv: %w", err)
	}
	defer f.Close() //nolint:errcheck

	for {
		// Check cancellation at the top of each pass before seeking.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("streamer: seek: %w", err)
		}
		r := csv.NewReader(f)
		r.FieldsPerRecord = 12 // strict 12-column enforcement (STREAM-04)
		// Discard the header row (it is the first line of every DCGM CSV).
		if _, err := r.Read(); err != nil {
			return fmt.Errorf("streamer: read header: %w", err)
		}
		for {
			record, err := r.Read()
			if err == io.EOF {
				break // end of this pass — seek back to top on next outer iteration
			}
			if err != nil {
				// Malformed row (wrong column count or parse error) — skip and log.
				// csv.ParseError with Err=csv.ErrFieldCount is the common case here.
				slog.Warn("skip malformed row", "error", err)
				continue
			}
			msg, err := recordToProto(record)
			if err != nil {
				slog.Warn("skip bad record", "error", err)
				continue
			}
			// produceWithRetry retries on transient MQ failures so a single blip
			// does not kill the instance (M-6). Returns only on success or ctx cancel.
			if err := produceWithRetry(ctx, client, msg); err != nil {
				return err // only ctx.Err() reaches here
			}
			if loopDelayMS > 0 {
				// ctx-aware sleep: cancel propagates immediately (Streamer MINOR).
				select {
				case <-time.After(time.Duration(loopDelayMS) * time.Millisecond):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if once {
			return nil
		}
	}
}

// Run is a backward-compatible wrapper that constructs a Runner and calls Run.
// Existing call sites (cmd/streamer, tests) continue to work without changes.
func Run(ctx context.Context, cfg Config) error {
	return NewRunner(cfg).Run(ctx)
}
