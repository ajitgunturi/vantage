package streamer

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/ajitg/vantage/pkg/pb"
)

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
func Stream(ctx context.Context, client pb.MQServiceClient, csvPath string, loopDelayMS int, once bool) error {
	f, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("streamer: open csv: %w", err)
	}
	defer f.Close()

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
				log.Printf("streamer: skip malformed row: %v", err)
				continue
			}
			msg, err := recordToProto(record)
			if err != nil {
				log.Printf("streamer: skip bad record: %v", err)
				continue
			}
			if _, err := client.Produce(ctx, &pb.ProduceRequest{Message: msg}); err != nil {
				return fmt.Errorf("streamer: produce: %w", err)
			}
			if loopDelayMS > 0 {
				time.Sleep(time.Duration(loopDelayMS) * time.Millisecond)
			}
		}
		if once {
			return nil
		}
	}
}

// Run validates the config, dials the MQ, and streams the CSV in an infinite
// loop until ctx is cancelled. It wraps Stream with config validation and
// connection lifecycle management.
//
// Returns context.Canceled on clean shutdown.
// Returns a non-nil error if CSVPath is empty or if the CSV cannot be opened.
func Run(ctx context.Context, cfg Config) error {
	if cfg.CSVPath == "" {
		return fmt.Errorf("streamer: CSVPath is required (set STREAMER_CSV_PATH)")
	}
	conn, err := dialMQ(cfg.MQAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewMQServiceClient(conn)
	return Stream(ctx, client, cfg.CSVPath, cfg.LoopDelayMS, false)
}
