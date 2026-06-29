---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
current_phase: 02
current_phase_name: storage-foundation-schema-connection-pool
status: planned
stopped_at: Phase 02 executed + verified (8/8 must-haves; coverage 94.1%; smoke-02 green)
last_updated: "2026-06-29T07:53:48.638Z"
progress:
  total_phases: 7
  completed_phases: 3
  total_plans: 11
  completed_plans: 11
  percent: 43
---

# Project State: vantage

## Project Reference

- **What:** Production-grade, horizontally-scalable GPU telemetry pipeline with a custom from-scratch in-memory message queue, built as four independent Go microservices on Kubernetes.
- **Core value:** `CSV → Streamer → custom MQ → Collector → PostgreSQL → API Gateway → client` works reliably under concurrency — no message loss or duplication across horizontally-scaled producers and consumers.
- **Current focus:** Phase 02 — storage-foundation-schema-connection-pool

## Current Position

- **Milestone:** v1 (MVP)
- **Phase:** 03 (pipeline-streamer-collector-integration) — PLANNED ✓
- **Plan:** 4 plans across 3 waves (checker-passed)
- **Status:** Phase 02 verified (8/8); Phase 03 planned (4 plans, 3 waves) — ready to execute
- **Progress:** [██████████] 100%

```
[ █▱▱▱▱▱ ] 1/6 phases
```

## Performance Metrics

- Phases complete: 1/6
- Requirements delivered: 9/38 (MQ-01..08, QA-02)
- Plans executed: 3

## Accumulated Context

### Key Decisions

- Vertical-MVP phase structure; 5 phases derived from the hard dependency chain (proto → MQ core → storage → pipeline → gateway → devops).
- Storage lifted into its own foundation phase (Phase 2) because the schema + pgxpool unblocks both the Collector (Phase 3) and the Gateway (Phase 4).
- Custom MQ on native Go concurrency only (channels / `sync.RWMutex` / ring buffer) — no third-party brokers. In-memory is the default; an opt-in WAL persistence backend (behind a `Store` interface) adds crash durability — batched group-commit fsync + replay-on-restart, at-least-once. Built as Phase 6; the interface seam lands in Phase 1.

### Open Decisions (per-phase, do not resolve at roadmap level)

- **Phase 1 (RESOLVED):** MQ primitive = bounded ring buffer + `sync.Mutex` (in `internal/queue`); drop-oldest buffer policy (proven by `TestRingStore_DropOldest`); proto tooling = raw `protoc` (single `api/proto/mq.proto`, `paths=source_relative`), not `buf`.
- **Phase 3:** Collector batch size / flush interval and Streamer rate limit — tune empirically against DCGM CSV row rate before fixing Helm values in Phase 5.

### Active TODOs

- Phase 2 context gathered (`/gsd-discuss-phase 2` done). Next: `/gsd-plan-phase 2` (storage foundation — time-series schema with composite index `(gpu_id, timestamp DESC)` + pgxpool).
- ✅ **RESOLVED — `make build` carry-over:** Makefile `build` target now skips service dirs that don't exist yet (`-- skip streamer ... --`), so `make build` is green from Phase 1 on; services slot in as they land.
- **New cross-cutting convention (DOC-01/QA-06/OPS-06):** living README + runnable manual smoke suite (`make smoke` / `make smoke-NN`), grown each phase; docker-compose dev stack arrives in Phase 2. Phase-1 backfill landed (README quickstart + `scripts/smoke/phase01-mq.sh` + `mqprobe` gRPC client).

### Blockers

- None.

### Roadmap Evolution

- Phase 01.1 inserted after Phase 1: Upgrade MQ delivery to broker-side at-least-once: bidi Consume with client credit + per-message Ack + redelivery-on-disconnect. Triggered by reproduced 1000-produce/20-consume silent loss (consumed_total=513, client read 20). Must reconcile with Phase 2 SC4 / Phase 3 SC2 / Phase 6 WAL. (URGENT)

## Session Continuity

**Last session:** 2026-06-29T07:53:10.638Z
**Stopped at:** Completed 02-01-PLAN.md
**Resume file:** .planning/phases/02-storage-foundation-schema-connection-pool/02-02-PLAN.md

- **Last action:** Plan 02-01 complete — pkg/db (New, Migrate, Config, FromEnv), migration SQL, and full integration suite (TestMigration, TestNew, TestUniqueConstraint, TestCompositeIndexUsed at 100k rows) all pass under -race.
- **Next action:** Execute Phase 3 (`/gsd-execute-phase 3`). Wave 1 = 03-01 (pkg/models) ∥ 03-02 (Streamer); Wave 2 = 03-03 (Collector); Wave 3 = 03-04 (E2E + smoke-03). Integration/E2E need Rancher Docker env: `DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true`. Service logic lives in internal/streamer + internal/collector (thin cmd wrappers) so the ≥90% coverage gate reaches it.
- **Notes:** Phase 3 locked decision: use INSERT...ON CONFLICT (not CopyFrom) for idempotent Collector upserts against uq_gpu_metrics_natural_key. Streamer must restamp at RFC3339Nano in Phase 3. Rancher Desktop docker socket: set DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true for integration tests.

---
*State initialized: 2026-06-27*

## Performance Metrics

| Phase | Plan | Duration | Notes |
|-------|------|----------|-------|
| Phase 01 P01 | 6m | 3 tasks | 7 files |
| Phase 01 P02 | 3m | 2 tasks | 5 files |
| Phase 01 P03 | ~15 minutes | 2 tasks | 6 files |
| Phase 01.1 P01 | 4m | - tasks | - files |
| Phase 01.1 P02 | 4m | 2 tasks | 5 files |
| Phase 02 P01 | 13min | 3 tasks | 7 files |
| Phase 02 P02 | 4m | 3 tasks | 5 files |

## Decisions

- [Phase ?]: proto contract + go module bootstrap (plan 01-01)
- [Phase ?]: pkg/pb generated stubs and cmd/ thin wiring excluded from 90% threshold
- [Phase 01]: sync.Mutex over sync.RWMutex for RingStore — TryDequeue mutates head+count every call; all paths are writes (plan 01-02)
- [Phase 01]: Store.Enqueue returns bool not error — in-memory backend is unconditional; bool signals drop-oldest without forcing callers to handle never-occurring errors (plan 01-02)
- [Phase 01]: Inspect() returns StoreStats by value — snapshot copy prevents callers from racing on internal state through a shared pointer (plan 01-02)
- [Phase ?]: dispatch uses defer close(workCh): eliminates double-close risk across all exit paths
- [Phase ?]: errgroup manages gRPC+HTTP+shutdown goroutines for ordered teardown on SIGTERM
- [Phase ?]: WorkChCap = max(BufferSize/10, 128) for pipeline headroom with safety floor
- [Phase ?]: Build gate scoped to pkg only; cmd/mq intentionally broken until Wave 2 server rewrite
- [Phase ?]: Drop-newest tail eviction during Requeue: prioritizes in-flight redelivery; ConsumeCredit env guard rejects n<=0 (T-01.1-03 mitigation)
- [Phase 02]: pgxpool.ParseConfig + NewWithConfig over bare pgxpool.New — explicit MaxConns + HealthCheckPeriod tuning without DSN manipulation (plan 02-01)
- [Phase 02]: uq_gpu_metrics_natural_key (gpu_id, metric_name, timestamp) — Phase 3 must use INSERT...ON CONFLICT, NOT CopyFrom (CopyFrom cannot express ON CONFLICT) (plan 02-01)
- [Phase 02]: RFC3339Nano Streamer restamp locked — TIMESTAMPTZ microsecond precision; second-granularity restamps collapse same-second readings on natural key (plan 02-01)
- [Phase ?]: cmd/migrate is a standalone binary — reusable by Phase-5 k8s init-job without shell dependency (plan 02-02)
