# Phase 3: Pipeline — Streamer + Collector + Integration - Research

**Researched:** 2026-06-29
**Domain:** Go gRPC bidi streaming, CSV parsing, pgx v5 Batch/SendBatch, testcontainers integration testing
**Confidence:** MEDIUM

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| STREAM-01 | Continuously loops the DCGM CSV file line-by-line, indefinitely | Infinite loop via file.Seek + re-create Reader; encoding/csv.ReadAll |
| STREAM-02 | Restamps each record with the current execution timestamp before publishing | time.Now().UTC().Format(time.RFC3339Nano) — RFC3339Nano mandatory per Phase 2 lock-in |
| STREAM-03 | Publishes records to the MQ via gRPC Produce client stub | pb.NewMQServiceClient(conn).Produce(ctx, req) unary call with grpc.NewClient |
| STREAM-04 | Parses the DCGM 12-column format; malformed lines skipped and logged | encoding/csv.NewReader, FieldsPerRecord=12 for strict validation, strconv.ParseFloat for value |
| STREAM-05 | Supports running up to 10 concurrent instances | Stateless Streamer — each instance dials its own gRPC connection; no shared state |
| COLL-01 | Establishes a long-lived gRPC Consume stream to the MQ | pb.NewMQServiceClient(conn).Consume(ctx) bidi stream; send initial credit handshake |
| COLL-02 | Reconnects automatically if the stream drops | Recv returns io.EOF or status error → cancel context, redial with exponential backoff |
| COLL-03 | Persists received telemetry into PostgreSQL via pgxpool batched writes | pgx.Batch + pool.SendBatch(ctx, batch); ticker + size trigger flush |
| COLL-04 | Maps wire payload to the DB model and persists reactively as data arrives | pkg/models.FromProto: proto.uuid → db.gpu_id; time.Parse RFC3339Nano |
| COLL-05 | Idempotent upsert (ON CONFLICT) so redelivered messages do not duplicate rows | INSERT ... ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING against uq_gpu_metrics_natural_key |
| QA-03 | Integration tests: end-to-end CSV→MQ→Collector→Postgres; gateway against seeded DB | bufconn in-process MQ + testcontainers Postgres; build on Phase 2 patterns |
</phase_requirements>

## Summary

Phase 3 builds two microservices — Streamer and Collector — and wires the full data pipeline from CSV to PostgreSQL. The Streamer reads the DCGM CSV in an infinite loop, restamps each row at RFC3339Nano precision, and publishes to the MQ via unary gRPC `Produce`. The Collector opens a long-lived bidi gRPC `Consume` stream to the MQ, accumulates received messages in a batch, and flushes them to PostgreSQL using `pgx.Batch` + `pool.SendBatch` with `ON CONFLICT DO NOTHING`.

No new Go module dependencies are required for Phase 3. All packages are already in `go.mod`: `google.golang.org/grpc v1.81.1` (includes `bufconn` sub-package for tests), `github.com/jackc/pgx/v5 v5.10.0` (Batch + SendBatch), `github.com/testcontainers/testcontainers-go v0.43.0` (integration tests), and `golang.org/x/sync v0.21.0` (errgroup for lifecycle management). A new `pkg/models` package is needed to house the `GpuMetric` struct and `FromProto` converter — this is shared with Phase 4's Gateway.

The most consequential cross-cutting concern is the RFC3339Nano restamp lock-in from Phase 2: all 2470 rows in the DCGM CSV share the same second-granularity timestamp `2025-07-18T20:42:34Z`. Any Streamer that uses `time.RFC3339` (second granularity) collapses every same-GPU/same-metric reading onto the same natural key — they all become "duplicates" and only the first row persists. The RFC3339Nano restamp is not optional.

**Primary recommendation:** Build Streamer and Collector as two separate `cmd/` services following the `cmd/mq` errgroup pattern. Use `pkg/models` as the shared conversion layer. Test CSV parsing and proto mapping as fast unit tests; test the full pipeline end-to-end with bufconn + testcontainers.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| CSV parsing + restamp | Streamer (cmd/streamer) | — | Data origin; Streamer is sole owner of the CSV file and the publish loop |
| Message publish | Streamer (gRPC client) | MQ (gRPC server, already built) | Unary Produce RPC; Streamer drives it |
| Message deliver + ack | MQ (already built) | Collector (gRPC client) | Bidi Consume stream; MQ is server, Collector is client |
| Wire-to-model mapping | pkg/models | — | Shared package; keeps mapping logic out of cmd/ binaries |
| Batch persistence | Collector (cmd/collector) | PostgreSQL | Collector owns the write path; PostgreSQL enforces uniqueness |
| Idempotent upsert | PostgreSQL (unique constraint) | Collector (ON CONFLICT SQL) | DB constraint is the final guard; Collector SQL expresses the conflict rule |
| Integration test orchestration | Test harness (bufconn + testcontainers) | — | Tests orchestrate all three tiers in-process |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| encoding/csv | stdlib | Parse DCGM 12-column CSV; handles embedded double-quotes | No dependency; correct RFC 4180 by default |
| strconv | stdlib | ParseFloat for CSV value column | No dependency |
| time | stdlib | RFC3339Nano restamp via time.Now().UTC().Format(time.RFC3339Nano) | No dependency |
| google.golang.org/grpc | v1.81.1 | gRPC Produce client (Streamer) + Consume bidi client (Collector) + grpc.NewServer (tests) | Already in go.mod [VERIFIED: go.mod] |
| github.com/jackc/pgx/v5 | v5.10.0 | pgx.Batch + pgxpool.Pool.SendBatch for idempotent batch upsert | Already in go.mod; CopyFrom cannot express ON CONFLICT (Phase 2 lock-in) [VERIFIED: go.mod] |
| golang.org/x/sync/errgroup | v0.21.0 | goroutine lifecycle management in Collector (recv + batch goroutines) | Already in go.mod; established pattern from cmd/mq [VERIFIED: go.mod] |
| google.golang.org/grpc/test/bufconn | (sub-pkg of grpc v1.81.1) | In-process gRPC transport for integration tests | Zero new dependency; sub-package of existing grpc [VERIFIED: go doc] |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/testcontainers/testcontainers-go/modules/postgres | v0.43.0 | Spin up postgres:17-alpine for integration tests | Collector + QA-03 end-to-end tests only |
| github.com/stretchr/testify | v1.11.1 | require/assert in all tests | Every test package |
| google.golang.org/grpc/credentials/insecure | (sub-pkg of grpc) | Insecure transport for in-process test connections | Test setup only — in-process, no TLS needed |
| google.golang.org/grpc/keepalive | (sub-pkg of grpc) | Client keepalive params matching MQ server enforcement policy | Collector gRPC dial options |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| pgx.Batch + SendBatch | pgxpool.CopyFrom | CopyFrom is faster but CANNOT express ON CONFLICT — Phase 2 explicitly locked to Batch for idempotent upserts |
| encoding/csv | bufio.Scanner + strings.Split | Scanner/Split does NOT handle RFC 4180 quoted fields with embedded double-quotes — labels_raw field would break |
| file.Seek(0, io.SeekStart) | re-open file | Seek resets the reader cheaply without closing/reopening the file descriptor |
| bufconn | real TCP listener (net.Listen :0) | bufconn is in-process, no OS port allocation, deterministic teardown; preferred for unit-level integration tests |

**Installation:**
No new packages needed. All are sub-packages of existing go.mod entries.

## Package Legitimacy Audit

Phase 3 adds ZERO new packages to go.mod. All packages used are already present and were verified during Phase 1/2 research.

| Package | Registry | Age | Source Repo | Verdict | Disposition |
|---------|----------|-----|-------------|---------|-------------|
| google.golang.org/grpc | Go modules | 8+ yrs | github.com/grpc/grpc-go | OK | Approved — v1.81.1 in go.mod [VERIFIED: go.mod] |
| github.com/jackc/pgx/v5 | Go modules | 5+ yrs | github.com/jackc/pgx | OK | Approved — v5.10.0 in go.mod [VERIFIED: go.mod] |
| github.com/testcontainers/testcontainers-go | Go modules | 5+ yrs | github.com/testcontainers/testcontainers-go | OK | Approved — v0.43.0 in go.mod [VERIFIED: go.mod] |
| encoding/csv, strconv, time | Go stdlib | N/A | golang.org/go | OK | Approved — standard library |

**Packages removed due to SLOP verdict:** none
**Packages flagged as suspicious SUS:** none

## Architecture Patterns

### System Architecture Diagram

```
CSV file (dcgm_metrics_*.csv)
  │  encoding/csv.Reader (12 cols, RFC 4180)
  │  RFC3339Nano restamp + strconv.ParseFloat
  ▼
cmd/streamer ──── gRPC Produce (unary) ────► cmd/mq (already built)
                                               │  ring buffer, credit/ack
                                               │  bidi Consume stream
                                               ▼
                                           cmd/collector
                                           ├─ recv goroutine (stream.Recv → msgCh)
                                           └─ batch goroutine (ticker + size → flush)
                                                    │  pkg/models.FromProto
                                                    │  pgx.Batch + SendBatch
                                                    │  INSERT ON CONFLICT DO NOTHING
                                                    ▼
                                               PostgreSQL (gpu_metrics table)
```

### Recommended Project Structure
```
cmd/
├── streamer/
│   ├── main.go          # errgroup wiring, signal handling
│   ├── config.go        # Config struct + FromEnv
│   └── streamer.go      # Run(ctx, cfg): CSV loop + gRPC Produce
cmd/collector/
│   ├── main.go          # errgroup wiring, signal handling, db.New + db.Migrate
│   ├── config.go        # Config struct + FromEnv
│   └── collector.go     # Run(ctx, cfg, pool): connect + consume loop
pkg/
└── models/
    ├── telemetry.go     # GpuMetric struct + FromProto(msg) + InsertSQL constant
    └── telemetry_test.go
```

### Pattern 1: CSV Infinite Loop with Restamp

**What:** Open the CSV once; loop forever by seeking back to start. Skip the header on each iteration. Skip and log malformed rows.
**When to use:** Streamer.Run inner loop.

```go
// Source: pkg.go.dev/encoding/csv + Go stdlib
func streamCSV(ctx context.Context, path string, send func(*pb.TelemetryMessage) error) error {
    f, err := os.Open(path)
    if err != nil {
        return fmt.Errorf("streamer: open csv: %w", err)
    }
    defer f.Close()

    for {
        if ctx.Err() != nil {
            return ctx.Err()
        }
        if _, err := f.Seek(0, io.SeekStart); err != nil {
            return fmt.Errorf("streamer: seek: %w", err)
        }
        r := csv.NewReader(f)
        r.FieldsPerRecord = 12   // reject rows with wrong column count
        // Read and discard header row
        if _, err := r.Read(); err != nil {
            return fmt.Errorf("streamer: read header: %w", err)
        }
        for {
            record, err := r.Read()
            if err == io.EOF {
                break // end of file — seek back to top
            }
            if err != nil {
                log.Printf("streamer: skip malformed row: %v", err)
                continue
            }
            msg, err := recordToProto(record)
            if err != nil {
                log.Printf("streamer: skip bad record: %v", err)
                continue
            }
            if err := send(msg); err != nil {
                return fmt.Errorf("streamer: send: %w", err)
            }
        }
    }
}
```

### Pattern 2: CSV Record to Proto Mapping

**What:** Map 12 CSV columns to TelemetryMessage. Restamp at RFC3339Nano. Map column 4 (uuid) to proto.uuid — NOT column 2 (gpu_id ordinal).
**When to use:** Inside streamCSV per-record callback.

```go
// Source: codebase mq.proto + dcgm_metrics CSV analysis
// Column indices:
// 0=timestamp(ignored) 1=metric_name 2=gpu_id(ordinal,NOT stored) 3=device
// 4=uuid(GPU UUID→db.gpu_id) 5=modelName 6=Hostname 7=container 8=pod
// 9=namespace 10=value 11=labels_raw
func recordToProto(record []string) (*pb.TelemetryMessage, error) {
    value, err := strconv.ParseFloat(record[10], 64)
    if err != nil {
        return nil, fmt.Errorf("parse value %q: %w", record[10], err)
    }
    return &pb.TelemetryMessage{
        Timestamp:  time.Now().UTC().Format(time.RFC3339Nano), // MUST be RFC3339Nano
        MetricName: record[1],
        GpuId:     record[2], // ordinal "0","1","2" — passed through, NOT stored in DB
        Device:     record[3],
        Uuid:      record[4], // GPU UUID — maps to db.gpu_id in Collector
        ModelName: record[5],
        Hostname:  record[6],
        Container: record[7],
        Pod:       record[8],
        Namespace: record[9],
        Value:     value,
        LabelsRaw: record[11],
    }, nil
}
```

### Pattern 3: pkg/models GpuMetric + FromProto

**What:** Shared model struct decoupling the Collector and API Gateway from pb types. Critical: `proto.uuid` → `model.GpuID` (NOT `proto.gpu_id`).
**When to use:** Collector persistence path; Gateway read queries.

```go
// Source: codebase analysis — pkg/db/migrations/000001_init_schema.up.sql + api/proto/mq.proto
package models

import (
    "fmt"
    "time"
    "github.com/ajitg/vantage/pkg/pb"
)

type GpuMetric struct {
    GpuID      string    // GPU UUID (proto.Uuid → gpu_metrics.gpu_id)
    Timestamp  time.Time
    MetricName string
    Value      float64
    Device     string
    ModelName  string
    Hostname   string
    Container  string
    Pod        string
    Namespace  string
    LabelsRaw  string
}

// FromProto converts a TelemetryMessage to GpuMetric.
// proto.Uuid → GpuID (the GPU UUID, not the ordinal proto.GpuId).
// proto.Timestamp must be RFC3339Nano; returns error if unparseable.
func FromProto(msg *pb.TelemetryMessage) (GpuMetric, error) {
    ts, err := time.Parse(time.RFC3339Nano, msg.GetTimestamp())
    if err != nil {
        // Fallback: try RFC3339 for timestamps without sub-second precision
        ts, err = time.Parse(time.RFC3339, msg.GetTimestamp())
        if err != nil {
            return GpuMetric{}, fmt.Errorf("models: parse timestamp %q: %w", msg.GetTimestamp(), err)
        }
    }
    return GpuMetric{
        GpuID:      msg.GetUuid(),        // UUID, not ordinal
        Timestamp:  ts.UTC(),
        MetricName: msg.GetMetricName(),
        Value:      msg.GetValue(),
        Device:     msg.GetDevice(),
        ModelName:  msg.GetModelName(),
        Hostname:   msg.GetHostname(),
        Container:  msg.GetContainer(),
        Pod:        msg.GetPod(),
        Namespace:  msg.GetNamespace(),
        LabelsRaw:  msg.GetLabelsRaw(),
    }, nil
}

// InsertSQL is the idempotent upsert for one gpu_metrics row.
// ON CONFLICT targets uq_gpu_metrics_natural_key (gpu_id, metric_name, timestamp).
const InsertSQL = `
INSERT INTO gpu_metrics
    (gpu_id, timestamp, metric_name, value, device, model_name, hostname, container, pod, namespace, labels_raw)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING`
```

### Pattern 4: Collector gRPC Bidi Stream + Batch Flush

**What:** Two-goroutine Collector: recv goroutine feeds msgCh; batch goroutine accumulates + flushes on size or ticker.
**When to use:** collector.Run inner consume loop.

```go
// Source: codebase internal/server/server.go bidi protocol + pkg.go.dev/google.golang.org/grpc
const (
    DefaultCredit    = 50    // initial credit window; must be >= batchSize
    DefaultBatchSize = 50    // max rows per pgx.Batch flush
    DefaultFlushMs   = 500   // flush interval (ms)
)

func consumeLoop(ctx context.Context, stream pb.MQService_ConsumeClient, pool *pgxpool.Pool) error {
    msgCh := make(chan *pb.TelemetryMessage, DefaultCredit)

    // Recv goroutine — sole caller of stream.Recv()
    recvDone := make(chan error, 1)
    go func() {
        defer close(msgCh)
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

    // Batch goroutine (this goroutine) — sole caller of stream.Send()
    // Send initial credit handshake
    if err := stream.Send(&pb.ConsumeClientMsg{
        Credit:     DefaultCredit,
        ConsumerId: consumerID(),
    }); err != nil {
        return fmt.Errorf("collector: send credit: %w", err)
    }

    ticker := time.NewTicker(DefaultFlushMs * time.Millisecond)
    defer ticker.Stop()

    var batch []*pb.TelemetryMessage
    flush := func() error {
        if len(batch) == 0 {
            return nil
        }
        if err := persistBatch(ctx, pool, batch); err != nil {
            return err
        }
        // Ack all messages in this batch
        for _, m := range batch {
            if err := stream.Send(&pb.ConsumeClientMsg{AckId: m.GetId()}); err != nil {
                return fmt.Errorf("collector: send ack: %w", err)
            }
        }
        batch = batch[:0]
        return nil
    }

    for {
        select {
        case msg, ok := <-msgCh:
            if !ok {
                _ = flush()
                return <-recvDone
            }
            batch = append(batch, msg)
            if len(batch) >= DefaultBatchSize {
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
```

### Pattern 5: pgx.Batch Idempotent Upsert

**What:** Build a batch of INSERT ON CONFLICT DO NOTHING queries and send as one round-trip.
**When to use:** persistBatch called by consumeLoop flush.

```go
// Source: pkg.go.dev/github.com/jackc/pgx/v5#Batch + codebase pkg/db migrations
func persistBatch(ctx context.Context, pool *pgxpool.Pool, msgs []*pb.TelemetryMessage) error {
    b := &pgx.Batch{}
    for _, msg := range msgs {
        m, err := models.FromProto(msg)
        if err != nil {
            log.Printf("collector: skip bad proto: %v", err)
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
    defer br.Close()
    for i := 0; i < b.Len(); i++ {
        if _, err := br.Exec(); err != nil {
            log.Printf("collector: batch row %d: %v", i, err)
            // Log and continue — partial failure does not abort other rows
        }
    }
    return nil
}
```

### Pattern 6: Collector gRPC Dial with Keepalive

**What:** grpc.NewClient (not deprecated Dial) with keepalive params matching MQ server enforcement policy (MinTime=15s → client Time should be >= 15s, not trigger too fast).
**When to use:** Collector startup, and on every reconnect.

```go
// Source: codebase cmd/mq/main.go keepalive params + pkg.go.dev/google.golang.org/grpc
func dialMQ(ctx context.Context, addr string) (*grpc.ClientConn, error) {
    return grpc.NewClient(addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                30 * time.Second, // matches server Time=30s
            Timeout:             10 * time.Second, // matches server Timeout=10s
            PermitWithoutStream: true,
        }),
    )
}
```

### Pattern 7: bufconn In-Process gRPC for Integration Tests

**What:** Use bufconn to wire real gRPC server (real MQ) to real client (real Collector) entirely in-process, no OS port.
**When to use:** QA-03 integration tests.

```go
// Source: pkg.go.dev/google.golang.org/grpc/test/bufconn (sub-pkg of existing grpc dep)
const bufSize = 1 << 20 // 1 MB

func newBufconnServer(t *testing.T, mqSrv *server.MQServer) *grpc.ClientConn {
    t.Helper()
    lis := bufconn.Listen(bufSize)
    s := grpc.NewServer()
    pb.RegisterMQServiceServer(s, mqSrv)
    t.Cleanup(func() { s.Stop(); lis.Close() })
    go s.Serve(lis) //nolint:errcheck

    conn, err := grpc.NewClient("passthrough:///bufnet",
        grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
            return lis.DialContext(ctx)
        }),
        grpc.WithTransportCredentials(insecure.NewCredentials()),
    )
    require.NoError(t, err)
    t.Cleanup(func() { conn.Close() })
    return conn
}
```

### Anti-Patterns to Avoid
- **Using CopyFrom for Collector inserts:** CopyFrom cannot express ON CONFLICT — will insert duplicates on redelivery. Phase 2 explicitly locks this to pgx.Batch.
- **Using time.RFC3339 for restamp:** Second-granularity collapses all readings from same GPU/metric within one second onto the same natural key — only first row persists. Must use time.RFC3339Nano.
- **Sending acks from the recv goroutine:** Only one goroutine may call stream.Send at a time. Acks must go through the batch goroutine's send path.
- **Setting credit < batch size:** If credit=20 and batchSize=50, the batch fills to 20 messages (credit window exhausted), no more messages arrive, batch timer triggers, acks flush credit, but the batch is only half-full. Set credit >= batchSize.
- **Calling br.Close() without draining all Exec():** pgx requires Exec() to be called exactly once per queued query before Close() — skipping calls leaks the pool connection.
- **Using grpc.Dial instead of grpc.NewClient:** grpc.Dial is deprecated since v1.63.0; use grpc.NewClient. The difference: NewClient does NOT eagerly establish the connection (lazy connect on first RPC).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| RFC 4180 CSV with embedded double-quotes | String splitting, manual quote stripping | encoding/csv.NewReader | Labels_raw field uses "" escaping that manual splitting cannot handle correctly |
| Proto field → DB model conversion | Ad hoc field access in each handler | pkg/models.FromProto | Centralized mapping prevents the uuid/gpu_id confusion from spreading across files |
| Batch database writes | Per-row Exec in a loop | pgx.Batch + pool.SendBatch | N per-row Execs = N round-trips; one SendBatch = 1 round-trip for N rows |
| In-process gRPC test transport | net.Listen("tcp",":0") + dynamic port | google.golang.org/grpc/test/bufconn | bufconn is deterministic, no OS port, no cross-test interference, already in go.mod |
| goroutine lifecycle + error propagation | sync.WaitGroup + error channel | golang.org/x/sync/errgroup | Already established pattern in cmd/mq; errgroup cancels on first error |
| Reconnect backoff | time.Sleep with hardcoded duration | exponential backoff via loop + time.After | Base: 100ms, max: 5s; cap prevents thundering herd on MQ restart |

**Key insight:** The bidi streaming protocol and pgx.Batch semantics each have subtle error modes (goroutine safety, partial failure, credit stall) that hand-rolled solutions consistently get wrong. Stick to the established patterns.

## Runtime State Inventory

Not applicable — Phase 3 creates new services (cmd/streamer, cmd/collector, pkg/models). No rename, refactor, or migration involved. No runtime state from earlier phases is affected by Phase 3 changes.

## Common Pitfalls

### Pitfall 1: RFC3339Nano vs RFC3339 restamp
**What goes wrong:** Streamer uses `time.Now().UTC().Format(time.RFC3339)` — second-granularity. All CSV rows read in the same second get the same timestamp. The Collector tries to insert them; the second one hits `uq_gpu_metrics_natural_key` (gpu_id, metric_name, timestamp) and is silently dropped (ON CONFLICT DO NOTHING). Only 1 row persists per GPU/metric pair per loop iteration instead of the expected N rows.
**Why it happens:** time.RFC3339 = `"2006-01-02T15:04:05Z07:00"` — no fractional seconds. time.RFC3339Nano = `"2006-01-02T15:04:05.999999999Z07:00"` — nanosecond precision.
**How to avoid:** Always use `time.Now().UTC().Format(time.RFC3339Nano)` in the Streamer.
**Warning signs:** Row count in Postgres equals the number of unique (gpu_id, metric_name) pairs, not the number of CSV rows processed. Specifically: Phase 3 CSV has 3 GPUs × 10 metrics × some rows; if you see exactly 30 rows after a full loop pass, RFC3339 was used.

### Pitfall 2: Credit window stall
**What goes wrong:** Collector sends initial credit C=20, batchSize=50. After 20 messages arrive, the credit window is exhausted. No new messages arrive from MQ (it's waiting for acks). Batch ticker fires but batch has only 20 rows — it flushes and sends 20 acks. Credit replenishes. Next 20 messages arrive. This creates 20-row micro-batches instead of 50-row batches. Performance suffers; also risks violating the intended batch semantics.
**Why it happens:** MQ enforces "outstanding-unacked <= credit" strictly (ADR-001 MQ-10).
**How to avoid:** Set credit >= batchSize. A credit of 2× batchSize gives headroom for pipelining.
**Warning signs:** Batches always arrive exactly at the credit limit, never at batchSize.

### Pitfall 3: proto.uuid vs proto.gpu_id in Collector
**What goes wrong:** Collector stores `proto.GpuId` (ordinal "0","1","2") into `gpu_metrics.gpu_id` instead of `proto.Uuid` (the GPU UUID "GPU-5fd4f087-..."). The DB constraint fires differently; API queries for GPU IDs return ordinals not UUIDs; Phase 4 Gateway's `/api/v1/gpus/{id}` breaks entirely.
**Why it happens:** The proto field named `gpu_id` maps to the CSV ordinal, but the DB column named `gpu_id` holds the UUID. Naming clash between proto and DB.
**How to avoid:** `pkg/models.FromProto` centralizes this mapping: `GpuID: msg.GetUuid()`. Unit test FromProto to assert the mapping explicitly.
**Warning signs:** `SELECT DISTINCT gpu_id FROM gpu_metrics` returns "0","1","2" instead of "GPU-xxx-..." UUIDs.

### Pitfall 4: Multiple goroutines calling stream.Send
**What goes wrong:** A goroutine sends acks while another goroutine sends the initial credit handshake (or while reconnecting). gRPC panics or corrupts the stream: "concurrent SendMsg calls are not allowed".
**Why it happens:** gRPC's bidi stream is goroutine-safe for concurrent Send+Recv, but NOT for concurrent Send+Send.
**How to avoid:** One goroutine owns all stream.Send calls (the batch goroutine). The recv goroutine only calls stream.Recv.
**Warning signs:** Panic: "concurrent access" or "unexpected status code" in gRPC transport logs.

### Pitfall 5: br.Close() not called or called before all Exec()
**What goes wrong:** pgx leaks the pool connection if BatchResults.Close() is not called. Conversely, if Close() is called before all Exec() calls, the connection is returned to the pool with pending server responses — the pool connection is corrupted.
**Why it happens:** BatchResults wraps a single pool connection; it must be fully drained (one Exec per queued query) before close signals "done".
**How to avoid:** `defer br.Close()` at creation; iterate exactly `b.Len()` times calling `br.Exec()`.
**Warning signs:** Pool connections steadily exhausted under load; "conn busy" errors on the next batch.

### Pitfall 6: CSV FieldsPerRecord mismatch on header row
**What goes wrong:** Setting `r.FieldsPerRecord = 12` causes the header row read to fail if the header has 12 columns but the data rows also have 12 columns. Actually this is fine. But watch out: FieldsPerRecord = 0 (default) infers from the first row (the header). Then data rows that have a different column count are silently rejected with ErrFieldCount — not skipped. You must handle the ErrFieldCount case explicitly.
**How to avoid:** Set `FieldsPerRecord = 12` explicitly after reading the header. Or set it to -1 for variable and validate len(record) == 12 manually. Either way, wrap the Err in a `var parseErr *csv.ParseError; errors.As(err, &parseErr)` check to log row/column position.
**Warning signs:** Entire CSV sections silently skipped; metrics missing from Postgres.

## Code Examples

Verified patterns from official sources:

### encoding/csv with FieldsPerRecord
```go
// Source: pkg.go.dev/encoding/csv [CITED: pkg.go.dev/encoding/csv]
r := csv.NewReader(f)
r.FieldsPerRecord = 12   // enforce exact column count; returns csv.ErrFieldCount otherwise
record, err := r.Read()  // returns nil, io.EOF at end of file
// ReadAll returns nil error on success (NOT io.EOF)
all, err := r.ReadAll()  // err == nil means success
```

### pgx.Batch + pool.SendBatch
```go
// Source: pkg.go.dev/github.com/jackc/pgx/v5 [CITED: pkg.go.dev/github.com/jackc/pgx/v5#Batch]
b := &pgx.Batch{}
b.Queue("INSERT INTO t (a) VALUES ($1) ON CONFLICT (a) DO NOTHING", val)
br := pool.SendBatch(ctx, b)
defer br.Close()         // MUST close; acquires one pool conn for the lifetime of br
_, err := br.Exec()      // call once per Queue; ON CONFLICT DO NOTHING → err=nil, 0 rows
```

### grpc.NewClient with keepalive
```go
// Source: codebase cmd/mq/main.go [VERIFIED: codebase]
conn, err := grpc.NewClient(addr,
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithKeepaliveParams(keepalive.ClientParameters{
        Time:                30 * time.Second,
        Timeout:             10 * time.Second,
        PermitWithoutStream: true,
    }),
)
```

### testcontainers Snapshot/Restore (Phase 2 pattern)
```go
// Source: codebase pkg/db/db_test.go [VERIFIED: codebase]
ctr, _ := postgres.Run(ctx, "postgres:17-alpine",
    postgres.WithDatabase("vantage_test"),
    postgres.WithUsername("postgres"),   // MUST be "postgres" for Snapshot/Restore
    postgres.WithPassword("secret"),
    postgres.BasicWaitStrategies(),
    postgres.WithSQLDriver("pgx"),
)
ctr.Snapshot(ctx)
// Per test:
ctr.Restore(ctx)
_ = pool.Ping(ctx)  // evict terminated connections
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| grpc.Dial | grpc.NewClient | gRPC v1.63.0 | Dial is deprecated; NewClient does lazy connect (first RPC) not eager |
| github.com/golang/protobuf | google.golang.org/protobuf | ~2020 | Old package deprecated; project already uses new package |
| per-row pgx.Exec in loop | pgx.Batch + SendBatch | pgx v5 | One round-trip for N rows vs N round-trips |

**Deprecated/outdated:**
- `grpc.Dial`: deprecated since v1.63.0; use `grpc.NewClient`. The codebase does not use Dial — don't introduce it.
- `pgx v4 pgxpool`: project uses v5; v4 and v5 pgxpool types are incompatible.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | CSV has exactly 12 columns per data row | CSV parsing patterns | FieldsPerRecord=12 would reject extra columns; verify empirically (2470 rows inspected via head, consistent) |
| A2 | time.Parse(time.RFC3339Nano, ...) correctly parses strings with no fractional seconds | Pattern 3 (FromProto) | Fallback to time.RFC3339 parse handles this; minimal risk |
| A3 | bufconn.Listen(1<<20) buffer is sufficient for test throughput | Pattern 7 | For tests with small batches (< 1000 messages), 1MB is ample; increase if tests show "send buffer full" |

## Open Questions

1. **Reconnect backoff parameters**
   - What we know: Collector must reconnect when stream drops (COLL-02); MQ runs as single-replica (no cluster)
   - What's unclear: Should backoff be configurable via env, or hardcoded constants? How long before giving up?
   - Recommendation: Hardcode base=100ms max=5s for Phase 3; make configurable in Phase 5 Helm values

2. **Streamer rate limiting**
   - What we know: Streamer loops indefinitely; STREAM-05 requires 10 concurrent instances; CSV has 2470 rows
   - What's unclear: At unbounded rate, 10 × 2470 rows/ms could overwhelm MQ or DB
   - Recommendation: Add a configurable sleep between loop iterations (default 1ms); expose STREAMER_LOOP_DELAY_MS env. Tune empirically in Phase 5.

3. **Phase 3 smoke script for Streamer + Collector**
   - What we know: Phase 2 smoke pattern established via `docker compose exec` for psql
   - What's unclear: Phase 3 smoke needs live Streamer → MQ → Collector → Postgres flow; requires all four services running
   - Recommendation: Phase 3 smoke script starts docker compose dev stack + runs each binary in background, asserts row count in Postgres after 3 seconds.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | All builds + tests | ✓ | 1.26.2 darwin/arm64 | — |
| Docker (Rancher Desktop) | testcontainers integration tests | ✓ | 29.1.3 at /Users/ajitg/.rd/docker.sock | — |
| grpcurl | Phase 3 smoke script | ✓ | 1.9.3 | — |
| psql (host) | Phase 3 smoke | ✗ | — | docker compose exec postgres psql (Phase 2 pattern) |
| bufconn | In-process gRPC tests | ✓ | Sub-pkg of grpc v1.81.1 | — |

**Missing dependencies with no fallback:** none

**Missing dependencies with fallback:**
- psql on host: not needed. Phase 2 smoke established `docker compose exec` pattern. Phase 3 smoke script must follow the same pattern.

**Required env vars for integration tests:**
```bash
DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock
TESTCONTAINERS_RYUK_DISABLED=true
```

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing + testify v1.11.1 |
| Config file | none — driven by Makefile targets |
| Quick run command | `go test -race ./cmd/streamer/... ./pkg/models/...` |
| Full suite command | `DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration -coverprofile=coverage.out ./...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| STREAM-01 | CSV loop seeks back after EOF, never returns | unit | `go test ./cmd/streamer/... -run TestLoop` | ❌ Wave 0 |
| STREAM-02 | Restamped timestamp matches RFC3339Nano format | unit | `go test ./cmd/streamer/... -run TestRestamp` | ❌ Wave 0 |
| STREAM-03 | Produce RPC called for each CSV row (bufconn mock MQ) | integration | `DOCKER_HOST=... go test -race -tags=integration ./cmd/streamer/... -run TestProduce` | ❌ Wave 0 |
| STREAM-04 | Malformed row (wrong column count) is skipped, not panic | unit | `go test ./cmd/streamer/... -run TestMalformedRow` | ❌ Wave 0 |
| STREAM-05 | 10 concurrent Streamer goroutines all publish without race | unit/race | `go test -race ./cmd/streamer/... -run TestConcurrent` | ❌ Wave 0 |
| COLL-01 | Initial credit handshake sent on stream open | integration | `DOCKER_HOST=... go test -race -tags=integration ./cmd/collector/... -run TestConsumeHandshake` | ❌ Wave 0 |
| COLL-02 | Stream reconnects after server closes the stream | integration | `DOCKER_HOST=... go test -race -tags=integration ./cmd/collector/... -run TestReconnect` | ❌ Wave 0 |
| COLL-03 | Batch of N messages persisted in one SendBatch round-trip | integration | `DOCKER_HOST=... go test -race -tags=integration ./cmd/collector/... -run TestBatchFlush` | ❌ Wave 0 |
| COLL-04 | FromProto maps proto.uuid → GpuMetric.GpuID correctly | unit | `go test ./pkg/models/... -run TestFromProto` | ❌ Wave 0 |
| COLL-05 | Duplicate proto message (redelivery) does not duplicate DB row | integration | `DOCKER_HOST=... go test -race -tags=integration ./cmd/collector/... -run TestIdempotentUpsert` | ❌ Wave 0 |
| QA-03 | End-to-end: CSV → Streamer → MQ → Collector → Postgres rows asserted | integration | `DOCKER_HOST=... go test -race -tags=integration ./... -run TestEndToEnd` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test -race ./cmd/streamer/... ./pkg/models/... ./cmd/collector/...` (unit tests only, ~1s)
- **Per wave merge:** `DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration ./... -count=1`
- **Phase gate:** `make coverage` (full suite + ≥90% line gate)

### Wave 0 Gaps
- [ ] `cmd/streamer/streamer_test.go` — covers STREAM-01, STREAM-02, STREAM-04, STREAM-05
- [ ] `cmd/streamer/integration_test.go` (build tag: integration) — covers STREAM-03
- [ ] `pkg/models/telemetry_test.go` — covers COLL-04 (FromProto unit tests)
- [ ] `cmd/collector/collector_test.go` — covers COLL-03, COLL-05
- [ ] `cmd/collector/integration_test.go` (build tag: integration) — covers COLL-01, COLL-02, COLL-03, QA-03
- [ ] `pkg/models/telemetry.go` — new file (GpuMetric struct + FromProto + InsertSQL)

## Security Domain

Phase 3 involves data ingestion from a local CSV file, internal gRPC communication, and parameterized PostgreSQL inserts. ASVS Level 1 applicable controls:

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | Internal microservice pipeline; no user-facing auth in this phase |
| V3 Session Management | No | Stateless gRPC streams; no session tokens |
| V4 Access Control | No | No user-facing endpoints in Streamer or Collector |
| V5 Input Validation | Yes | CSV parser skips malformed rows (ErrFieldCount, ParseFloat error); proto payload validated via GetXxx() nil-safe accessors |
| V6 Cryptography | No | In-cluster gRPC with insecure transport (mTLS is Phase 5 / OPS); no credentials in this phase |

### Known Threat Patterns for this Stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| SQL injection via CSV field content | Tampering | Parameterized queries ($1..$11) in pgx.Batch.Queue — never string-interpolate SQL |
| CSV injection (formula injection) | Tampering | DCGM CSV is machine-generated telemetry, not user input; no formula cells; encoding/csv doesn't execute content |
| Panic on malformed CSV | DoS | skip-and-log on parse error, never panic; FieldsPerRecord validation |
| DSN in error strings | Information Disclosure | Enforce CLAUDE.md rule: DSN never in error strings; use fmt.Errorf("collector: ...") without DSN |
| Unbounded reconnect loop | DoS (self) | Exponential backoff with max cap (5s) prevents CPU spin on permanent MQ failure |

## Sources

### Primary (MEDIUM confidence)
- [CITED: pkg.go.dev/encoding/csv] — Reader fields, RFC 4180 embedded quote behavior, ReadAll semantics
- [CITED: pkg.go.dev/github.com/jackc/pgx/v5#Batch] — Batch.Queue, Conn.SendBatch, BatchResults.Exec/Close
- [CITED: pkg.go.dev/google.golang.org/grpc#section-readme] — NewClient, bidi stream goroutine safety, keepalive ClientParameters
- [CITED: pkg.go.dev/github.com/testcontainers/testcontainers-go/modules/postgres] — Run, Snapshot, Restore, BasicWaitStrategies
- [VERIFIED: codebase cmd/mq/main.go] — keepalive server params (Time=30s, Timeout=10s, MinTime=15s, PermitWithoutStream=true)
- [VERIFIED: codebase internal/server/server.go] — bidi Consume protocol, one-goroutine-per-direction rule, credit/ack semantics
- [VERIFIED: codebase pkg/db/db_test.go] — testcontainers Snapshot/Restore pattern, DOCKER_HOST, restoreDB()
- [VERIFIED: codebase api/proto/mq.proto] — TelemetryMessage field numbering, ConsumeClientMsg protocol
- [VERIFIED: codebase pkg/db/migrations/000001_init_schema.up.sql] — ON CONFLICT column set (gpu_id, metric_name, timestamp), natural key name
- [VERIFIED: go doc google.golang.org/grpc/test/bufconn] — bufconn available as sub-package of grpc v1.81.1, no new dependency

### Secondary (LOW confidence)
- [WebSearch: pkg.go.dev/encoding/csv] — encoding/csv documentation via web fetch; confirmed by pkg.go.dev

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all packages already in go.mod, versions verified
- Architecture: MEDIUM — patterns derived from existing codebase (cmd/mq, pkg/db); proto contract locked
- CSV parsing: MEDIUM — empirically verified on actual dcgm_metrics_*.csv file (2470 rows, 12 columns)
- Pitfalls: MEDIUM — derived from locked Phase 2 decisions + direct MQ server code reading

**Research date:** 2026-06-29
**Valid until:** 2026-07-29 (30 days — stable Go/pgx/gRPC ecosystem; proto contract locked)
