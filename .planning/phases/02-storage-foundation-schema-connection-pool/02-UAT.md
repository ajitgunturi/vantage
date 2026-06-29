---
status: resolved
phase: 02-storage-foundation-schema-connection-pool
source: [02-VERIFICATION.md]
started: 2026-06-29T08:46:38Z
updated: 2026-06-29T09:40:00Z
---

## Current Test

number: 1
name: Run `make smoke-02` end-to-end against the local dev stack
expected: |
  Script applies the migration via cmd/migrate, confirms the gpu_metrics table
  and both indexes (composite + natural-key unique), seeds 100k rows, runs
  ANALYZE, and prints "Index Scan" in EXPLAIN output (NOT "Seq Scan on
  gpu_metrics"). Exits 0 with a green PASS banner across all 7 steps.
awaiting: none — resolved 2026-06-29 (passed; harness containerized, host psql no longer required)

## Tests

### Test 1 — make smoke-02 (QA-06)

- **status:** passed — 2026-06-29. `make smoke-02` exits 0, all 7 steps green,
  EXPLAIN shows `Bitmap Index Scan on idx_gpu_metrics_gpu_id_ts` (no Seq Scan).
  Verified with no host `psql` installed; the harness runs psql via
  `docker compose exec postgres` (commit 0f50716).
- **expected:** All 7 steps pass: postgres up → `cmd/migrate` applies schema →
  table+columns asserted → both indexes asserted → 100k seed → ANALYZE →
  EXPLAIN shows `Index Scan` not `Seq Scan`. Green PASS banner, exit 0.
- **why_human:** `psql` is not installed on this machine, so the smoke script
  cannot run past its dependency check. The underlying Index Scan property is
  already independently proven by `TestCompositeIndexUsed` (testcontainers,
  100k rows + ANALYZE-then-EXPLAIN), so this is a confidence check on the
  human-runnable harness itself, not a correctness gap.
- **how_to_run:**
  ```sh
  brew install libpq && brew link --force libpq   # if psql is not installed
  export DOCKER_HOST="unix://$HOME/.rd/docker.sock"
  export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE="$HOME/.rd/docker.sock"
  make smoke-02
  ```
