---
phase: "03"
plan: "04"
subsystem: e2e-test-smoke-docs
status: complete
tags: [e2e, integration-test, smoke, documentation, exactly-once, concurrency]
requirements: [QA-03, QA-06, DOC-01]

dependency_graph:
  requires: ["03-02", "03-03"]
  provides: ["e2e-exactly-once-proof", "smoke-03", "README-phase3"]
  affects: ["README.md", "test/e2e/pipeline_test.go", "scripts/smoke/phase03-pipeline.sh"]

tech_stack:
  added: []
  patterns:
    - "bufconn in-process gRPC for integration tests (google.golang.org/grpc/test/bufconn)"
    - "testcontainers Snapshot/Restore for test isolation (ported from pkg/db/db_test.go)"
    - "pollUntilStable: 5×100ms stability window to drain pipeline before assertions"
    - "consumerErrs slice (written in goroutines, read after wg.Wait) for concurrent error capture"

key_files:
  created:
    - test/e2e/pipeline_test.go
    - scripts/smoke/phase03-pipeline.sh
  modified:
    - README.md

decisions:
  - "G=10 GPU UUIDs × M=20 metric names = 200 rows; each (uuid, metric_name) unique so assertion is robust to nanosecond restamp collisions"
  - "FlushMS=100ms in E2E (vs production default 500ms) so 5×100ms=500ms stability window converges fast"
  - "consCtx separate from test bg-ctx — enables cancel-after-stable lifecycle without timeouts interfering with DB assertions"
  - "smoke-03 sets COLLECTOR_BATCH_SIZE=500 (vs default 50) for higher throughput in 5s window; COLLECTOR_FLUSH_MS=200ms for responsive flushing"

metrics:
  duration: "11m15s"
  completed_date: "2026-06-29T12:05:35Z"
  tasks_completed: 2
  tasks_total: 2
  files_changed: 3

---

# Phase 03 Plan 04: E2E Test, Smoke Harness, and README — Summary

End-to-end integration proof (QA-03), human-runnable smoke harness (QA-06), and living README extension (DOC-01) for the Phase 3 pipeline capstone.

## What was built

**QA-03: `test/e2e/pipeline_test.go`**

`TestEndToEnd_ExactlyOnce` (package `e2e`, `//go:build integration`) proves the full pipeline in-process using bufconn + testcontainers. Design:
- `TestMain`: starts `postgres:17-alpine` via testcontainers, migrates, snapshots, opens pool (Snapshot/Restore isolation pattern from `pkg/db/db_test.go`)
- Fixture: 10 GPU UUIDs × 20 metric names = 200 rows; each (uuid, metric_name) pair unique — assertion is robust to restamp-timestamp collisions
- K=3 concurrent `collector.Consume` goroutines consuming from the same bufconn MQ
- `streamer.Stream(once=true)` publishes all 200 rows
- `pollUntilStable` polls `count(*)` every 100ms until unchanged for ≥500ms
- Three assertions: `count(*)>0`, `count(*)==count(distinct natural key)` (exactly-once), `count(distinct gpu_id)==10` (UUID mapping end-to-end)

Test result: 200/200 rows persisted, 200 distinct natural keys, 10 distinct gpu_ids — test passes in 0.86s under `-race`.

**QA-06: `scripts/smoke/phase03-pipeline.sh`**

Live-stack smoke harness served by the existing `smoke-%` Makefile pattern rule (no Makefile change). Pattern follows `phase02-postgres.sh`:
- Starts dev Postgres via `make dev-up` if not running
- Resolves `dcgm_metrics_*.csv` in repo root; fails cleanly if absent
- Builds `bin/mq`, `bin/collector`, `bin/streamer` via `go build`
- Kills stale pipeline processes from prior run, starts three binaries in background (trap-based cleanup)
- Waits 5s for pipeline to flow
- Asserts `count(*)>0`, `count(*)==count(distinct natural key)`, no ordinal `gpu_id` values
- All SQL via `docker compose exec -T psql` (no host psql required)
- Smoke result: 403,072 rows, 0 duplicates, 257 distinct GPU UUIDs — PASS

**DOC-01: README.md Phase 3 section**

Added "Phase 3 — Pipeline (Streamer + Collector)" section with:
- Architecture data-flow diagram reference
- Prerequisites (DCGM CSV, `make dev-up`)
- Run instructions for all three services locally
- Environment variables table for Streamer and Collector
- GPU identity convention (D-04 UUID vs ordinal)
- Inspect-rows commands via `docker compose exec psql`
- Exactly-once delivery explanation
- `make smoke-03` description with step-by-step output
- Services table updated (Streamer/Collector: ⏳ → ✅ Phase 3)
- Phase status table updated (Phase 3: _(coming)_ → ✅)

## Gate results

| Gate | Result |
|------|--------|
| `make build` | PASS — mq, streamer, collector (gateway skipped, not yet created) |
| `make test` | PASS — all unit+integration tests green under `-race` |
| `make coverage` | PASS — 90.2% (floor: 90%) — no regression |
| `make lint` | PASS — golangci-lint clean |
| E2E test `-race` | PASS — 200 rows, 0 duplicates, 10 distinct gpu_ids |
| `make smoke-03` | PASS — 403,072 rows, 0 duplicates, 257 distinct GPU UUIDs |

## Deviations from Plan

None — plan executed exactly as written.

The fixture uses 10×20=200 rows (not the plan's suggested "200 rows across G UUIDs and M metrics") with G=10 and M=20 as a clean G×M grid. Every (uuid, metric_name) is unique per row, satisfying the plan's robustness requirement for restamp collisions without adding test complexity.

## Known Stubs

None. All assertions are wired against real data flowing through the real service code.

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced. The E2E test and smoke script operate as test/QA artifacts with no runtime surface.

## Self-Check: PASSED

- test/e2e/pipeline_test.go: FOUND (commit a90c133)
- scripts/smoke/phase03-pipeline.sh: FOUND (commit b2194ab)
- README.md Phase 3 section: FOUND (commit b2194ab)
- All gates green (build/test/coverage 90.2%/lint)
