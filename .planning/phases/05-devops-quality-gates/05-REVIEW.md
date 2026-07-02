---
phase: 05-devops-quality-gates
status: issues
depth: standard
files_reviewed: 19
findings:
  critical: 0
  warning: 1
  info: 3
  total: 4
reviewed: 2026-07-02
---

# Phase 5 Code Review — DevOps + Quality Gates

**Depth:** standard | **Files:** 19 (5 Dockerfiles, Makefile, 10 Helm files, compose, fixture, harness, 2 scripts, README, go.mod/go.sum)

## Verified Good

- All five images build (`make docker` exit 0); migrate image inspected
- `helm lint` 0-failed; `helm template` renders MQ with literal `replicas: 1` + `Recreate`; values.yaml carries no replicas/strategy keys
- busybox `nc -z` empirically verified (exit 0 open / 1 closed) — migrate-job init wait loop is sound
- Harness ran green against the live five-container stack (16.3s); hermetic teardown confirmed (no residual containers)
- `make test` + `make coverage` (90.3%) + `make lint` all green; e2e tag isolation proven
- Both shell scripts pass `bash -n`, use `set -euo pipefail`, trap-based cleanup, fail-fast cluster guards
- No secrets in images (DSN injected at runtime); dev credentials documented LOCAL DEV ONLY (accepted threat T-05-04)

## Findings

### WR-01 [Warning] No .dockerignore — build context ships .git and local CSVs
- **Files:** build/*.Dockerfile (all five)
- **Issue:** `COPY . .` sends the full repo as build context: `.git/`, `bin/`, `coverage.out`, and any local gitignored `dcgm_metrics_*.csv` (potentially hundreds of MB). Builds work but context upload is slow and cache-busts on unrelated file changes.
- **Fix:** Add a `.dockerignore` with `.git`, `bin/`, `coverage.*`, `dcgm_metrics_*.csv`, `.planning/`, `deployments/`, `*.md`.
- **Impact:** Build speed/caching only — no correctness or security impact.

### IN-01 [Info] soak.sh hardcodes Bitnami pod name
- **File:** scripts/soak.sh (`row_count` uses `vantage-postgresql-0`)
- **Note:** Correct for the `vantage` release + Bitnami StatefulSet naming; breaks silently under a different release name. Consistent with the scripts' RELEASE=vantage scoping — acceptable for the local workflow.

### IN-02 [Info] harness pollGPUs retries all failures uniformly
- **File:** test/harness/harness_test.go
- **Note:** A persistent gateway 500 burns the full 60s deadline before a generic timeout message. Distinguishing "connection refused" (stack starting) from HTTP 5xx (real failure) would fail faster with a clearer message. Cosmetic — the deadline bounds it.

### IN-03 [Info] docker-compose.full.yml maps mq host ports 50051/8080
- **File:** docker-compose.full.yml
- **Note:** Collides if a local dev MQ process is listening on those ports when the harness runs. The postgres collision was fixed (no 5432 mapping); mq mappings kept intentionally for manual inspect access during KEEP=1 debugging.

## Recommendation

WR-01 is a cheap quality win (`/gsd-code-review 5 --fix` or add .dockerignore manually). Nothing blocks phase verification.
