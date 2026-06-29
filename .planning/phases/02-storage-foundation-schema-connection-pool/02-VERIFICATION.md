---
phase: 02-storage-foundation-schema-connection-pool
verified: 2026-06-29T09:00:00Z
status: passed
score: 8/8 must-haves verified
behavior_unverified: 0
overrides_applied: 0
resolution_note: "QA-06 (make smoke-02) cleared 2026-06-29. The smoke harness was containerized (commit 0f50716) so psql runs via `docker compose exec postgres` — no host psql required. `make smoke-02` now exits 0 with all 7 steps green and EXPLAIN showing `Bitmap Index Scan on idx_gpu_metrics_gpu_id_ts`. See 02-UAT.md (status: resolved)."
---

# Phase 02: Storage Foundation Verification Report

**Phase Goal:** PostgreSQL holds GPU telemetry in a time-series schema whose composite index is provably used, accessed through a shared pgxpool that both Collector and Gateway reuse.
**Verified:** 2026-06-29T09:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Migration creates gpu_metrics table with gpu_id, timestamp TIMESTAMPTZ, metric_name, value (DB-01) | VERIFIED | `TestMigration` passed (integration, testcontainers postgres:17-alpine); `000001_init_schema.up.sql` has correct DDL; information_schema.columns query confirms all 4 required columns present |
| 2 | EXPLAIN on (gpu_id, timestamp DESC) range query at 100k ANALYZE'd rows shows Index Scan, not Seq Scan (DB-02) | VERIFIED | `TestCompositeIndexUsed` passed (integration, -race, 100k rows seeded via parameterized INSERT, ANALYZE run, EXPLAIN asserts "Index Scan" and NotContains "Seq Scan on gpu_metrics"); no enable_seqscan used |
| 3 | pkg/db.New returns healthy *pgxpool.Pool importable by Collector and Gateway; pkg/db.Migrate applies embedded SQL idempotently (DB-03) | VERIFIED | `TestNew` passed; `db.go` uses ParseConfig+NewWithConfig+Ping(5s timeout); Migrate uses iofs+go:embed, pgx5:// conversion, ErrNoChange treated as success; both unit + integration tests pass |
| 4 | Duplicate (gpu_id, metric_name, timestamp) violates uq_gpu_metrics_natural_key (DB-04) | VERIFIED | `TestUniqueConstraint` passed; error string contains "uq_gpu_metrics_natural_key"; UNIQUE INDEX in up.sql is on (gpu_id, metric_name, timestamp) |
| 5 | make dev-up starts postgres:17-alpine via docker compose and waits for healthcheck; make dev-down stops it (OPS-06) | VERIFIED | docker-compose.yml: postgres:17-alpine + pg_isready healthcheck (interval 2s, retries 15, --wait); Makefile: dev-up (`docker compose up -d --wait`) + dev-down (`docker compose down`); both in .PHONY |
| 6 | make smoke-02 runs scripts/smoke/phase02-postgres.sh — asserts table + composite index + EXPLAIN Index Scan (QA-06) | PRESENT_BEHAVIOR_UNVERIFIED | Script exists, passes `bash -n`, follows phase01 harness (set -euo pipefail, pass/fail helpers, dep checks); ANALYZE + Index Scan assertion logic is correct; `make smoke-%` wildcard discovers it. End-to-end execution blocked by psql absent from PATH. |
| 7 | Coverage gate scope widened to ./pkg/... so pkg/db is on the >=90% gate | VERIFIED | Makefile coverage target: `go list ./internal/... ./pkg/... \| grep -v '/pkg/pb'` with `-tags=integration`; orchestrator confirmed 94.1% >= 90% with pkg/db at 91.7% |
| 8 | README has Phase-2 Storage Foundation quickstart (make dev-up, cmd/migrate, VANTAGE_DB_DSN, gpu_id-uuid, smoke-02) (DOC-01) | VERIFIED | README contains Phase 2 section; grep confirms: "Storage Foundation", "make dev-up", "VANTAGE_DB_DSN", "smoke-02", gpu_id-uuid convention, Phase status table updated to shipped |

**Score:** 7/8 truths verified (1 present, behavior-unverified)

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/db/migrations/000001_init_schema.up.sql` | CREATE TABLE + composite index + unique constraint | VERIFIED | All three DDL objects present; sql comments document D-04 gpu_id-uuid and RFC3339Nano restamp requirement; no CONCURRENTLY (transaction-safe) |
| `pkg/db/migrations/000001_init_schema.down.sql` | DROP TABLE IF EXISTS gpu_metrics | VERIFIED | Correct single-statement drop; indexes drop with table |
| `pkg/db/config.go` | Config{DSN,MaxConns} + FromEnv() | VERIFIED | FromEnv hard-errors on missing VANTAGE_DB_DSN without including the DSN value in error; MaxConns parsed with strconv.ParseInt base-10 bitSize-32 |
| `pkg/db/db.go` | New (pgxpool) + Migrate (iofs/go:embed) | VERIFIED | ParseConfig+NewWithConfig+Ping(5s); iofs source; pgx5:// DSN conversion; ErrNoChange accepted; all errors wrapped as "db: <context>: %w" without DSN |
| `pkg/db/db_test.go` | integration test suite (//go:build integration) | VERIFIED | TestMain + 4 requirement tests + seedRows + restoreDB; build-tagged integration; parameterized seedRows (no string SQL); assert.Contains for plan (non-fatal) |
| `pkg/db/config_test.go` | unit tests (no build tag) | VERIFIED | 4 functions covering all FromEnv branches + New/Migrate error paths; runs under `go test` (no Docker); contributed 91.7% coverage for pkg/db |
| `docker-compose.yml` | postgres:17-alpine dev stack | VERIFIED | Image, healthcheck (pg_isready -U vantage -d vantage), named pgdata volume, ports 5432:5432; credentials annotated LOCAL DEV ONLY |
| `cmd/migrate/main.go` | one-shot migration runner | VERIFIED | Calls db.FromEnv() + db.Migrate(ctx, cfg.DSN); exits 1 on error; DSN never logged |
| `scripts/smoke/phase02-postgres.sh` | human-runnable smoke (table + indexes + EXPLAIN Index Scan) | PRESENT_BEHAVIOR_UNVERIFIED | Syntactically correct; correct assertions for Index Scan and both index names; ANALYZE before EXPLAIN; ON CONFLICT DO NOTHING seed; psql not on PATH in verifier |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/db/db.go` | golang-migrate pgx/v5 driver | blank import `_ ".../database/pgx/v5"`; `strings.Replace("postgres://", "pgx5://", 1)` | WIRED | Pitfall-3 guard confirmed; scheme conversion on line 78 of db.go |
| `pkg/db/db_test.go` | testcontainers Snapshot/Restore | `WithUsername("postgres")` + `WithSQLDriver("pgx")` + DB name "vantage_test" (not "postgres") | WIRED | Pitfall-5 guards present; restoreDB helper flushes dead pool connections with discarded Ping |
| `cmd/migrate/main.go` | `pkg/db.Migrate` | `db.FromEnv()` + `db.Migrate(ctx, cfg.DSN)` | WIRED | Direct import of `github.com/ajitg/vantage/pkg/db`; called in `run()` function |
| `scripts/smoke/phase02-postgres.sh` | `cmd/migrate` | `go run ./cmd/migrate` with `VANTAGE_DB_DSN` exported | WIRED | Step 2 of smoke script; uses the same DSN env var pattern as production |
| Makefile `coverage` | `./pkg/...` | `go list ./internal/... ./pkg/... | grep -v '/pkg/pb'` with `-tags=integration` | WIRED | Widened from ./internal/... only; integration tags included so testcontainers tests count toward gate |
| `go.mod` | pgx/v5, golang-migrate, testcontainers | `go get` pins at v5.10.0 / v4.19.1 / v0.43.0 | WIRED | Commits 24da0a9 (deps) + 87b140f (go.sum pgx5:// sub-package) |

---

### Data-Flow Trace (Level 4)

Not applicable to this phase. pkg/db is a pure library (no UI component, no page, no API handler rendering dynamic data). Its data flow is: DSN env var → pgxpool → Postgres ← verified end-to-end by integration tests.

---

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| pkg/db builds cleanly | `go build ./pkg/db/...` | exit 0 | PASS |
| cmd/migrate builds cleanly | `go build ./cmd/migrate/...` | exit 0 | PASS |
| Unit tests pass (no Docker) | `go test -race ./pkg/db/... -count=1` | `ok github.com/ajitg/vantage/pkg/db 1.337s` | PASS |
| All 4 integration requirement tests pass (Docker) | `DOCKER_HOST=unix://$HOME/.rd/docker.sock ... go test -race -tags=integration ./pkg/db/... -count=1 -v` | TestMigration PASS, TestNew PASS, TestUniqueConstraint PASS, TestCompositeIndexUsed PASS (35.4s, 100k rows) | PASS |
| Smoke script is syntactically valid | `bash -n scripts/smoke/phase02-postgres.sh` | exit 0 | PASS |
| Smoke script end-to-end (make smoke-02) | Requires psql on PATH | psql not found — cannot run | SKIP (human needed) |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| DB-01 | 02-01-PLAN.md | Time-series schema with gpu_id, timestamp TIMESTAMPTZ, numeric metric columns | SATISFIED | up.sql DDL + TestMigration passed (integration) |
| DB-02 | 02-01-PLAN.md | Composite index (gpu_id, timestamp DESC) — EXPLAIN-verified at scale | SATISFIED | TestCompositeIndexUsed passed: 100k rows, ANALYZE, Index Scan confirmed, Seq Scan not present |
| DB-03 | 02-01-PLAN.md | pgxpool shared in pkg/db, importable by Collector and Gateway | SATISFIED | db.go New()+Migrate() implemented; cmd/migrate imports it; TestNew passes |
| DB-04 | 02-01-PLAN.md | Natural-key unique constraint for idempotent inserts | SATISFIED | uq_gpu_metrics_natural_key on (gpu_id, metric_name, timestamp); TestUniqueConstraint passes |
| OPS-06 | 02-02-PLAN.md (success criteria) | docker-compose.yml dev stack + make dev-up/dev-down | SATISFIED | docker-compose.yml + Makefile targets verified; Phase 2 establishes harness |
| QA-06 | 02-02-PLAN.md (success criteria) | Runnable manual smoke suite (scripts/smoke/phase02-postgres.sh via make smoke-02) | PARTIAL (human needed) | Script exists and is syntactically correct; make smoke-02 wildcard discovers it; end-to-end run requires psql |
| DOC-01 | 02-02-PLAN.md (success criteria) | Living README.md grown with Phase-2 quickstart | SATISFIED (Phase-2 portion) | README Phase 2 section verified; cross-cutting requirement continues through all phases |

**Orphaned requirements:** None. All requirement IDs declared in both PLAN frontmatter fields (DB-01..DB-04 in both plans) are accounted for.

**REQUIREMENTS.md tracking note:** DB-01..DB-04 are marked [x] complete (commit 3281f16). OPS-06 in the REQUIREMENTS.md traceability table is still shown "Pending" even though the Phase 2 delivery is complete — the checkbox `- [ ] **OPS-06**` was not updated in 3281f16. This is a documentation tracking gap, not an implementation gap. DOC-01/QA-06 are intentionally marked Pending as they are ongoing cross-cutting requirements.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | No TBD/FIXME/XXX in any phase-modified file | — | Clean |
| `scripts/smoke/phase02-postgres.sh` | 110 | `grep -qv 'Seq Scan on gpu_metrics' ... \|\| true` is logically redundant (would exit 0 if any output line differs from pattern — always true) | INFO | No impact; the actual Seq Scan gate is lines 111-113 (`if grep -q ... then fail`) which is correct. Redundant line cannot cause false pass or false fail. |

No debt markers. No placeholder implementations. No hardcoded empty returns. No string-built SQL in any non-test source file. No enable_seqscan trick in tests or smoke script (comment on db_test.go line 208 explicitly disclaims it). No DSN value in any error string (config.go error says "VANTAGE_DB_DSN is required"; db.go errors say "db: parse config:", "db: create pool:", "db: ping:").

---

### Prohibition Verification

| Prohibition | Status | Evidence |
|-------------|--------|----------|
| NO third-party message broker | PASS | No Kafka/NATS/Rabbit/Redis imports in any Phase-2 file |
| NO hardcoded DB credentials in code; DSN from VANTAGE_DB_DSN only | PASS | config.go reads os.Getenv("VANTAGE_DB_DSN"); docker-compose.yml creds annotated LOCAL DEV ONLY; cmd/migrate never logs DSN |
| NO string-built SQL with interpolated values | PASS | seedRows uses `INSERT ... VALUES ($1, $2, $3, $4)`; smoke script uses generate_series with fixed literals; no string concatenation into SQL anywhere |
| NO destructive auto-migration on startup | PASS | db.go calls only `m.Up()`; ErrNoChange = success; down.sql never auto-applied |
| NO SET enable_seqscan=off to fake index proof | PASS | Not present in db_test.go or smoke script; db_test.go comment line 208 explicitly says "without enable_seqscan tricks" |

---

### Human Verification Required

#### 1. Smoke Script End-to-End (make smoke-02)

**Test:** Install psql (`brew install postgresql@17` or equivalent), export the Rancher Desktop socket env vars, then run:

```bash
export DOCKER_HOST="unix://$HOME/.rd/docker.sock"
export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE="$HOME/.rd/docker.sock"
export TESTCONTAINERS_RYUK_DISABLED=true
make smoke-02
```

**Expected:** Script completes all 7 steps with green checkmarks:
1. Postgres reachable (or starts via make dev-up)
2. Schema migration applied (idempotent)
3. Table gpu_metrics exists with gpu_id, timestamp, metric_name, value
4. Indexes idx_gpu_metrics_gpu_id_ts and uq_gpu_metrics_natural_key exist
5. 100,000 rows seeded (ON CONFLICT DO NOTHING)
6. ANALYZE complete
7. EXPLAIN shows Index Scan (not Seq Scan on gpu_metrics)

Final output: `PASS — Phase 2 storage smoke (table + indexes + Index Scan proven)`

**Why human:** psql is not on PATH in the verifier environment. The underlying Index Scan property has been independently verified by TestCompositeIndexUsed (integration tests with testcontainers, 100k rows, ANALYZE, same assertion pattern). The smoke script verification confirms the human-runnable harness works end-to-end.

---

### Gaps Summary

No gaps. All four ROADMAP success criteria are fully verified by passing integration tests:

- DB-01: TestMigration PASS — table and columns confirmed in information_schema
- DB-02: TestCompositeIndexUsed PASS — Index Scan confirmed at 100k ANALYZE'd rows, no Seq Scan
- DB-03: TestNew PASS — pgxpool healthy; Migrate idempotent
- DB-04: TestUniqueConstraint PASS — named constraint violation confirmed

The one item flagged for human verification (smoke-02 end-to-end) is a manual QA harness by design (QA-06). It is not blocking the core phase goal — its underlying behavioral claim (Index Scan at scale) is proven programmatically.

---

_Verified: 2026-06-29T09:00:00Z_
_Verifier: Claude (gsd-verifier)_
