# Roadmap: vantage — Elastic GPU Telemetry Pipeline with Custom Message Queue

## Overview

Vantage is built dependency-first, with the **custom durable message queue as the root deliverable**.
The journey: harden the broker's append-only segment log and crash recovery (the heart, highest risk),
wire its gRPC surface with partitions + durable group offsets, wrap it in a client library that becomes
the single integration seam, then build the streamer and collector (parallel tracks that share only
`mq/client` + the Postgres schema), expose the data through a read-only API gateway, package everything
into Helm/Docker for a dynamic-scale demo, instrument with Prometheus + a performance harness across
producer/consumer ratios, and finally cement no-loss/ordering claims with end-to-end integration tests
and the mandatory README + AI-usage documentation. Each layer's correctness is verified before the next
is built on top of it — if the log loses messages, nothing downstream matters.

## Pre-Build Gate (GATE 0): ADR-0005 — ✅ ACCEPTED (2026-06-24)

> **Resolved during project initialization. Phases 2/5/6 are unblocked.**

ADR-0005 (canonical GPU identity = `uuid`) is **Accepted** as of 2026-06-24. The schema is frozen:
the MQ partition routing key = `hash(uuid) % N`, the Postgres PK / idempotency key =
`(uuid, metric_name, ts)`, and the API `{id}` path parameter = `uuid` (with `hostname` + `gpu_id`
exposed as friendly attributes via the `/gpus` list). Partition routing (Phase 2), the collector
schema (Phase 5), and API routing (Phase 6) may now proceed against this identity.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Broker Durable Segment Log + Crash Recovery** - The heart: CRC-framed append-only log that survives kill -9 with zero loss
- [ ] **Phase 2: Broker gRPC Surface — Partitions, Offsets, Delivery** - Produce/Consume/Commit over ≥10 uuid-routed partitions with durable group offsets
- [ ] **Phase 3: MQ Client Library + Graceful Shutdown Seam** - Producer/consumer lib (`mq/client`) — the only coupling point; SIGTERM drain pattern
- [ ] **Phase 4: Streamer (Producer)** - Loop DCGM CSV, re-stamp ts at produce time, produce by uuid, scale 1–10
- [ ] **Phase 5: Collector + PostgreSQL Schema** - Idempotent commit-after-persist upsert on `(uuid, metric_name, ts)`, scale 1–10
- [ ] **Phase 6: API Gateway** - 3 read-only REST endpoints + auto-generated OpenAPI spec
- [ ] **Phase 7: Helm/Docker Packaging + Dynamic Scale Demo** - Deployable to kind; broker StatefulSet+PVC; scale to 10×10 with no loss
- [ ] **Phase 8: Prometheus Metrics + Performance Harness** - Per-service metrics, then (10:2)/(2:10)/(5:5) characterization table + analysis
- [ ] **Phase 9: Integration Tests + Documentation** - End-to-end no-loss/ordering-across-restart test + README + AI-usage doc

## Phase Details

### Phase 1: Broker Durable Segment Log + Crash Recovery
**Goal**: A hand-built append-only segment log that durably persists messages, recovers cleanly from torn writes, and applies disk-based backpressure — the durability foundation everything else depends on.
**Depends on**: Nothing (first phase; builds on the existing scaffold + stubs)
**Requirements**: MQ-01, MQ-08, MQ-05, TEST-01
**Success Criteria** (what must be TRUE):
  1. Broker survives `kill -9` mid-write and on restart replays every fsync'd record while dropping the torn tail — verified by a mandatory crash-recovery test (writes N records, injects a truncated tail, asserts no committed record lost or duplicated).
  2. On restart the broker rebuilds its in-memory offset index solely from CRC-validated records — reads return the exact payload for every appended offset across segment-roll boundaries.
  3. Under a producer-faster-than-consumer load, broker RSS plateaus while the on-disk log grows (disk is the buffer, not memory) — no unbounded in-memory backlog.
  4. Segments roll at a size bound (named by base offset) so index rebuild stays bounded and disk is reclaimable.
  5. `make cover` reports a real number and `make cover-logic` enforces 100% branch on the new broker logic (fail-open coverage gate confirmed fired).
**Plans**: TBD
**Research flag**: NEEDS DEEPER PHASE-SPECIFIC RESEARCH — run `/gsd-plan-phase --research-phase 1` first (group-commit channel design, CRC32c frame format, segment-roll directory fsync, truncation state machine, crash-recovery test harness).

### Phase 2: Broker gRPC Surface — Partitions, Offsets, Delivery
**Goal**: The broker speaks the existing `mq/proto/mqv1` contract — produce, consume, and commit over ≥10 uuid-routed partitions with at-least-once delivery and durable, restart-surviving consumer-group offsets.
**Depends on**: Phase 1
**Requirements**: MQ-02, MQ-03, MQ-04, MQ-06
**Success Criteria** (what must be TRUE):
  1. A producer's message is acknowledged only after it is fsync'd to disk (fsync-before-ack) — the at-least-once durability boundary holds.
  2. The broker exposes ≥10 partitions routed by `key = hash(uuid) % N`, so all metrics for one GPU land on one partition in arrival order (per-GPU ordering preserved) and up to 10 consumers can each own ≥1 partition.
  3. Consumer-group committed offsets per `(group, partition)` survive a broker restart — after restart, a consumer resumes from its last committed offset, never re-reading committed data nor skipping uncommitted data.
  4. Produce, Consume, and Commit RPCs work end-to-end against the live segment log over gRPC (single-writer-per-partition; `-race` clean).
**Plans**: TBD
**Gate**: ADR-0005 **Accepted** (GATE 0, 2026-06-24) — partition key = `uuid`.

### Phase 3: MQ Client Library + Graceful Shutdown Seam
**Goal**: A Go client library (`mq/client`) that gives producers and consumers a clean API over gRPC — the single integration seam that decouples app services from the broker — plus the SIGTERM-drain shutdown pattern every service reuses.
**Depends on**: Phase 2
**Requirements**: MQ-07, MQ-09
**Success Criteria** (what must be TRUE):
  1. A producer can batch + partition-route (`hash(key)%N`) and a consumer can poll + commit purely through `mq/client`, importing only `mq/client` (never `mq/broker`) — proving the seam that unblocks parallel streamer/collector work.
  2. An end-to-end integration test runs produce → fsync → consume → commit → broker restart and asserts zero message loss and correct per-GPU ordering.
  3. On SIGTERM, a service performs graceful shutdown — flush + fsync, commit pending offsets, drain in-flight streams — and a shutdown-under-load test confirms no committed message is lost.
  4. A consumer disconnect cancels the broker-side stream handler (context propagated through every blocking op) — broker goroutines/buffers for a dead peer drop to zero (no leak).
**Plans**: TBD

### Phase 4: Streamer (Producer)
**Goal**: A stateless producer that loops the DCGM CSV, re-stamps timestamps at produce time, and produces over the MQ keyed by GPU uuid — scaling from 1 to 10 instances.
**Depends on**: Phase 3
**Requirements**: STREAM-01, STREAM-02, STREAM-03, STREAM-04
**Success Criteria** (what must be TRUE):
  1. The streamer reads the DCGM CSV row-by-row and loops continuously on EOF to simulate a live stream (never `ReadAll`).
  2. Each datapoint is re-stamped `ts = time.Now()` at produce time and the CSV's original timestamp column is discarded — a test asserts two loops of the same CSV row yield two distinct `ts` values (and therefore two distinct downstream rows, not a silent `ON CONFLICT` collapse).
  3. The streamer produces over the MQ with `key = uuid` at a configurable, rate-limited cadence.
  4. Running 1 to 10 streamer instances concurrently produces correctly with no client-side message loss.
**Plans**: TBD

### Phase 5: Collector + PostgreSQL Schema
**Goal**: A stateless consumer that parses DCGM rows and idempotently persists them into a normalized Postgres schema using commit-after-persist — scaling from 1 to 10 instances with no duplicate rows on redelivery.
**Depends on**: Phase 3 (client lib); parallelizable with Phase 4 (shares only `mq/client` + this schema)
**Requirements**: COLL-01, COLL-02, COLL-03, COLL-04, COLL-05
**Success Criteria** (what must be TRUE):
  1. The Postgres schema normalizes into `gpus` (dimension: uuid, host, gpu_id, device, model_name) and `telemetry` (fact) with `PRIMARY KEY (uuid, metric_name, ts)` and a `(uuid, ts)` index — the idempotency anchor and the time-window query path both exist before upsert logic.
  2. The collector consumes from the MQ, parses DCGM rows, and upserts via `INSERT … ON CONFLICT (uuid, metric_name, ts) DO NOTHING` — sending the identical message twice yields exactly one row; 1000 messages with 30% duplicates yield exactly the distinct count.
  3. The collector commits its consumer offset only after the batch is durably persisted (commit-after-persist) — a crash injected between persist and commit causes redelivery absorbed as a no-op, never a skip.
  4. Running 1 to 10 collector instances each owns ≥1 partition and ingests without exceeding Postgres `max_connections` (pgxpool sized so `collectors × MaxConns + API < max_connections`).
**Plans**: TBD
**Gate**: ADR-0005 **Accepted** (GATE 0, 2026-06-24) — PK = `(uuid, metric_name, ts)`.

### Phase 6: API Gateway
**Goal**: A thin read-only REST layer exposing GPUs and their time-ordered telemetry, with an auto-generated OpenAPI spec — depends only on the Postgres schema, no MQ coupling.
**Depends on**: Phase 5 (Postgres schema)
**Requirements**: API-01, API-02, API-03, API-04
**Success Criteria** (what must be TRUE):
  1. `GET /api/v1/gpus` returns all GPUs for which telemetry exists.
  2. `GET /api/v1/gpus/{id}/telemetry` returns one GPU's telemetry ordered by time, and `?start_time=…&end_time=…` filters to an inclusive window — `EXPLAIN` shows an Index Scan on `(uuid, ts)`, not a Seq Scan.
  3. `make openapi` regenerates the OpenAPI (Swagger) spec from the hand-authored `openapi.yaml` via oapi-codegen (chi-server handlers).
  4. The gateway runs with RequestID/Recoverer/Timeout/slog middleware and serves `/healthz` + `/readyz`.
**Plans**: TBD
**Gate**: ADR-0005 **Accepted** (GATE 0, 2026-06-24) — `{id}` = `uuid`.
**UI hint**: yes

### Phase 7: Helm/Docker Packaging + Dynamic Scale Demo
**Goal**: All four services packaged into runnable images and a Helm umbrella chart deployable to a local kind cluster, with a dynamic-scale demo proving correctness at the 10×10 ceiling under k8s SIGTERM.
**Depends on**: Phase 6 (all four binaries exist)
**Requirements**: DEPLOY-01, DEPLOY-02, DEPLOY-03, DEPLOY-04
**Success Criteria** (what must be TRUE):
  1. Each service has a multi-stage Dockerfile producing a runnable image.
  2. A Helm umbrella chart deploys all services + PostgreSQL with the broker as a `StatefulSet` + `volumeClaimTemplates` (RWO PVC) and streamer/collector/API as `Deployment`s — no Multi-Attach error on broker rollout.
  3. The full stack deploys to a local kind cluster via `make kind` + `make helm`; the broker readiness probe goes ready only after segment-log recovery completes.
  4. `kubectl scale` to 10 streamers and 10 collectors runs with graceful SIGTERM drain and zero message loss across scale up/down.
**Plans**: TBD

### Phase 8: Prometheus Metrics + Performance Harness
**Goal**: Every component exposes Prometheus metrics, then a performance harness characterizes the assembled system across producer/consumer ratios — the marquee bonus, meaningful only on a correct, deployed pipeline.
**Depends on**: Phase 7 (full pipeline deployed); metrics (OBS-01) precede the harness (OBS-02)
**Requirements**: OBS-01, OBS-02
**Success Criteria** (what must be TRUE):
  1. Each service exposes `/metrics`: broker (`fsync_latency_seconds`, `partition_offset_max`, `messages_persisted_total`), collector (`upsert_latency_seconds`, `offset_committed_total`, derived consumer lag), streamer (`produced_total`, `produce_latency_seconds`), API (`requests_total`, `query_latency_seconds`).
  2. The harness drives (producers, consumers) ∈ {(10,2),(2,10),(5,5)} with a discarded warmup window and a fixed workload across all three ratios — producing a comparison table of throughput (msg/s + MB/s), end-to-end latency (p50/p95/p99), broker queue depth, consumer lag, and broker RSS trend.
  3. Alongside perf, a correctness assertion confirms Postgres row count equals messages produced (throughput reconciles with consumer lag — no "measuring intake not delivery" trap).
  4. A written analysis of the comparison table lands in the README.
**Plans**: TBD

### Phase 9: Integration Tests + Documentation
**Goal**: End-to-end tests that cement the no-loss + per-GPU-ordering claims across a broker restart, plus the mandatory README and AI-usage documentation.
**Depends on**: Phase 8
**Requirements**: TEST-02, DOC-01, DOC-02
**Success Criteria** (what must be TRUE):
  1. An end-to-end integration test spins broker + streamer + collector + Postgres, produces N messages, restarts the broker mid-stream, and asserts zero loss and correct per-GPU ordering.
  2. The README documents architecture & design, build/packaging, install workflow, a sample user workflow, and the Phase 8 performance comparison table.
  3. The AI-usage doc records the exact prompts used to bootstrap repo/code/tests/build, where they fell short, and the manual interventions required.
**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9
(Phase 5 may run in parallel with Phase 4 — both depend only on Phase 3's `mq/client` + the Phase 5 schema.)

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Broker Durable Segment Log + Crash Recovery | 0/TBD | Not started | - |
| 2. Broker gRPC Surface — Partitions, Offsets, Delivery | 0/TBD | Not started | - |
| 3. MQ Client Library + Graceful Shutdown Seam | 0/TBD | Not started | - |
| 4. Streamer (Producer) | 0/TBD | Not started | - |
| 5. Collector + PostgreSQL Schema | 0/TBD | Not started | - |
| 6. API Gateway | 0/TBD | Not started | - |
| 7. Helm/Docker Packaging + Dynamic Scale Demo | 0/TBD | Not started | - |
| 8. Prometheus Metrics + Performance Harness | 0/TBD | Not started | - |
| 9. Integration Tests + Documentation | 0/TBD | Not started | - |
