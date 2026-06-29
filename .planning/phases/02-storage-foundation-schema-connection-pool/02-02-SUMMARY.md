---
phase: 02-storage-foundation-schema-connection-pool
plan: "02"
subsystem: devops-qa
tags: [docker-compose, smoke, makefile, readme, cmd/migrate, coverage]

requires:
  - phase: 02-01
    provides: pkg/db.New, pkg/db.Migrate, pkg/db.Config, pkg/db.FromEnv — the seam cmd/migrate calls

provides:
  - docker-compose.yml dev stack (postgres:17-alpine, pgdata volume, healthcheck, local-dev-only creds)
  - cmd/migrate one-shot runner (FromEnv + db.Migrate; used by smoke script and Phase-5 init-job)
  - make dev-up / make dev-down targets in Makefile (.PHONY)
  - coverage gate scope widened from ./internal/... to ./internal/... ./pkg/... with -tags=integration
  - scripts/smoke/phase02-postgres.sh — runnable by hand; asserts table + both indexes + EXPLAIN Index Scan at 100k rows
  - README Phase-2 Storage Foundation quickstart (make dev-up, cmd/migrate, VANTAGE_DB_DSN, gpu_id-uuid convention, make smoke-02)

affects:
  - phase: 03-collector (cmd/migrate is the Phase-5 init-job pattern; smoke script is the manual verification path)
  - phase: 05-devops (coverage gate already widened; smoke suite pattern established)

tech-stack:
  added: []
  patterns:
    - docker-compose healthcheck: pg_isready -U vantage -d vantage (postgres:17-alpine, interval 2s, retries 15)
    - cmd/migrate one-shot runner: FromEnv() + db.Migrate(ctx, cfg.DSN) + log success/exit 1 on error
    - smoke harness pattern: phase01-mq.sh structure (set -euo pipefail, pass/fail helpers, dep checks, cleanup trap, STARTED_STACK guard)
    - idempotent seed: INSERT ... ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING
    - generate_series seed: 10 GPUs x 10 metrics x 1000 timestamps = 100k rows in one SQL statement
    - ANALYZE before EXPLAIN: mandatory for planner statistics to reflect seeded data (Pitfall 1)

key-files:
  created:
    - docker-compose.yml
    - cmd/migrate/main.go
    - scripts/smoke/phase02-postgres.sh
  modified:
    - Makefile
    - README.md

decisions:
  - "cmd/migrate is a separate binary (not a Makefile shell command) — enables reuse by Phase-5 k8s init-job without shell dependency; also lets the smoke script run it with a simple go run"
  - "STARTED_STACK=0 guard in smoke script: tracks whether the script started docker compose so cleanup can stop it; leaves it running if it was already up (inspector convenience)"
  - "ON CONFLICT DO NOTHING in seed: makes the smoke script idempotent; running twice does not fail or double the row count"
  - "Smoke script leaves dev stack running: T-02-09 disposition is 'accept'; operator runs make dev-down when done"

metrics:
  duration: 8min
  completed: "2026-06-29"
  tasks: 3
  files_modified: 5

status: complete
---

# Phase 02 Plan 02: Dev Stack + Smoke + Coverage + README Summary

**docker-compose.yml dev stack, cmd/migrate one-shot runner, Phase-2 smoke script (table + index + EXPLAIN Index Scan at 100k rows), Makefile dev-up/dev-down + widened coverage gate (./pkg/...), and README Phase-2 quickstart — storage foundation fully human-verifiable**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-06-29T07:48:11Z
- **Completed:** 2026-06-29T07:56:00Z
- **Tasks:** 3
- **Files modified:** 5

## Accomplishments

- `docker-compose.yml`: `postgres:17-alpine` with `pg_isready` healthcheck, named `pgdata` volume, local-dev-only credentials annotated inline (T-02-06 mitigated)
- `cmd/migrate/main.go`: calls `db.FromEnv()` + `db.Migrate(ctx, cfg.DSN)`; logs success and exits non-zero on error; DSN never logged (T-02-07 mitigated)
- `Makefile`: `dev-up` (`docker compose up -d --wait`) + `dev-down` (`docker compose down`) in `.PHONY`; coverage target scope widened to `./internal/... ./pkg/...` with `-tags=integration`
- `scripts/smoke/phase02-postgres.sh`: phase01-mq.sh harness; deps (go/psql/docker); `make dev-up` if needed; `go run ./cmd/migrate`; `psql \d` table + column asserts; `psql \di` index asserts; 100k-row `generate_series` seed (ON CONFLICT DO NOTHING); `ANALYZE`; `EXPLAIN` Index Scan assert; idempotent
- `README.md`: PostgreSQL row flipped to shipped; Prerequisites updated (Docker + docker compose + psql); Phase-2 quickstart section (dev-up, cmd/migrate, env vars, gpu_id-uuid convention, smoke-02); Phase status table updated

## Task Commits

1. **Task 1: docker-compose dev stack + cmd/migrate + Makefile dev-up/down + coverage scope** — `7c3e2a6` (feat)
2. **Task 2: Phase-2 smoke script** — `4ac83b6` (feat)
3. **Task 3: README Phase-2 Storage Foundation quickstart** — `d5bbd0d` (docs)

## Files Created/Modified

- `docker-compose.yml` — postgres:17-alpine dev stack; local-dev-only creds annotated
- `cmd/migrate/main.go` — one-shot migration runner; no DSN in logs
- `scripts/smoke/phase02-postgres.sh` — human-runnable smoke; table + indexes + EXPLAIN Index Scan
- `Makefile` — dev-up/dev-down targets; coverage scope widened to ./pkg/... with -tags=integration
- `README.md` — Phase-2 quickstart; gpu_id-uuid convention; env var table; phase status updated

## Decisions Made

- `cmd/migrate` is a standalone binary (not a shell snippet) — reusable by Phase-5 k8s init-job without shell dependency
- `STARTED_STACK` guard in smoke script: stops docker compose on EXIT only if this script started it; leaves it running if already up
- `ON CONFLICT DO NOTHING` seed: makes smoke idempotent (safe to re-run without failure)
- Coverage scope widened with `-tags=integration` so testcontainers db tests count toward the 90% gate

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None — all deliverables are fully wired. `cmd/migrate` calls the real `pkg/db.Migrate`; the smoke script exercises real Postgres; `make dev-up/dev-down` drive real `docker compose`.

## Threat Flags

None — no new network endpoints, auth paths, or schema changes beyond the plan's threat model. docker-compose credentials are local-dev defaults (T-02-06: annotated inline as such). cmd/migrate does not log the DSN (T-02-07). Smoke SQL uses fixed literals only (T-02-08).

## Self-Check: PASSED

- `docker-compose.yml` — FOUND
- `cmd/migrate/main.go` — FOUND
- `scripts/smoke/phase02-postgres.sh` — FOUND
- Commit `7c3e2a6` — FOUND (feat(02-02): dev stack, cmd/migrate runner, Makefile dev-up/down + coverage scope)
- Commit `4ac83b6` — FOUND (feat(02-02): Phase-2 smoke script)
- Commit `d5bbd0d` — FOUND (docs(02-02): README Phase-2 Storage Foundation quickstart)

---
*Phase: 02-storage-foundation-schema-connection-pool*
*Completed: 2026-06-29*
