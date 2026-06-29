# Phase 3: Pipeline — Streamer + Collector + Integration - Pattern Map

**Mapped:** 2026-06-29
**Files analyzed:** 10 (new/modified files for Phase 3)
**Analogs found:** 10 / 10

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `cmd/streamer/main.go` | service entrypoint | request-response | `cmd/mq/main.go` | exact |
| `cmd/streamer/config.go` | config | request-response | `internal/config/config.go` | exact |
| `cmd/streamer/streamer.go` | service | streaming + request-response | `internal/server/server.go` (Produce loop) | role-match |
| `cmd/streamer/streamer_test.go` | test | — | `internal/server/server_test.go` | exact |
| `cmd/collector/main.go` | service entrypoint | request-response | `cmd/mq/main.go` | exact |
| `cmd/collector/config.go` | config | request-response | `internal/config/config.go` | exact |
| `cmd/collector/collector.go` | service | streaming + CRUD | `internal/server/server.go` (Consume loop) | role-match |
| `cmd/collector/collector_test.go` | test | — | `pkg/db/db_test.go` + `internal/server/server_test.go` | role-match |
| `pkg/models/telemetry.go` | model + utility | transform | `pkg/db/config.go` (struct + FromEnv pattern) | role-match |
| `pkg/models/telemetry_test.go` | test | — | `internal/server/server_test.go` | role-match |

## Pattern Assignments

---

### `cmd/streamer/main.go` (service entrypoint, request-response)

**Analog:** `cmd/mq/main.go`

**Imports pattern** (`cmd/mq/main.go` lines 8-27):
```go
import (
    "context"
    "errors"
    "log"
    "os/signal"
    "syscall"

    "golang.org/x/sync/errgroup"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/grpc/keepalive"

    // local packages
    "github.com/ajitg/vantage/pkg/pb"
)
```

**errgroup + signal wiring** (`cmd/mq/main.go` lines 63-98):
```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer stop()

g, gctx := errgroup.WithContext(ctx)

g.Go(func() error {
    return streamer.Run(gctx, cfg)
})

g.Go(func() error {
    <-gctx.Done()
    return nil // Streamer.Run detects ctx.Err() internally
})

if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
    log.Fatal(err)
}
```

**Config + startup log** (`cmd/mq/main.go` lines 29-31, 94):
```go
cfg := config.FromEnv()
// ...
log.Printf("streamer: MQ addr %s, CSV %s", cfg.MQAddr, cfg.CSVPath)
```

---

### `cmd/streamer/config.go` (config, request-response)

**Analog:** `internal/config/config.go`

**Struct + FromEnv pattern** (`internal/config/config.go` lines 1-70):
```go
package config

import (
    "os"
    "strconv"
)

type Config struct {
    MQAddr        string // STREAMER_MQ_ADDR (default :50051)
    CSVPath       string // STREAMER_CSV_PATH (required)
    LoopDelayMS   int    // STREAMER_LOOP_DELAY_MS (default 1)
}

func FromEnv() Config {
    cfg := Config{
        MQAddr:      ":50051",
        LoopDelayMS: 1,
    }
    if v := os.Getenv("STREAMER_MQ_ADDR"); v != "" {
        cfg.MQAddr = v
    }
    if v := os.Getenv("STREAMER_CSV_PATH"); v != "" {
        cfg.CSVPath = v
    }
    if v := os.Getenv("STREAMER_LOOP_DELAY_MS"); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n >= 0 {
            cfg.LoopDelayMS = n
        }
    }
    return cfg
}
```

Note: CSVPath is required at runtime but validated in `streamer.Run` (not in FromEnv), following the same pattern as `pkg/db/config.go` where DSN emptiness is checked in `New()`.

---

### `cmd/streamer/streamer.go` (service, streaming)

**Analog:** `internal/server/server.go` (Produce handler) + RESEARCH.md patterns 1–2

**gRPC client dial pattern** (`cmd/mq/main.go` keepalive + RESEARCH.md Pattern 6):
```go
func dialMQ(addr string) (*grpc.ClientConn, error) {
    return grpc.NewClient(addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                30 * time.Second,
            Timeout:             10 * time.Second,
            PermitWithoutStream: true,
        }),
    )
}
```

**CSV infinite loop skeleton** (RESEARCH.md Pattern 1 — derived from DCGM CSV analysis + stdlib):
```go
func Run(ctx context.Context, cfg Config) error {
    conn, err := dialMQ(cfg.MQAddr)
    if err != nil {
        return fmt.Errorf("streamer: dial: %w", err)
    }
    defer conn.Close()
    client := pb.NewMQServiceClient(conn)

    f, err := os.Open(cfg.CSVPath)
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
        r.FieldsPerRecord = 12
        if _, err := r.Read(); err != nil { // discard header
            return fmt.Errorf("streamer: read header: %w", err)
        }
        for {
            record, err := r.Read()
            if err == io.EOF {
                break
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
            if _, err := client.Produce(ctx, &pb.ProduceRequest{Message: msg}); err != nil {
                return fmt.Errorf("streamer: produce: %w", err)
            }
            if cfg.LoopDelayMS > 0 {
                time.Sleep(time.Duration(cfg.LoopDelayMS) * time.Millisecond)
            }
        }
    }
}
```

**recordToProto — RFC3339Nano restamp + uuid→GpuId mapping** (RESEARCH.md Pattern 2):
```go
// Column indices: 0=timestamp(ignored) 1=metric_name 2=gpu_id(ordinal) 3=device
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
        GpuId:      record[2],
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
```

---

### `cmd/streamer/streamer_test.go` (test, unit)

**Analog:** `internal/server/server_test.go`

**Test file structure** (`internal/server/server_test.go` lines 1-21):
```go
package streamer_test  // black-box test package

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/ajitg/vantage/pkg/pb"
)
```

**Table-driven unit test style** (`internal/server/server_test.go` pattern — require for fatal, assert for non-fatal):
```go
func TestRecordToProto(t *testing.T) {
    cases := []struct {
        name    string
        record  []string
        wantErr bool
    }{
        {"valid 12-col", []string{"ts","metric","0","dev","GPU-xxx","model","host","","","",  "42.5",""}, false},
        {"bad value",    []string{"ts","metric","0","dev","GPU-xxx","model","host","","","","not-float",""}, true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            // ...
            require.NoError(t, err)
        })
    }
}
```

**No build tag for unit tests** — integration tests use `//go:build integration` (see `pkg/db/db_test.go` line 1).

---

### `cmd/collector/main.go` (service entrypoint, request-response)

**Analog:** `cmd/mq/main.go`

**errgroup + db.Migrate + db.New startup** (`cmd/mq/main.go` + `pkg/db/db.go`):
```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    cfg := config.FromEnv()

    dbCfg, err := db.FromEnv()
    if err != nil {
        log.Fatal(err)
    }
    if err := db.Migrate(ctx, dbCfg.DSN); err != nil {
        log.Fatalf("collector: migrate: %v", err)
    }
    pool, err := db.New(ctx, dbCfg)
    if err != nil {
        log.Fatalf("collector: db pool: %v", err)
    }
    defer pool.Close()

    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error {
        return collector.Run(gctx, cfg, pool)
    })
    g.Go(func() error {
        <-gctx.Done()
        return nil
    })

    if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
        log.Fatal(err)
    }
}
```

---

### `cmd/collector/config.go` (config, request-response)

**Analog:** `internal/config/config.go`

**Struct + FromEnv pattern** (same shape as `internal/config/config.go` lines 34-70):
```go
type Config struct {
    MQAddr      string // COLLECTOR_MQ_ADDR (default :50051)
    BatchSize   int    // COLLECTOR_BATCH_SIZE (default 50)
    FlushMS     int    // COLLECTOR_FLUSH_MS (default 500)
    Credit      int    // COLLECTOR_CREDIT (default 100 = 2×BatchSize)
}

func FromEnv() Config {
    cfg := Config{
        MQAddr:    ":50051",
        BatchSize: 50,
        FlushMS:   500,
        Credit:    100,
    }
    // os.Getenv + strconv.Atoi pattern — identical to internal/config/config.go
}
```

---

### `cmd/collector/collector.go` (service, streaming + CRUD)

**Analog:** `internal/server/server.go` (Consume handler — two-goroutine recv/send split)

**gRPC dial** (same as Streamer, `cmd/mq/main.go` keepalive params):
```go
func dialMQ(addr string) (*grpc.ClientConn, error) {
    return grpc.NewClient(addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true,
        }),
    )
}
```

**Reconnect loop with exponential backoff** (COLL-02 — no analog in codebase; use hardcoded constants):
```go
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
        backoff = 100 * time.Millisecond // reset on successful connect

        err = consumeLoop(ctx, cfg, pb.NewMQServiceClient(conn), pool)
        conn.Close()
        if errors.Is(err, context.Canceled) || ctx.Err() != nil {
            return ctx.Err()
        }
        log.Printf("collector: stream ended (%v) — reconnecting", err)
    }
}
```

**Two-goroutine bidi stream** (`internal/server/server.go` lines 132-292 — mirrored at the client side; see RESEARCH.md Pattern 4):
```go
func consumeLoop(ctx context.Context, cfg Config, client pb.MQServiceClient, pool *pgxpool.Pool) error {
    stream, err := client.Consume(ctx)
    if err != nil {
        return fmt.Errorf("collector: open stream: %w", err)
    }

    msgCh := make(chan *pb.TelemetryMessage, cfg.Credit)

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

    // Send initial credit handshake — batch goroutine is sole caller of stream.Send()
    if err := stream.Send(&pb.ConsumeClientMsg{Credit: int32(cfg.Credit)}); err != nil {
        return fmt.Errorf("collector: send credit: %w", err)
    }

    ticker := time.NewTicker(time.Duration(cfg.FlushMS) * time.Millisecond)
    defer ticker.Stop()

    var batch []*pb.TelemetryMessage
    flush := func() error { /* persistBatch + ack loop — see Pattern 5 */ }

    for {
        select {
        case msg, ok := <-msgCh:
            if !ok {
                _ = flush()
                return <-recvDone
            }
            batch = append(batch, msg)
            if len(batch) >= cfg.BatchSize {
                if err := flush(); err != nil { return err }
            }
        case <-ticker.C:
            if err := flush(); err != nil { return err }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

**pgx.Batch idempotent upsert** (`pkg/db/db_test.go` lines 106-124 for parameterized pattern; RESEARCH.md Pattern 5):
```go
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
    defer br.Close() // MUST be deferred before the Exec loop
    for i := 0; i < b.Len(); i++ {
        if _, err := br.Exec(); err != nil {
            log.Printf("collector: batch row %d: %v", i, err)
            // log and continue — partial failure does not abort other rows
        }
    }
    return nil
}
```

---

### `cmd/collector/collector_test.go` (test, integration)

**Analog:** `pkg/db/db_test.go` (TestMain + testcontainers) + `internal/server/server_test.go` (mockConsumeStream)

**Build tag + TestMain pattern** (`pkg/db/db_test.go` lines 1-80):
```go
//go:build integration

package collector_test

import (
    "context"
    "log"
    "os"
    "testing"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/stretchr/testify/require"
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/modules/postgres"
    "google.golang.org/grpc/test/bufconn"

    "github.com/ajitg/vantage/pkg/db"
    "github.com/ajitg/vantage/pkg/pb"
)

var (
    testPool *pgxpool.Pool
    testCtr  *postgres.PostgresContainer
)

func TestMain(m *testing.M) {
    ctx := context.Background()
    ctr, err := postgres.Run(ctx,
        "postgres:17-alpine",
        postgres.WithDatabase("vantage_test"),
        postgres.WithUsername("postgres"),   // MUST be "postgres" for Snapshot/Restore
        postgres.WithPassword("secret"),
        postgres.BasicWaitStrategies(),
        postgres.WithSQLDriver("pgx"),
    )
    if err != nil { log.Fatalf("start postgres: %v", err) }
    defer testcontainers.TerminateContainer(ctr) //nolint:errcheck
    testCtr = ctr

    dsn := ctr.MustConnectionString(ctx, "sslmode=disable")
    if err := db.Migrate(ctx, dsn); err != nil { log.Fatalf("migrate: %v", err) }
    if err := ctr.Snapshot(ctx); err != nil { log.Fatalf("snapshot: %v", err) }

    pool, err := db.New(ctx, db.Config{DSN: dsn, MaxConns: 5})
    if err != nil { log.Fatalf("pool: %v", err) }
    testPool = pool
    defer pool.Close()
    os.Exit(m.Run())
}
```

**restoreDB helper** (`pkg/db/db_test.go` lines 132-139):
```go
func restoreDB(ctx context.Context, t *testing.T) {
    t.Helper()
    if err := testCtr.Restore(ctx); err != nil {
        t.Errorf("restore: %v", err)
    }
    _ = testPool.Ping(context.Background()) // evict terminated connections
}
```

**bufconn in-process MQ server** (RESEARCH.md Pattern 7 — no analog; new pattern):
```go
const bufSize = 1 << 20

func newBufconnMQ(t *testing.T) (*grpc.ClientConn, *server.MQServer) {
    t.Helper()
    lis := bufconn.Listen(bufSize)
    mqSrv := server.NewMQServer(queue.NewRingStore(1000), 100)
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
    return conn, mqSrv
}
```

---

### `pkg/models/telemetry.go` (model + utility, transform)

**Analog:** `pkg/db/config.go` (exported struct with constructor)

**Package doc comment style** (`pkg/db/config.go` lines 1-13):
```go
// Package models provides the shared GpuMetric data model and the wire-to-model
// converter used by the Collector and API Gateway.
package models
```

**Struct + constructor pattern** (`pkg/db/config.go` lines 23-63):
```go
import (
    "fmt"
    "time"

    "github.com/ajitg/vantage/pkg/pb"
)

type GpuMetric struct {
    GpuID      string    // GPU UUID from proto.Uuid (NOT proto.GpuId ordinal)
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
// CRITICAL: proto.Uuid → GpuID (the GPU UUID, not the ordinal proto.GpuId).
// Timestamp must be RFC3339Nano; falls back to RFC3339 if no sub-second part.
func FromProto(msg *pb.TelemetryMessage) (GpuMetric, error) {
    ts, err := time.Parse(time.RFC3339Nano, msg.GetTimestamp())
    if err != nil {
        ts, err = time.Parse(time.RFC3339, msg.GetTimestamp())
        if err != nil {
            return GpuMetric{}, fmt.Errorf("models: parse timestamp %q: %w", msg.GetTimestamp(), err)
        }
    }
    return GpuMetric{
        GpuID:      msg.GetUuid(), // GPU UUID, not ordinal
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

---

### `pkg/models/telemetry_test.go` (test, unit)

**Analog:** `internal/server/server_test.go` (package-level unit tests, no build tag)

**Test file structure** (`internal/server/server_test.go` lines 1-21):
```go
package models_test

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    "github.com/ajitg/vantage/pkg/pb"
    "github.com/ajitg/vantage/pkg/models"
)

func TestFromProto_UUIDMapping(t *testing.T) {
    msg := &pb.TelemetryMessage{
        Uuid:       "GPU-5fd4f087-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
        GpuId:      "0", // ordinal — must NOT be stored
        Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
        MetricName: "DCGM_FI_DEV_GPU_UTIL",
        Value:      42.5,
    }
    m, err := models.FromProto(msg)
    require.NoError(t, err)
    require.Equal(t, msg.GetUuid(), m.GpuID, "GpuID must be the UUID, not the ordinal")
}
```

---

## Shared Patterns

### errgroup + signal.NotifyContext
**Source:** `cmd/mq/main.go` lines 63-98
**Apply to:** `cmd/streamer/main.go`, `cmd/collector/main.go`
```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer stop()
g, gctx := errgroup.WithContext(ctx)
// ...
if err := g.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
    log.Fatal(err)
}
```

### Config FromEnv (env-first, defaults inline)
**Source:** `internal/config/config.go` lines 42-70
**Apply to:** `cmd/streamer/config.go`, `cmd/collector/config.go`
- struct with all fields defaulted in the literal
- one `if v := os.Getenv(key); v != ""` block per field
- strconv.Atoi for integers; invalid values silently kept as default (not error)
- `db.FromEnv()` is the exception: returns `error` because DSN has no fallback

### gRPC NewClient with keepalive
**Source:** `cmd/mq/main.go` lines 39-49 (server params) — match with client params
**Apply to:** `cmd/streamer/streamer.go`, `cmd/collector/collector.go`
```go
grpc.NewClient(addr,
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithKeepaliveParams(keepalive.ClientParameters{
        Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true,
    }),
)
```
Note: `grpc.Dial` is deprecated since v1.63.0 — always use `grpc.NewClient`.

### Error wrapping (no DSN in errors)
**Source:** `pkg/db/db.go` lines 29-54, `pkg/db/config.go` lines 46-50
**Apply to:** all service files
```go
return nil, fmt.Errorf("streamer: open csv: %w", err)  // service-prefixed, no credentials
```

### testcontainers Snapshot/Restore
**Source:** `pkg/db/db_test.go` lines 39-80 + 132-139
**Apply to:** `cmd/collector/collector_test.go` (integration, `//go:build integration`)
```go
// TestMain: postgres.Run → db.Migrate → ctr.Snapshot → db.New
// Per-test cleanup: t.Cleanup(func() { restoreDB(ctx, t) })
// restoreDB: ctr.Restore → testPool.Ping (evict terminated conns)
```

### Two-goroutine gRPC bidi stream split
**Source:** `internal/server/server.go` lines 132-292 (server side)
**Apply to:** `cmd/collector/collector.go` (client side — mirror the shape)
- One goroutine: sole caller of `stream.Recv()` → forwards via channel
- Other goroutine (main): sole caller of `stream.Send()` → sends credit + acks
- Never call `Send` from the recv goroutine (gRPC panics on concurrent Send+Send)

### pgx.Batch exact drain
**Source:** `pkg/db/db_test.go` lines 106-124 (pattern); RESEARCH.md Pattern 5
**Apply to:** `cmd/collector/collector.go` `persistBatch`
- `defer br.Close()` immediately after `pool.SendBatch`
- loop `for i := 0; i < b.Len(); i++ { br.Exec() }` — exactly b.Len() calls
- log-and-continue per row error; do not abort the batch

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| (none) | — | — | All Phase 3 files have usable codebase analogs |

The reconnect backoff loop in `cmd/collector/collector.go` has no exact codebase analog (only pattern-6 from RESEARCH.md). Planner should use the RESEARCH.md hardcoded-backoff pattern (base=100ms, max=5s).

## Metadata

**Analog search scope:** `cmd/mq/`, `internal/config/`, `internal/server/`, `pkg/db/`, `pkg/pb/`
**Files scanned:** 9 Go source files + RESEARCH.md patterns
**Pattern extraction date:** 2026-06-29
