---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
current_phase: 02
current_phase_name: Storage Foundation — Schema + Connection Pool
status: planning
stopped_at: Phase 01.1 planned (6 plans, 4 waves)
last_updated: "2026-06-28T15:07:56.830Z"
progress:
  total_phases: 7
  completed_phases: 2
  total_plans: 9
  completed_plans: 9
  percent: 29
---

# Project State: vantage

## Project Reference

- **What:** Production-grade, horizontally-scalable GPU telemetry pipeline with a custom from-scratch in-memory message queue, built as four independent Go microservices on Kubernetes.
- **Core value:** `CSV → Streamer → custom MQ → Collector → PostgreSQL → API Gateway → client` works reliably under concurrency — no message loss or duplication across horizontally-scaled producers and consumers.
- **Current focus:** Phase 01.1 — mq-at-least-once-delivery-bidi-consume-and-ack

## Current Position

- **Milestone:** v1 (MVP)
- **Phase:** 02 — Storage Foundation — Schema + Connection Pool
- **Plan:** Not started
- **Status:** Ready to plan
- **Progress:** [██████░░░░] 56%

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

**Last session:** 2026-06-28T10:21:59.560Z
**Stopped at:** Phase 01.1 planned (6 plans, 4 waves)
**Resume file:** .planning/phases/01.1-mq-at-least-once-delivery-bidi-consume-and-ack/01.1-01-PLAN.md

- **Last action:** Phase 2 discuss-phase complete (schema/identity/migration/index forks locked in `02-CONTEXT.md`). Backfilled the Phase-1 manual smoke suite + living README: `make smoke-01` passes (produced 20 = consumed 20, inspect counters verified); `make build` made resilient to absent services (2026-06-28).
- **Next action:** `/gsd-plan-phase 2` — storage foundation (schema + pgxpool); plans must include README + `smoke-02` tasks per the new convention.
- **Notes:** Phase 1 exit gate is non-negotiable — `go test -race -count=50` clean and N produced = N consumed across K consumers before anything connects to the MQ. Phase 2 exit requires an `EXPLAIN`-verified composite index. Phase 5 must ship MQ as `replicas: 1` + `strategy: Recreate`. Durability is an opt-in WAL added in Phase 6 (Store-interface seam built in Phase 1); consumer idempotency (unique constraint DB-04 in Phase 2, upsert COLL-05 in Phase 3) is built upfront so enabling the WAL later is safe.

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
