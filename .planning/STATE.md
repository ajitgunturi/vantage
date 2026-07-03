---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
current_phase: 06
current_phase_name: production-hardening-assignment-alignment
status: executing
stopped_at: Completed 06-02-PLAN.md — pagination + health handlers
last_updated: "2026-07-03T04:35:44.449Z"
progress:
  total_phases: 8
  completed_phases: 6
  total_plans: 31
  completed_plans: 26
  percent: 75
---

# Project State: vantage

## Project Reference

- **What:** Production-grade, horizontally-scalable GPU telemetry pipeline with a custom from-scratch in-memory message queue, built as four independent Go microservices on Kubernetes.
- **Core value:** `CSV → Streamer → custom MQ → Collector → PostgreSQL → API Gateway → client` works reliably under concurrency — no message loss or duplication across horizontally-scaled producers and consumers.
- **Current focus:** Phase 06 — production-hardening-assignment-alignment

## Current Position

- **Milestone:** v1 (MVP)
- **Phase:** 06 (production-hardening-assignment-alignment) — EXECUTING
- **Plan:** 4 of 8
- **Status:** Ready to execute
- **Progress:** [████████░░] 84%

```
[ ██████▱▱ ] 6/8 phases
```

## Performance Metrics

- Phases complete: 6/8 (1, 01.1, 2, 3, 4, 5)
- Requirements delivered: 40/52 (remaining: DOC-02/03, OBS-01/02, OPS-07/08/09, API-05, QA-07 — Phase 6; DUR-01/02, QA-05 — Phase 7)
- Plans executed: 23

## Accumulated Context

### Key Decisions

- Vertical-MVP phase structure; 5 phases derived from the hard dependency chain (proto → MQ core → storage → pipeline → gateway → devops).
- Storage lifted into its own foundation phase (Phase 2) because the schema + pgxpool unblocks both the Collector (Phase 3) and the Gateway (Phase 4).
- Custom MQ on native Go concurrency only (channels / `sync.RWMutex` / ring buffer) — no third-party brokers. In-memory is the default; an opt-in WAL persistence backend (behind a `Store` interface) adds crash durability — batched group-commit fsync + replay-on-restart, at-least-once. Built as Phase 7 (renumbered from 6 on 2026-07-03); the interface seam lands in Phase 1.
- **Phase 5:** migrate hook moved `pre-install` → `post-install,post-upgrade` (deviation from locked D-09, owner-approved) — pre-install deadlocked against the same release's Postgres. Bounded loud failure convention: `activeDeadlineSeconds: 300` on the Job + `--timeout 6m` on helm-install (WR-04).
- **Phase 5:** MQ `replicas: 1` + `strategy: Recreate` hardcoded in the chart template (not values-overridable) — chart-layer enforcement of the single-replica broker invariant.
- **Phase 5:** SECURITY.md verified — 17 threats closed, 0 open (ASVS L1, plan-time register).

### Open Decisions (per-phase, do not resolve at roadmap level)

- **Phase 1 (RESOLVED):** MQ primitive = bounded ring buffer + `sync.Mutex` (in `internal/queue`); drop-oldest buffer policy (proven by `TestRingStore_DropOldest`); proto tooling = raw `protoc` (single `api/proto/mq.proto`, `paths=source_relative`), not `buf`.
- **Phase 3 (RESOLVED in Phase 5):** Collector batch/flush and Streamer rate fixed in Helm values (`deployments/values.yaml`); soak-proven at 3 and 10 streamers.

### Active TODOs

- Plan Phase 6 (`/gsd-plan-phase 6`) — Production Hardening + Assignment Alignment: DOC-02/03, OBS-01/02, OPS-07/08/09, API-05, QA-07 (source: 06-REVIEW.md).
- Phase 7 (after Phase 6) — WAL-backed `Store`: config flag, group-commit fsync, replay-on-restart, crash-recovery test (DUR-01, DUR-02, QA-05).

### Blockers

- None.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260702-ku8 | fix all the gaps identified as part of this review (mid-assignment 4-agent review: C-1 ack-on-persist data loss, MQ liveness M-2/M-3, gateway M-4, ADR-002, AI_USAGE docs) | 2026-07-02 | 2a2a1f7 | [260702-ku8-fix-all-the-gaps-identified-as-part-of-t](./quick/260702-ku8-fix-all-the-gaps-identified-as-part-of-t/) |
| fast | smoke phase03: read count(*)/count(distinct) from one snapshot — two-query MVCC race falsely failed the exactly-once check | 2026-07-02 | b89753c | — |
| Phase 05 P01 | 8 min | 2 tasks | 6 files |
| Phase 05 P02 | 9 min | 2 tasks | 16 files |
| Phase 05 P03 | 14 min | 2 tasks | 5 files |
| Phase 05 P04 | 10 min | 2 tasks | 3 files |
| Phase 06 P01 | 480s | 3 tasks | 9 files |
| Phase 06 P02 | 412 | 3 tasks | 8 files |
| Phase 06 P03 | 114 | 2 tasks | 4 files |

### Roadmap Evolution

- Phase 01.1 inserted after Phase 1: Upgrade MQ delivery to broker-side at-least-once: bidi Consume with client credit + per-message Ack + redelivery-on-disconnect. Triggered by reproduced 1000-produce/20-consume silent loss (consumed_total=513, client read 20). Must reconcile with Phase 2 SC4 / Phase 3 SC2 / WAL phase (now Phase 7). (URGENT)
- Phase 6 inserted after Phase 5: Production Hardening + Assignment Alignment (audit-driven: AI-prompt log, health+probes, resources/HPA, pagination, CI, slog); WAL durability renumbered to Phase 7

## Session Continuity

**Last session:** 2026-07-03T04:35:03.752Z
**Stopped at:** Completed 06-02-PLAN.md — pagination + health handlers
**Resume file:** None

- **Last action:** Phase 5 UAT completed 2026-07-02 — Test 1 (kind E2E deploy + smoke-05) and Test 2 (sustained soak: rows grew, produced≥consumed, bounded depth, streamer restored) both human-verified. 05-SECURITY.md written: 17 threats closed / 0 open. VERIFICATION.md canonicalized to passed; phase marked complete in ROADMAP/STATE; PROJECT.md evolved (10 requirements moved to Validated; only WAL remains Active).
- **Next action:** Plan Phase 6 (`/gsd-plan-phase 6`) — AI-prompt log (DOC-02), health endpoints + probes (OBS-01/OPS-07), resources/HPA (OPS-08/09), pagination (API-05), CI (QA-07), slog (OBS-02), README accuracy (DOC-03). Source: 06-REVIEW.md
- **Notes:** Machine-local `.env` must carry `DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock` (three slashes — scheme + absolute path) + `TESTCONTAINERS_RYUK_DISABLED=true`; committed `.env.example` documents this. Phase 7 (WAL) relies on: Phase 2 `uq_gpu_metrics_natural_key` + Phase 3 idempotent upsert for safe at-least-once replay.

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
| Phase 03 P01 | 116 | 2 tasks | 2 files |
| Phase 03 P02 | 269s | 3 tasks | 5 files |
| Phase 03-pipeline-streamer-collector-integration P03 | 27 | 2 tasks | 6 files |
| Phase 03-pipeline-streamer-collector-integration P04 | 675 | 2 tasks | 3 files |
| Phase 04 P01 | 356 | 2 tasks | 9 files |
| Phase 04 P02 | 558 | 2 tasks | 6 files |
| Phase 04 P03 | 636 | 3 tasks | 11 files |

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
- [Phase ?]: GpuMetric.GpuID sourced from msg.GetUuid() — COLL-04/D-04 single enforcement point
- [Phase ?]: InsertSQL positional args - in DDL column order — shared Collector contract
- [Phase ?]: 03-02: RFC3339Nano restamp locked in for Streamer — second-granularity collapses same readings
- [Phase ?]: 03-02: Stream exported seam once=true enables deterministic unit and bufconn tests without infinite production loop
- [Phase ?]: Two-goroutine bidi split for gRPC Consume stream — recv goroutine sole Recv caller, batch goroutine sole Send caller
- [Phase ?]: ctx.Err() != nil as sole reconnect exit discriminant in Run — gRPC maps server-side codes.Canceled to stdlib context.Canceled making errors.Is unreliable
- [Phase ?]: grpcSrv.Stop() not GracefulStop in tests — GracefulStop blocks ~30s drain interval, Stop() immediately RSTs connections
- [Phase ?]: ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING — Collector idempotency absorbs MQ at-least-once redeliveries without in-memory dedup state
- [Phase ?]: E2E test: G=10 GPU UUIDs x M=20 metric names = 200 rows for restamp-collision robustness
- [Phase 04]: Plan 04-01: gateway.Config.MaxRows = VANTAGE_GATEWAY_MAX_ROWS (default 1000) as safety ceiling; nil pool guard in ListGPUs ensures JSON Content-Type invariant
- [Phase 04]: Plan 04-02: two-query approach in db.Telemetry — separate simple/windowed SQL paths keep idx_gpu_metrics_gpu_id_ts index use clean; COALESCE on nullable text cols avoids *string scan panics
- [Phase 04]: Plan 04-02: GPUExists (SELECT EXISTS) before Telemetry call separates 404 (unknown GPU) from 200-[] (known GPU, empty window) per OQ-2
- [Phase ?]: swag runtime version mismatch
- [Phase ?]: limit+1 sentinel for has_next avoids COUNT query (plan 06-02)
- [Phase ?]: OFFSET as pgx placeholder only — no SQL string concat (T-06-03, plan 06-02)
- [Phase ?]: ReadyzHandler returns generic status only — no DSN/driver text in response (T-06-05, plan 06-02)
- [Phase ?]: 06-03
- [Phase ?]: 06-03
