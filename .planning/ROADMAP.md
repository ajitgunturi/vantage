# Roadmap: vantage — Elastic GPU Telemetry Pipeline

**Created:** 2026-06-27
**Milestone:** v1 (MVP)
**Granularity:** standard
**Core Value:** End-to-end telemetry flow (CSV → Streamer → custom MQ → Collector → PostgreSQL → API Gateway) works reliably under concurrency, with no message loss or duplication.

## Phases

- [x] **Phase 1: Foundation — Proto Contract + MQ Core** - Race-safe in-memory MQ over gRPC + HTTP, exactly-once-to-one-consumer delivery (completed 2026-06-27)
- [x] **Phase 2: Storage Foundation — Schema + Connection Pool** - Time-series PostgreSQL schema with an EXPLAIN-verified composite index, shared via pgxpool (completed 2026-06-29)
- [x] **Phase 3: Pipeline — Streamer + Collector + Integration** - Live CSV telemetry flowing end-to-end into PostgreSQL under concurrency (completed 2026-06-29)
- [x] **Phase 4: API Gateway + OpenAPI Docs** - Documented REST access to stored GPU telemetry (completed 2026-06-30)
- [ ] **Phase 5: DevOps + Quality Gates** - Independent containerized services on Kubernetes via Helm, with enforced quality bar
- [ ] **Phase 6: MQ Durability — Opt-in WAL Persistence** - Crash-durable broker mode behind the Store interface; at-least-once via replay

## Phase Details

### Phase 1: Foundation — Proto Contract + MQ Core

**Goal**: A race-safe, in-memory custom message queue is reachable over gRPC (data plane) and HTTP (control plane), delivering each enqueued message to exactly one consumer under concurrency. Storage sits behind a `Store` interface (in-memory default) so a durable backend can be added later (Phase 6) without touching consumers.
**Mode:** mvp
**Depends on**: Nothing (first phase; the proto contract is the import root)
**Requirements**: MQ-01, MQ-02, MQ-03, MQ-04, MQ-05, MQ-06, MQ-07, MQ-08, QA-02
**Success Criteria** (what must be TRUE):

> _Note: SC 1–2 describe Phase 1 as delivered (server-stream, exactly-once-to-one). The `Consume` contract is **superseded by Phase 01.1** (bidi + at-least-once, ADR-001); this record is retained as history._

  1. A producer client can call `Produce` (unary gRPC) and a consumer client can call `Consume` (server-stream) against the running MQ, exchanging telemetry payloads defined by `api/proto/mq.proto` and its generated stubs.
  2. With K concurrent consumers and N produced messages, exactly N messages are consumed in total — no duplication, no loss (delivery-count test: N produced = N consumed across K consumers).
  3. `go test -race -count=50` runs clean (no data races, no deadlocks from holding a lock across a channel send) and the MQ package meets the ≥90% line-coverage gate.
  4. `GET /api/v1/queue/inspect` returns a JSON summary of queue status; the bounded buffer enforces a defined drop policy when full, and a consumer disconnect is handled gracefully with no leaked goroutines.

**Plans**: 3/3 plans complete

- [x] 01-01-PLAN.md
- [x] 01-02-PLAN.md
- [x] 01-03-PLAN.md

### Phase 01.1: MQ At-Least-Once Delivery — Bidi Consume and Ack (INSERTED)

**Goal:** Upgrade MQ delivery from server-stream at-most-once to broker-side **at-least-once** — bidirectional `Consume` with client-driven credit + per-message ack + redelivery-on-disconnect — in memory, no disk. **Deliberate, owner-approved deviation from the brief** (see `docs/adr/ADR-001-bidi-at-least-once-delivery.md`). Supersedes the Phase-1 "server-stream / exactly-once-to-one" contract.
**Requirements**: MQ-09, MQ-10 (reframes MQ-02, MQ-03)
**Depends on:** Phase 1
**Context:** `01.1-CONTEXT.md` (discussed 2026-06-28) · **ADR:** ADR-001
**Plans:** 6/6 plans complete

Plans:

- [x] 01.1-01-PLAN.md — Proto contract: bidi Consume + uint64 id + ConsumeClientMsg; regen pkg/pb (wave 1)
- [x] 01.1-02-PLAN.md — Store.Requeue front-insertion + ConsumeCredit config (wave 1)
- [x] 01.1-03-PLAN.md — Bidi at-least-once engine rewrite + bidi mock + 5 D-10 race tests + cmd/mq wiring (wave 2)
- [x] 01.1-04-PLAN.md — Inspect counters: consumed=acks, delivered/redelivered/in_flight (wave 3)
- [x] 01.1-05-PLAN.md — mqprobe bidi+ack client rewrite + -credit flag (wave 3)
- [x] 01.1-06-PLAN.md — Smoke suite ack-based + late-join no-loss + README (wave 4)

### Phase 2: Storage Foundation — Schema + Connection Pool

**Goal**: PostgreSQL holds GPU telemetry in a time-series schema whose composite index is provably used, accessed through a shared `pgxpool` that both Collector and Gateway reuse.
**Mode:** mvp
**Depends on**: Nothing (independent foundation; parallelizable with Phase 1)
**Requirements**: DB-01, DB-02, DB-03, DB-04
**Success Criteria** (what must be TRUE):

  1. A migration creates a relational time-series table with `gpu_id`, `timestamp TIMESTAMPTZ`, and numeric metric columns.
  2. `EXPLAIN` on a `(gpu_id, timestamp DESC)` range query confirms the composite index is used (index scan, not seq scan) at representative scale.
  3. `pkg/db` initializes a `pgxpool` connection pool that both the Collector and the API Gateway can import and reuse.
  4. The schema carries a natural-key unique constraint enabling idempotent inserts, so at-least-once redelivery (once durability is enabled in Phase 6) cannot create duplicate rows.

**Plans**: 2/2 plans complete

- [x] 02-01-PLAN.md — pkg/db slice: migration (table + composite index + natural-key constraint) + pgxpool + golang-migrate + testcontainers index-proof at 100k rows (wave 1)
- [x] 02-02-PLAN.md — cross-cutting harness: docker-compose dev stack, cmd/migrate, make dev-up/dev-down + coverage scope to ./pkg/..., phase02 smoke script, README (wave 2)

### Phase 3: Pipeline — Streamer + Collector + Integration

**Goal**: Live CSV telemetry flows end-to-end from the Streamer, through the MQ, into PostgreSQL via the Collector — correctly under concurrency.
**Mode:** mvp
**Depends on**: Phase 1 (MQ contract + running broker), Phase 2 (schema + pgxpool). Streamer and Collector are independent and built in parallel.
**Requirements**: STREAM-01, STREAM-02, STREAM-03, STREAM-04, STREAM-05, COLL-01, COLL-02, COLL-03, COLL-04, COLL-05, QA-03
**Success Criteria** (what must be TRUE):

  1. The Streamer loops the DCGM CSV indefinitely, restamps each record with the current UTC timestamp, parses the 12-column format (malformed lines skipped and logged cleanly), and publishes via the generated gRPC `Produce` client; up to 10 instances run concurrently.
  2. The Collector holds a long-lived `Consume` stream, auto-reconnects when the stream drops, maps the wire payload to the DB model, and batch-inserts to PostgreSQL via `pgxpool` reactively as data arrives — using idempotent upsert (`ON CONFLICT`) so a redelivered message never duplicates a row.
  3. An end-to-end integration test (CSV→MQ→Collector→Postgres) confirms rows land in the database, and with multiple concurrent collectors each message is persisted exactly once.

**Plans**: 4/4 plans complete

Plans:

- [x] 03-01-PLAN.md — pkg/models shared slice: GpuMetric + FromProto (uuid→gpu_id) + InsertSQL; TDD unit-proves COLL-04 (wave 1)
- [x] 03-02-PLAN.md — Streamer service: CSV infinite loop + RFC3339Nano restamp + 12-col parse + gRPC Produce; STREAM-01..05 (wave 1)
- [x] 03-03-PLAN.md — Collector service: long-lived bidi Consume + reconnect backoff + pgx.Batch ON CONFLICT upsert; COLL-01/02/03/05 (wave 2)
- [x] 03-04-PLAN.md — QA-03 exactly-once E2E (bufconn + testcontainers) + smoke-03 + README; QA-03/DOC-01/QA-06 (wave 3)

### Phase 4: API Gateway + OpenAPI Docs

**Goal**: Clients can query stored GPU telemetry over a documented REST API backed directly by PostgreSQL.
**Mode:** mvp
**Depends on**: Phase 2 (schema + pgxpool). Off the critical path — parallelizable with Phase 3 once the schema exists; tested against a seeded DB.
**Requirements**: API-01, API-02, API-03, API-04
**Success Criteria** (what must be TRUE):

  1. `GET /api/v1/gpus` returns the unique list of GPU IDs from PostgreSQL.
  2. `GET /api/v1/gpus/{id}/telemetry` returns that GPU's telemetry ordered by time, and the `?start_time=&end_time=` variant filters by time window (using the composite index).
  3. `swag init` regenerates a valid OpenAPI spec entirely from code annotations (no hand-written spec) and serves it via Swagger UI.

**Plans**: 3/3 plans complete

- [x] 04-01-PLAN.md — Gateway foundation + GPU-list slice (API-01) [wave 1]
- [x] 04-02-PLAN.md — Telemetry endpoint + time window + composite-index proof (API-02, API-03) [wave 2]
- [x] 04-03-PLAN.md — OpenAPI generation + composition root + coverage gate + smoke/README (API-04) [wave 3]

### Phase 5: DevOps + Quality Gates

**Goal**: Every microservice builds and deploys independently to a single Kubernetes cluster via Helm, and the repository enforces its quality bar end-to-end.
**Mode:** mvp
**Depends on**: Phase 3 (pipeline must exist to package and soak-test). Parallelizable with Phase 4.
**Requirements**: OPS-01, OPS-02, OPS-03, OPS-04, OPS-05, QA-01, QA-04
**Success Criteria** (what must be TRUE):

  1. Each service (mq, streamer, collector, gateway) has a multi-stage Dockerfile and a Helm sub-chart (plus a PostgreSQL dependency); `helm install` brings all pods to Running, and each service builds and deploys independently.
  2. The MQ Deployment runs as a single replica (`replicas: 1`) with `strategy: Recreate` — no rolling-update split-brain of the in-memory broker.
  3. The Makefile exposes `proto`, `build`, `test`, `coverage`, and `swagger` targets; unit tests span all services and the coverage gate enforces ≥90% line coverage.

**Plans**: TBD

### Phase 6: MQ Durability — Opt-in WAL Persistence

**Goal**: With durability enabled via config, the MQ persists produced messages to a write-ahead log and replays them on restart, so a broker crash loses no un-consumed message — while the in-memory default stays byte-for-byte unchanged.
**Mode:** mvp
**Depends on**: Phase 1 (the `Store` interface). Safe at-least-once relies on Phase 2 (unique constraint) + Phase 3 (idempotent collector) already being in place.
**Requirements**: DUR-01, DUR-02, QA-05
**Success Criteria** (what must be TRUE):

  1. A config flag selects the WAL-backed `Store`; when off, behavior is the in-memory default. The WAL appends each `Produce` and fsyncs once per batch (group commit), preserving throughput rather than fsync-per-message.
  2. On restart in WAL mode, all persisted messages are replayed (at-least-once); combined with the idempotent collector, the resulting PostgreSQL state is correct with no duplicate rows.
  3. A crash-recovery test simulates a broker restart mid-stream and verifies no un-consumed message is lost.

**Plans**: TBD

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Foundation — Proto Contract + MQ Core | 3/3 | Complete   | 2026-06-27 |
| 01.1 MQ At-Least-Once — Bidi Consume + Ack (INSERTED) | 6/6 | Complete    | 2026-06-28 |
| 2. Storage Foundation — Schema + Connection Pool | 2/2 | Complete   | 2026-06-29 |
| 3. Pipeline — Streamer + Collector + Integration | 4/4 | Complete   | 2026-06-29 |
| 4. API Gateway + OpenAPI Docs | 3/3 | Complete   | 2026-06-30 |
| 5. DevOps + Quality Gates | 0/TBD | Not started | - |
| 6. MQ Durability — Opt-in WAL Persistence | 0/TBD | Not started | - |

## Coverage

- v1 requirements: 43 total (+MQ-09, MQ-10 for Phase 01.1; see REQUIREMENTS.md)
- Mapped to phases: 43
- Orphaned: 0 ✓

---
*Roadmap created: 2026-06-27*
*Updated 2026-06-28: inserted Phase 01.1 (bidi at-least-once, ADR-001); +MQ-09/MQ-10.*
