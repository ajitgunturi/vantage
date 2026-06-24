# vantage — Elastic GPU Telemetry Pipeline with Custom Message Queue

> Canonical project context (single source of truth). Synthesized from the original
> assignment (`GPU Telemetry Pipeline Message Queue.pdf`) + dataset analysis of
> `dcgm_metrics_20250718_134233.csv`, reconciled with the live codebase map in
> `.planning/codebase/`. Module path root: `github.com/ajitgunturi/vantage`.

## What This Is

An elastic, scalable, restart-safe telemetry pipeline for an AI/GPU cluster (many hosts, 1+ GPUs
each), built in idiomatic Go and deployed via Docker + Kubernetes + Helm. A **Streamer** loops a
DCGM telemetry CSV and produces re-stamped datapoints over a **custom-built message queue**; a
**Collector** consumes and persists them idempotently into PostgreSQL; an **API Gateway** exposes
the data over REST with an auto-generated OpenAPI spec. The MQ is hand-built (no Kafka/RabbitMQ/
ZeroMQ) with its own durable append-only segment log so it survives broker restarts.

## Core Value

The **custom message queue** must be durable, scalable, and correct under load — it is the heart of
the exercise. Everything else (streamer, collector, API) exists to exercise and demonstrate the MQ
across producer/consumer ratios up to 10×10. If the MQ loses or corrupts messages, the project fails.

## Requirements

### Validated

<!-- Built and confirmed working in the current scaffold (see .planning/codebase/ + git history). -->

- ✓ Multi-module monorepo scaffold: `go.work` + 5 modules (`mq`, `streamer`, `collector`, `apigateway`, `k8s-infra`) — existing (ADR-0003)
- ✓ Custom MQ gRPC contract + generated stubs (`mq/proto/mqv1/mq.proto` → `mq/gen/mqv1`) — existing (ADR-0004)
- ✓ Makefile toolchain: `build`, `test`, `cover`, `cover-check`, `cover-logic`, `proto`, `lint`, `kind`, `helm`, `hooks` — existing
- ✓ CI pipeline (build / test / lint / coverage) green on `main` — existing
- ✓ Coverage gates: 90% line (native) + 100% branch (gobco), fail-open until code lands — existing
- ✓ Pre-commit hook (gofmt + golangci-lint) — existing
- ✓ GitHub repo (public) + protected `main` + ephemeral-branch → PR workflow (BRANCHING.md) — existing
- ✓ Architecture decision records ADR-0001…0005 + PROMPT_HISTORY — existing

### Active

<!-- Pending implementation. These are the build targets the roadmap decomposes into phases. -->

- [ ] Custom MQ broker: durable append-only segment log + WAL on a PVC; rebuilds offset index on restart (survives downtime)
- [ ] MQ client library: producer + consumer over gRPC streaming; consumer-group offset/ack tracking; at-least-once delivery
- [ ] MQ partitions: unit of consumer parallelism (count ≥ 10) enabling collector scale-out
- [ ] Backpressure / flow control: producers > consumers grows the on-disk log rather than OOM-ing the broker
- [ ] Streamer (producer): loop the DCGM CSV, re-stamp timestamps at stream time, produce over the MQ; scale 1–10
- [ ] Collector (consumer): consume, parse, idempotent upsert into PostgreSQL on `(uuid, metric_name, ts)`; scale 1–10
- [ ] PostgreSQL schema: `gpus` (dimension) + `telemetry` (fact) tables, normalized on canonical GPU `uuid`
- [ ] API Gateway: `GET /api/v1/gpus`, `GET /api/v1/gpus/{id}/telemetry`, time-window filter (`start_time`/`end_time`)
- [ ] Auto-generated OpenAPI (Swagger) spec via a Makefile target
- [ ] Prometheus-style metrics exposed from each component (throughput, latency, queue depth, consumer lag)
- [ ] Performance harness: drive `(producers, consumers)` ∈ {(10,2),(2,10),(5,5)}; measure throughput, end-to-end latency, broker queue depth / consumer lag, CPU/mem; produce a comparison table + analysis
- [ ] Helm umbrella chart + per-service subcharts + PostgreSQL + PVCs; deployable to kind locally
- [ ] Dockerfiles per service
- [ ] README (architecture & design writeup, build/packaging, install workflow, sample user workflow) + AI-usage doc (exact prompts, where they fell short, manual interventions)

### Out of Scope

- Off-the-shelf brokers (Kafka / RabbitMQ / ZeroMQ) — assignment mandates a **custom** MQ
- More than 10 streamer or 10 collector instances — exercise scale ceiling
- Using PostgreSQL (or any external store) as the MQ persistence layer — keeps "custom MQ" honest; Postgres is reserved for collector data (ADR-0001/0002)
- Replaying the CSV's original timestamps — we **re-stamp** at stream time (processing time = telemetry timestamp)
- Exactly-once delivery — at-least-once + idempotent collector upserts is the chosen correctness model

## Context

- **Dataset:** NVIDIA DCGM exporter scrape (`dcgm_metrics_20250718_134233.csv`), 2,470 rows. Columns: `timestamp, metric_name, gpu_id, device, uuid, modelName, Hostname, container, pod, namespace, value, labels_raw`.
- **Cardinality (measured):** 10 metric names (each ×247), 31 hosts (`mtv5-dgx1-hgpu-001..032`), 8 GPUs/host, 247 distinct UUIDs ≈ physical GPUs. `container`/`pod`/`namespace` are empty here (carry as nullable). 4 source timestamps (irrelevant — we re-stamp).
- **Data model:** a datapoint = `(uuid, metric_name, value, ts)` + GPU dimensions (host, gpu_id, device, modelName). Canonical GPU identity = `uuid` (globally unique); `gpu_id` 0–7 is only unique within a host.
- **Current state:** scaffold + tooling complete; **no service logic implemented yet** (stubs + generated gRPC only). Next concrete step: stub the service mains into compiling skeletons, then build the broker durable segment log first (see `.planning/ROADMAP.md`).
- **Stack locked by assignment:** Go, PostgreSQL, Docker, Kubernetes, Helm. Locked libs: `pgx`, `slog`, `testify`, Prometheus client. Local k8s: kind.

## Constraints

- **Tech stack**: Custom MQ only — no off-the-shelf brokers — *core grading criterion; "custom" must stay honest (no DB-backed queue)*.
- **Scale**: ≤ 10 streamers, ≤ 10 collectors — *exercise ceiling; partition count must still be ≥ 10 for consumer parallelism*.
- **Deployment**: All components deployable to Kubernetes via Helm — *assignment deliverable*.
- **Durability**: MQ must survive broker crash/restart with no message loss — *user-added hard requirement driving the segment-log design*.
- **Language/quality**: idiomatic Go, graceful error handling + memory management, `slog` logging — *grading criterion*.
- **Testing**: unit tests required (system tests bonus); coverage via Makefile; OpenAPI auto-generated via Makefile — *deliverable + grading criterion*.
- **Documentation**: comprehensive README + AI-usage doc with exact prompts and where they fell short — *explicit deliverable*.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| ADR-0001: Custom MQ = independent service, append-only segment-log durability | Survives restart without an external broker; keeps "custom" honest | ✓ Good (direction set; depth TBD at build) |
| ADR-0002: PostgreSQL for collector data | Open-source DB allowed; relational fits the dimension/fact model | ✓ Good |
| ADR-0003: Multi-module monorepo + `go.work` | Independent deployables, shared workspace, clean module boundaries | ✓ Good (built) |
| ADR-0004: gRPC streaming transport for MQ | Framed, efficient, typed contract over HTTP/2 | ✓ Good (contract + stubs built) |
| ADR-0005: Canonical GPU id = `uuid` | Globally unique/stable; `gpu_id` only unique within a host | ✓ Accepted (2026-06-24) — PK `(uuid, metric_name, ts)`, partition key, API `{id}` all = `uuid` |
| kind for local k8s dev | Lightweight, CI-friendly | ✓ Good (tooling; PROMPT_HISTORY) |
| MQ persistence depth/fidelity | Segment-log direction set; full-WAL vs bounded TBD | — Pending (decide at MQ build) |
| Streaming cadence (per-row / batch / loop) | Implementation detail of the streamer | — Pending (decide at streamer build) |
| OpenAPI generator (`swaggo/swag` vs `oapi-codegen`) | Affects handler authoring style | — Pending (decide at API-gateway build) |
| HTTP router (`chi` vs `gin`) | API gateway routing | — Pending (decide at API-gateway build) |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-06-24 after initialization (brownfield — scaffold mapped, logic pending)*
