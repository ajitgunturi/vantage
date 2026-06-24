# Domain Pitfalls

**Domain:** Custom durable message queue + idempotent GPU-telemetry pipeline in Go, on Kubernetes
**Researched:** 2026-06-24
**Overall confidence:** HIGH (domain patterns are well-established; project-specific traps drawn from PROJECT.md / CONCERNS.md / TESTING.md)

> Scope note: this is a greenfield *logic* build on a *locked, scaffolded* stack. The stack is not the risk — the hand-built durable log, the at-least-once + idempotency contract, backpressure, Go concurrency, and the k8s packaging are. Pitfalls below are ordered by blast radius. Each carries warning signs, prevention, and the phase that must own it.
>
> Phase names are referenced generically (Broker / Client / Collector / Streamer / API / Helm-Deploy / Perf) so the roadmap can map them to whatever numbering it lands on. The single most important item is **P0 (ADR-0005)** — it is a *schema-freezing blocker* that gates the Collector, API, and MQ-partition-key phases.

---

## Critical Pitfalls

Mistakes that cause rewrites, silent data loss, or "the MQ failed the exercise."

### P0: Freezing schema / API / partition key before ADR-0005 (GPU id) is Accepted
**Phase to own:** BEFORE Collector and API phases — a gating decision, not a build task.
**What goes wrong:** ADR-0005 (canonical GPU id = `uuid`) is still *Proposed*. The `{id}` in `GET /api/v1/gpus/{id}/telemetry`, the Postgres PK / unique constraint, and the MQ partition key for GPU messages all resolve to this choice. If code lands on `uuid` and the project later pivots to `hostname:gpu_id` (or exposes the human-friendly `gpu_id` 0–7 in the API), you rewrite the schema, the upsert, partition routing, and every lookup.
**Why it happens:** The dataset has *both* a globally-unique `uuid` (247 distinct) and a host-local `gpu_id` (0–7, only unique within a host). It is tempting to route `{id}` to the short `gpu_id` for ergonomics. They are not interchangeable.
**Consequences:** Multi-module rewrite (`collector/` schema+upsert, `apigateway/` routing, `mq/` partition key) once persistence code exists.
**Prevention:** Confirm ADR-0005 → *Accepted* (uuid as canonical identity; `gpu_id`/host carried as GPU *dimensions*, not identity) **before** the first Collector schema line. If the API must accept a friendly id, decide explicitly now whether `{id}` is the uuid or a `host:gpu_id` lookup that *resolves to* uuid internally — and document it.
**Detection / warning sign:** A PR touching `collector/` schema or `apigateway/` routing merges while ADR-0005 still reads *Proposed*. Treat that as a stop-the-line.

### P1: Committing/acking offsets before the message is durably persisted (message loss)
**Phase to own:** Broker + Client (delivery semantics).
**What goes wrong:** Consumer acks or the offset advances *before* the collector's Postgres write commits (or before the broker fsyncs the record). A crash in the window loses messages the broker believes were delivered — and at-least-once silently becomes at-most-once. For the MQ this is the *one* failure that fails the exercise.
**Why it happens:** Natural code ordering ("read → ack → process") and async commit loops that advance offsets on a timer regardless of downstream progress.
**Prevention:** Enforce strict ordering everywhere: **persist, then advance offset.** Broker: a record is "committed" only after fsync (or group-commit fsync) returns. Consumer: the collector commits the offset only *after* the Postgres upsert transaction commits. Make the offset store itself durable (survives broker restart) and commit it after, never before, the data.
**Detection:** Crash-injection test — kill the consumer between Postgres commit and offset commit; on restart the message must be reprocessed (a dup, absorbed by idempotency), never skipped. Track "delivered offset" vs "persisted offset" as metrics; they must never invert.

### P2: Idempotency that isn't — missing unique constraint on `(uuid, metric_name, ts)`
**Phase to own:** Collector (schema + upsert).
**What goes wrong:** At-least-once *will* redeliver. If the `telemetry` table has no UNIQUE constraint / PK on `(uuid, metric_name, ts)`, `ON CONFLICT` has nothing to conflict on — duplicates insert silently and row counts drift, or naive INSERTs error on retry and stall the consumer. Idempotency is a *database* guarantee here, not application logic.
**Why it happens:** Schema written for the happy path; the constraint is treated as an optimization rather than the correctness anchor.
**Prevention:** Define `UNIQUE (uuid, metric_name, ts)` (or PK) *before* TDD-ing the upsert. Use `INSERT ... ON CONFLICT (uuid, metric_name, ts) DO UPDATE/ DO NOTHING`. Decide DO UPDATE vs DO NOTHING deliberately: since we *re-stamp* timestamps at stream time, the same `(uuid, metric, ts)` should carry the same value on redelivery → DO NOTHING is the cleaner idempotent choice; DO UPDATE is fine but must be value-stable. Keep `value` NOT NULL so retries can't make the conflict ambiguous.
**Detection:** Test: send identical message twice → exactly one row. Send 1000 messages with 30% duplicates → exactly the distinct count lands. [Verified — MEDIUM: pre-PG17 COPY can't do conflict handling, so batch via `INSERT ... ON CONFLICT` / UNNEST, not COPY, if you batch.]

### P3: Torn writes + no crash-recovery test on the segment log
**Phase to own:** Broker (durable log + recovery).
**What goes wrong:** Broker (or kernel/node) crashes mid-write. The last record is half-written (torn at sector granularity, 512B/4KB). On restart, naive index rebuild reads garbage as a valid record, or panics, or — worse — silently serves a corrupt frame. Without a crash-recovery test, this is discovered in the perf harness or the demo, not in CI.
**Why it happens:** fsync is the durability barrier and writes can reorder without it; a partially-flushed tail looks like data. [Verified — MEDIUM]
**Prevention:** Length-prefixed, **CRC-checksummed records.** Recovery is a deterministic state machine: read records sequentially, validating CRC + length, until the first short read or CRC failure, then **truncate the tail** to the last good record and resume appending. fsync the file after committed batches, and fsync the *directory* when creating a new segment so the file's existence is durable. Never trust the tail of a segment after a crash without re-validating it.
**Detection (mandatory test):** Append N records, `kill -9` mid-write (or write a deliberately truncated tail to a fixture segment), restart, assert: every fsync'd record recovers, the torn tail is dropped, and no committed record is lost or duplicated. This test is non-negotiable for the "durable" claim.

### P4: fsync mis-tuned — either no durability or no throughput
**Phase to own:** Broker (write path) — revisit in Perf.
**What goes wrong:** Two opposite failures. (a) Never/rarely fsync → "durable" is a lie; a crash loses the page cache. (b) fsync *per record* → throughput collapses (a few hundred-thousand syncs/sec is impossible on real disks) and the perf harness shows the broker as the bottleneck. CONCERNS.md flags fsync batching policy as explicitly TBD.
**Why it happens:** fsync semantics are subtle; the easy correct-looking choice (sync every write) is the slow one. [Verified — MEDIUM: fsync-per-op maximizes durability but kills throughput.]
**Prevention:** **Group commit.** Batch appends and fsync once per batch / per small interval, returning "committed" to producers only after the batch fsync. Make the durability window explicit and documented (e.g., "≤ N ms or ≤ M records may be lost on power-loss"). This is the legitimate durability-vs-throughput knob and it is also a *perf story* for the README. Tie the producer ack to the post-fsync point so P1 still holds.
**Detection:** Perf harness with producers>consumers should show throughput scaling with batch size, and broker disk-fsync rate bounded (not 1:1 with messages). If throughput is flat and CPU is in `fsync`, the batch policy is wrong.

### P5: Unbounded in-memory buffers → broker OOM when producers > consumers
**Phase to own:** Client (flow control) + Broker (intake) — proven in Perf.
**What goes wrong:** The whole point of the (10,2) scenario is producers outrunning consumers. If the broker buffers undelivered messages *in memory* (or the client has an unbounded send queue), memory grows until the broker is OOMKilled — the exact opposite of the requirement ("grows the on-disk log rather than OOM-ing"). Under k8s this is a sudden pod restart mid-test.
**Why it happens:** Easiest gRPC streaming implementation reads as fast as the producer sends into an unbounded channel.
**Prevention:** Backpressure by design. The durable log on the PVC *is* the buffer — append to disk, don't pile up in RAM. Bound every in-memory queue (channels with capacity); when full, **block the producer** (gRPC stream stops reading / flow-control window closes) rather than allocate. Consumers pull from disk by offset, not from a memory backlog. The broker's steady-state memory must be O(active segments + index), not O(undelivered messages).
**Detection:** Run (10,2) and watch broker RSS — it must plateau while the on-disk log grows. Warning sign: broker memory tracks queue depth linearly. Set a k8s memory limit deliberately *low* in a test to confirm you get backpressure, not OOMKill.

---

## Moderate Pitfalls

Mistakes that cause flaky tests, slow queries, or operational pain — recoverable but costly.

### P6: Data races on the log / offset store; goroutine leaks
**Phase to own:** Broker + Client (concurrency).
**What goes wrong:** Concurrent appends, index reads during rebuild, and offset commits race. `-race` (already in `make test`) catches the obvious ones, but logical races (two consumers in a group double-owning a partition; offset map mutated without a lock) slip through. Per-connection goroutines that never exit on stream close leak until the broker dies.
**Prevention:** Single-writer discipline for the append path (one goroutine owns each partition's tail; others enqueue). Guard the offset map with a mutex or route through a channel. Every `go func()` gets a clear exit condition tied to context cancellation. Keep `-race` green and add a test that opens/closes many streams and asserts goroutine count returns to baseline (`runtime.NumGoroutine`).
**Detection:** `-race` failures; growing goroutine count under churn; `make cover-logic` (100% branch) forcing you to exercise the contended branches.

### P7: No graceful shutdown — buffered messages / uncommitted offsets lost on SIGTERM
**Phase to own:** Every service `cmd/` (cross-cutting) — flagged in CONCERNS.md as not-yet-addressed.
**What goes wrong:** k8s sends SIGTERM on rollout/scale/evict. If services exit immediately, the broker loses un-fsync'd batches, the collector loses an in-flight transaction, the streamer drops un-acked produces. Looks like random message loss during deploys.
**Prevention:** Signal-driven `context` cancellation propagated to every goroutine. On SIGTERM: stop accepting new work, flush+fsync the log, commit pending offsets, drain in-flight Postgres txns, then exit. Honor `terminationGracePeriodSeconds`. The producer ack point (P1) must already be crash-safe so even a *hard* kill is correct; graceful shutdown is the clean path.
**Detection:** Shutdown test — produce under load, SIGTERM the broker, restart, assert no committed message lost. Warning sign: message-loss only during rollouts.

### P8: Context cancellation not propagated through gRPC streams
**Phase to own:** Client + Broker.
**What goes wrong:** A producer timeout or consumer disconnect doesn't cancel the server-side stream handler; the broker keeps a goroutine + buffer alive for a dead peer. Compounds P5 and P6 under the 10×10 churn.
**Prevention:** Thread the stream's `ctx` into every blocking op (channel send/recv, disk wait, DB call) with `select { case <-ctx.Done(): ... }`. Return promptly when the stream context is cancelled; release buffers and offsets-in-flight.
**Detection:** Kill a consumer mid-stream; broker goroutines/buffers for that stream must drop to zero.

### P9: Postgres — no index for time-window queries; connection-pool exhaustion under 10 collectors
**Phase to own:** Collector (schema) + Collector/API (pooling).
**What goes wrong:** `GET /gpus/{id}/telemetry?start_time&end_time` does a seq scan without an index on `(uuid, ts)` → slow as `telemetry` grows. Separately, 10 collectors each opening a large pgxpool can exceed Postgres `max_connections` (every PG connection is a full OS process) → `too many clients` errors and stalled ingest.
**Why it happens:** The unique constraint on `(uuid, metric_name, ts)` covers idempotency but range-scans by `(uuid, ts)` may want a dedicated/leading-column index; pool defaults are per-instance and don't know about the other 9.
**Prevention:** Add a B-tree index supporting `(uuid, ts)` range scans (the `(uuid, metric_name, ts)` unique index already leads with uuid — confirm it serves the query plan, else add `(uuid, ts)`). Size pgxpool modestly per collector (e.g. 4–8) and ensure `collectors × MaxConns < Postgres max_connections` with headroom for the API gateway. pgxpool default `max(4, NumCPU)` is *not* sized for fan-out. [Verified — MEDIUM]
**Detection:** `EXPLAIN` the telemetry query shows Index Scan, not Seq Scan. Load 10 collectors and watch for `FATAL: sorry, too many clients`. Warning sign: ingest throughput cliffs as collector count rises.

### P10: StatefulSet vs Deployment + PVC access mode for the broker
**Phase to own:** Helm-Deploy.
**What goes wrong:** Broker deployed as a `Deployment` with a shared RWO PVC → rollouts deadlock (new pod can't mount the volume the old pod holds), or you get two pods fighting over one log directory → corruption. RWO binds the PVC to a single node; you can't scale that broker horizontally by accident.
**Why it happens:** Deployment is the default reflex; PVC access modes are easy to get wrong.
**Prevention:** Run the stateful broker as a **StatefulSet** with `volumeClaimTemplates` (stable identity, stable per-replica PVC, ordered start) — even if there's a single broker, this is the correct primitive and documents the intent. Keep RWO (correct for single-writer-per-volume). If you ever shard brokers, each gets its *own* PVC via the template, never a shared RWO. Stateless services (streamer, collector, API) stay Deployments. [Verified — MEDIUM]
**Detection:** Rolling update on the broker hangs in `ContainerCreating` / `Multi-Attach error`. That's the RWO+Deployment smell.

### P11: Readiness gating + resource limits → OOMKilled / traffic to a not-ready broker
**Phase to own:** Helm-Deploy.
**What goes wrong:** (a) No readiness probe, or one that's ready before the segment-log index is rebuilt on restart → consumers connect and get errors during recovery. (b) Memory `limit` set too low for the broker's working set (segments + index + batch buffers) → OOMKilled under the perf load, masquerading as "the broker crashed." (c) `limit == request` with no headroom and a buffer spike → kill.
**Prevention:** Readiness probe returns ready only *after* recovery/index-rebuild completes (ties to P3/P7). Set memory `request`/`limit` from observed steady-state under the (10,2) load plus headroom for batch buffers; since buffers are bounded (P5), this is predictable. Set CPU requests so fsync/serialization isn't throttled. Liveness probe must *not* kill a broker that's slow due to legitimate recovery.
**Detection:** `kubectl get events` shows `OOMKilling` or readiness flaps during/after restart. Consumer errors immediately after a broker pod restart.

---

## Minor Pitfalls

Lower blast radius, but each has bitten projects like this.

### P12: Unbounded segment growth — no roll / no retention
**Phase to own:** Broker.
**What goes wrong:** Single ever-growing segment file → slow index rebuild, no way to reclaim disk, PVC fills and the broker wedges (no space to fsync).
**Prevention:** Roll segments at a size bound (e.g. 100MB per CONCERNS.md guidance). Name segments by base offset for O(log) lookup. Decide retention now (the exercise re-loops a 2,470-row CSV, so the log grows unboundedly over a long run) — at minimum, document it; ideally drop fully-consumed old segments.
**Detection:** PVC usage climbs without bound during a long perf run; index-rebuild time grows linearly with uptime.

### P13: Offset-index corruption / inconsistent rebuild
**Phase to own:** Broker (recovery).
**What goes wrong:** The in-memory offset→file-position index is rebuilt from segments on startup; if rebuild logic disagrees with the write path (off-by-one at segment boundaries, mid-record roll), reads return the wrong message or skip one.
**Prevention:** Derive the index *only* by replaying validated (CRC-checked) records — same parser as recovery (P3). Test roll at exact segment boundary and mid-record. Treat the segment files as the single source of truth; the index is a derived cache, always reconstructable.
**Detection:** Read-back test after roll: every appended offset resolves to its exact payload across segment boundaries.

### P14: Partition count below the consumer ceiling
**Phase to own:** Broker (partitioning) — constraint from PROJECT.md.
**What goes wrong:** Partitions are the unit of consumer parallelism. With fewer partitions than collectors, extra collectors sit idle (a consumer group can't put two consumers on one partition) → the (2,10) scenario can't actually use 10 consumers, and the perf table is misleading.
**Prevention:** Partition count **≥ 10** (PROJECT.md hard guidance) so each of up to 10 collectors can own ≥ 1 partition in steady state. Pick a stable partition key (the canonical GPU `uuid` per P0) so a given GPU's stream is ordered and consistently routed.
**Detection:** In (2,10), `consumers > partitions` ⇒ idle consumers; throughput doesn't rise past `partition` count.

### P15: Re-stamp vs replay timestamp confusion
**Phase to own:** Streamer.
**What goes wrong:** The CSV has 4 original source timestamps that are *irrelevant* — the design **re-stamps at stream time** (processing time = telemetry timestamp). If the streamer accidentally replays the CSV's `timestamp` column, every loop of the CSV reuses the same 4 timestamps → the idempotency key `(uuid, metric_name, ts)` collapses and `ON CONFLICT` silently discards almost everything as "duplicates," so the telemetry table barely grows and the pipeline looks broken-but-green.
**Why it happens:** The CSV *has* a `timestamp` column; using it is the obvious-but-wrong default.
**Prevention:** Streamer assigns `ts = time.Now()` (or a monotonic stream clock) at produce time and ignores the CSV timestamp. Make this explicit in code + a test asserting two loops of the same row produce *different* ts (and therefore two distinct rows downstream).
**Detection:** After running for a while, `SELECT count(*)` on telemetry is suspiciously close to 2,470 (one CSV pass) instead of growing per loop. That's replayed timestamps colliding on the idempotency key.

### P16: The fail-open coverage gate masking untested logic
**Phase to own:** Every logic phase (process discipline) — flagged in TESTING.md / CONCERNS.md.
**What goes wrong:** `coverage-gate.sh` and `logic-coverage.sh` exit 0 when no coverage profile / no `internal/*.go` exists ("fail-open until code"). CI is green and *feels* covered, but the gates are vacuously true until real logic lands. A PR that adds logic *and* skips wiring it into the tested package set can ride the grace period and ship uncovered.
**Prevention:** When the first logic package lands in a module, verify the gate actually *fired*: `make cover` reports a real number (not "no profile"), `make cover-check` enforces ≥90%, `make cover-logic` runs gobco at 100% branch. Add a checklist item to the first logic PR per module: "confirm gates are now active." TDD-first (per CLAUDE.md) makes this natural — the test exists before the code.
**Detection:** `make cover` prints "no profile" / "skip" *after* logic merged; coverage numbers don't move when you add code.

---

## Phase-Specific Warnings

| Phase Topic | Likely Pitfall | Mitigation |
|-------------|----------------|------------|
| **Pre-build gate** | P0 — schema/API/partition key frozen on unconfirmed ADR-0005 | Accept ADR-0005 (uuid canonical) before any Collector/API/partition code |
| **Broker — durable log** | P3 torn writes, P4 fsync tuning, P12 segment growth, P13 index rebuild | CRC+length frames; truncate-on-corrupt recovery; group-commit fsync; size-based roll; index derived only from validated records |
| **Broker — concurrency** | P6 races, P8 ctx propagation | single-writer append; mutex/channel offset store; ctx into every blocking op; `-race` + goroutine-leak test |
| **Client — delivery + flow control** | P1 offset-before-persist, P5 unbounded buffers | persist→then-advance offset; durable offset store; bounded channels that block producers; disk is the buffer |
| **Collector — persistence** | P2 missing unique constraint, P9 index/pool | `UNIQUE(uuid,metric_name,ts)` before upsert TDD; `ON CONFLICT DO NOTHING`; `(uuid,ts)` query index; bound pgxpool so 10×pool < max_connections |
| **Streamer** | P15 replay vs re-stamp | assign ts at produce time; test that two CSV loops yield distinct ts |
| **Partitioning** | P14 partitions < consumers | ≥10 partitions; partition key = canonical uuid |
| **All `cmd/` mains** | P7 graceful shutdown | SIGTERM → flush+fsync+commit-offsets+drain; honor grace period |
| **Helm-Deploy** | P10 StatefulSet/PVC, P11 readiness+limits | StatefulSet+volumeClaimTemplates(RWO) for broker; readiness gates on recovery-complete; mem limits from observed steady-state + headroom |
| **Perf harness** | P17 (below) measurement traps | warmup to steady state; report consumer lag + queue depth; isolate broker disk IO |
| **Every logic PR** | P16 fail-open gate | confirm coverage gates fired once logic lands |

### P17: Perf harness measurement traps
**Phase to own:** Perf.
**What goes wrong:** (a) Measuring throughput from cold start includes JIT-free Go warmup, pool fill, and segment pre-allocation → understated steady-state numbers. (b) Reporting throughput while *ignoring consumer lag* hides that the broker is just buffering, not delivering. (c) Co-locating broker disk with everything else means you measure noisy-neighbor IO, not broker capacity. (d) Comparing (10,2)/(2,10)/(5,5) without fixed duration/message budget makes the comparison table apples-to-oranges.
**Prevention:** Discard a warmup window; measure steady state. Report end-to-end latency **and** consumer lag **and** broker queue depth together (Prometheus metrics already required by PROJECT.md). Give the broker its own PVC/disk so disk IO is isolated. Fix the workload (same total messages or same wall-clock) across all three ratios. The fsync batch policy (P4) is a legitimate lever to showcase here.
**Detection:** Throughput numbers that don't reconcile with consumer lag (high throughput + growing lag = you're measuring intake, not delivery).

---

## Sources

- PostgreSQL upsert / pgxpool sizing / COPY-vs-INSERT batch ingest — built-in WebSearch, cross-checked across Tiger Data ingest benchmarks, pgx pkg.go.dev, and pgxpool pooling write-ups [MEDIUM, verified]
- fsync durability barrier, torn-write sector granularity, CRC + truncate-on-corrupt recovery state machine — built-in WebSearch, cross-checked (MIT 6.828 sync notes, WAL/corruption-proof-log write-ups) [MEDIUM, verified]
- StatefulSet vs Deployment, RWO PVC behavior, OOMKilled / readiness gating — built-in WebSearch [MEDIUM, verified]
- Project-specific traps (ADR-0005, re-stamp vs replay, fail-open coverage gate, 10-instance ceiling vs partition count) — `.planning/PROJECT.md`, `.planning/codebase/CONCERNS.md`, `.planning/codebase/TESTING.md` [HIGH — primary project sources]
- Delivery-semantics ordering (persist→ack), idempotency-as-DB-constraint, backpressure-via-disk, Go concurrency/shutdown patterns — established domain knowledge for durable logs / at-least-once pipelines [HIGH]
