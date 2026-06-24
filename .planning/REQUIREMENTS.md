# Requirements: vantage

**Defined:** 2026-06-24
**Core Value:** The custom message queue must be durable, scalable, and correct under load — a fast broker that loses or reorders messages fails the assignment.

> Derived from `.planning/PROJECT.md` (assignment + dataset analysis) and `.planning/research/SUMMARY.md`
> (feature tiers). Mandated + table-stakes-correctness features are v1; bonus/differentiators are split
> between v1 (where the assignment rewards them) and v2. Traceability is populated during roadmap creation.

## v1 Requirements

### Message Queue (MQ) — the core deliverable

- [ ] **MQ-01**: Broker persists messages to a durable append-only segment log and survives process restart with zero message loss (rebuilds offset index from segments on startup)
- [ ] **MQ-02**: Broker provides at-least-once delivery — a message is acknowledged to the producer only after it is fsync'd to disk (fsync-before-ack)
- [ ] **MQ-03**: Broker tracks durable consumer-group offsets per (group, partition) that survive broker restart
- [ ] **MQ-04**: Broker supports ≥10 partitions as the unit of consumer parallelism, with records routed by partition key = GPU `uuid` (preserves per-GPU ordering)
- [ ] **MQ-05**: Broker applies disk-based backpressure — when producers outrun consumers the on-disk log grows rather than broker memory (RSS plateaus under a 10-producer / 2-consumer load)
- [ ] **MQ-06**: Broker exposes a gRPC surface for produce, consume, and commit-offset over the existing `mq/proto/mqv1` contract
- [ ] **MQ-07**: A Go client library (`mq/client`) provides producer (batch + partition routing) and consumer (poll + commit) APIs over gRPC
- [ ] **MQ-08**: Broker recovers from torn writes — CRC-framed records, truncating to the last intact record on restart (verified by a crash-recovery test)
- [ ] **MQ-09**: Every service performs graceful shutdown on SIGTERM (flush + fsync + commit offsets + drain in-flight streams)

### Streamer (producer)

- [ ] **STREAM-01**: Streamer reads the DCGM telemetry CSV and loops it continuously to simulate a live stream
- [ ] **STREAM-02**: Streamer re-stamps each datapoint's timestamp at produce time (processing time = telemetry timestamp); the CSV's original timestamps are discarded
- [ ] **STREAM-03**: Streamer produces over the MQ with partition key = GPU `uuid` at a configurable rate
- [ ] **STREAM-04**: Streamer scales dynamically from 1 to 10 instances

### Collector (consumer)

- [ ] **COLL-01**: Collector consumes from the MQ and parses DCGM rows into the telemetry data model
- [ ] **COLL-02**: Collector performs idempotent upsert into PostgreSQL on conflict key `(uuid, metric_name, ts)` (redelivery produces no duplicate rows)
- [ ] **COLL-03**: Collector commits its consumer offset only after the batch is durably persisted (commit-after-persist)
- [ ] **COLL-04**: Collector scales dynamically from 1 to 10 instances
- [ ] **COLL-05**: PostgreSQL schema normalizes into `gpus` (dimension: uuid, host, gpu_id, device, modelName) and `telemetry` (fact: uuid, metric_name, value, ts) with PK `(uuid, metric_name, ts)` and a `(uuid, ts)` index

### API Gateway

- [ ] **API-01**: `GET /api/v1/gpus` returns all GPUs for which telemetry exists
- [ ] **API-02**: `GET /api/v1/gpus/{id}/telemetry` returns all telemetry for one GPU, ordered by time
- [ ] **API-03**: `GET /api/v1/gpus/{id}/telemetry?start_time=…&end_time=…` filters telemetry to an inclusive time window
- [ ] **API-04**: The OpenAPI (Swagger) spec is auto-generated via a Makefile target

### Deployment (Docker / Kubernetes / Helm)

- [ ] **DEPLOY-01**: Each service has a Dockerfile producing a runnable image
- [ ] **DEPLOY-02**: A Helm umbrella chart deploys all services + PostgreSQL with the broker as a StatefulSet (RWO PVC) and stateless services as Deployments
- [ ] **DEPLOY-03**: The full stack deploys to a local kind cluster via Makefile targets
- [ ] **DEPLOY-04**: Streamers and collectors can be dynamically scaled up/down (demonstrated to 10 each) with no message loss

### Observability & Performance

- [ ] **OBS-01**: Each component exposes Prometheus-style metrics (throughput, latency, broker queue depth, consumer lag, CPU/mem)
- [ ] **OBS-02**: A performance harness characterizes the system across producer/consumer ratios (10:2, 2:10, 5:5), producing a comparison table + written analysis (throughput, end-to-end latency p50/p95/p99, queue depth, consumer lag)

### Testing & Documentation

- [ ] **TEST-01**: Unit tests cover business logic with coverage reported via Makefile (gate: 90% line, 100% branch on logic)
- [ ] **TEST-02**: An end-to-end integration test proves no-loss + correct per-GPU ordering across a broker restart
- [ ] **DOC-01**: README documents architecture & design, build/packaging, install workflow, and a sample user workflow
- [ ] **DOC-02**: AI-usage doc records the exact prompts used to bootstrap repo/code/tests/build, and where prompts fell short and needed manual intervention

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Observability

- **OBS-03**: Grafana dashboard over the Prometheus metrics

### Message Queue

- **MQ-10**: Log retention / compaction policy (safe drop below the minimum committed offset)
- **MQ-11**: Message compression on the wire

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Off-the-shelf brokers (Kafka / RabbitMQ / ZeroMQ) | Assignment mandates a custom MQ — this is the core grading criterion |
| More than 10 streamer / 10 collector instances | Exercise scale ceiling |
| PostgreSQL (or any external store) as the MQ persistence layer | Keeps "custom MQ" honest; Postgres is reserved for collector data (ADR-0001/0002) |
| Replaying the CSV's original timestamps | We re-stamp at stream time (processing time = telemetry timestamp) |
| Exactly-once delivery | At-least-once + idempotent collector upsert is the chosen correctness model |
| Authentication / authorization on the API | Out of scope for the exercise |

## Traceability

Which phases cover which requirements. **Populated during roadmap creation.**

> **GATE 0 (ADR-0005):** Not a requirement — a schema-freezing decision gate. Must read *Accepted*
> before Phase 2 (partition key), Phase 5 (collector schema), and Phase 6 (`{id}` routing).

| Requirement | Phase | Status |
|-------------|-------|--------|
| MQ-01 | Phase 1 | Pending |
| MQ-08 | Phase 1 | Pending |
| MQ-05 | Phase 1 | Pending |
| TEST-01 | Phase 1 (anchored; cross-cutting TDD across all logic phases) | Pending |
| MQ-02 | Phase 2 | Pending |
| MQ-03 | Phase 2 | Pending |
| MQ-04 | Phase 2 | Pending |
| MQ-06 | Phase 2 | Pending |
| MQ-07 | Phase 3 | Pending |
| MQ-09 | Phase 3 (anchored; verified per-service in Phase 7) | Pending |
| STREAM-01 | Phase 4 | Pending |
| STREAM-02 | Phase 4 | Pending |
| STREAM-03 | Phase 4 | Pending |
| STREAM-04 | Phase 4 | Pending |
| COLL-01 | Phase 5 | Pending |
| COLL-02 | Phase 5 | Pending |
| COLL-03 | Phase 5 | Pending |
| COLL-04 | Phase 5 | Pending |
| COLL-05 | Phase 5 | Pending |
| API-01 | Phase 6 | Pending |
| API-02 | Phase 6 | Pending |
| API-03 | Phase 6 | Pending |
| API-04 | Phase 6 | Pending |
| DEPLOY-01 | Phase 7 | Pending |
| DEPLOY-02 | Phase 7 | Pending |
| DEPLOY-03 | Phase 7 | Pending |
| DEPLOY-04 | Phase 7 | Pending |
| OBS-01 | Phase 8 | Pending |
| OBS-02 | Phase 8 | Pending |
| TEST-02 | Phase 9 | Pending |
| DOC-01 | Phase 9 | Pending |
| DOC-02 | Phase 9 | Pending |

**Coverage:**
- v1 requirements: 28 total
- Mapped to phases: 28 (100%)
- Unmapped: 0

---
*Requirements defined: 2026-06-24*
*Last updated: 2026-06-24 after roadmap creation (28/28 mapped across 9 phases)*
