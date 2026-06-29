# Requirements: vantage — Elastic GPU Telemetry Pipeline

**Defined:** 2026-06-27
**Core Value:** End-to-end telemetry flow (CSV → Streamer → custom MQ → Collector → PostgreSQL → API Gateway) works reliably under concurrency, with no message loss or duplication.

> Scope is fixed by `instructions.md`. Every v1 requirement below maps to an explicit line in the
> assignment brief — there is no discretionary v1/v2 scoping. v2 = research-surfaced differentiators;
> Out of Scope = anti-features the brief explicitly excludes.

## v1 Requirements

### Message Queue (MQ)

- [x] **MQ-01**: `Produce` unary gRPC RPC accepts a telemetry payload and enqueues it
- [x] **MQ-02**: `Consume` gRPC RPC delivers enqueued messages to a collector _(Phase 1: server-streaming; **revised to bidirectional streaming in Phase 01.1** — see MQ-09/MQ-10, ADR-001)_
- [x] **MQ-03**: Each message is delivered to exactly one consumer in steady state — no duplication across concurrent collectors _(at-least-once redelivery on disconnect may duplicate; absorbed by the idempotent Collector — MQ-09, ADR-001)_
- [x] **MQ-04**: In-memory thread-safe queue built from native Go concurrency only (no third-party broker, no disk)
- [x] **MQ-05**: Bounded buffer with a defined drop policy when full (no unbounded memory growth)
- [x] **MQ-06**: HTTP `GET /api/v1/queue/inspect` returns a JSON summary of queue status
- [x] **MQ-07**: Consumer disconnect is handled gracefully with no goroutine leak
- [x] **MQ-08**: MQ storage sits behind a `Store` interface; the in-memory backend is the default implementation
- [x] **MQ-09**: Broker-side **at-least-once** delivery — a message is removed only when the consumer acks it (by broker-assigned `uint64 id`); unacked in-flight messages are re-queued and redelivered when a consumer disconnects. Achieved in memory via redelivery, no disk. _(Phase 01.1 — ADR-001)_
- [x] **MQ-10**: `Consume` is a **bidirectional** stream with **client-driven credit** flow control (initial credit `C`; outstanding-unacked ≤ `C`, replenished per ack) so the broker never over-pulls beyond a consumer's in-flight window. _(Phase 01.1 — ADR-001)_

### MQ Durability (DUR)

- [ ] **DUR-01**: Opt-in WAL-backed `Store` — appends each `Produce` to a write-ahead log with batched group-commit fsync; enabled via config (in-memory remains the default)
- [ ] **DUR-02**: On restart in WAL mode, the broker replays all persisted messages (crash-recovery durability; consumers must be idempotent). _Note: delivery-level at-least-once now lives in MQ-09 (Phase 01.1); Phase 6 narrows to crash durability — reconcile when Phase 6 is planned (ADR-001)._

### Streamer (STREAM)

- [ ] **STREAM-01**: Continuously loops the DCGM CSV file line-by-line, indefinitely
- [ ] **STREAM-02**: Restamps each record with the current execution timestamp before publishing
- [ ] **STREAM-03**: Publishes records to the MQ via a generated gRPC `Produce` client stub
- [ ] **STREAM-04**: Parses the DCGM 12-column format; malformed lines are skipped and logged cleanly
- [ ] **STREAM-05**: Supports running up to 10 concurrent instances

### Collector (COLL)

- [ ] **COLL-01**: Establishes a long-lived gRPC `Consume` stream to the MQ
- [ ] **COLL-02**: Reconnects automatically if the stream drops
- [ ] **COLL-03**: Persists received telemetry into PostgreSQL via `pgxpool` batched writes
- [x] **COLL-04**: Maps the wire payload to the DB model and persists reactively as data arrives
- [ ] **COLL-05**: Performs idempotent upsert (`ON CONFLICT`) so a redelivered message does not duplicate a row

### Storage / Schema (DB)

- [x] **DB-01**: Relational time-series schema (`gpu_id`, `timestamp TIMESTAMPTZ`, numeric metric columns)
- [x] **DB-02**: Composite index `(gpu_id, timestamp DESC)` defined and verified used via `EXPLAIN`
- [x] **DB-03**: `pgxpool` connection-pool initialization in shared `pkg/db`, reused by collector and gateway
- [x] **DB-04**: A natural-key unique constraint enables idempotent inserts (so at-least-once redelivery cannot create duplicate rows)

### API Gateway (API)

- [ ] **API-01**: `GET /api/v1/gpus` returns the unique list of GPU IDs
- [ ] **API-02**: `GET /api/v1/gpus/{id}/telemetry` returns that GPU's telemetry ordered by time
- [ ] **API-03**: `GET /api/v1/gpus/{id}/telemetry?start_time=&end_time=` filters by time window
- [ ] **API-04**: OpenAPI spec fully auto-generated from `swag` code annotations (no hand-written spec)

### DevOps / Deployment (OPS)

- [ ] **OPS-01**: Multi-stage Dockerfile per service (mq, streamer, collector, gateway)
- [ ] **OPS-02**: Helm chart with a sub-chart per microservice plus a PostgreSQL dependency
- [ ] **OPS-03**: Each microservice builds and deploys independently
- [ ] **OPS-04**: MQ deploys as a single replica with `strategy: Recreate`
- [ ] **OPS-05**: Makefile targets for `proto`, `build`, `test`, `coverage`, `swagger`

### Quality (QA)

- [ ] **QA-01**: Unit tests across all services
- [x] **QA-02**: Race-detector tests for MQ concurrency proving N produced = N consumed across K consumers
- [ ] **QA-03**: Integration tests (end-to-end CSV→MQ→Collector→Postgres; gateway against a seeded DB)
- [ ] **QA-04**: ≥90% line coverage enforced via the Makefile coverage gate
- [ ] **QA-05**: Crash-recovery test — after a simulated broker restart in WAL mode, no un-consumed message is lost (replay verified)

### Documentation & Manual Verification (DOC / QA) — cross-cutting cadence

- [ ] **DOC-01**: Living `README.md` quickstart, grown **incrementally** as each phase completes — a reader can clone → run → see each shipped component work. Every phase plan includes a README-update task.
- [ ] **QA-06**: Runnable manual smoke suite the user executes to verify each phase's deliverables — `scripts/smoke/phaseNN-*.sh` driven by `make smoke-NN` (one phase) and `make smoke` (all phases shipped). Distinct from automated integration tests (QA-03) and the coverage gate (QA-04).
- [ ] **OPS-06**: `docker-compose.yml` dev stack + `make dev-up`/`make dev-down` provides local dependencies (Postgres from Phase 2 on) for manual smoke testing, independent of the Phase-5 kind/Helm stack.

> DOC-01, QA-06, OPS-06 are **cross-cutting** — the harness is established in Phase 2 (first phase needing Postgres/Docker, with a thin Phase-1 MQ backfill) and extended by every subsequent phase.

## v2 Requirements

Research-surfaced differentiators. Tracked, not in the current roadmap.

### Enhancements

- **ENH-01**: Configurable MQ drop policy (drop-oldest vs reject-newest)
- **ENH-02**: Configurable Collector batch size / flush interval
- **ENH-03**: Kubernetes readiness/liveness health probes per service
- **ENH-04**: Graceful drain of in-flight MQ messages on shutdown
- **ENH-05**: gRPC client retry with backoff (Streamer/Collector)
- **ENH-06**: Richer `/inspect` output (per-consumer slot state, throughput)

## Out of Scope

Explicitly excluded by `instructions.md`. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Third-party message brokers (Kafka/NATS/RabbitMQ/Redis) | MQ must be built from scratch |
| MQ clustering / multi-replica | Spec: single-replica in-memory broker (durable WAL is local-disk, not a cluster) |
| ~~Per-message ACK/NAK protocol~~ | **No longer out of scope** — now in scope as MQ-09/MQ-10 (Phase 01.1, ADR-001). Broker-side at-least-once via per-message ack + credit + redelivery, in memory. |
| Consumer groups / message replay | Out of assignment scope |
| API authentication / authorization | Not in assignment scope |
| Hand-written OpenAPI spec | Must be auto-generated from annotations |
| API pagination | Not in assignment scope |
| Multi-region / cross-cluster deployment | Single Kubernetes cluster only |

## Traceability

Final mapping against ROADMAP.md (5 phases). Every v1 requirement maps to exactly one phase.

| Requirement | Phase | Status |
|-------------|-------|--------|
| MQ-01 | Phase 1 | Complete |
| MQ-02 | Phase 1 | Complete |
| MQ-03 | Phase 1 | Complete |
| MQ-04 | Phase 1 | Complete |
| MQ-05 | Phase 1 | Complete |
| MQ-06 | Phase 1 | Complete |
| MQ-07 | Phase 1 | Complete |
| MQ-08 | Phase 1 | Complete |
| QA-02 | Phase 1 | Complete |
| MQ-09 | Phase 01.1 | Complete |
| MQ-10 | Phase 01.1 | Complete |
| DB-01 | Phase 2 | Complete |
| DB-02 | Phase 2 | Complete |
| DB-03 | Phase 2 | Complete |
| DB-04 | Phase 2 | Complete |
| STREAM-01 | Phase 3 | Pending |
| STREAM-02 | Phase 3 | Pending |
| STREAM-03 | Phase 3 | Pending |
| STREAM-04 | Phase 3 | Pending |
| STREAM-05 | Phase 3 | Pending |
| COLL-01 | Phase 3 | Pending |
| COLL-02 | Phase 3 | Pending |
| COLL-03 | Phase 3 | Pending |
| COLL-04 | Phase 3 | Complete |
| COLL-05 | Phase 3 | Pending |
| QA-03 | Phase 3 | Pending |
| API-01 | Phase 4 | Pending |
| API-02 | Phase 4 | Pending |
| API-03 | Phase 4 | Pending |
| API-04 | Phase 4 | Pending |
| OPS-01 | Phase 5 | Pending |
| OPS-02 | Phase 5 | Pending |
| OPS-03 | Phase 5 | Pending |
| OPS-04 | Phase 5 | Pending |
| OPS-05 | Phase 5 | Pending |
| QA-01 | Phase 5 | Pending |
| QA-04 | Phase 5 | Pending |
| DUR-01 | Phase 6 | Pending |
| DUR-02 | Phase 6 | Pending |
| QA-05 | Phase 6 | Pending |
| DOC-01 | All phases (harness: Phase 2) | Pending |
| QA-06 | All phases (harness: Phase 2) | Pending |
| OPS-06 | Phase 2 | Pending |

**Coverage:**

- v1 requirements: 43 total
- Mapped to phases: 43
- Unmapped: 0 ✓

---
*Requirements defined: 2026-06-27*
*Last updated: 2026-06-28 — added MQ-09/MQ-10 (broker-side at-least-once: bidi Consume + ack + credit + redelivery; Phase 01.1, ADR-001); reframed MQ-02/MQ-03; per-message-ack moved out of Out-of-Scope; DUR-02 narrowed to crash durability*
