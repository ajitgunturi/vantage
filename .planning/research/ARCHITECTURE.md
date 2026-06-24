# Architecture Research

**Domain:** Custom durable message queue + GPU telemetry pipeline (Go, gRPC, Postgres, k8s/Helm)
**Researched:** 2026-06-24
**Confidence:** HIGH on component boundaries / build order (derived from accepted ADRs + well-documented Kafka/NATS patterns); MEDIUM on the durability/fsync, pgx-upsert, and k8s-packaging specifics (cross-checked web).

> This document researches the **internal** architecture of each component and how they
> integrate. The high-level topology (Streamer → MQ broker → Collector → Postgres; API reads
> Postgres) is already locked by ADR-0001/0002/0004 and `mq/proto/mqv1/mq.proto`. Nothing here
> contradicts ADR-0001…0005; ADR-0005 (uuid identity) remains **Proposed** — every place it
> appears is flagged.

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                          APPLICATION LAYER (k8s pods)                          │
│   ┌────────────┐         ┌──────────────────┐          ┌──────────────┐        │
│   │ Streamer×N │ Produce │   MQ BROKER ×1   │ Consume  │ Collector×M  │        │
│   │ (Deploy)   │────────▶│  (StatefulSet)   │─────────▶│  (Deploy)    │        │
│   │ CSV loop   │ stream  │ partitioned log  │ srv-push │ parse+upsert │        │
│   │ re-stamp ts│◀────────│ offset assign    │◀─────────│ commit offset│        │
│   │ key=uuid   │  ack    │ group offsets    │  Commit  │ idempotent   │        │
│   └────────────┘         └────────┬─────────┘          └──────┬───────┘        │
│                                   │ append+fsync               │ INSERT…       │
│                          ┌────────┴─────────┐                  │ ON CONFLICT   │
│        ┌─────────────┐   │   PVC (RWO)      │                  ▼               │
│        │ API Gateway │   │  segment files   │          ┌──────────────┐        │
│        │ (Deploy)    │   │  + offset index  │          │  PostgreSQL  │        │
│        │ 3 REST eps  │   │  + group offsets │          │ (StatefulSet)│        │
│        │ reads PG    │───┼──────────────────┼─────────▶│ gpus+telemetry        │
│        └─────────────┘   └──────────────────┘  query   └──────────────┘        │
└──────────────────────────────────────────────────────────────────────────────┘
       Cross-cutting (every pod): /metrics (Prometheus) · /healthz+/readyz · slog · SIGTERM drain
```

**Data flow direction (single direction, no cycles):**
`CSV → Streamer → [Produce stream] → Broker → PVC log → [Consume push] → Collector → Postgres → [HTTP] → API → client`.
The only back-edges are control acks: Broker→Streamer `ProduceResponse(offset)` and Collector→Broker `Commit(offset)`. `mq` is imported by streamer/collector/apigateway; never the reverse (no import cycle).

### Component Responsibilities

| Component | Owns (boundary) | Talks to | Typical implementation (pattern borrowed) |
|-----------|-----------------|----------|-------------------------------------------|
| **MQ Broker** | Durable partitioned append-only log, per-partition monotonic offsets, group offset store, partition→consumer assignment, gRPC surface | ← Streamer (Produce), → Collector (Consume), ← Collector (Commit); PVC filesystem | Kafka-lite: per-partition segment dir, sparse offset index, group commit, fsync-on-ack (ADR-0001/0004) |
| **MQ Client lib** (`mq/client`) | Producer batching + partition routing (`hash(key)%N`); consumer poll/ack loop; reconnect/backoff | Broker over gRPC streams | Thin wrapper over generated stubs; the *only* dependency streamer/collector have on the broker |
| **Streamer** | Load CSV once, loop rows, re-stamp `ts=now`, route by `key=uuid`, rate control, backpressure-aware send | MQ client → Broker | Stateless producer; gRPC backpressure = natural flow control |
| **Collector** | Consume assigned partitions, parse DCGM row → telemetry struct, idempotent upsert, commit-after-persist | MQ client → Broker; pgxpool → Postgres | Stateless consumer; idempotency via `ON CONFLICT` (ADR-0002) |
| **API Gateway** | Thin read layer: 3 endpoints, time-window query, OpenAPI spec | pgxpool → Postgres (read-only) | `chi` router + `swaggo/swag` annotations; no MQ dependency |
| **Postgres** | `gpus` dimension + `telemetry` fact; range queries | written by Collector, read by API | Vanilla PG via Helm subchart; index on `(uuid, ts)` |

## Recommended Project Structure

The scaffold (`.planning/codebase/STRUCTURE.md`) already nails the layout. Internal package breakdown to confirm at build time:

```
mq/
├── broker/
│   ├── segment_log.go      # append + fsync + segment roll; one log dir per partition
│   ├── offset_index.go     # sparse <offset → file position> index; rebuild on boot
│   ├── partition.go        # owns a segment_log + writer mutex + nextOffset (monotonic)
│   ├── group_store.go      # consumer-group committed offsets, fsync'd separately
│   ├── assignment.go       # partition → consumer_id ownership (range/round-robin)
│   └── broker.go           # gRPC Broker impl: Produce/Consume/Commit/CreateTopic/Health
├── client/
│   ├── producer.go         # batching, hash(key)%N routing, send on Produce stream
│   └── consumer.go         # Consume stream loop, in-order handler, Commit timing
streamer/internal/{source,stream}      # CSV iter ; re-stamp+rate+produce
collector/internal/{consume,store}     # consume+parse ; pgxpool upsert
apigateway/internal/{api,store}        # chi handlers ; range queries
```

### Structure Rationale

- **`partition.go` as the concurrency unit:** fsync + offset assignment is *not* concurrent-safe, so each partition wraps its log behind one writer mutex (or a single owning goroutine + channel). Cross-partition writes proceed in parallel; intra-partition writes serialize → preserves per-uuid ordering (ADR-0005 partition-by-uuid). This is the single most important internal boundary.
- **`group_store.go` separate from the message log:** committed offsets must survive restart independently of message data (Kafka uses a dedicated `__consumer_offsets` topic; we use a small fsync'd file/segment per group). Keeping them separate avoids rewriting message segments on every commit.
- **`mq/client` is the integration seam:** streamer and collector depend *only* on `mq/client`, never on `mq/broker`. This lets the broker and the two app services be built and tested in parallel against the gRPC contract.

## Architectural Patterns

### Pattern 1: Per-partition append-only segment log with sparse index (Kafka-lite)

**What:** Each partition is an ordered directory of segment files named by base offset
(`00000000000000000000.seg`) plus a sparse offset index (`.segidx`). Append assigns the next
monotonic offset, writes a length-prefixed + CRC'd frame, and rolls to a new segment at a size
threshold. On boot, scan segments, validate the tail frame's CRC/magic, truncate any torn write,
and rebuild the in-memory `nextOffset` + index.

**When to use:** This is the core of the assignment — durable, ordered, restart-safe queue without an off-the-shelf broker (ADR-0001).

**Trade-offs:** (+) Real durability + ordering, strong talking point, O(1) append. (−) You own
recovery correctness — torn-write detection, segment roll, index rebuild all need unit tests.

**Example (record frame + recovery sketch):**
```go
// On-disk frame: [len:uint32][crc32:uint32][offset:int64][ts:int64][key,value...]
// Recovery: read frames until CRC mismatch or short read → that's the durable tail.
func (p *Partition) recover() error {
    for {
        rec, err := readFrame(p.tail)   // verifies len + CRC32 + magic trailer
        if errors.Is(err, errTorn) || errors.Is(err, io.ErrUnexpectedEOF) {
            return p.truncateTo(rec.endOffset) // drop the partial write; "written != finished"
        }
        if err == io.EOF { break }
        p.nextOffset = rec.offset + 1
        p.index.maybeAdd(rec.offset, rec.pos)
    }
    return nil
}
```

### Pattern 2: fsync-on-ack with optional group commit (durability ↔ throughput knob)

**What:** The broker `fsync`s the segment **before** replying `ProduceResponse(offset)`. That ack is
the at-least-once durability boundary. Under load, a single fsync per message is the bottleneck, so
batch: collect appends for a short window (e.g. `flush.ms`-style) or N messages, fsync once, then ack
the whole batch — group commit.

**When to use:** Always fsync before ack (correctness). Add group-commit batching only once the perf
harness shows fsync is the bottleneck under producers>consumers.

**Trade-offs:** (+) Tunable durability/throughput single knob; honest at-least-once. (−) Larger batch
window = higher tail latency; never disable fsync.

**Example:**
```go
func (p *Partition) appendAndAck(reqs []*ProduceRequest) []int64 {
    offs := p.appendAll(reqs) // serialized by partition writer
    p.file.Sync()             // ONE fsync for the batch (group commit)
    return offs               // now safe to ack all
}
```

### Pattern 3: Commit-after-persist consumer (at-least-once correctness boundary)

**What:** The collector consumes a message (or batch), **durably upserts to Postgres**, and only
*then* sends `Commit(partition, offset)`. If it crashes between persist and commit, the broker
redelivers; the idempotent upsert makes redelivery a no-op. This is the other half of at-least-once
(broker fsync-before-ack + collector commit-after-persist).

**When to use:** Always, for the collector. The proto comment on `CommitRequest` already encodes this
contract ("I have durably handled up to and including this offset").

**Trade-offs:** (+) No message loss, no duplicates at rest. (−) Possible in-flight redelivery (that's
the chosen model — exactly-once is explicitly out of scope per PROJECT.md).

### Pattern 4: Partition-by-key routing for per-GPU ordering

**What:** Producer routes with `partition = hash(key) % partitionCount`, `key = uuid`. All metrics for
one GPU land on one partition and stay ordered; partitions are the unit of consumer parallelism
(≥10 partitions so ≤10 collectors each own ≥1). Assignment maps partitions→`consumer_id` (range or
round-robin) on group join.

**When to use:** Core to the design (ADR-0005 *Proposed*; the `Message.key` proto field exists for
exactly this). If ADR-0005 flips, only the *routing key* changes, not the mechanism.

**Trade-offs:** (+) Cheap, deterministic ordering. (−) Hash skew can hot-spot a partition if uuid
distribution is uneven (247 uuids over ≥10 partitions is fine here).

## Data Flow

### Request Flow (telemetry path)

```
CSV row → Streamer: re-stamp ts=now, encode Message{key=uuid,value,ts}
   ↓ Produce(stream)
Broker: hash(uuid)%N → partition.append → fsync → ProduceResponse{partition,offset}
   ↓ Consume(group, push)            ↑ ack (at-least-once boundary #1)
Collector: parse → UpsertGPU + UpsertTelemetry (ON CONFLICT)
   ↓ Commit(partition, offset)       ↑ commit AFTER persist (boundary #2)
Broker: group_store.persist(offset)
   ─────────────────────────────────────────────────────
API: GET /gpus → SELECT gpus ; GET /gpus/{uuid}/telemetry?start&end → SELECT … WHERE uuid AND ts BETWEEN … ORDER BY ts
```

### State Management

| State | Owner | Durability |
|-------|-------|-----------|
| Per-partition next offset + message log | Broker (segment files) | fsync'd; rebuilt from segments on boot |
| Consumer-group committed offsets | Broker (`group_store`) | fsync'd separately; survives restart |
| Streamer position | none (stateless; re-reads CSV each cycle) | n/a |
| Collector dedup | none in-process; Postgres upsert key is the dedup | Postgres |
| API | read-only | n/a |

### Key Data Flows

1. **Produce/ack:** streaming pipeline; offset returned per message; backpressure via gRPC flow control (slow broker → blocked `Send` → streamer slows — this is the backpressure mechanism, prevents OOM).
2. **Consume/commit:** server-push to assigned partitions; commit lags persist by design.
3. **Recovery:** on broker restart, replay segments → rebuild offsets → resume; uncommitted messages redeliver to consumers.

## Data Model (Postgres)

```sql
-- dimension
CREATE TABLE gpus (
  uuid       TEXT PRIMARY KEY,           -- canonical id (ADR-0005, Proposed)
  gpu_id     INT,                        -- 0–7, unique only within host
  hostname   TEXT,
  device     TEXT,
  model_name TEXT,
  UNIQUE (hostname, gpu_id)              -- alt lookup; ADR-0005 fallback if uuid is rejected
);
-- fact
CREATE TABLE telemetry (
  uuid        TEXT NOT NULL REFERENCES gpus(uuid),
  metric_name TEXT NOT NULL,
  value       DOUBLE PRECISION,
  ts          TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (uuid, metric_name, ts)    -- natural key = idempotency key
);
CREATE INDEX telemetry_uuid_ts ON telemetry (uuid, ts);  -- the time-window query path
```

- **Idempotency key = PK `(uuid, metric_name, ts)`** — exactly the upsert conflict target (ADR-0002).
- **`(uuid, ts)` index** serves `WHERE uuid=? AND ts BETWEEN ? AND ? ORDER BY ts`.
- **Upsert strategy (MEDIUM):** for the collector's batch sizes, `pgx` `SendBatch` with per-row
  `INSERT … ON CONFLICT DO UPDATE` is simplest and gives per-row diagnostics. If the perf harness shows
  write-amplification, switch the hot path to `CopyFrom` into an `UNLOGGED` staging temp table then
  `INSERT … SELECT … ON CONFLICT` (benchmarks: ~24ms vs ~110ms per 5k rows). Don't pre-optimize — start with `SendBatch`.
- **Pool sizing (MEDIUM):** `pgxpool` `MaxConns ≈ (cores*2)+storage`; each PG connection is an OS
  process, so over-sizing the pool across 10 collectors thrashes the DB — cap total conns deliberately.

## Build Order (dependency-ordered)

Derived from the import graph (`mq` ← everyone) and the at-least-once correctness boundaries. Each
stage is independently testable (TDD) before the next depends on it.

```
0. Service skeletons (compile-only mains)          ── STATE.md "next" step; unblocks everything
   │
1. mq/broker — segment_log + offset_index          ── HARDEST + highest risk; the heart (ADR-0001)
   │   (append, fsync, segment roll, CRC/torn-write recovery, rebuild on boot)
   │   + partition.go (per-partition writer mutex, monotonic offset)
   │   + group_store.go (durable group offsets)
   │   ▼ unit tests: ordering, crash recovery, partial-write truncation
2. mq/broker gRPC surface + mq/client lib          ── wire Produce/Consume/Commit/CreateTopic/Health
   │   (partition routing hash(key)%N; consumer poll+ack loop; reconnect/backoff)
   │   ▼ integration test: produce→fsync→consume→commit→restart→no loss
   ├──────────────┬───────────────────────────────── client lib unblocks BOTH in parallel ↓
3a. streamer      3b. collector                     ── BUILD IN PARALLEL (both depend only on mq/client)
    CSV loop,         consume+parse+idempotent
    re-stamp,         upsert (ON CONFLICT),
    rate, key=uuid    commit-after-persist
    ▼ needs Postgres schema for 3b → land schema with collector
   │
4. apigateway                                       ── depends on Postgres schema (3b), NOT on the MQ
   │   3 endpoints + time-window query + OpenAPI gen (swag)
   │
5. Helm packaging                                   ── needs all 4 binaries + Dockerfiles
   │   broker=StatefulSet+PVC(RWO); streamer/collector/api=Deployment; Postgres subchart
   │
6. Perf harness                                     ── needs the full pipeline deployed
       (10,2)/(2,10)/(5,5): throughput, e2e latency, queue depth, consumer lag, CPU/mem
```

**Why this order:**
- **Broker log first** because it carries all the durability risk and *everything* imports the contract behind it. If the log is wrong, nothing downstream matters (PROJECT.md Core Value).
- **Client lib before app services** because it is the *only* coupling point — once it exists, streamer and collector are independent parallel tracks.
- **Postgres schema rides with the collector** (collector is the only writer); API only reads, so it
  comes after the schema is real.
- **Helm last among code** — it packages binaries that must already exist; **perf harness dead last**
  because it exercises the assembled system.

## Scaling Considerations

| Scale | Architecture adjustments |
|-------|--------------------------|
| 1 streamer / 1 collector | Single partition fine; no group rebalance; baseline correctness. |
| ≤10 / ≤10 (exercise ceiling) | ≥10 partitions so every collector owns ≥1; partition-by-uuid keeps ordering; group-commit fsync batching to keep broker throughput up; cap total pgx connections. |
| Beyond (out of scope) | Would need multi-broker + replication (Raft, à la Redpanda); explicitly out of scope. |

### Scaling Priorities (what breaks first)

1. **Broker fsync throughput** under producers>consumers → add group-commit batching (Pattern 2).
2. **Postgres write amplification** under many collectors → switch upsert to CopyFrom+staging (above).
3. **PVC fill** under sustained backpressure → bounded by gRPC flow control slowing producers; monitor `broker_partition_offset_max` / queue depth in the harness.

## Anti-Patterns

### Anti-Pattern 1: Postgres- or KV-backed queue
**What people do:** Back the "custom MQ" with Postgres/bbolt to dodge writing the log.
**Why it's wrong:** Defeats the entire exercise (PROJECT.md Out of Scope, ADR-0001) and couples queue throughput to the DB.
**Do this instead:** Hand-roll the append-only segment log; reserve Postgres strictly for collector telemetry.

### Anti-Pattern 2: Commit offset before the upsert is durable
**What people do:** Ack/commit on receive, then write to Postgres.
**Why it's wrong:** A crash after commit but before write loses data — breaks at-least-once.
**Do this instead:** Commit-after-persist (Pattern 3); rely on the idempotent PK to absorb redelivery.

### Anti-Pattern 3: Concurrent appends to one partition / ordering loss
**What people do:** Fan multiple goroutines onto one partition's file, or round-robin a uuid across partitions.
**Why it's wrong:** fsync+offset assignment isn't concurrent-safe → torn offsets; per-GPU timeline scrambles.
**Do this instead:** One writer per partition (mutex or owning goroutine); always `partition = hash(uuid)%N`.

### Anti-Pattern 4: Broker as a Deployment with a shared/RWO PVC mishandled
**What people do:** Run the broker as a `Deployment` with a `ReadWriteOnce` PVC.
**Why it's wrong:** Deployments roll pods with overlap; two pods can't both mount an RWO volume → stuck/`Multi-Attach` errors; identity churns.
**Do this instead (MEDIUM):** Broker = `StatefulSet` with `volumeClaimTemplates` (RWO per pod, stable identity, volume survives rescheduling). Streamer/collector/API stay `Deployment` (stateless).

## Integration Points

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| Streamer ↔ Broker | gRPC `Produce` (bidi stream) | backpressure = flow control; ack carries offset |
| Collector ↔ Broker | gRPC `Consume` (server stream) + `Commit` (unary) | push delivery; commit lags persist |
| Collector → Postgres | pgxpool, `ON CONFLICT` upsert | idempotency boundary |
| API → Postgres | pgxpool, read-only | no MQ coupling; can ship independently after schema |
| streamer/collector → `mq/client` | Go import | the only dependency on the broker module |

### External Services / Infra

| Service | Integration pattern | Notes |
|---------|---------------------|-------|
| PostgreSQL | Helm subchart (vanilla PG) | StatefulSet; schema init by collector or migration step |
| PVC (broker log) | `volumeClaimTemplates`, RWO | scaling down does NOT delete the volume (data safety) |
| Prometheus | `/metrics` per pod | broker fsync latency + queue depth; collector lag; streamer throughput |
| Kubernetes lifecycle | `/healthz`+`/readyz`, SIGTERM drain | broker must finish in-flight fsync + flush group offsets before exit |

## Cross-Cutting Concerns

- **Graceful shutdown:** trap SIGTERM; broker drains in-flight appends, fsyncs, flushes group offsets, then closes streams; collectors finish current batch + commit before exit. Set k8s `terminationGracePeriodSeconds` accordingly.
- **Config:** env-var driven per service (`BROKER_LISTEN_ADDR`, `LOG_DIR`, `PARTITION_COUNT`; `MQ_BROKER_ADDR`, `MQ_TOPIC`, `MQ_GROUP`, `DATABASE_URL`); injected via Helm `values.yaml` → ConfigMap/Secret.
- **Metrics:** Prometheus client per service — broker (`fsync_latency`, `partition_offset_max`, `messages_persisted_total`), collector (`upsert_latency`, `offset_committed_total`, derive lag from offset_max − committed), streamer (`produced_total`, `produce_latency`), API (`requests_total`, `query_latency`).
- **Logging:** `slog` structured, context keys `uuid/partition/offset/latency`.

## Sources

- [Unix's fsync(), write ahead logs, and durability vs integrity](https://utcc.utoronto.ca/~cks/space/blog/tech/FsyncDurabilityVsIntegrity) — MEDIUM
- [Building a Corruption-Proof Write-Ahead Log in Go (UnisonDB)](https://unisondb.io/blog/building-corruption-proof-write-ahead-log-in-go/) — MEDIUM
- [hashicorp/raft-wal (Go WAL, SyncDelay/group commit)](https://pkg.go.dev/github.com/hashicorp/raft-wal) — MEDIUM
- [fgrosse/wal — Go write-ahead log](https://github.com/fgrosse/wal) — MEDIUM
- [StatefulSets — Kubernetes docs](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/) — MEDIUM
- [StatefulSet vs Deployment (Spacelift)](https://spacelift.io/blog/statefulset-vs-deployment) — MEDIUM
- [pgx Bulk Insert Showdown: CopyFrom vs SendBatch vs Multi-Row INSERT (Gold Lapel)](https://goldlapel.com/grounds/go-postgres/pgx-bulk-insert-benchmarks) — MEDIUM
- [High-Performance Connection Pooling with pgxpool (Medium)](https://medium.com/@linz07m/high-performance-connection-pooling-with-pgxpool-5a4bfc73f15c) — MEDIUM
- Internal: `docs/adr/0001,0002,0004,0005`, `mq/proto/mqv1/mq.proto`, `.planning/codebase/ARCHITECTURE.md` — HIGH (project-canonical)

---
*Architecture research for: custom durable MQ + GPU telemetry pipeline (vantage)*
*Researched: 2026-06-24*
