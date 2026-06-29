---
phase: 02-storage-foundation-schema-connection-pool
plan: "01"
subsystem: database
tags: [postgres, pgxpool, pgx/v5, golang-migrate, testcontainers, time-series, schema]

requires:
  - phase: 01-mq-grpc-core
    provides: go.mod module path (github.com/ajitg/vantage) and established testify/require style

provides:
  - pkg/db.New(ctx, cfg) returning a *pgxpool.Pool with startup Ping validation
  - pkg/db.Migrate(ctx, dsn) applying embedded SQL migrations via golang-migrate/iofs (idempotent)
  - pkg/db.Config{DSN, MaxConns} + FromEnv() reading VANTAGE_DB_DSN
  - Versioned migration 000001: gpu_metrics table with composite index (gpu_id, timestamp DESC) and unique constraint uq_gpu_metrics_natural_key
  - Integration test suite proving DB-01..DB-04 at representative scale (100k rows, ANALYZE, EXPLAIN)

affects:
  - phase: 03-collector (imports pkg/db.New + pkg/db.Migrate; uses uq_gpu_metrics_natural_key for ON CONFLICT)
  - phase: 04-gateway (imports pkg/db.New; queries gpu_metrics via the composite index)
  - phase: 05-devops (Makefile coverage gate must extend to ./pkg/...)
  - phase: 02-02 (Makefile dev-up/dev-down, docker-compose, smoke script)

tech-stack:
  added:
    - github.com/jackc/pgx/v5 v5.10.0
    - github.com/golang-migrate/migrate/v4 v4.19.1
    - github.com/testcontainers/testcontainers-go v0.43.0
    - github.com/testcontainers/testcontainers-go/modules/postgres v0.43.0
  patterns:
    - pgxpool.ParseConfig + NewWithConfig (not bare pgxpool.New) for MaxConns + HealthCheckPeriod tuning
    - iofs + go:embed for embedded SQL migrations run programmatically at startup
    - testcontainers-go TestMain + Snapshot/Restore for cheap per-test isolation
    - restoreDB() helper flushes dead pool connections after Snapshot restore (pool reconnection pattern)
    - pgx5:// DSN scheme for golang-migrate pgx/v5 driver (converted from postgres://)
    - DSN credential redaction: error strings carry context but never the DSN value

key-files:
  created:
    - pkg/db/config.go
    - pkg/db/db.go
    - pkg/db/db_test.go
    - pkg/db/migrations/000001_init_schema.up.sql
    - pkg/db/migrations/000001_init_schema.down.sql
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "pgxpool.ParseConfig + NewWithConfig used instead of bare pgxpool.New — gives explicit MaxConns and HealthCheckPeriod control without DSN manipulation"
  - "iofs + go:embed for migrations — files embedded in binary; works on distroless final stage with no runtime filesystem access"
  - "Migrate() runs only m.Up() programmatically; migrate.ErrNoChange treated as success for idempotent restarts"
  - "uq_gpu_metrics_natural_key on (gpu_id, metric_name, timestamp) established for Phase 3 ON CONFLICT upsert (CopyFrom cannot express ON CONFLICT — Phase 3 must use INSERT...ON CONFLICT or pgx.Batch)"
  - "RFC3339Nano restamp requirement locked: TIMESTAMPTZ holds microseconds; Streamer must restamp at nanosecond precision in Phase 3 or readings collapse on the natural key"
  - "DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock + TESTCONTAINERS_RYUK_DISABLED=true required on Rancher Desktop for testcontainers; make test without -tags=integration skips container tests safely"

patterns-established:
  - "restoreDB() helper: call Restore() then discard a Ping() to flush terminated pool connections before next test"
  - "integration build tag on db_test.go: //go:build integration guards testcontainers tests from Docker-less environments"
  - "error wrapping: db: <context>: %w — DSN value never in error chain"

requirements-completed: [DB-01, DB-02, DB-03, DB-04]

coverage:
  - id: D1
    description: "Migration creates gpu_metrics table with gpu_id, timestamp TIMESTAMPTZ, metric_name, value columns (DB-01)"
    requirement: DB-01
    verification:
      - kind: integration
        ref: "pkg/db/db_test.go#TestMigration"
        status: pass
    human_judgment: false
  - id: D2
    description: "EXPLAIN on selective (gpu_id, timestamp DESC) range query over 100k ANALYZE'd rows shows Index Scan, not Seq Scan (DB-02)"
    requirement: DB-02
    verification:
      - kind: integration
        ref: "pkg/db/db_test.go#TestCompositeIndexUsed"
        status: pass
    human_judgment: false
  - id: D3
    description: "pkg/db.New(ctx, cfg) returns healthy *pgxpool.Pool importable by Collector and Gateway; Migrate applies embedded SQL idempotently (DB-03)"
    requirement: DB-03
    verification:
      - kind: integration
        ref: "pkg/db/db_test.go#TestNew"
        status: pass
    human_judgment: false
  - id: D4
    description: "Inserting duplicate (gpu_id, metric_name, timestamp) violates uq_gpu_metrics_natural_key unique constraint (DB-04)"
    requirement: DB-04
    verification:
      - kind: integration
        ref: "pkg/db/db_test.go#TestUniqueConstraint"
        status: pass
    human_judgment: false

duration: 13min
completed: "2026-06-29"
status: complete
---

# Phase 02 Plan 01: Storage Foundation — Schema + Connection Pool Summary

**PostgreSQL storage foundation: versioned migration creates gpu_metrics time-series table with composite index (gpu_id, timestamp DESC) and natural-key unique constraint; pkg/db exposes pgxpool initializer and iofs-embedded migration runner; all four DB-01..DB-04 requirements proven by testcontainers integration suite at 100k-row scale**

## Performance

- **Duration:** ~13 min
- **Started:** 2026-06-29T06:58:08Z
- **Completed:** 2026-06-29T07:10:51Z
- **Tasks:** 3 (TDD: RED → schema → GREEN)
- **Files modified:** 7

## Accomplishments

- `pkg/db.New(ctx, cfg)` opens a `*pgxpool.Pool` using `ParseConfig + NewWithConfig + Ping(5s)` — importable by Collector (Phase 3) and Gateway (Phase 4)
- `pkg/db.Migrate(ctx, dsn)` applies the embedded `000001_init_schema.up.sql` via `golang-migrate/iofs`; idempotent on restart (`ErrNoChange` = success); advisory lock handles concurrent startup
- `gpu_metrics` table created with composite index `idx_gpu_metrics_gpu_id_ts (gpu_id, timestamp DESC)` and unique constraint `uq_gpu_metrics_natural_key (gpu_id, metric_name, timestamp)`
- Full integration suite with testcontainers postgres:17-alpine: Snapshot/Restore per-test isolation; `TestCompositeIndexUsed` seeds 100k rows, runs `ANALYZE`, asserts `Index Scan` in EXPLAIN plan (not `enable_seqscan` shortcut)

## Task Commits

1. **Task 1: RED — deps + failing integration suite** — `24da0a9` (test)
2. **Task 2: Schema migration up/down SQL** — `0d7bd1f` (feat)
3. **Task 3: GREEN — config.go + db.go + restoreDB fix** — `87b140f` (feat)

## Files Created/Modified

- `pkg/db/config.go` — Config struct + FromEnv(); DSN required, never in error strings
- `pkg/db/db.go` — New() pgxpool constructor + Migrate() iofs+go:embed runner
- `pkg/db/db_test.go` — Integration suite: TestMain + 4 requirement tests + seedRows helper + restoreDB helper
- `pkg/db/migrations/000001_init_schema.up.sql` — CREATE TABLE gpu_metrics + 2 indexes (composite + unique)
- `pkg/db/migrations/000001_init_schema.down.sql` — DROP TABLE IF EXISTS gpu_metrics
- `go.mod` — 4 new dependencies pinned
- `go.sum` — updated

## Decisions Made

- `pgxpool.ParseConfig + NewWithConfig` rather than bare `pgxpool.New` — explicit control over `MaxConns` and `HealthCheckPeriod` without DSN manipulation
- `Migrate()` lives in `pkg/db` alongside the pool (not a separate binary) — advisory lock in golang-migrate makes concurrent startup safe; Phase 5 can wrap in init-job if needed
- `uq_gpu_metrics_natural_key (gpu_id, metric_name, timestamp)` established now — Phase 3 MUST use `INSERT ... ON CONFLICT` (not `pgx.CopyFrom`) for idempotent upserts; CopyFrom cannot express `ON CONFLICT`
- RFC3339Nano precision locked as Phase 3 requirement: `TIMESTAMPTZ` holds microseconds; Streamer restamping at second granularity would collapse same-second readings on the natural key

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed testcontainers Snapshot/Restore pool reconnection**
- **Found during:** Task 3 (GREEN — running integration tests)
- **Issue:** After `testCtr.Restore(ctx)`, all pool connections are terminated by pg_terminate_backend(). The next test's first pool operation received "FATAL: terminating connection due to administrator command" because pgxpool returned a stale terminated connection before it could detect the failure and create a new one.
- **Fix:** Added `restoreDB(ctx, t)` helper that calls `Restore()` then discards a `Ping()` error. The failing Ping causes pgxpool to evict the dead connection; the next test's first operation creates a fresh connection to the restored database.
- **Files modified:** `pkg/db/db_test.go`
- **Verification:** All 4 tests pass sequentially under `-race` flag
- **Committed in:** `87b140f` (Task 3 commit)

**2. [Rule 3 - Blocking] Added golang-migrate database/pgx/v5 sub-package to go.sum**
- **Found during:** Task 3 (building pkg/db)
- **Issue:** `go build ./pkg/db/...` failed: "missing go.sum entry for module providing package github.com/jackc/pgerrcode". The blank import `_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"` requires this transitive dep in go.sum.
- **Fix:** `go get github.com/golang-migrate/migrate/v4/database/pgx/v5@v4.19.1` to populate go.sum entry
- **Files modified:** `go.sum`
- **Verification:** `go build ./pkg/db/...` succeeds
- **Committed in:** `87b140f` (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (1 Rule 1 bug, 1 Rule 3 blocking)
**Impact on plan:** Both fixes essential for correctness. No scope creep.

## Issues Encountered

- Rancher Desktop docker socket at `~/.rd/docker.sock` (not `/var/run/docker.sock`) — testcontainers needs `DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true` on this machine. Without these env vars, the test suite fails to start the container. The `//go:build integration` tag ensures `make test` (no `-tags=integration`) skips this gracefully in Docker-less environments.

## Known Stubs

None — pkg/db is a pure library; all exported functions are fully implemented.

## Threat Flags

None — no new network endpoints, auth paths, or schema changes beyond those in the plan's threat model.

## Next Phase Readiness

- Phase 02 Plan 02 (docker-compose + smoke + Makefile + README): unblocked — pkg/db is now importable
- Phase 03 (Collector): `pkg/db.New` and `pkg/db.Migrate` are ready; must use `INSERT ... ON CONFLICT (gpu_id, metric_name, timestamp)` not `CopyFrom` for idempotent inserts (CopyFrom cannot express ON CONFLICT — this is a locked Phase 3 decision)
- Phase 04 (Gateway): `pkg/db.New` ready; composite index `(gpu_id, timestamp DESC)` proven at 100k rows

## Self-Check: PASSED

- `pkg/db/config.go` — FOUND
- `pkg/db/db.go` — FOUND
- `pkg/db/db_test.go` — FOUND
- `pkg/db/migrations/000001_init_schema.up.sql` — FOUND
- `pkg/db/migrations/000001_init_schema.down.sql` — FOUND
- Commit `24da0a9` — FOUND (test(02-01): RED)
- Commit `0d7bd1f` — FOUND (feat(02-01): schema migration)
- Commit `87b140f` — FOUND (feat(02-01): GREEN)

---
*Phase: 02-storage-foundation-schema-connection-pool*
*Completed: 2026-06-29*
