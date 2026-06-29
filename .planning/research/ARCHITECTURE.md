# Architecture Research

**Domain:** Custom in-memory message queue + GPU telemetry pipeline (Go microservices on Kubernetes)
**Researched:** 2026-06-27
**Confidence:** HIGH

---

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                        Kubernetes Cluster                            │
│                                                                      │
│  ┌────────────────────────────┐                                       │
│  │  Streamer (0–10 replicas)  │                                       │
│  │  cmd/streamer              │                                       │
│  │  - CSV line reader         │                                       │
│  │  - restamp timestamp       │                                       │
│  │  - gRPC Produce client     │                                       │
│  └──────────────┬─────────────┘                                       │
│                 │  gRPC Produce (unary, per record)                   │
│                 ▼                                                      │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │  MQ (single replica)  cmd/mq                                  │    │
│  │                                                               │    │
│  │  ┌─────────────────────────────────────────────────────┐     │    │
│  │  │  In-memory bounded channel  (chan *pb.TelemetryMsg) │     │    │
│  │  │  cap = configurable (default 10 000)                │     │    │
│  │  └─────────────────────────────────────────────────────┘     │    │
│  │                                                               │    │
│  │  gRPC :50051 (data plane)      HTTP :8080 (control plane)     │    │
│  │   • Produce (unary)             • GET /api/v1/queue/inspect   │    │
│  │   • Consume (server-stream)                                   │    │
│  └────────────────┬─────────────────────────────────────────────┘    │
│                   │  gRPC Consume (server-stream, long-lived)         │
│                   ▼                                                    │
│  ┌────────────────────────────┐                                       │
│  │  Collector (N replicas)    │                                       │
│  │  cmd/collector             │                                       │
│  │  - gRPC stream consumer    │                                       │
│  │  - pgxpool batch insert    │                                       │
│  └──────────────┬─────────────┘                                       │
│                 │  SQL batch INSERT (pgxpool)                          │
│                 ▼                                                      │
│  ┌──────────────────────────────────┐                                 │
│  │  PostgreSQL (Helm dependency)    │                                 │
│  │  - gpu_telemetry table           │                                 │
│  │  - INDEX (gpu_id, timestamp DESC)│                                 │
│  └──────────────┬───────────────────┘                                 │
│                 │  SQL SELECT                                          │
│                 ▼                                                      │
│  ┌──────────────────────────────────┐                                 │
│  │  API Gateway  cmd/gateway        │                                 │
│  │  - GET /api/v1/gpus              │                                 │
│  │  - GET /api/v1/gpus/{id}/...     │                                 │
│  │  - swag-generated OpenAPI        │                                 │
│  └──────────────────────────────────┘                                 │
└──────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Communicates With |
|-----------|----------------|-------------------|
| Streamer | Read CSV in an infinite loop, restamp `now`, push via gRPC Produce | MQ (gRPC client) |
| MQ | Accept Produce calls, buffer messages, fan messages to Consume streams; expose inspect | Streamer (gRPC server), Collector (gRPC server), curl/ops (HTTP) |
| Collector | Maintain long-lived Consume stream, batch-insert received records | MQ (gRPC client), PostgreSQL (pgxpool) |
| API Gateway | Serve read-only REST API over PostgreSQL; auto-generate OpenAPI | PostgreSQL (pgxpool or sql queries) |
| PostgreSQL | Persist and index telemetry; single source of truth | Collector (writer), API Gateway (reader) |

---

## MQ Internal Design — Concurrency Model

### Delivery Semantics: Competing Consumers (not per-consumer cursors)

The spec requires multiple concurrent Collectors to receive **unique** messages — meaning each message is delivered to **exactly one** Collector. This is the **competing consumers / work-queue** pattern, not broadcast/fan-out.

**Chosen model: buffered Go channel as the queue.**

```go
type Queue struct {
    messages chan *pb.TelemetryMsg   // bounded FIFO; goroutine-safe by construction
    capacity int

    mu           sync.RWMutex       // guards metadata only (produced/consumed counters)
    producedTotal int64
    consumedTotal int64
    activeConsumers int32            // atomic
}
```

Why a channel and not a ring buffer with `sync.RWMutex`:
- A buffered channel provides FIFO ordering, bounded capacity, and compete-safe dequeue (`<-ch` atomically dequeues exactly one reader) with no additional mutex needed around the data structure itself.
- A ring buffer with `sync.RWMutex` is the right primitive when multiple consumers need **independent read cursors** (pub-sub / broadcast). The spec calls for work-queue semantics, not broadcast. Ring buffer + cursor overhead here buys nothing and adds complexity.
- The mutex is still needed for the inspect counters and active-consumer count, but not for the message path.

### Produce Handler (gRPC unary)

```go
func (s *MQServer) Produce(ctx context.Context, req *pb.ProduceRequest) (*pb.ProduceResponse, error) {
    select {
    case s.q.messages <- req.Message:
        atomic.AddInt64(&s.q.producedTotal, 1)
        return &pb.ProduceResponse{Ok: true}, nil
    case <-ctx.Done():
        return nil, status.Error(codes.Canceled, "context canceled")
    default:
        // queue full — return ResourceExhausted so Streamer can back off
        return nil, status.Error(codes.ResourceExhausted, "queue full")
    }
}
```

Choosing `default` (non-blocking) rather than blocking the producer lets Streamers apply their own retry/backoff instead of holding a gRPC call open. Alternately, replace `default` with a timeout select if the spec requires blocking backpressure.

### Consume Handler (gRPC server-stream)

```go
func (s *MQServer) Consume(req *pb.ConsumeRequest, stream pb.MQ_ConsumeServer) error {
    atomic.AddInt32(&s.q.activeConsumers, 1)
    defer atomic.AddInt32(&s.q.activeConsumers, -1)

    for {
        select {
        case msg, ok := <-s.q.messages:
            if !ok {
                return nil  // queue closed — graceful shutdown
            }
            if err := stream.Send(msg); err != nil {
                // Collector disconnected; message is lost (no persistence per spec)
                return err
            }
            atomic.AddInt64(&s.q.consumedTotal, 1)
        case <-stream.Context().Done():
            return stream.Context().Err()
        }
    }
}
```

Key invariant: the channel `<-` dequeue is atomic from Go's perspective. Two concurrent Consume goroutines can never receive the same message. Unique delivery is guaranteed by the channel primitive itself — no additional deduplication logic needed.

### Backpressure

- Produce: `ResourceExhausted` when `len(messages) == cap(messages)`. Streamers must implement exponential backoff on this error.
- Consume: natural flow control — each Consume goroutine blocks on `stream.Send` until the TCP window allows it. If a slow Collector falls behind, the channel fills, and Produce starts returning `ResourceExhausted`, which propagates backpressure all the way to the CSV reader.

### Graceful Shutdown

```
1. Receive SIGTERM
2. Stop accepting new Produce RPCs (return Unavailable)
3. close(q.messages) → all Consume goroutines exit their select loop
4. gRPC server.GracefulStop() → waits for in-flight RPCs to finish
5. HTTP server.Shutdown(ctx)
```

Use a `sync.WaitGroup` inside the MQ server to track active Consume streams; wait for it before returning from main.

### HTTP Control Plane — /api/v1/queue/inspect

Runs on a separate port (e.g., :8080) in the same process. A simple `net/http` handler reads atomics; no locks needed in the hot path:

```json
{
  "capacity": 10000,
  "length": 342,
  "produced_total": 58210,
  "consumed_total": 57868,
  "active_consumers": 3
}
```

---

## Service Boundaries and pkg/ Contract Surface

### Shared Surface (pkg/) — Strict Minimalism

```
pkg/
├── pb/        Generated Go from api/proto/mq.proto.
│              Consumed by: cmd/mq (server), cmd/streamer (client), cmd/collector (client).
│              NOT consumed by: cmd/gateway (Gateway talks to Postgres, not MQ).
│
├── db/        pgxpool init + query helpers.
│              Consumed by: cmd/collector (write path), cmd/gateway (read path).
│              NOT consumed by: cmd/mq or cmd/streamer.
│
└── models/    Go structs for the telemetry domain (GpuTelemetry, etc.).
               Consumed by: cmd/collector (mapping pb→model before insert),
                            cmd/gateway (query result mapping).
               NOT consumed by: cmd/mq (MQ is transport-only, has no domain knowledge).
```

### Coupling Rules (enforced by code review, not the compiler)

| Rule | Rationale |
|------|-----------|
| `cmd/<svc>` never imports another `cmd/<svc>` | Service independence |
| `pkg/models` never imports `pkg/pb` | Wire types must not leak into domain layer |
| `pkg/db` never imports `pkg/pb` | DB layer is MQ-agnostic |
| `cmd/gateway` never imports `pkg/pb` | Gateway has no gRPC; uses SQL only |
| `cmd/mq` never imports `pkg/db` or `pkg/models` | MQ is storage-agnostic |

Proto contract changes (`api/proto/mq.proto`) require coordinated updates across all three consuming services and are the highest-risk shared surface.

---

## Data Flow

### Write Path (telemetry ingestion)

```
dcgm_metrics.csv (local file)
    │
    │  Streamer: readline loop, restamp timestamp field with time.Now()
    ▼
pb.TelemetryMsg (Protobuf wire format)
    │
    │  gRPC Produce (unary) — one call per CSV record
    ▼
MQ: q.messages <- msg  (buffered channel, cap N)
    │
    │  gRPC Consume (server-stream) — one long-lived stream per Collector replica
    ▼
Collector: receives pb.TelemetryMsg
    │  maps to models.GpuTelemetry
    │  accumulates batch (e.g., 100 records or 500ms flush interval)
    ▼
pgxpool.SendBatch() → PostgreSQL gpu_telemetry table
```

### Read Path (API queries)

```
HTTP client
    │  GET /api/v1/gpus/{id}/telemetry?start_time=X&end_time=Y
    ▼
API Gateway (net/http)
    │  pkg/db query helper
    │  SELECT * FROM gpu_telemetry WHERE gpu_id=$1 AND timestamp BETWEEN $2 AND $3
    │  ORDER BY timestamp DESC  (index: gpu_id, timestamp DESC)
    ▼
PostgreSQL
    │  returns rows
    ▼
API Gateway: serialize to JSON
    │
    ▼
HTTP response (JSON array)
```

### Key Data Flow Observations

1. **MQ is stateless from the domain perspective.** It carries `pb.TelemetryMsg` bytes; it does not parse `gpu_id` or `timestamp`. All domain logic lives in Streamer (CSV parsing) and Collector (protobuf→model mapping).
2. **No synchronous coupling between write path and read path.** The API Gateway reads from Postgres independently; it does not touch the MQ. This means the gateway can be built and tested with a populated Postgres fixture before the streaming pipeline exists.
3. **Collector is the only process that writes to Postgres.** The Gateway is read-only. This makes the Collector the sole owner of schema compatibility.

---

## PostgreSQL Schema

```sql
-- Time-series telemetry table
CREATE TABLE gpu_telemetry (
    id          BIGSERIAL    PRIMARY KEY,
    gpu_id      TEXT         NOT NULL,
    timestamp   TIMESTAMPTZ  NOT NULL,
    metric_name TEXT         NOT NULL,
    device      TEXT,
    uuid        TEXT,
    model_name  TEXT,
    hostname    TEXT,
    container   TEXT,
    pod         TEXT,
    namespace   TEXT,
    value       DOUBLE PRECISION,
    labels_raw  TEXT
);

-- Mandatory composite index — covers (gpu_id point lookup) + (time range scan)
-- DESC on timestamp matches ORDER BY timestamp DESC in API queries (avoids sort)
CREATE INDEX idx_gpu_telemetry_gpu_ts
    ON gpu_telemetry (gpu_id, timestamp DESC);
```

Index usage:
- `GET /api/v1/gpus/{id}/telemetry` → index scan on `gpu_id` prefix
- `?start_time=&end_time=` → index range scan on `(gpu_id, timestamp DESC)`, no file sort needed
- `GET /api/v1/gpus` → `SELECT DISTINCT gpu_id` uses the index prefix; consider a separate index or materialized GPU registry if cardinality grows large

The DCGM CSV has one row per (timestamp, metric_name, gpu_id) combination — the schema is a flat event log. Do not try to pivot metrics into columns; the `(metric_name, value)` pair is the correct normalized form for a time-series event store.

---

## Repository Structure

```
github.com/ajitg/vantage/          (single go.mod — one module)
│
├── api/
│   └── proto/
│       └── mq.proto               # Produce + Consume RPC definitions
│
├── pkg/
│   ├── pb/                        # generated: protoc output (mq.pb.go, mq_grpc.pb.go)
│   ├── db/                        # pgxpool init, query functions, DDL migration
│   └── models/                    # GpuTelemetry struct (matches gpu_telemetry table)
│
├── cmd/
│   ├── mq/
│   │   └── main.go                # gRPC server + HTTP inspect server wired together
│   ├── streamer/
│   │   └── main.go                # CSV loop + gRPC Produce client
│   ├── collector/
│   │   └── main.go                # gRPC Consume client + pgxpool batch insert
│   └── gateway/
│       └── main.go                # net/http REST server + swag annotations
│
├── build/
│   ├── mq.Dockerfile
│   ├── streamer.Dockerfile
│   ├── collector.Dockerfile
│   └── gateway.Dockerfile
│
├── deployments/
│   └── charts/
│       ├── mq/
│       ├── streamer/
│       ├── collector/
│       ├── gateway/
│       └── postgres/              # Bitnami postgres sub-chart
│
├── Makefile                       # proto, build, test, coverage, swagger, docker, k8s
└── go.mod
```

---

## Build Order and Dependency Graph for Vertical-MVP

### Dependency Graph

```
api/proto/mq.proto
    └─► pkg/pb (protoc generation)
            ├─► cmd/mq           (gRPC server; no other pkg/ deps)
            ├─► cmd/streamer     (gRPC client; no db deps)
            └─► cmd/collector    (gRPC client)
                    └─► pkg/db + pkg/models + PostgreSQL schema
                                └─► cmd/gateway (read-only; pkg/db + pkg/models)
```

### Phase Ordering Rationale

| Phase | What to Build | Why This Order |
|-------|---------------|----------------|
| **1. Foundation** | `go.mod`, `api/proto/mq.proto`, `pkg/pb` (generated), `Makefile proto` target | Everything compiles against generated pb types; proto is the dependency root |
| **2. MQ Core** | `cmd/mq` in-memory queue + gRPC server + HTTP inspect; race tests | MQ must exist before producers or consumers can connect; prove correctness first with `-race` |
| **3. Streamer** | `cmd/streamer` CSV loop + gRPC Produce client | Can be integration-tested against live MQ from Phase 2 |
| **4. Storage** | PostgreSQL DDL (`pkg/db`), `pkg/models`, `cmd/collector` stream consumer + batch insert | Collector depends on MQ (Phase 2) and Postgres schema; builds on Streamer's proto contract |
| **5. API Gateway** | `cmd/gateway` REST endpoints + swag OpenAPI generation | Can be built against a populated Postgres fixture; zero MQ dependency |
| **6. DevOps** | `build/*.Dockerfile`, `deployments/charts/`, Makefile docker/k8s targets | Containerization is independent of business logic; can parallelize with Phase 5 |
| **7. Hardening** | Integration test suite, coverage gate (≥90%), race detector CI | Runs after all services exist; proves end-to-end pipeline correctness |

### Critical Path

`mq.proto` → `pkg/pb` → `cmd/mq` → `cmd/streamer` + `cmd/collector` → end-to-end pipeline test

The API Gateway is **off the critical path**. It can be built concurrently with Phase 3–4 using a static Postgres fixture.

---

## Architectural Patterns

### Pattern 1: Channel-backed Competing Consumer Queue

**What:** A `chan *pb.TelemetryMsg` is the entire queue. Multiple Consume goroutines block on `<-ch`; the Go runtime ensures exactly one goroutine receives each message.

**When to use:** Any work-queue (each job processed by exactly one worker) where producer and consumer are in the same OS process.

**Trade-offs:** Simple and correct. Bounded by memory. No persistence. Closing the channel is a clean shutdown signal that propagates to all consumers automatically. Not suitable for pub-sub (where every consumer needs every message).

### Pattern 2: Dual-Port Single Process (gRPC + HTTP in one binary)

**What:** `cmd/mq/main.go` starts two listeners: gRPC on :50051 (data plane) and `net/http` on :8080 (control plane). Both run in the same Go process, sharing the queue struct via pointer.

**When to use:** When a service has two different clients with different protocol needs (high-throughput streaming vs. human-readable admin API).

**Trade-offs:** Simple ops (one pod, one Dockerfile). The HTTP control plane can read queue state without any IPC. Risk: a bug in HTTP handler can crash the gRPC server. Mitigate with `recover` middleware on the HTTP side.

### Pattern 3: Protobuf-Only Cross-Service Contract

**What:** All inter-service communication uses generated types from `pkg/pb`. Services never share Go structs directly — only wire-format messages.

**When to use:** Microservices in the same language that need future language-independence or schema evolution without coordinated deploys.

**Trade-offs:** Protobuf adds a serialization step but gives explicit schema versioning. `pkg/models` (Go structs) is separate from `pkg/pb` (wire types); the mapping layer in Collector is deliberate.

---

## Anti-Patterns

### Anti-Pattern 1: Ring Buffer with Per-Consumer Read Cursors

**What people do:** Implement a ring buffer with `sync.RWMutex` where each Collector holds a read position and advances it independently.

**Why it's wrong:** Per-consumer cursors implement **pub-sub** (broadcast) — every consumer sees every message. The spec requires **unique delivery** (competing consumers). With cursors, three Collectors each see all 1000 messages = 3000 inserts of duplicates.

**Do this instead:** Use a buffered channel. Each dequeue is exclusive by Go's memory model. One consumer per message, guaranteed.

### Anti-Pattern 2: Storing Protobuf Types in the Database Layer

**What people do:** Pass `*pb.TelemetryMsg` directly to `pgxpool` insert functions, storing protobuf field names as column names.

**Why it's wrong:** Couples the PostgreSQL schema to the gRPC wire format. Any proto field rename requires a schema migration. Proto types also carry unexported fields that don't serialize cleanly with `pgx`.

**Do this instead:** Define `pkg/models.GpuTelemetry` with `db` struct tags. Collector maps `pb.TelemetryMsg → models.GpuTelemetry` explicitly. The mapping is a clear seam between protocol and persistence.

### Anti-Pattern 3: Gateway Calling MQ Directly

**What people do:** Add a `GET /api/v1/queue/inspect` proxy in the Gateway so the client has a single HTTP entry point for everything.

**Why it's wrong:** Creates a runtime coupling between Gateway and MQ. Gateway becomes unavailable if MQ is down, even for database-only reads. Violates strict service independence.

**Do this instead:** Expose MQ's HTTP control plane directly on its own port/URL. Ops or clients call it directly. The Gateway is a read-only API over Postgres only.

### Anti-Pattern 4: Global Queue Mutex on Message Path

**What people do:** Wrap `q.messages <- msg` in a `sync.Mutex.Lock()` "for safety."

**Why it's wrong:** Channel sends are already goroutine-safe. Adding a mutex serializes all producers through a single lock, eliminating the concurrency benefit and creating a false sense of extra safety.

**Do this instead:** Trust the channel. Use `sync.RWMutex` only for metadata (counters, stats) that the channel doesn't protect.

---

## Scaling Considerations

| Concern | Current Scope (single-replica MQ) | If Scaling Later |
|---------|-----------------------------------|-----------------|
| MQ throughput | One buffered channel; Go can push ~1M msg/s in-process | Would require distributed broker (out of scope) |
| Streamer concurrency | Up to 10 replicas; each is independent gRPC client | Linear scale; MQ is the bottleneck |
| Collector concurrency | N replicas; each holds one Consume stream | Each new replica reduces per-collector load |
| Postgres write throughput | pgxpool batch inserts; tune `max_conns` and batch size | Partitioning on `timestamp` (range partitioning) |
| API Gateway read throughput | Read-only; scale horizontally behind load balancer | Connection pool sizing; read replicas if needed |

---

## Sources

- Go specification on channel memory model guarantees (goroutine-safe send/receive)
- `jackc/pgx/v5` documentation: pgxpool configuration and SendBatch patterns
- gRPC Go server-streaming RPC patterns (google.golang.org/grpc)
- Project spec: `instructions.md` (authoritative — overrides all other sources)
- Project context: `.planning/PROJECT.md`, `CLAUDE.md`

---
*Architecture research for: Elastic GPU Telemetry Pipeline — Custom MQ + Go Microservices*
*Researched: 2026-06-27*
