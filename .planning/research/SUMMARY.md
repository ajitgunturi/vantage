# Project Research Summary

**Project:** vantage — Elastic GPU Telemetry Pipeline with Custom Message Queue
**Domain:** Custom durable message queue + idempotent GPU telemetry pipeline (Go, gRPC, PostgreSQL, k8s/Helm)
**Researched:** 2026-06-24
**Confidence:** HIGH

## Executive Summary

Vantage is a greenfield Go service system whose central grading criterion is a **hand-built durable message queue** — not the streamer, not the API, not the Helm chart. The MQ must survive broker restarts with no message loss, support at-least-once delivery, and scale to 10 producers and 10 consumers simultaneously. All four research threads converge on the same core principle: correctness before features. A Kafka-lite append-only segment log (CRC-framed records, fsync-before-ack, group commit, sparse offset index) is the right pattern, borrowed from Kafka/etcd idioms but implemented without any off-the-shelf broker code (ADR-0001 is a hard constraint). The gRPC streaming contract already exists in `mq/proto/mqv1/mq.proto`; the two previously-open tooling decisions (OpenAPI generator and HTTP router) are now resolved: `oapi-codegen/v2` v2.7.1 spec-first with `chi/v5` v5.3.0.

The mandatory build order is fully determined by the dependency graph and cannot be safely reordered: durable segment log → broker gRPC surface + client lib → streamer and collector (parallel) → API gateway → Helm packaging → Prometheus + performance harness → integration tests. Every phase downstream of the broker log depends on its durability guarantees being correct; if the log loses messages, nothing else matters. The collector and API gateway are deliberately decoupled from the MQ (they share only the Postgres schema), so they can be built and tested in parallel once the client lib exists.

The single largest risk is the **ADR-0005 blocker**: canonical GPU identity (`uuid` vs `hostname:gpu_id`) is still *Proposed* and gates the Postgres schema, the idempotency key `(uuid, metric_name, ts)`, the API `{id}` path parameter, and the MQ partition routing key. Code must not land in the collector, API gateway, or partition logic until ADR-0005 is *Accepted*. The second-largest risk is the broker segment log itself — torn-write detection, CRC-checksum recovery, and group-commit fsync semantics are all correctness-critical and are the phase most likely to require a dedicated crash-recovery test suite and deeper phase-specific research before coding begins.

## Key Findings

### Recommended Stack

The core stack is locked by ADR-0001…0005. Research resolved implementation-level decisions within that stack and bumped two stale version pins.

**Core technologies (versions confirmed via Go module proxy):**
- `google.golang.org/grpc` v1.81.1 — MQ transport (current go.mod pins v1.71.0; bump for keepalive/flow-control fixes)
- `google.golang.org/protobuf` v1.36.11 — message serialization (bump from v1.36.6)
- `github.com/jackc/pgx/v5` v5.10.0 — PostgreSQL driver + pool; use `pgxpool`, never bare `pgx` (v3) or `database/sql`
- `github.com/go-chi/chi/v5` v5.3.0 — HTTP router for API gateway (**RESOLVED**: preferred over gin for a read-only 2-endpoint API with stdlib-native handlers)
- `github.com/oapi-codegen/oapi-codegen/v2` v2.7.1 — OpenAPI spec-first generator (**RESOLVED**: preferred over swaggo/swag, which emits Swagger 2.0 only; swaggo v2/3.1 is still RC as of June 2026)
- `github.com/prometheus/client_golang` v1.23.2 — metrics exposition on every service; `*Vec` collectors are goroutine-safe

**Critical implementation constraints (not just preferences):**
- Segment log write path: buffered `os.File` + explicit `Sync()` — never mmap (no control over flush timing, fights fsync-before-ack)
- Collector upsert: `pgx.Batch` of `INSERT … ON CONFLICT (uuid, metric_name, ts) DO NOTHING` — never bare `CopyFrom` (has no `ON CONFLICT`)
- Streamer: `csv.Reader.Read()` row-by-row with `ReuseRecord = true` — never `ReadAll()`
- Backpressure: rely on gRPC HTTP/2 per-stream flow control blocking `stream.Send()` — no custom credit protocol
- Rate control in streamer: `golang.org/x/time/rate.Limiter` (token bucket) — no hand-rolled ticker

### Expected Features

**Must have — mandated deliverables (missing = incomplete grade):**
- Custom MQ broker: produce + consume + commit over gRPC, durable segment log, survives restart
- Partitions ≥ 10 with `key=uuid` routing for per-GPU ordering and consumer parallelism
- Streamer: loop DCGM CSV, re-stamp `ts=now()` at produce time (not CSV timestamp), produce at configurable rate
- Collector: consume, parse, idempotent upsert into Postgres on `(uuid, metric_name, ts)`, commit-after-persist
- Postgres schema: `gpus` (dimension) + `telemetry` (fact), PK `(uuid, metric_name, ts)`
- Three REST endpoints: `GET /api/v1/gpus`, `GET /api/v1/gpus/{id}/telemetry?start_time&end_time` (time-ordered)
- Auto-generated OpenAPI spec via `make openapi` target (oapi-codegen from `openapi.yaml`)
- Helm umbrella chart + Dockerfiles, deployable to kind; dynamic scale 1–10 streamers/collectors demonstrated
- Unit tests + coverage (`make test` / `make cover-check`): 90% line, 100% branch on logic
- README + AI-usage doc (exact prompts + interventions)

**Must have — table-stakes correctness (not separately graded, but failure here fails the core value):**
- Durability: fsync-before-ack on the broker; rebuild offset index from segments on restart
- At-least-once delivery: ack only after fsync; redeliver uncommitted on consumer restart
- Consumer-group offsets: durable committed offsets per (group, partition), survive broker restart
- Idempotent upsert: `ON CONFLICT (uuid, metric_name, ts) DO NOTHING`; database-level guarantee, not application logic
- Backpressure: disk is the buffer; gRPC flow control blocks producers when broker can't keep up; broker RSS must plateau under (10,2)
- Graceful shutdown: SIGTERM → flush+fsync+commit offsets+drain streams on every service

**Should have — bonus / quality ceiling:**
- Prometheus metrics on every component (broker fsync latency, queue depth, consumer lag; enables the perf harness)
- Performance harness: (10,2)/(2,10)/(5,5) — throughput, end-to-end latency (p50/p95/p99), queue depth, consumer lag, CPU/mem; comparison table + written analysis
- System / integration tests (end-to-end no-loss + ordering across restart)
- Grafana dashboard (pure presentation once Prometheus is exporting)

**Defer to v2+ / stretch:**
- Message compression (measure first; adds CPU cost)
- Retention / log compaction (backpressure covers the demo window)
- Producer-side dedup window (idempotent upsert already gives correctness)

### Architecture Approach

The system is a single-direction pipeline: `CSV → Streamer → [gRPC Produce] → Broker (PVC) → [gRPC Consume] → Collector → Postgres → [HTTP] → API`. The only back-edges are control acks (`ProduceResponse(offset)` and `Commit(offset)`). The broker is the only stateful service and must run as a `StatefulSet` with `volumeClaimTemplates` (RWO PVC per pod); all other services are stateless `Deployment`s. The `mq/client` lib is the single integration seam: streamer and collector import only `mq/client`, never `mq/broker`, which enables parallel development once the client lib is stable.

**Major components and responsibilities:**
1. **MQ Broker** (`mq/broker/`) — partitioned append-only segment log, sparse offset index, group offset store, fsync-before-ack, gRPC surface (Produce/Consume/Commit/CreateTopic/Health). Highest complexity; must be built and crash-tested first.
2. **MQ Client lib** (`mq/client/`) — producer batching + `hash(key)%N` partition routing, consumer poll+ack loop, reconnect/backoff. The seam that decouples app services from the broker.
3. **Streamer** (`streamer/`) — stateless CSV loop, re-stamp `ts=now`, rate-limited produce via client lib.
4. **Collector** (`collector/`) — stateless consume + parse + idempotent pgxpool upsert, commit-after-persist.
5. **API Gateway** (`apigateway/`) — chi router, oapi-codegen-generated handlers, pgxpool read-only queries; no MQ dependency.
6. **Postgres** — `gpus` dimension + `telemetry` fact; B-tree index on `(uuid, ts)` for time-window queries.

**Key internal patterns:**
- One writer per partition (mutex or owning goroutine) — serializes append + offset assignment, preserves per-GPU ordering
- `group_store.go` separate from the message log — committed offsets fsync'd independently, never co-mingled with message data
- Commit-after-persist in the collector — the two at-least-once boundaries are broker fsync-before-ack AND collector commit-after-persist

### Critical Pitfalls

1. **ADR-0005 not Accepted before collector/API/partition code lands (P0)** — The `{id}` in the API, the Postgres PK `(uuid, metric_name, ts)`, and the partition routing key `hash(uuid)%N` all resolve to this choice. If code merges while ADR-0005 is still *Proposed*, a pivot to `hostname:gpu_id` forces a multi-module rewrite. Prevention: treat ADR-0005 acceptance as a pre-build decision gate, not a task. Block any collector/API/partition PR until it reads *Accepted*.

2. **Acking/committing offset before the message is durably persisted (P1)** — Silent at-most-once masquerading as at-least-once. Prevention: enforce broker fsync-before-ack AND collector commit-after-persist as architectural invariants; test with crash injection between persist and commit.

3. **Missing unique constraint on `(uuid, metric_name, ts)` (P2)** — `ON CONFLICT` has nothing to conflict on; duplicates insert or the upsert errors. Prevention: define `PRIMARY KEY (uuid, metric_name, ts)` on `telemetry` before TDD-ing the upsert.

4. **Torn writes without CRC-checksum recovery on the segment log (P3)** — A broker crash mid-write leaves a partial record at the tail; naive index rebuild reads garbage or panics. Prevention: length-prefixed CRC32c-framed records; recovery truncates to the last intact record. A mandatory crash-recovery test gates the broker phase.

5. **Re-stamping vs replaying CSV timestamps (P15)** — If the streamer uses the CSV `timestamp` column, every loop reuses the same 4 timestamps; `ON CONFLICT DO NOTHING` silently discards nearly everything, the telemetry table barely grows, and the pipeline appears green but broken. Prevention: `ts = time.Now()` at produce time; test that two CSV loops of the same row yield distinct `ts` values.

## Implications for Roadmap

Build order is fully determined by the dependency graph. All four research files converge on the same sequence.

### GATE 0: Accept ADR-0005 (Pre-build decision — not a phase)

This is not a development task — it is a schema-freezing decision that must be made before any code in the collector, API gateway, or MQ partition logic is written.

Accept ADR-0005: canonical GPU identity = `uuid` (globally unique per DCGM dataset: 247 distinct UUIDs across 31 hosts × 8 GPUs). `gpu_id` (0–7) and `hostname` are dimensions, not identity. The API `{id}` parameter = `uuid`. The Postgres PK = `(uuid, metric_name, ts)`. The MQ partition key = `hash(uuid) % N`.

If the API must expose a human-friendly identifier, decide now whether `{id}` is the `uuid` directly or a lookup that resolves to `uuid` internally — then freeze that choice in ADR-0005. Do not write the first line of collector schema or API routing until this reads *Accepted*.

### Phase 1: Broker — Durable Segment Log + Crash Recovery

**Rationale:** Everything downstream depends on the log's correctness guarantees. This is the highest-risk, highest-complexity phase and must be isolated, crash-tested, and proven before the gRPC surface or any application service is built on top of it.

**Delivers:**
- `mq/broker/segment_log.go`: append + CRC32c-framed records + segment roll at size threshold
- `mq/broker/offset_index.go`: sparse offset → file-position index, rebuilt only from validated records
- `mq/broker/partition.go`: one writer per partition (mutex), monotonic offset assignment
- `mq/broker/group_store.go`: durable per-(group, partition) committed offsets, fsync'd independently
- Crash-recovery test suite: write N records, kill -9 mid-write (or inject truncated tail), restart, assert all fsync'd records recover and torn tail is dropped

**Implements:** FEATURES.md table-stakes durability; ARCHITECTURE.md Pattern 1 (segment log) + Pattern 2 (fsync-on-ack with group commit)

**Avoids:** P3 (torn writes), P4 (fsync mis-tuning), P12 (unbounded segment growth), P13 (index inconsistency)

**Research flag: NEEDS DEEPER PHASE-SPECIFIC RESEARCH.** This is the highest-risk phase. Before coding begins, run `/gsd-plan-phase --research-phase 1` to research: (a) exact group-commit channel design for batching concurrent Produce streams under a per-partition writer; (b) segment roll with directory fsync for durable file creation; (c) CRC32c frame format and truncation state machine in Go; (d) minimal crash-recovery test harness.

### Phase 2: Broker gRPC Surface + MQ Client Library

**Rationale:** The gRPC contract already exists. Wiring it to the segment log and wrapping it in a client lib creates the integration seam that unblocks streamer and collector in parallel.

**Delivers:**
- `mq/broker/broker.go`: gRPC Produce (bidi), Consume (server-push), Commit (unary), CreateTopic, Health
- Partition assignment: consumer-group join, range/round-robin partition → consumer_id mapping
- `mq/client/producer.go`: batch + `hash(key)%N` routing
- `mq/client/consumer.go`: Consume stream loop, in-order handler, Commit timing
- Integration test: produce → fsync → consume → commit → broker restart → assert no loss, correct ordering
- Version bumps: grpc v1.81.1, protobuf v1.36.11 (regenerate stubs via `make proto`)
- gRPC keepalive parameters and graceful `GracefulStop()` on SIGTERM

**Avoids:** P5 (unbounded in-memory buffers), P6 (data races), P8 (context cancellation through streams), P14 (partitions ≥ 10)

**Research flag:** Standard patterns. No phase research needed.

### Phase 3a + 3b: Streamer + Collector (Build in Parallel)

**Rationale:** Both services depend only on `mq/client` and the Postgres schema. They can be developed simultaneously. Schema must land with the collector (3b).

**3a — Streamer delivers:**
- `csv.Reader.Read()` row-by-row, `ReuseRecord=true`, loop on EOF
- Re-stamp `ts=time.Now().UnixNano()` (discard CSV timestamp column), encode as protobuf `TelemetryRow` into `Message.value`, `rate.Limiter`-paced produce, `key=uuid`
- Test: two loops of the same CSV row produce distinct `ts` and two distinct downstream Postgres rows

**3b — Collector delivers:**
- Postgres schema: `gpus(uuid PK, ...)` + `telemetry(uuid, metric_name, value, ts, PRIMARY KEY(uuid, metric_name, ts))` + `CREATE INDEX telemetry_uuid_ts ON telemetry(uuid, ts)`
- `pgxpool.SendBatch` of `INSERT … ON CONFLICT (uuid, metric_name, ts) DO NOTHING` wrapped in transaction, commit-after-persist
- Test: identical message twice → one row; 1000 messages with 30% duplicates → distinct count

**Avoids:** P2, P9, P15, P1

**Research flag:** Standard patterns. No phase research needed.

### Phase 4: API Gateway

**Rationale:** Depends on Postgres schema (Phase 3b); no MQ dependency. Can start as soon as schema is real.

**Delivers:**
- `apigateway/openapi.yaml`: hand-authored OpenAPI 3.x spec
- `make openapi` target via oapi-codegen/v2 with `chi-server: true`, `embedded-spec: true`
- Handlers: `GET /api/v1/gpus`, `GET /api/v1/gpus/{id}/telemetry?start_time&end_time`
- chi middleware: RequestID, Recoverer, Timeout, slog access logger; `/healthz` + `/readyz`

**Research flag:** Standard patterns. No phase research needed.

### Phase 5: Helm Packaging + Docker + Dynamic Scale Demo

**Rationale:** Packages all four binaries into deployable images. Dynamic scale demo validates partition assignment, graceful shutdown, and backpressure under k8s SIGTERM.

**Delivers:**
- Multi-stage Dockerfiles per service
- Helm umbrella chart: broker=`StatefulSet`+`volumeClaimTemplates`(RWO); streamer/collector/API=`Deployment`; Postgres subchart; PVCs
- `terminationGracePeriodSeconds` configured
- `make kind` + `make helm` smoke test on kind
- Dynamic scale demo: `kubectl scale` to 10 streamers and 10 collectors; verify no loss + graceful SIGTERM

**Avoids:** P7, P10, P11

**Research flag:** Standard patterns. No phase research needed.

### Phase 6: Prometheus Metrics + Performance Harness

**Rationale:** Metrics are prerequisite for the harness. The harness is the marquee bonus; requires the full pipeline deployed and correct.

**Delivers:**
- `promauto` + `promhttp.Handler()` on `/metrics` per service
- Broker: `fsync_latency_seconds` histogram, `partition_offset_max` gauge, `messages_persisted_total` counter
- Collector: `upsert_latency_seconds` histogram, `offset_committed_total` counter, derived consumer lag
- Streamer: `produced_total` counter, `produce_latency_seconds` histogram
- API: `requests_total` counter, `query_latency_seconds` histogram
- Perf harness: (10,2)/(2,10)/(5,5) × 5-min steady-state after 60s warmup; report throughput (msg/s + MB/s), end-to-end latency (p50/p95/p99 via `produce_ns` stamped in message), broker queue depth, consumer lag, broker RSS trend
- Correctness assertion alongside perf: Postgres row count == messages produced
- Written comparison table + analysis in README

**Avoids:** P17 (warmup discarded; percentiles not mean; throughput reconciled with consumer lag)

**Research flag:** Standard patterns. No phase research needed.

### Phase 7: Integration Tests + Documentation

**Rationale:** Bonus phase that cements no-loss + ordering claims and completes mandatory documentation.

**Delivers:**
- System/integration tests: spin broker + streamer + collector + Postgres, produce N messages, restart broker mid-stream, assert zero loss and correct per-GPU ordering
- README: architecture + design writeup, build/packaging, install workflow, sample user workflow, perf comparison table
- AI-usage doc: exact prompts used, where they fell short, manual interventions (maintain PROMPT_HISTORY during earlier phases)

**Research flag:** Standard. No phase research needed.

### Phase Ordering Rationale

- Broker segment log first: highest risk, root dependency, everything else's durability guarantee is only as strong as the log.
- Client lib before app services: `mq/client` is the only coupling point; once stable, streamer and collector are independent parallel tracks.
- Streamer and collector in parallel: share only Postgres schema and `mq/client` interface — no direct dependency on each other.
- API gateway after schema: reads Postgres, zero MQ dependency; can start as soon as schema is real.
- Helm packaging after all binaries: wraps artifacts that must exist.
- Perf harness last: exercises the assembled, correct, deployed system; benchmarking a broken broker produces meaningless numbers.
- ADR-0005 gated out-of-band: a decision, not a build task; costs nothing to unblock early, prevents multi-module rewrite later.

### Research Flags

**Needs deeper phase-specific research before coding:**
- **Phase 1 (Broker segment log):** Highest risk. Run `/gsd-plan-phase --research-phase 1` before planning. Research: group-commit channel design, CRC32c frame format, truncation state machine, directory fsync on segment creation, crash-recovery test harness.

**Standard patterns — skip phase research:**
- Phase 2 (gRPC + client lib), Phase 3a/3b (Streamer + Collector), Phase 4 (API Gateway), Phase 5 (Helm/Docker), Phase 6 (Metrics + Perf), Phase 7 (Integration tests + docs) — all well-documented patterns.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Versions verified against Go module proxy. Two open tooling decisions resolved with rationale. |
| Features | HIGH | Mandated set fixed by assignment + PROJECT.md. Perf harness methodology cross-checked against MQ benchmarking literature. |
| Architecture | HIGH on component boundaries + build order; MEDIUM on fsync/pgx specifics | ADRs 0001–0004 accepted; ADR-0005 is the one open variable. |
| Pitfalls | HIGH | Domain patterns (WAL, at-least-once, idempotency) well-established. Project-specific traps drawn from PROJECT.md/CONCERNS.md/TESTING.md. |

**Overall confidence: HIGH**

### Gaps to Address

- **ADR-0005 (canonical GPU identity):** Must be *Accepted* before Phase 1 code starts touching partition routing, and before Phase 3b collector schema. This is the only material open decision.
- **WAL depth decision:** PROJECT.md flags "full WAL vs bounded segment log" as TBD. Research recommends the bounded Kafka-lite segment log — confirm in the Phase 1 plan and document in ADR-0001 update or new ADR.
- **Postgres max_connections budget:** With up to 10 collectors each running a pgxpool, set collector `MaxConns` to 4–8 and verify `10 × MaxConns + API connections < Postgres max_connections` in Helm values before Phase 5.
- **Perf harness isolation:** Confirm kind resource allocation supports separate load-generator pod before Phase 6 to avoid confounding CPU measurements.

## Sources

### Primary (HIGH confidence)
- `proxy.golang.org/@latest` (Go module proxy) — pgx/v5 v5.10.0, chi/v5 v5.3.0, grpc v1.81.1, protobuf v1.36.11, client_golang v1.23.2, oapi-codegen/v2 v2.7.1
- `docs/adr/0001,0002,0004,0005`, `mq/proto/mqv1/mq.proto`, `.planning/codebase/ARCHITECTURE.md` — project-canonical
- `.planning/PROJECT.md`, `.planning/codebase/CONCERNS.md`, `.planning/codebase/TESTING.md` — primary project context

### Secondary (MEDIUM confidence)
- [SoftwareMill mqperf](https://softwaremill.com/mqperf/) — MQ benchmark methodology
- [Brave New Geek — Benchmarking Message Queue Latency](https://bravenewgeek.com/benchmarking-message-queue-latency/) — coordinated omission, HDR histograms
- [WarpStream — time-based consumer lag](https://www.warpstream.com/blog/the-kafka-metric-youre-not-using-stop-counting-messages-start-measuring-time)
- [Gold Lapel — pgx bulk insert benchmarks](https://goldlapel.com/grounds/go-postgres/pgx-bulk-insert-benchmarks) — CopyFrom vs SendBatch
- swaggo/swag GitHub releases — Swagger 2.0 limitation; swaggo v2/3.1 still RC June 2026
- hashicorp/raft-wal, fgrosse/wal, UnisonDB WAL write-up — group commit, CRC framing, truncation idioms
- Kubernetes StatefulSet docs; Spacelift StatefulSet vs Deployment comparison

---
*Research completed: 2026-06-24*
*Ready for roadmap: yes*
