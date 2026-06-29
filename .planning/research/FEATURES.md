# Feature Research

**Domain:** Elastic GPU Telemetry Pipeline — Custom In-Memory Message Queue + Ingestion Services
**Researched:** 2026-06-27
**Confidence:** HIGH (source: authoritative project brief in instructions.md + PROJECT.md; no inference needed)

---

> **Scope contract:** `instructions.md` is the authoritative brief. Every table-stakes feature below maps
> directly to an explicit requirement there. Anything not in the brief is marked differentiator or
> anti-feature. No scope has been invented.

---

## Feature Landscape

### Table Stakes (Required by Brief)

Features that must exist for the system to meet the assignment. Missing any of these = system is incomplete.

#### MQ Microservice — Data Plane (gRPC)

| Feature | Why Required | Complexity | Implementation Notes |
|---------|--------------|------------|----------------------|
| `Produce` unary RPC | Streamers push telemetry into the MQ via gRPC unary call | LOW | Proto v3; generated stub; single message per call |
| `Consume` server-streaming RPC | Collectors receive a live stream of messages; long-lived connection | MEDIUM | Server pushes messages as they arrive; stream stays open |
| Unique delivery to concurrent consumers | Multiple Collectors must each get distinct messages (work-queue, not fan-out) | HIGH | Round-robin or slot-based dispatch across active consumer streams; requires careful mutex or channel-per-consumer design |
| Thread-safe in-memory ring buffer | Internal storage must be safe for concurrent producers and consumers | HIGH | `sync.RWMutex` or channel-based; fixed capacity; native Go only — no external libs |
| At-most-once delivery semantics | In-memory single-replica: message delivered once; loss on crash acceptable | LOW | Drop-on-full is acceptable; no ACK/NAK protocol required |

#### MQ Microservice — Control Plane (HTTP)

| Feature | Why Required | Complexity | Implementation Notes |
|---------|--------------|------------|----------------------|
| `GET /api/v1/queue/inspect` | Returns JSON queue status for `curl` debugging and observability | LOW | Fields: capacity, current depth, connected consumer count, messages produced/consumed counters |

#### Streamer Microservice

| Feature | Why Required | Complexity | Implementation Notes |
|---------|--------------|------------|----------------------|
| CSV file loop (indefinite) | Simulates a continuous live metrics stream; file is finite so must re-read | MEDIUM | Seek to start after EOF; no external state tracking needed |
| Timestamp restamping | Each record must carry current system time, not the original CSV timestamp | LOW | Overwrite the `timestamp` column value with `time.Now().UTC()` before publishing |
| gRPC Produce client | Sends records to MQ using the generated stub | LOW | Connection with retry on startup; one client per Streamer instance |
| Up to 10 concurrent instances | 10 independent Streamer pods all producing to the same MQ | MEDIUM | Each instance is stateless; Helm `replicaCount` controls scale; no instance coordination needed |
| DCGM CSV column parsing | Must handle the 12-column DCGM exporter format exactly | MEDIUM | Columns: `timestamp, metric_name, gpu_id, device, uuid, modelName, Hostname, container, pod, namespace, value, labels_raw`; skip header row |

#### Collector Microservice

| Feature | Why Required | Complexity | Implementation Notes |
|---------|--------------|------------|----------------------|
| Long-lived gRPC Consume stream | Maintains a persistent connection to MQ to receive messages reactively | MEDIUM | Reconnect loop on stream error; context-aware cancellation |
| Batch insert to PostgreSQL via pgxpool | High-throughput persistence; pool handles concurrency across Collector instances | MEDIUM | `pgxpool` from `jackc/pgx/v5`; batch with `pgx.Batch` or `COPY`; tune pool size |
| PostgreSQL schema: time-series telemetry | Relational schema for GPU metrics with correct types | LOW | Columns include `gpu_id TEXT`, `timestamp TIMESTAMPTZ`, `metric_name TEXT`, `value FLOAT8`, and DCGM metadata columns |
| Composite index `(gpu_id, timestamp DESC)` | Optimizes the time-window and per-GPU query patterns used by API Gateway | LOW | Single `CREATE INDEX` DDL statement; required by spec |

#### API Gateway Microservice

| Feature | Why Required | Complexity | Implementation Notes |
|---------|--------------|------------|----------------------|
| `GET /api/v1/gpus` | Lists all distinct GPU IDs seen in the database | LOW | `SELECT DISTINCT gpu_id FROM telemetry` or equivalent; JSON array response |
| `GET /api/v1/gpus/{id}/telemetry` | Returns telemetry records for a specific GPU | LOW | Filter by `gpu_id`; ordered by `timestamp DESC`; JSON array |
| `GET /api/v1/gpus/{id}/telemetry?start_time=&end_time=` | Time-window filtering on telemetry | MEDIUM | Parse RFC3339 query params; use composite index; same handler as above, params are optional |
| OpenAPI spec via `swag` annotations | Spec must be completely auto-generated from code annotations — not hand-written | MEDIUM | `swag init` generates `docs/` from `// @Summary`, `// @Param`, `// @Success` comments; serve via Swagger UI or raw JSON |

#### Infrastructure and Quality

| Feature | Why Required | Complexity | Implementation Notes |
|---------|--------------|------------|----------------------|
| Multi-stage Dockerfile per service | Minimal production images; each service builds independently | LOW | Builder stage: `golang:1.22-alpine`; runner stage: `alpine`; one Dockerfile per `cmd/` entrypoint |
| Helm sub-chart per service | Independent deployments; Helm manages pod spec, ConfigMaps, Services | MEDIUM | 5 sub-charts: `mq`, `streamer`, `collector`, `gateway`, `postgres`; parent chart with `values.yaml` |
| Makefile targets: `proto`, `build`, `test`, `coverage`, `swagger` | Reproducible local workflow; CI-ready | LOW | `proto`: `protoc` invocation; `coverage`: `go test -coverprofile` + threshold check at 90% |
| Unit + integration tests; `go test -race` for MQ | Race detector catches concurrent map/slice access bugs in the MQ | HIGH | MQ concurrency tests are the hardest; test concurrent Produce + Consume goroutines under `-race` |
| ≥90% line coverage gate | Enforced by Makefile `coverage` target; blocks ship if under threshold | MEDIUM | `go tool cover -func coverage.out | grep total` then `awk` threshold check |

---

### Differentiators (Valuable but Not in Brief)

These add quality or robustness without being explicitly required. Build them only if table stakes are solid.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Ring buffer drop policy (drop-oldest or drop-newest) | Makes overflow behavior explicit and predictable instead of blocking forever | LOW | Add a `full_policy` config: `drop_oldest` (overwrite tail) vs `drop_newest` (reject incoming); log drops |
| Malformed CSV line handling | Prevents a single bad line from crashing the Streamer loop | LOW | Log and skip lines that fail `strconv.ParseFloat` or have wrong column count; counter for skipped lines |
| Configurable batch size + flush interval for Collector | Lets operators tune throughput vs latency tradeoff without recompile | LOW | Env vars `BATCH_SIZE` (default 100) and `FLUSH_INTERVAL_MS` (default 500); already a common pattern with pgxpool |
| Kubernetes liveness + readiness probes | Standard K8s health integration; Helm already wires them | LOW | MQ: TCP probe on gRPC port; Gateway: HTTP probe on `/healthz`; Collector/Streamer: exec probe |
| Graceful shutdown with drain | On SIGTERM, finish in-flight inserts before exiting; avoids partial batches | MEDIUM | Listen for `os.Signal`; cancel context; wait for active writes to finish with timeout |
| gRPC connection retry with exponential backoff | Streamer/Collector reconnect automatically when MQ restarts | LOW | `google.golang.org/grpc/backoff` config; already recommended by gRPC Go docs |
| `/inspect` response includes per-consumer slot state | Shows which consumers are active; aids debugging under load | LOW | Add `consumers: [{id, messages_delivered}]` array to existing inspect JSON |

---

### Anti-Features (Explicitly Out of Scope)

Do not build these. They are either forbidden by the brief or would introduce accidental complexity that contradicts the assignment's goals.

| Feature | Why It Seems Appealing | Why It's Wrong Here | What to Do Instead |
|---------|------------------------|--------------------|--------------------|
| Disk persistence / WAL for MQ | "Real" message queues persist to disk for durability | Spec explicitly says in-memory single-replica only; adds Badger/BoltDB dependency and recovery complexity | Accept at-most-once; note the trade-off in `/inspect` response |
| Third-party brokers (Kafka, NATS, RabbitMQ, Redis) | Would give all MQ semantics for free | Explicitly forbidden by brief: "Do NOT use any existing third-party message brokers" | Implement the custom ring buffer from scratch |
| MQ clustering / multi-replica | High availability for the queue | Out of scope; complicates unique delivery semantics enormously | Single-replica Kubernetes Deployment; document the SPOF |
| At-least-once delivery (ACK/NAK) | Prevents data loss on consumer crash | Requires ACK tracking, redelivery queue, dead-letter handling — far beyond the brief | Accept at-most-once; PostgreSQL `UNIQUE` constraint on insert can deduplicate if needed later |
| Consumer groups / topic routing | Multiple logical streams for different metric types | Not in the data model; adds broker-like complexity | All messages go into one queue; consumers get unique slices |
| Message replay / seek | Re-process historical messages from the MQ | In-memory ring buffer has no history once consumed; replay comes from PostgreSQL via API Gateway | Query PostgreSQL for historical data |
| Authentication / authorization | Secure the gRPC and HTTP endpoints | Explicitly out of scope in PROJECT.md | Note the gap; keep in mind for a future hardening phase |
| Hand-written OpenAPI YAML | Spec might seem more precise if written manually | Brief mandates swag auto-generation from annotations; mixing the two creates drift | All endpoint docs live as `// @` comments in `cmd/gateway/` |
| Pagination on API Gateway endpoints | Correct for large result sets | Not in the brief; adds `limit`/`offset` params and complexity to queries | Return all matching rows; note the limitation; add as a differentiator if time allows |
| Multi-region / cross-cluster deployment | Production resilience | Out of scope; single Kubernetes cluster per PROJECT.md | Helm makes it portable; document the limitation |

---

## Feature Dependencies

```
Proto contract (api/proto/mq.proto)
    └──required by──> MQ gRPC server (Produce + Consume RPCs)
    └──required by──> Streamer gRPC client (Produce stub)
    └──required by──> Collector gRPC client (Consume stub)

In-memory ring buffer (thread-safe)
    └──required by──> Unique delivery to concurrent consumers
    └──required by──> /api/v1/queue/inspect (reads buffer state)

PostgreSQL schema + composite index
    └──required by──> Collector batch inserts
    └──required by──> API Gateway all three endpoints

Collector (data in DB)
    └──required by──> API Gateway returning meaningful responses

MQ running + reachable
    └──required by──> Streamer (Produce calls succeed)
    └──required by──> Collector (Consume stream connects)

swag annotations on Gateway handlers
    └──required by──> OpenAPI spec generation (swag init)

Dockerfiles per service
    └──required by──> Helm chart image references
    └──required by──> Kubernetes deployment

go test -race (MQ concurrency)
    └──depends on──> Thread-safe ring buffer implementation being complete
```

### Dependency Notes

- **Proto contract before everything:** The `.proto` file is the interface contract between MQ, Streamer, and Collector. `protoc` must run (`make proto`) before any of those three services compile.
- **DB schema before Collector:** pgxpool inserts require the table to exist; schema migration (DDL in `pkg/db/migrations/` or inline `CREATE TABLE IF NOT EXISTS`) must run at Collector startup.
- **Collector inserting before Gateway is meaningful:** The Gateway queries PostgreSQL; if nothing is in the DB yet, all responses are empty. Not a blocker for testing the Gateway itself, but integration tests need Collector to have run.
- **Unique delivery requires ring buffer complete:** The consumer-slot dispatch logic lives inside the ring buffer code; it cannot be tested independently.

---

## MVP Definition

This is a greenfield assignment with a fixed scope. The brief defines the MVP precisely. The phase ordering should reflect the dependency graph above.

### Phase 1 — Foundation (must ship first)

- [ ] Proto contract defined and `make proto` generating Go stubs — blocks everything
- [ ] In-memory ring buffer with thread-safe Produce/Consume — the core of the assignment
- [ ] MQ gRPC server: `Produce` unary + `Consume` server-streaming with unique delivery
- [ ] `GET /api/v1/queue/inspect` HTTP endpoint
- [ ] PostgreSQL schema + composite index DDL

### Phase 2 — Pipeline (end-to-end data flow)

- [ ] Streamer: CSV loop + timestamp restamping + gRPC Produce client
- [ ] Collector: gRPC Consume stream + pgxpool batch inserts
- [ ] Verify data flows: CSV → Streamer → MQ → Collector → PostgreSQL

### Phase 3 — API Gateway

- [ ] `GET /api/v1/gpus` — list GPUs
- [ ] `GET /api/v1/gpus/{id}/telemetry` — telemetry by GPU
- [ ] `GET /api/v1/gpus/{id}/telemetry?start_time=&end_time=` — time-window filter
- [ ] `swag` annotations on all handlers; `make swagger` generates valid OpenAPI JSON

### Phase 4 — DevOps + Quality Gate

- [ ] Multi-stage Dockerfiles for all 4 services
- [ ] Helm sub-charts (mq, streamer, collector, gateway, postgres)
- [ ] Makefile: `proto`, `build`, `test`, `coverage` (≥90% gate), `swagger`
- [ ] `go test -race` suite passing on MQ concurrency tests
- [ ] 10-instance Streamer scale test

---

## Feature Prioritization Matrix

| Feature | Assignment Value | Implementation Cost | Priority |
|---------|-----------------|---------------------|----------|
| Proto contract + code generation | HIGH | LOW | P1 |
| Thread-safe ring buffer | HIGH | HIGH | P1 |
| gRPC Produce RPC | HIGH | LOW | P1 |
| gRPC Consume RPC (server-streaming) | HIGH | MEDIUM | P1 |
| Unique delivery to concurrent consumers | HIGH | HIGH | P1 |
| `GET /api/v1/queue/inspect` | HIGH | LOW | P1 |
| PostgreSQL schema + composite index | HIGH | LOW | P1 |
| CSV loop + timestamp restamping | HIGH | MEDIUM | P1 |
| 10 concurrent Streamer instances | HIGH | LOW | P1 |
| Collector long-lived stream + batch insert | HIGH | MEDIUM | P1 |
| `GET /api/v1/gpus` | HIGH | LOW | P1 |
| `GET /api/v1/gpus/{id}/telemetry` | HIGH | LOW | P1 |
| Time-window query params | HIGH | LOW | P1 |
| `swag` OpenAPI generation | HIGH | MEDIUM | P1 |
| Multi-stage Dockerfiles | HIGH | LOW | P1 |
| Helm sub-charts | HIGH | MEDIUM | P1 |
| Makefile targets + coverage gate | HIGH | LOW | P1 |
| `go test -race` MQ concurrency tests | HIGH | MEDIUM | P1 |
| Malformed CSV line handling | MEDIUM | LOW | P2 |
| Ring buffer drop policy config | MEDIUM | LOW | P2 |
| gRPC retry / exponential backoff | MEDIUM | LOW | P2 |
| Graceful shutdown with drain | MEDIUM | MEDIUM | P2 |
| K8s liveness + readiness probes | MEDIUM | LOW | P2 |
| Pagination on Gateway endpoints | LOW | MEDIUM | P3 |
| Per-consumer slot state in /inspect | LOW | LOW | P3 |

**Priority key:** P1 = required by brief, P2 = differentiator (add if P1 complete), P3 = future

---

## Sources

- `instructions.md` — authoritative project brief (primary source; HIGH confidence)
- `.planning/PROJECT.md` — validated project context and explicit out-of-scope list (HIGH confidence)
- DCGM exporter CSV column schema inferred from `pkg/models` references in PROJECT.md context

---
*Feature research for: Elastic GPU Telemetry Pipeline (vantage)*
*Researched: 2026-06-27*
