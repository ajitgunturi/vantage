<!-- refreshed: 2026-06-24 -->
# Architecture

**Analysis Date:** 2026-06-24

## System Overview

```text
┌────────────────────────────────────────────────────────────────────────────┐
│                    ELASTIC GPU TELEMETRY PIPELINE                          │
├────────────────────┬─────────────────────────┬──────────────────┬──────────┤
│   STREAMER (×N)    │   CUSTOM MQ BROKER      │   COLLECTOR (×M) │  API     │
│  `streamer/cmd`    │   `mq/cmd/mqbroker`     │   `collector/cmd`│ GATEWAY  │
│  - CSV loop        │   - Partitioned topics  │   - gRPC consume │ REST API │
│  - Timestamp       │   - Durable WAL/log     │   - Parse        │ OpenAPI  │
│  - gRPC produce    │   - Offset mgmt         │   - Idempotent   │ Schema   │
│                    │   - At-least-once       │     upsert       │`apigatew-│
└────────┬───────────┴──────────────┬──────────┴──────────┬────────┴──────────┘
         │                          │                     │
         │ Produce                  │ Consume             │ Query
         │ (streaming)              │ (server push)       │ (HTTP)
         │                          │                     │
         ▼                          ▼                     ▼
┌────────────────────────────────────────────────────────────────────────────┐
│                     CUSTOM MQ SERVICE (gRPC Streaming)                      │
│                        `mq/` (broker + client lib)                          │
│  Durable append-only segment log on PVC; ordered per partition; fsync;     │
│  consumer-group offset tracking; survives restart; at-least-once delivery  │
│                                                                              │
│  Key Contracts:                                                             │
│  - Produce(stream ProduceRequest) → (stream ProduceResponse) [offset/part] │
│  - Consume(group, topic) → (stream ConsumeResponse) [push from broker]     │
│  - Commit(group, partition, offset) [ack after durably persisted]          │
│  - CreateTopic(spec, partitions≥10) [enable consumer parallelism]          │
│  - Health() [liveness probe]                                               │
└────────────────────────────────────────────────────────────────────────────┘
         │
         ▼
┌────────────────────────────────────────────────────────────────────────────┐
│                      PERSISTENT STORAGE LAYER                              │
├──────────────────────────────────────┬──────────────────────────────────────┤
│   MQ PARTITION LOG (PVC)             │   POSTGRESQL (pgx/native)            │
│   - Segment files per partition      │   - `gpus(uuid PK, ...)`             │
│   - Offset index                     │   - `telemetry(uuid FK, metric,      │
│   - Consumer offsets per group       │     ts, value, ...)`                 │
│   - Survives broker restart          │   - Idempotent upsert key:           │
│                                      │     (uuid, metric_name, ts)          │
└──────────────────────────────────────┴──────────────────────────────────────┘
```

## Component Responsibilities

| Component | Responsibility | File(s) |
|-----------|----------------|---------|
| **Streamer** | Read DCGM CSV, loop continuously, re-stamp timestamps, produce over gRPC MQ with key=uuid for ordering | `streamer/cmd/streamer`, `streamer/internal/source`, `streamer/internal/stream` |
| **Collector** | Consume from MQ (gRPC server-push), parse telemetry, idempotent upsert to Postgres on (uuid, metric_name, ts) | `collector/cmd/collector`, `collector/internal/consume`, `collector/internal/store` |
| **MQ Broker** | Manage durable partitioned append-only log on PVC; track consumer-group offsets; deliver at-least-once via gRPC streaming | `mq/cmd/mqbroker`, `mq/broker/` (pending), `mq/gen/mqv1` (stubs) |
| **API Gateway** | Serve `GET /api/v1/gpus`, `GET /api/v1/gpus/{uuid}/telemetry?start_time=...&end_time=...` against Postgres; auto-gen OpenAPI spec | `apigateway/cmd/apigateway`, `apigateway/internal/api`, `apigateway/internal/store` |

## Pattern Overview

**Overall:** Distributed telemetry pipeline with custom durable message queue.

**Key Characteristics:**
- **Durable, partitioned, ordered queue** — append-only segment log on PVC with at-least-once semantics and consumer-group offset tracking (ADR-0001, ADR-0004).
- **Idempotent collectors** — upsert pattern on natural key `(uuid, metric_name, ts)` tolerates at-least-once redelivery (ADR-0002).
- **Canonical GPU identity = UUID** — globally unique, stable across host reboots; used as partition key to order all metrics for one GPU (ADR-0005, *Proposed* — confirm before freezing schema).
- **Scalable consumer/producer** — partitions enable collector scale-out (≥10 partitions for ≥10 collectors); stress patterns tested (3 ratios: producers>consumers, producers<consumers, balanced).
- **gRPC streaming transport** — HTTP/2 multiplexing, backpressure, typed client lib (ADR-0004).
- **TDD + coverage gates** — 90% line coverage, 100% branch coverage on business logic (internal/ packages).

## Layers

**Application Layer (Producers/Consumers):**
- Purpose: Streamer reads CSV and produces telemetry; Collector consumes and persists; API Gateway queries persisted data.
- Location: `streamer/`, `collector/`, `apigateway/` modules.
- Contains: `cmd/` (thin main wiring) + `internal/` (business logic, TDD-covered).
- Depends on: MQ client lib (`mq/client`), PostgreSQL client (`pgx`), gRPC stubs (`mq/gen/mqv1`).
- Used by: Deployed as independent k8s services.

**Message Queue Service Layer:**
- Purpose: Durable, ordered, partitioned message broker with at-least-once delivery and consumer-group offset tracking.
- Location: `mq/` module: `cmd/mqbroker/` (service entry), `broker/` (segment log + offset index), `client/` (producer/consumer lib).
- Contains: gRPC service implementation, segment-log WAL, consumer-group state, partition assignment.
- Depends on: gRPC server/client, protobuf (`mq/gen/mqv1`), filesystem (PVC mount).
- Used by: Streamer (Produce), Collector (Consume, Commit), API (optional liveness queries).

**Persistence Layer:**
- Purpose: Durable storage for queue state (segments/offsets on PVC) and telemetry (Postgres).
- Location: PVC mounts (broker logs); `postgresql` (managed via Helm subchart in k8s-infra).
- Contains: Segment files (`<baseOffset>.seg`, `<baseOffset>.segidx`), consumer offsets; `gpus` + `telemetry` tables.
- Used by: Broker (on restart, rebuilds in-memory indexes from segments), Collector (writes), API (reads).

**Infrastructure Layer:**
- Purpose: Kubernetes deployment, networking, volume provisioning, Helm templating.
- Location: `k8s-infra/helm/telemetry/` (umbrella chart), `k8s-infra/kind/` (local dev cluster config).
- Contains: Helm sub-charts for broker, streamer, collector, apigateway; Postgres subchart or Bitnami dependency; PVC specs.
- Used by: `make kind-up`, `helm upgrade --install`.

## Data Flow

### Primary Request Path (Telemetry Stream)

1. **CSV Read & Stream** (`streamer/cmd/streamer` + `streamer/internal/source`)
   - Load DCGM CSV (`dcgm_metrics_*.csv`) once per startup.
   - Loop continuously: for each row, extract uuid, metric_name, value.
   - **Re-stamp ts = now** (ignore original CSV timestamp; we measure streaming latency).
   - Encode as MQ Message with key=uuid (partition by GPU for ordering).

2. **Produce** (`streamer/internal/stream` → `mq/client` → `mq/cmd/mqbroker`)
   - Open gRPC streaming Produce RPC to broker (client library in `mq/client/`).
   - Stream ProduceRequest for each message.
   - Broker assigns monotonic per-partition offset; fsync to segment log; reply with ProduceResponse (partition, offset).
   - Streamer ack received (at-least-once from broker side).

3. **Consume & Parse** (`collector/cmd/collector` + `collector/internal/consume`)
   - Join broker's Consume RPC as a consumer group member.
   - Broker pushes ConsumeResponse stream (server-push, partition-assigned).
   - Parse message.value → telemetry struct (uuid, metric_name, value, ts).
   - After durable persist, commit offset to broker.

4. **Persist** (`collector/internal/store` → PostgreSQL)
   - Idempotent upsert to `telemetry(uuid, metric_name, ts, value, ...)` on key `(uuid, metric_name, ts)`.
   - Also upsert GPU attributes to `gpus(uuid, gpu_id, hostname, device, model_name)`.
   - Commit returns to broker (Commit RPC with offset).

5. **Query & Respond** (`apigateway/cmd/apigateway` + `apigateway/internal/api`)
   - Listen on `:8080` (REST, HTTP).
   - `GET /api/v1/gpus` → SELECT DISTINCT uuid FROM gpus; return with hostname, gpu_id, model_name.
   - `GET /api/v1/gpus/{uuid}/telemetry?start_time=...&end_time=...` → SELECT * FROM telemetry WHERE uuid=? AND ts BETWEEN ? AND ? ORDER BY ts.
   - Auto-generated OpenAPI (Swagger) spec (`apigateway/docs/swagger.json` via `swag` or `oapi-codegen`).

### State Management

- **Broker partition offsets:** Persisted per-partition in segment directory; rebuilt at startup.
- **Consumer group offsets:** Persisted separately (proto-defined CommitRequest); durable across restart.
- **Streamer state:** Stateless; only loops CSV. No checkpoints needed (CSV is fully read each cycle).
- **Collector state:** Stateless; offset tracking is the broker's job. Idempotency via DB upsert.
- **API state:** Read-only; queries Postgres.

## Key Abstractions

**Message & Partition:**
- Purpose: Represent a queued unit and its position.
- Pattern: `Message{key, value, timestamp_ns}` (proto in `mq/proto/mqv1/mq.proto` lines 8–12). Key routes to partition via hash; monotonic offset assigned per partition.
- Examples: `streamer/internal/source` encodes a telemetry row into a Message.

**Topic & Consumer Group:**
- Purpose: Logical namespace for messages; consumer-group enables parallel consumption and offset tracking.
- Pattern: Topic name (e.g., "telemetry"); group name (e.g., "collector-group"); consumer_id (e.g., "collector-0"). Offset committed per group/partition.
- Examples: Broker creates "telemetry" topic with ≥10 partitions; collector joins as "collector-group" member.

**Idempotent Upsert:**
- Purpose: Ensure at-least-once redelivery doesn't create duplicates.
- Pattern: Natural key `(uuid, metric_name, ts)` + INSERT ... ON CONFLICT DO UPDATE in `collector/internal/store`.
- Examples: Collector receives same message twice → both upserts succeed, second is a no-op.

**Segment Log:**
- Purpose: Durable, append-only storage for queue messages.
- Pattern: Sequence of files `<baseOffset>.seg` (messages) + `<baseOffset>.segidx` (offset index); fsync on write.
- Examples: Broker writes ProduceRequest to current segment, fsync, then replies; on restart reads segments and rebuilds partition offset ranges.

## Entry Points

**Streamer Service:**
- Location: `streamer/cmd/streamer` (pending – stub)
- Triggers: `make build`; `streamer` Pod started in k8s.
- Responsibilities: Load CSV, loop, produce to MQ, handle gRPC backpressure.
- Env vars: `MQ_BROKER_ADDR`, `MQ_TOPIC` (TBD at impl).

**Collector Service:**
- Location: `collector/cmd/collector` (pending – stub)
- Triggers: `make build`; `collector` Pod started in k8s.
- Responsibilities: Consume from MQ, parse, upsert to Postgres, commit offsets.
- Env vars: `MQ_BROKER_ADDR`, `MQ_TOPIC`, `MQ_GROUP`, `DATABASE_URL` (TBD at impl).

**MQ Broker Service:**
- Location: `mq/cmd/mqbroker` (pending – stub)
- Triggers: `make build`; `mqbroker` Pod started in k8s.
- Responsibilities: Listen on gRPC, manage partitions, fsync to log on PVC, serve at-least-once semantics.
- Env vars: `BROKER_LISTEN_ADDR`, `LOG_DIR` (PVC mount), `PARTITION_COUNT` (TBD at impl).

**API Gateway Service:**
- Location: `apigateway/cmd/apigateway` (pending – stub)
- Triggers: `make build`; `apigateway` Pod started in k8s.
- Responsibilities: Serve REST endpoints, query Postgres, gen/serve OpenAPI spec.
- Env vars: `API_LISTEN_ADDR`, `DATABASE_URL` (TBD at impl).

## Architectural Constraints

- **Threading:** Go's goroutines; gRPC server handles concurrent requests. MQ broker must use mutexes or channels to serialize partition writes (fsync is not concurrent-safe).
- **Global state:** Broker's in-memory partition offset ranges and consumer-group state; initialized at startup from segment files. Streamer/Collector/API stateless.
- **Circular imports:** None expected in multi-module layout; `mq` is imported by streamer/collector/apigateway, not vice versa.
- **Partitioning strategy:** All messages for a UUID land on the same partition (key=uuid); enables ordering per GPU. ≥10 partitions for ≥10 collectors.
- **At-least-once semantics:** Broker acks after fsync; no explicit deduplication. Collectors idempotent via upsert. **NOT exactly-once yet** — Postgres upsert + at-least-once = effective exactly-once at rest, but in-flight redelivery possible.
- **Consumer scaling ceiling:** 10 collectors (exercise limit); partition count ≥10 (one per consumer worst-case).

## Anti-Patterns

### **Out-of-order messages per GPU**

**What happens:** If a UUID's messages are consumed out of order (e.g., network jitter, partition rebalance), the telemetry timeline is incorrect.

**Why it's wrong:** The API's time-window queries assume monotonic timestamps. Out-of-order writes to the DB don't corrupt it (upsert on ts), but the data *presented* is misleading if a metric's value jumps back in time.

**Do this instead:** Always hash key=uuid to assign a partition; all messages for one GPU go to one partition in order. Broker maintains per-partition offset monotonicity. See `mq/proto/mqv1/mq.proto` line 8 (key routing) and partition assignment logic (TBD in `mq/broker/`).

### **Collectors not idempotent**

**What happens:** If a Collector dies mid-upsert (e.g., after writing but before committing offset), the broker retransmits the batch. If the Collector then inserts instead of upserting, duplicates appear in `telemetry`.

**Why it's wrong:** At-least-once delivery guarantees redelivery on failure. Non-idempotent writes break that contract; the data is corrupt.

**Do this instead:** Always upsert on `(uuid, metric_name, ts)`. See `collector/internal/store/` for the INSERT ... ON CONFLICT pattern (TBD; test in `collector/internal/store/store_test.go`).

### **Unbounded queue growth under backpressure**

**What happens:** If collectors are slow (e.g., Postgres latency) and streamers produce faster, the MQ partition log grows without bound. Eventually, the PVC fills, and the broker crashes.

**Why it's wrong:** No flow control; the system degrades catastrophically.

**Do this instead:** Broker's Produce RPC is bidirectional (streaming); use gRPC backpressure (client blocks on send if server can't keep up). See `mq/proto/mqv1/mq.proto` line 59 (Produce RPC). Perf harness measures queue depth under (producers>consumers) regime to validate backpressure.

## Error Handling

**Strategy:** Graceful degradation; explicit logging; idempotent retries where safe.

**Patterns:**
- **Streamer CSV load failure:** Log error, exit (no retry — CSV is static). K8s restarts the Pod.
- **Streamer → MQ network error:** Retry with exponential backoff (TBD at impl; gRPC built-in retry policies).
- **Collector → Postgres write failure:** Log, continue (next message may succeed). Commit offset only after successful upsert (avoid offset drift).
- **Broker fsync failure:** Log fatally, exit. K8s restarts; PVC persists data; recovery from segments.
- **API query timeout:** Return HTTP 500 to client; log slow query time (SLA/monitoring).

## Cross-Cutting Concerns

**Logging:** `slog` package (idiomatic Go); structured logs with context keys (uuid, partition, offset, latency).

**Validation:** 
- Streamer: validate CSV row (uuid non-empty, metric_name in allowed set, value numeric).
- Collector: validate message format (protobuf parse + field presence).
- API: validate time-range params (start < end, reasonable bounds).

**Authentication:** Not implemented (exercise scope); all services internal to cluster. Future: mTLS or JWT if exposed externally.

**Metrics:** Prometheus client (per CLAUDE.md); expose in each service:
- Streamer: `streamer_messages_produced_total`, `streamer_produce_latency_seconds`.
- Broker: `broker_messages_persisted_total`, `broker_fsync_latency_seconds`, `broker_partition_offset_max`.
- Collector: `collector_messages_consumed_total`, `collector_upsert_latency_seconds`, `collector_offset_committed_total`.
- API: `api_requests_total`, `api_query_latency_seconds`.

---

*Architecture analysis: 2026-06-24*
