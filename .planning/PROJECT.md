# vantage — Elastic GPU Telemetry Pipeline

## What This Is

A production-grade, horizontally-scalable pipeline that ingests live GPU telemetry (NVIDIA DCGM
metrics), moves it through a **custom-built message queue**, persists it to PostgreSQL, and exposes
it via a documented REST API. It is built from scratch in idiomatic Go as four strictly independent
microservices on Kubernetes, intended as a reference-quality distributed-systems implementation.

## Core Value

The end-to-end telemetry flow must work reliably under concurrency:
`CSV → Streamer → custom MQ → Collector → PostgreSQL → API Gateway → client`,
with **no message loss or duplication** across horizontally-scaled producers and consumers.

## Requirements

### Validated

- [x] Custom in-memory MQ (from scratch, no third-party brokers) with gRPC data plane + HTTP control plane _(Validated in Phase 1)_
- [x] gRPC `Produce` (unary) and `Consume` (**bidi stream**, ADR-001); concurrent collectors get unique messages in steady state, with **broker-side at-least-once** (per-message ack + client credit + redelivery-on-disconnect) _(Validated in Phase 01.1 — proven by `-race -count=50` + `make smoke-01` redelivered_total>0)_
- [x] HTTP `GET /api/v1/queue/inspect` returns queue status JSON _(Validated in Phase 1; at-least-once counters delivered/consumed=acks/redelivered/in_flight added in Phase 01.1)_

- [x] PostgreSQL time-series schema with composite index `(gpu_id, timestamp DESC)` _(Validated in Phase 2 — EXPLAIN-proven index scan at 100k rows)_
- [x] Consumer-side idempotency (DB unique constraint + collector upsert) so at-least-once replay cannot duplicate rows _(Validated in Phases 2+3 — `uq_gpu_metrics_natural_key` + `ON CONFLICT DO NOTHING`, exactly-once E2E proven)_
- [x] Streamer loops the DCGM CSV indefinitely, restamps current timestamp, publishes via gRPC; up to 10 instances _(Validated in Phase 3; 10-streamer soak re-proven in Phase 5)_
- [x] Collector consumes the MQ stream and batch-inserts to PostgreSQL via pgxpool _(Validated in Phase 3)_
- [x] API Gateway exposes `GET /api/v1/gpus`, `/gpus/{id}/telemetry`, and time-window filtering _(Validated in Phase 4)_
- [x] OpenAPI spec fully auto-generated from `swag` code annotations _(Validated in Phase 4)_
- [x] Multi-stage Dockerfile + Helm sub-chart per service; each builds & deploys independently _(Validated in Phase 5 — kind E2E + targeted mq-only upgrade demo, human-verified UAT)_
- [x] Makefile targets: proto, build, test, coverage (≥90% gate), swagger _(Validated in Phase 5)_
- [x] Unit + integration tests; race-detector tests for MQ concurrency; ≥90% coverage _(Validated in Phase 5 — QA-01/QA-04 closed at 90.3%)_
- [x] Living README quickstart + runnable manual smoke suite (`make smoke`) + docker-compose dev stack, grown incrementally each phase _(Validated through Phase 5 — smoke-01..05, soak, test-harness)_

### Active

- [ ] MQ storage behind a `Store` interface: in-memory default, plus an opt-in WAL persistence backend (batched group-commit fsync + replay-on-restart, at-least-once)

### Out of Scope

- Third-party message brokers (Kafka/NATS/RabbitMQ/Redis) — the MQ must be built from scratch
- MQ clustering / multi-replica — single-replica only, per spec (durable WAL mode persists to local disk, never a cluster)
- Authentication/authorization on the APIs — not in the assignment scope
- Multi-region / cross-cluster deployment — single Kubernetes cluster only
- Hand-written OpenAPI spec — must be generated from annotations

## Context

- **Input data:** `dcgm_metrics_20250718_134233.csv` (~1.1MB), DCGM exporter format with columns
  `timestamp, metric_name, gpu_id, device, uuid, modelName, Hostname, container, pod, namespace, value, labels_raw`.
  Gitignored — kept local, not committed.
- **Canonical spec:** `instructions.md` (full brief). Per-role briefs in `.ai/agents/*.md`. Operational
  conventions in `CLAUDE.md`.
- **Repo layout** (single Go module `github.com/ajitg/vantage`): `cmd/{mq,streamer,collector,gateway}`
  entrypoints, shared `pkg/{pb,db,models}`, `api/proto/`, `build/` Dockerfiles, `deployments/` Helm.
- **Work parallelization:** four specialist roles map to directory boundaries — MQ Engineer
  (`api/proto`, `cmd/mq`), Storage/Pipeline (`cmd/streamer`, `cmd/collector`, `pkg/db`, `pkg/models`),
  Gateway/Docs (`cmd/gateway`), DevOps/QA (`build`, `deployments`, `Makefile`).

## Constraints

- **Tech stack**: Go (idiomatic), PostgreSQL via `jackc/pgx/v5` (`pgxpool`), gRPC (Protobuf v3) + HTTP/1.1 JSON — fixed by spec.
- **Architecture**: Strictly independent microservices; only shared surface is `pkg/` (proto contracts + DB models).
- **MQ implementation**: Native Go concurrency only (channels, `sync.RWMutex`, ring buffers). No third-party brokers. In-memory is the default; an OPTIONAL WAL persistence backend (behind a `Store` interface) extends the brief's in-memory-only baseline for crash durability.
- **Quality**: ≥90% line coverage enforced via Makefile gate; `go test -race` for MQ concurrency. TDD-first.
- **Deployment**: Docker multi-stage builds; Kubernetes + Helm; each service deploys independently.
- **Docs**: OpenAPI auto-generated from `swag` annotations only.
- **Process**: Built with the GSD framework, phase by phase, Vertical-MVP structure.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Single Go module (`cmd/` + shared `pkg/`) over multi-module | Matches `instructions.md` blueprint; simpler builds; independence enforced by directory/Dockerfile convention | — Pending |
| Custom MQ on native Go channels + RWMutex | Spec forbids third-party brokers; demonstrates from-scratch concurrency design | — Pending |
| MQ durability as opt-in WAL behind a `Store` interface (in-memory default) | Brief mandates in-memory, but a broker crash loses un-consumed messages; an opt-in WAL (batched group-commit fsync + replay-on-restart) adds crash durability without breaking the spec-compliant default | — Pending |
| **Bidi `Consume` + broker-side at-least-once** (Phase 01.1, ADR-001) — DEVIATION | Brief specifies *server-side streaming* `Consume` and the project had placed per-message ack out of scope. A reproduced defect (produce 1000, short consumer reads 20 → ~493 silently lost on disconnect) motivated moving at-least-once into the broker: bidi stream + per-message ack + client-driven credit + redelivery-on-disconnect. Owner-approved, documented deviation; in-memory/no-disk/single-replica constraints unchanged. Delivery-level at-least-once now lives here; Phase 6 WAL narrows to crash durability. See `docs/adr/ADR-001-bidi-at-least-once-delivery.md` | — Pending |
| Vertical-MVP phase structure | Get a running end-to-end pipeline early, then harden slice by slice | — Pending |
| `swag` for OpenAPI generation | Spec mandates fully auto-generated docs from code annotations | — Pending |
| Living README + runnable manual smoke suite, built incrementally per phase | Readers can clone → run → see each component work; user wants a hands-on suite to verify each phase's deliverables. Makefile-driven shell smoke scripts + docker-compose dev stack; harness established in Phase 2 (first Postgres/Docker phase, with Phase-1 MQ backfill) | Good — smoke-01..05 + soak + live-infra harness all green through Phase 5 |
| Migrate hook moved `pre-install` → `post-install,post-upgrade` (Phase 5, deviation from locked D-09) | Pre-install hook's wait-for-postgres init container deadlocked against the same release's Postgres (Helm runs pre-install hooks before installing manifests). Post-install preserves intent — dedicated Job per release op, Helm blocks until done; services tolerate the brief pre-migration window | Good — deploy + smoke-05 human-verified on clean kind cluster |
| Bounded loud failure on deploy-critical Jobs | `activeDeadlineSeconds` on the migrate Job spec + explicit `--timeout` on helm-install convert silent hangs into named, bounded errors | Good — raised to 300s/6m (WR-04) for cold image pulls |
| MQ `replicas: 1` + `strategy: Recreate` hardcoded in template, not values | In-memory broker must never scale or rolling-update (split-brain); making it non-overridable enforces the invariant at the chart layer | Good — T-05-03 closed |

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
*Last updated: 2026-07-02 after Phase 5 (DevOps + Quality Gates) completed and verified — only Phase 6 (WAL durability) remains in v1*
