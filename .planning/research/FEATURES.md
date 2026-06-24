# Feature Research

**Domain:** Custom durable message queue + elastic GPU telemetry pipeline (Go, k8s/Helm) — assignment deliverable
**Researched:** 2026-06-24
**Confidence:** HIGH (mandated set is fixed by the assignment + PROJECT.md; perf-harness methodology cross-checked against MQ benchmarking literature)

> Categorization legend for this project (assignment-specific, overrides generic "table-stakes/differentiator"
> framing): **MANDATED** = explicit assignment deliverable, missing it fails grading. **TABLE-STAKES** =
> not separately graded but required for the MQ to be *correct* under load (a durable MQ that loses or
> reorders messages fails the core value even if all endpoints exist). **DIFFERENTIATOR/BONUS** = called out
> as bonus or raises the quality ceiling. **ANTI-FEATURE** = deliberately excluded (scope discipline).

## Feature Landscape

### MANDATED — Assignment Deliverables (grading-critical)

Missing any of these = the deliverable is incomplete and fails grading regardless of code quality.

| Feature | Why Mandated | Complexity | Notes |
|---------|--------------|------------|-------|
| `GET /api/v1/gpus` — list all GPUs | Explicit endpoint #1 | LOW | `SELECT` distinct GPUs from `gpus` dimension table; return uuid + hostname + gpu_id + model_name. Identity = `uuid` (ADR-0005). |
| `GET /api/v1/gpus/{id}/telemetry` — telemetry for one GPU, ordered by time | Explicit endpoint #2 | LOW | `WHERE uuid=? ORDER BY ts`. Ordering is a stated requirement, not optional. `{id}` = uuid pending ADR-0005 freeze. |
| Time-window filter `?start_time=&end_time=` on telemetry endpoint | Explicit endpoint #3 (often phrased as a variant of #2) | LOW | `ts BETWEEN ? AND ?`. Validate `start < end`; decide inclusive/exclusive + timestamp format (RFC3339 vs epoch) early — see dependencies. |
| Custom MQ — produce path | Core value; "custom" is the central grading criterion | HIGH | gRPC streaming `Produce`; broker assigns monotonic per-partition offset, fsync, acks. No off-the-shelf broker, no DB-backed queue. |
| Custom MQ — consume path | Core value | HIGH | gRPC server-push `Consume` for a consumer group; `Commit` offset after durable persist. |
| Streamer loops CSV, **re-stamps** timestamps, produces | Explicit deliverable; re-stamp is a stated rule | MEDIUM | Loop DCGM CSV continuously; `ts = now()` at stream time (NOT original CSV ts); key = uuid for per-GPU ordering. |
| Collector consumes → parses → persists to PostgreSQL | Explicit deliverable | MEDIUM | Parse message → telemetry struct; upsert into Postgres. Idempotency is table-stakes (below), persistence itself is mandated. |
| Dynamic scale up/down of streamers & collectors (≤10 each) | Explicit "elastic/scalable" requirement | MEDIUM | Achieved via k8s replica count + partition assignment. Scale ceiling = 10; partitions must be ≥10 so 10 collectors can each own ≥1. |
| Helm deploy to Kubernetes | Explicit deliverable ("deployable via Helm") | MEDIUM | Umbrella chart + per-service subcharts + Postgres + PVCs; runs on kind locally. |
| Auto-generated OpenAPI (Swagger) via a Makefile target | Explicit deliverable — must be *generated*, not hand-written | LOW | `make openapi` (or similar) runs `swaggo/swag` or `oapi-codegen`. "Auto-generated via Makefile" is the literal requirement. |
| Unit tests + coverage via Makefile | Explicit deliverable + grading criterion | MEDIUM | `make test` / `make cover` / `make cover-check`. Gates already wired: 90% line, 100% branch on logic. TDD for MQ log, collector upserts, API handlers. |
| README (architecture/design, build, install, sample workflow) + AI-usage doc | Explicit deliverable | LOW | AI-usage doc must include *exact prompts, where they fell short, and manual interventions* — keep PROMPT_HISTORY as you go, don't reconstruct at the end. |
| Dockerfiles per service | Implied by "deployable via Helm/k8s"; images must exist | LOW | One per service; multi-stage Go build. |

### TABLE-STAKES — Required for a *correct* durable MQ (not separately graded, but failure here fails the core value)

The assignment's core value is an MQ that does **not lose or corrupt messages under load**. These make that true.
They are not "bonus" — a broker missing these is incorrect, not merely less polished.

| Feature | Why Required | Complexity | Notes |
|---------|--------------|------------|-------|
| Durability across broker restart | Stated hard requirement ("survives broker restart, no message loss") | HIGH | Append-only segment log on PVC, fsync before ack; rebuild offset index from segments on startup (ADR-0001). This is the load-bearing design. |
| At-least-once delivery | Chosen correctness model (PROJECT.md) | MEDIUM | Ack only after fsync; redeliver uncommitted on consumer failure. Pairs with idempotent collector. |
| Consumer-group offsets + acks | Needed for at-least-once + scale-out without double-processing | HIGH | Per-(group, partition) committed offset, durable across restart. `Commit` RPC after persist. |
| Partitions as the unit of parallelism (≥10) | Without partitions, collectors can't scale past 1 | HIGH | Key = uuid → hash → partition; all of one GPU's metrics land on one partition (preserves ordering). Count ≥10 enables 10 collectors. |
| Idempotent consumer upsert | At-least-once *requires* this or duplicates corrupt data | MEDIUM | `INSERT ... ON CONFLICT (uuid, metric_name, ts) DO UPDATE`. Makes at-least-once + Postgres = effectively exactly-once at rest. |
| Backpressure / flow control | Producers > consumers must grow the on-disk log, not OOM the broker | MEDIUM | gRPC streaming flow control (client blocks when broker can't keep up); log grows on PVC. Validated by the (10,2) perf regime. |
| Graceful shutdown | Idiomatic-Go grading criterion; avoids partial writes / lost acks | MEDIUM | Drain in-flight, fsync, close streams on SIGTERM (k8s sends it on scale-down). Critical because scale-down is a mandated operation. |
| Health / readiness probes | k8s won't route to / restart pods correctly without them | LOW | `Health()` RPC + HTTP `/healthz` `/readyz`; broker readiness gates on log recovery complete. |
| Per-GPU ordering preserved | API time-window queries assume monotonic timeline per GPU | MEDIUM | Falls out of "key=uuid → one partition + monotonic offset." Cross-partition global order is NOT needed (and not provided). |

### DIFFERENTIATORS / BONUS (raise the ceiling; explicitly bonus or quality-signaling)

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Performance-characterization harness (ratios 10:2, 2:10, 5:5) | Directly demonstrates the MQ's core value under the 3 stress regimes; explicit bonus-grade analysis deliverable | HIGH | Drive each `(producers, consumers)` ratio; collect metrics; emit a comparison table + written analysis. See "Performance Harness" section for exact metrics + methodology. |
| Prometheus metrics on every component | Feeds the perf harness; signals production maturity; locked lib | MEDIUM | Prereq for measuring throughput/lag/queue-depth cheaply. Per-component counters/histograms already enumerated in ARCHITECTURE.md. |
| Grafana dashboards | Visualizes the perf story; nice-to-have on top of Prometheus | LOW–MEDIUM | Pure presentation once Prometheus is exporting; one dashboard JSON. Skip if time-boxed — metrics endpoint is the substance. |
| System / integration tests | Explicitly called out as bonus (unit tests mandated, system tests bonus) | MEDIUM | End-to-end: spin broker + streamer + collector + Postgres (testcontainers/kind), assert no loss + correct ordering across a restart. |
| Message compression | Throughput/PVC-footprint win under high produce rate | MEDIUM | Per-batch gzip/snappy on the wire and/or on disk. Bonus polish; adds CPU cost — measure, don't assume. |
| Retention / compaction | Bounds unbounded log growth over long runs | MEDIUM–HIGH | Time/size-based segment deletion, or key-compaction keeping latest per key. Interacts with offsets — only safe to delete below the min committed offset. |
| Dedup / "exactly-once-ish" producer idempotency | Reduces in-flight duplicates before they hit the collector | HIGH | Producer sequence numbers + broker dedup window. The idempotent upsert already covers correctness, so this is marginal — treat as stretch. |

### ANTI-FEATURES (deliberately NOT building — scope discipline)

| Feature | Why Tempting | Why Excluded | Instead |
|---------|--------------|--------------|---------|
| Off-the-shelf broker (Kafka / RabbitMQ / NATS / ZeroMQ) | "Why rebuild a queue?" | Defeats the assignment's central purpose; instant fail | Hand-built segment-log broker (ADR-0001/0004). |
| DB-backed queue (Postgres as the MQ) | Easy durability for free | Makes "custom MQ" dishonest; Postgres is reserved for collector data | Custom append-only log on PVC (ADR-0001/0002). |
| >10 streamers or >10 collectors | "Scale higher = more impressive" | Stated exercise ceiling; partition math is sized for 10 | Cap at 10 each; ≥10 partitions sized to the ceiling. |
| Exactly-once delivery | Sounds strictly better | High complexity for no marginal correctness here | At-least-once + idempotent upsert = effectively-once at rest. |
| Replaying original CSV timestamps | "Faithful to source data" | Breaks latency measurement; stated rule is to re-stamp | Re-stamp `ts = now()` at stream time (processing time = telemetry time). |
| Auth / authN-authZ on the API | "Production APIs need auth" | Out of exercise scope; all services are intra-cluster | No auth; note mTLS/JWT as future work in README only. |
| Cross-partition global ordering | "Total order is cleaner" | Kills parallelism; not required | Per-GPU (per-partition) order only — sufficient for the time-window API. |

## Feature Dependencies

```
Durable segment log (PVC + fsync + index rebuild)
    └──enables──> At-least-once delivery (ack-after-fsync)
                      └──enables──> Consumer-group offsets/acks
                                        └──enables──> Idempotent collector upsert (tolerates redelivery)

Partitions (key=uuid, count >=10)
    └──enables──> Collector scale-out (1..10) and Streamer scale-out (1..10)
    └──enables──> Per-GPU ordering (one GPU -> one partition)

Postgres schema (gpus + telemetry, key (uuid, metric_name, ts))
    └──required by──> Idempotent upsert  AND  all 3 REST endpoints

Prometheus metrics (per component)
    └──required by──> Performance harness (throughput / lag / queue-depth / CPU-mem)
                          └──requires──> Durability + backpressure ALREADY correct
                                          (else you benchmark a broken broker)

Graceful shutdown
    └──required by──> Dynamic scale-down (k8s SIGTERM on replica decrease)

Helm chart + Dockerfiles + PVCs
    └──required by──> Any multi-instance scale test AND the perf harness

OpenAPI auto-gen (make target)  ──depends on──> API handler annotations/contract
Backpressure  ──conflicts-if-missing──> Retention (without one of them, log grows unbounded)
```

### Dependency Notes (load-bearing ordering for the roadmap)

- **Durability is the root.** At-least-once, offset tracking, and idempotency all assume an ack means "fsync'd." Build and test the segment log (write → fsync → restart → recover) *before* anything that relies on delivery guarantees.
- **Partitions gate all scale-out.** Collector scale (a mandated feature) is impossible with one partition. Partition assignment + key=uuid routing must land before the scale demo and before the perf harness.
- **Idempotent upsert depends on the Postgres schema AND on at-least-once existing.** Freeze the `(uuid, metric_name, ts)` key (ADR-0005 — *currently Proposed*) before writing the collector store or the API queries; the same key shape is the API's `{id}` and ordering axis. Confirm this first.
- **Perf harness depends on correctness + metrics + Helm.** It is the last major feature: it needs a correct broker (else numbers are meaningless), Prometheus metrics (cheap measurement), and a Helm-deployable multi-instance topology to vary producer/consumer counts. Sequence it after durability, partitions, backpressure, and metrics.
- **Backpressure and retention are partial substitutes for the same risk** (unbounded log). Backpressure is table-stakes (it's how producers>consumers is supposed to behave); retention is bonus. Don't skip backpressure assuming retention covers it — they address different ends of the pipe.
- **Graceful shutdown is a hidden prerequisite of "scale down."** Scaling collectors from N→M sends SIGTERM; without drain+commit you lose in-flight offsets and re-process on the next pod. Scale-down is mandated, so graceful shutdown is effectively mandated too.

## Performance Harness — Metrics & Measurement Methodology

The harness is the marquee bonus and the clearest demonstration of the MQ's core value. Run each
`(producers, consumers)` regime — **(10,2)** producer-bound / backpressure stress, **(2,10)**
consumer-bound / starvation, **(5,5)** balanced — for a fixed duration (literature standard ≈ 5 min
steady-state after warm-up), with a constant message size, and emit a comparison table + written analysis.

| Metric | What it tells you | How to measure (recommended) | Pitfall to avoid |
|--------|-------------------|------------------------------|------------------|
| **Throughput (msg/s, and MB/s)** | Raw capacity; where the system saturates | `rate(broker_messages_persisted_total[1m])` for ingest; `rate(collector_messages_consumed_total[1m])` for drain. Report both — they diverge under backpressure. | Don't report a single number; report producer-side vs consumer-side. In (10,2) consume-rate is the real ceiling. |
| **End-to-end latency (p50/p95/p99)** | User-visible freshness: produce → persisted-in-Postgres | Stamp `produce_ns` into the message; collector computes `now - produce_ns` at upsert; record in an **HDR-style histogram** (Prometheus histogram buckets or `hdrhistogram-go`). Report percentiles, not mean. | **Coordinated omission**: a naive timer hides queue-wait. Measure from intended-send time; HDR histograms are the standard fix. Co-locating load-gen with the broker also skews results. |
| **Broker queue depth (backlog)** | How much is buffered on disk = backpressure / lag pressure | Per partition: `log_end_offset - min(committed_offset across consumers)`. Export as `broker_partition_backlog`. | This is the disk-resident backlog; distinct from in-flight. Spikes in (10,2) and should *bound* (backpressure working), not grow unbounded. |
| **Consumer lag** | Are consumers keeping up | Count-based: `log_end_offset - committed_offset` per (group, partition) (Kafka-standard formula). **Better: time-based lag** = age of the oldest un-consumed message (how stale is the front of the queue). | Count-based lag is noisy and size-dependent; current best practice (WarpStream/SoftwareMill) favors *time lag*. Report at least count-lag; time-lag if cheap. |
| **CPU / memory per component** | Efficiency + the "memory management" grading criterion; proves no OOM under backpressure | `cAdvisor` / kubelet pod metrics (`container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`) scraped by Prometheus; or `/metrics` Go runtime collector (`go_memstats_*`, goroutines). | Working-set, not RSS-at-exit. Watch broker memory in (10,2): flat = backpressure healthy; climbing = unbounded buffering bug. |

**Methodology notes (cross-checked against MQ benchmarking literature):**
- **Warm-up then steady-state.** Discard the first ~30–60s; report a stable window (~5 min) so JIT-free Go GC and segment rolls average out.
- **Fixed, documented message size** (e.g. ~50–200 B for a DCGM datapoint). Capacity is meaningless without it; state it in the table.
- **Percentiles, never just mean latency** — tail latency (p99) is the interesting signal under backpressure.
- **Isolate load generation** from the system under test where feasible (separate pods/nodes) to avoid confounding CPU.
- **Correctness assertion alongside perf**: every regime must end with *zero message loss* and *correct per-GPU ordering* (count rows in Postgres == messages produced, modulo dedup). A fast broker that drops messages fails the core value — the harness should prove both.

## MVP Definition (assignment-grading lens)

### Launch With (v1 — required to pass)
- [ ] Durable segment-log broker: write → fsync → ack → restart → recover — *core value; everything depends on it.*
- [ ] MQ client lib: produce + consume + commit over gRPC streaming — *the contract every service uses.*
- [ ] Partitions ≥10 with key=uuid routing — *gates scale-out + ordering.*
- [ ] Streamer (CSV loop + re-stamp + produce) — *mandated source.*
- [ ] Collector (consume + parse + idempotent upsert) — *mandated sink; idempotency non-negotiable.*
- [ ] Postgres schema (gpus + telemetry) on `(uuid, metric_name, ts)` — *underpins upsert + all 3 endpoints.*
- [ ] 3 REST endpoints + auto-generated OpenAPI via Makefile — *mandated API surface.*
- [ ] Backpressure + graceful shutdown + health/readiness — *correctness under load + scale-down.*
- [ ] Helm chart + Dockerfiles + PVCs, deployable to kind — *mandated deployment.*
- [ ] Dynamic scale 1–10 streamers/collectors demonstrated — *mandated elasticity.*
- [ ] Unit tests + coverage via Makefile — *mandated + gated.*
- [ ] README + AI-usage doc — *mandated; write AI-usage continuously.*

### Add After Core Works (bonus — raises grade)
- [ ] Prometheus metrics on all components — *trigger: core pipeline passes end-to-end; needed for harness.*
- [ ] Performance harness (10:2, 2:10, 5:5) + comparison table + analysis — *trigger: metrics + Helm multi-instance ready.*
- [ ] System/integration tests (restart + no-loss + ordering) — *trigger: components stable individually.*
- [ ] Grafana dashboard — *trigger: Prometheus exporting; pure presentation.*

### Future Consideration (stretch / defer)
- [ ] Message compression — *defer: measure CPU vs throughput tradeoff before adopting.*
- [ ] Retention / compaction — *defer: only needed for very long runs; backpressure covers the demo.*
- [ ] Producer idempotency / dedup window — *defer: idempotent upsert already gives correctness.*

## Feature Prioritization Matrix

| Feature | Grading Value | Implementation Cost | Priority |
|---------|---------------|---------------------|----------|
| Durable segment log + recovery | HIGH (core value) | HIGH | P1 |
| MQ produce/consume/commit client lib | HIGH | HIGH | P1 |
| Partitions ≥10 + key=uuid routing | HIGH | HIGH | P1 |
| Idempotent collector upsert | HIGH | MEDIUM | P1 |
| 3 REST endpoints + ordering + time-window | HIGH (mandated) | LOW | P1 |
| Streamer (loop + re-stamp) | HIGH (mandated) | MEDIUM | P1 |
| Auto-gen OpenAPI via Makefile | HIGH (mandated) | LOW | P1 |
| Backpressure + graceful shutdown + health | HIGH (correctness) | MEDIUM | P1 |
| Helm + Dockerfiles + dynamic scale 1–10 | HIGH (mandated) | MEDIUM | P1 |
| Unit tests + coverage | HIGH (mandated/gated) | MEDIUM | P1 |
| README + AI-usage doc | HIGH (mandated) | LOW | P1 |
| Prometheus metrics | MEDIUM (bonus + enables harness) | MEDIUM | P2 |
| Performance harness + analysis | MEDIUM–HIGH (marquee bonus) | HIGH | P2 |
| System/integration tests | MEDIUM (bonus) | MEDIUM | P2 |
| Grafana dashboard | LOW–MEDIUM (presentation) | LOW | P3 |
| Compression / retention / dedup | LOW (stretch) | MEDIUM–HIGH | P3 |

## Sources

- PROJECT.md (canonical assignment context, synthesized from the original assignment PDF + dataset analysis) — HIGH confidence (curated, project source of truth)
- .planning/codebase/ARCHITECTURE.md (system structure, contracts, anti-patterns, per-component metrics) — HIGH confidence (curated)
- [Evaluating persistent, replicated message queues — SoftwareMill (mqperf)](https://softwaremill.com/mqperf/) — MQ benchmark methodology (warm-up, message sizes, steady-state duration)
- [Benchmarking Message Queues — MDPI](https://www.mdpi.com/2673-4001/4/2/18) — throughput in msg/s + MB/s, fixed message size, multi-percentile latency
- [Benchmarking Message Queue Latency — Brave New Geek](https://bravenewgeek.com/benchmarking-message-queue-latency/) — coordinated omission, HDR Histogram, load-gen isolation
- [Monitor the Consumer Lag in Apache Kafka — Baeldung](https://www.baeldung.com/java-kafka-consumer-lag) — lag = log-end-offset − committed-offset formula
- [The Kafka metric you're not using: measure time, not messages — WarpStream](https://www.warpstream.com/blog/the-kafka-metric-youre-not-using-stop-counting-messages-start-measuring-time) — time-based lag vs count-based lag
- [The hidden problem with Kafka lag monitoring — SoftwareMill](https://softwaremill.com/the-hidden-problem-with-kafka-lag-monitoring/) — caveats on count-based lag

---
*Feature research for: custom durable MQ + GPU telemetry pipeline (vantage)*
*Researched: 2026-06-24*
