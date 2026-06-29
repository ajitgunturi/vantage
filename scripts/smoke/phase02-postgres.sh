#!/usr/bin/env bash
# Phase 2 smoke check — Storage Foundation (Postgres schema + composite index).
#
# Starts the local dev Postgres (if not already running), applies the schema
# migration, confirms the gpu_metrics table and both indexes exist, seeds
# >=100k rows, runs ANALYZE, then proves the composite index is used (not a
# sequential scan) via EXPLAIN on a selective single-gpu range query.
#
# Requires: go, docker.  Run via: make smoke-02
# (psql is NOT required on the host — all SQL runs via docker compose exec.)
# Idempotent — safe to re-run; leaves the dev stack running for inspection.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

DEV_DSN="${VANTAGE_DB_DSN:-postgres://vantage:vantage@localhost:5432/vantage?sslmode=disable}"

# Compose service + credentials (must match docker-compose.yml).
# All SQL assertions run through the postgres image already bundled in the
# compose stack — no host psql binary required.
COMPOSE_SVC="postgres"
COMPOSE_USER="vantage"
COMPOSE_DB="vantage"
COMPOSE_PASS="vantage"

if [ -t 1 ]; then GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RST=$'\033[0m'
else GREEN=''; RED=''; BOLD=''; RST=''; fi
pass() { echo "${GREEN}✓${RST} $*"; }
fail() { echo "${RED}✗ $*${RST}"; exit 1; }

command -v go     >/dev/null 2>&1 || fail "go not found on PATH"
command -v docker >/dev/null 2>&1 || fail "docker not found on PATH"

# pg_exec: run psql inside the running compose Postgres container.
# Uses PGPASSWORD so psql never prompts for a password on stdin (-T disables TTY).
pg_exec() {
  docker compose exec -T -e PGPASSWORD="$COMPOSE_PASS" "$COMPOSE_SVC" \
    psql -U "$COMPOSE_USER" -d "$COMPOSE_DB" "$@"
}

# pg_ready: lightweight connectivity probe — pg_isready is included in the
# postgres image and doesn't require a full connection handshake.
pg_ready() {
  docker compose exec -T "$COMPOSE_SVC" \
    pg_isready -U "$COMPOSE_USER" -d "$COMPOSE_DB" >/dev/null 2>&1
}

# Track whether this script started the dev stack (so cleanup can stop it).
STARTED_STACK=0
cleanup() {
  if [ "$STARTED_STACK" = 1 ]; then
    echo "stopping dev stack (started by this script)..."
    make dev-down 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "${BOLD}== Phase 2 smoke: Postgres storage foundation ==${RST}"

# ── Step 1: ensure Postgres is up ────────────────────────────────────────────
if ! pg_ready 2>/dev/null; then
  echo "dev stack not running — starting with make dev-up..."
  make dev-up || fail "make dev-up failed"
  STARTED_STACK=1
  # Additional guard: wait up to 30 s for connectivity
  for _ in $(seq 1 30); do
    pg_ready && break
    sleep 1
  done
  pg_ready || fail "Postgres did not become reachable after 30 s"
fi
pass "Postgres reachable"

# ── Step 2: apply schema migration via cmd/migrate ───────────────────────────
echo "applying schema migration..."
VANTAGE_DB_DSN="$DEV_DSN" go run ./cmd/migrate || fail "cmd/migrate failed"
pass "schema migration applied (idempotent)"

# ── Step 3: assert table + columns exist ─────────────────────────────────────
TABLE_DESC=$(pg_exec -c '\d gpu_metrics' 2>&1) || fail "psql \\d gpu_metrics failed — table may not exist"
echo "$TABLE_DESC" | grep -q 'gpu_id'       || fail "column gpu_id not found in gpu_metrics"
echo "$TABLE_DESC" | grep -q 'timestamp'    || fail "column timestamp not found in gpu_metrics"
echo "$TABLE_DESC" | grep -q 'metric_name'  || fail "column metric_name not found in gpu_metrics"
echo "$TABLE_DESC" | grep -q 'value'        || fail "column value not found in gpu_metrics"
pass "table gpu_metrics exists with expected columns (gpu_id, timestamp, metric_name, value)"

# ── Step 4: assert both indexes exist ────────────────────────────────────────
# Use a SQL query instead of \di — the \di metacommand wildcard pattern does not
# work reliably when psql is driven via -c inside docker compose exec.
IDX_LIST=$(pg_exec -c "SELECT indexname FROM pg_indexes WHERE tablename = 'gpu_metrics';" 2>&1) \
  || fail "pg_indexes query failed"
echo "$IDX_LIST" | grep -q 'idx_gpu_metrics_gpu_id_ts'  || fail "index idx_gpu_metrics_gpu_id_ts not found"
echo "$IDX_LIST" | grep -q 'uq_gpu_metrics_natural_key' || fail "unique index uq_gpu_metrics_natural_key not found"
pass "indexes idx_gpu_metrics_gpu_id_ts and uq_gpu_metrics_natural_key exist"

# ── Step 5: seed >=100k rows for planner statistics ──────────────────────────
# generate_series produces 10 GPUs × 10 metrics × 1000 timestamps = 100,000 rows.
# Timestamps span the past 24 hours in 1-second increments (86 400 s → dense range).
# Idempotent: ON CONFLICT DO NOTHING skips duplicate (gpu_id, metric_name, timestamp).
echo "seeding 100,000 rows (10 GPUs × 10 metrics × 1000 timestamps; ON CONFLICT DO NOTHING)..."
pg_exec -c "
INSERT INTO gpu_metrics (gpu_id, metric_name, timestamp, value)
SELECT
    'GPU-' || lpad(g::text, 8, '0'),
    'METRIC_' || m,
    now() - ((1000 - t) * interval '1 second'),
    random() * 100
FROM generate_series(1, 10)   AS g,
     generate_series(1, 10)   AS m,
     generate_series(1, 1000) AS t
ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING;
" >/dev/null || fail "seed INSERT failed"
pass "100,000 rows seeded (or already present)"

# ── Step 6: ANALYZE so planner statistics are current ────────────────────────
pg_exec -c 'ANALYZE gpu_metrics;' >/dev/null || fail "ANALYZE failed"
pass "ANALYZE complete"

# ── Step 7: EXPLAIN selective range query — assert Index Scan ─────────────────
# Query a single GPU over a 1-hour window ordered by timestamp DESC.
# The composite index (gpu_id, timestamp DESC) should be chosen by the planner.
TARGET_GPU='GPU-00000001'
EXPLAIN_OUTPUT=$(pg_exec -c "
EXPLAIN (FORMAT TEXT)
SELECT gpu_id, metric_name, timestamp, value
FROM   gpu_metrics
WHERE  gpu_id = '$TARGET_GPU'
  AND  timestamp >= now() - interval '1 hour'
ORDER BY timestamp DESC;
" 2>&1) || fail "EXPLAIN query failed"
echo "$EXPLAIN_OUTPUT"

echo "$EXPLAIN_OUTPUT" | grep -q 'Index Scan'       || fail "EXPLAIN did not show Index Scan — composite index not used by planner"
echo "$EXPLAIN_OUTPUT" | grep -qv 'Seq Scan on gpu_metrics' 2>/dev/null || true
if echo "$EXPLAIN_OUTPUT" | grep -q 'Seq Scan on gpu_metrics'; then
  fail "EXPLAIN shows Seq Scan on gpu_metrics — composite index is being bypassed"
fi
pass "EXPLAIN shows Index Scan (composite index used for selective gpu_id + time-range query)"

echo "${GREEN}${BOLD}PASS${RST} — Phase 2 storage smoke (table + indexes + Index Scan proven)"
