#!/usr/bin/env bash
# Phase 3 smoke check — Pipeline (Streamer + Collector + Postgres).
#
# Proves the full CSV → Streamer → MQ → Collector → Postgres data flow against
# the local dev stack. Builds and starts the three binaries in the background,
# lets the pipeline run for ~5 seconds, then asserts via psql (inside the
# compose postgres container — no host psql required):
#
#   1. count(*) FROM gpu_metrics > 0                (rows landed)
#   2. count(*) == count(DISTINCT natural key)       (zero duplicate rows)
#   3. No ordinal gpu_id values ('0','1',…)         (UUID mapping held end-to-end)
#
# Requires: go, docker. Run via: make smoke-03
# Idempotent — safe to re-run. Leaves the dev Postgres running for inspection;
# kills only the three background pipeline processes on exit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

DEV_DSN="${VANTAGE_DB_DSN:-postgres://vantage:vantage@localhost:5432/vantage?sslmode=disable}"

# Compose service + credentials (must match docker-compose.yml).
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

# ── pg_exec: run psql inside the running compose Postgres container ───────────
# Uses PGPASSWORD so psql never prompts for a password on stdin (-T disables TTY).
pg_exec() {
  docker compose exec -T -e PGPASSWORD="$COMPOSE_PASS" "$COMPOSE_SVC" \
    psql -U "$COMPOSE_USER" -d "$COMPOSE_DB" "$@"
}

# ── pg_ready: lightweight connectivity probe ──────────────────────────────────
pg_ready() {
  docker compose exec -T "$COMPOSE_SVC" \
    pg_isready -U "$COMPOSE_USER" -d "$COMPOSE_DB" >/dev/null 2>&1
}

# ── PID tracking for cleanup ──────────────────────────────────────────────────
MQ_PID=""
COLLECTOR_PID=""
STREAMER_PID=""

cleanup() {
  # Kill only the three background pipeline binaries; leave the dev Postgres running.
  for P in "$MQ_PID" "$COLLECTOR_PID" "$STREAMER_PID"; do
    [ -n "$P" ] && kill "$P" 2>/dev/null || true
  done
}
trap cleanup EXIT

echo "${BOLD}== Phase 3 smoke: Streamer + Collector + Postgres pipeline ==${RST}"

# ── Step 1: ensure Postgres is up ────────────────────────────────────────────
if ! pg_ready 2>/dev/null; then
  echo "dev stack not running — starting with make dev-up..."
  make dev-up || fail "make dev-up failed"
  for _ in $(seq 1 30); do pg_ready && break; sleep 1; done
  pg_ready || fail "Postgres did not become reachable after 30s"
fi
pass "Postgres reachable"

# ── Step 2: find the DCGM CSV ────────────────────────────────────────────────
# Resolve the most-recently-modified dcgm_metrics_*.csv in the repo root.
CSV=$(ls -t "$ROOT"/dcgm_metrics_*.csv 2>/dev/null | head -1)
if [ -z "$CSV" ]; then
  fail "No dcgm_metrics_*.csv found in $ROOT — copy a DCGM CSV here before running make smoke-03"
fi
pass "DCGM CSV: $CSV"

# ── Step 3: build the three service binaries ──────────────────────────────────
echo "building mq, collector, streamer..."
go build -o bin/mq       ./cmd/mq       || fail "go build cmd/mq failed"
go build -o bin/collector ./cmd/collector || fail "go build cmd/collector failed"
go build -o bin/streamer  ./cmd/streamer  || fail "go build cmd/streamer failed"
pass "binaries built"

# ── Step 4: kill any stale pipeline processes from a previous run ─────────────
for BINARY in bin/mq bin/collector bin/streamer; do
  pkill -f "$ROOT/$BINARY" 2>/dev/null || true
done
sleep 0.5  # brief pause to release ports

# ── Step 5: start MQ in background (gRPC :50051, HTTP :8080) ─────────────────
MQ_GRPC_ADDR=":50051" MQ_HTTP_ADDR=":8080" \
  ./bin/mq &
MQ_PID=$!
echo "mq started (PID $MQ_PID)"
# Brief pause for the MQ gRPC listener to bind before consumers connect.
sleep 1

# ── Step 6: start Collector in background (auto-migrates schema) ──────────────
# VANTAGE_DB_DSN and COLLECTOR_MQ_ADDR are the only required env vars.
VANTAGE_DB_DSN="$DEV_DSN" \
  COLLECTOR_MQ_ADDR=":50051" \
  COLLECTOR_BATCH_SIZE="500" \
  COLLECTOR_FLUSH_MS="200" \
  COLLECTOR_CREDIT="200" \
  ./bin/collector &
COLLECTOR_PID=$!
echo "collector started (PID $COLLECTOR_PID)"
# Wait for auto-migration to complete before the streamer starts producing.
sleep 2

# ── Step 7: start Streamer in background (loops CSV, 1ms inter-row delay) ─────
# STREAMER_CSV_PATH points at the resolved DCGM CSV; STREAMER_MQ_ADDR is the MQ.
STREAMER_CSV_PATH="$CSV" \
  STREAMER_MQ_ADDR=":50051" \
  STREAMER_LOOP_DELAY_MS="1" \
  ./bin/streamer &
STREAMER_PID=$!
echo "streamer started (PID $STREAMER_PID)"

# ── Step 8: let the pipeline flow ────────────────────────────────────────────
echo "pipeline running — waiting 5s for telemetry to land in Postgres..."
sleep 5

# ── Step 9: assert rows landed (count(*) > 0) ────────────────────────────────
TOTAL=$(pg_exec -tAc "SELECT count(*) FROM gpu_metrics;" 2>&1 | tr -d '[:space:]')
# Guard: psql output should be a plain integer; any non-numeric output means an error.
[[ "$TOTAL" =~ ^[0-9]+$ ]] || fail "count(*) query returned non-integer: $TOTAL"
[ "$TOTAL" -gt 0 ]         || fail "gpu_metrics is empty after 5s — pipeline produced no rows"
pass "Rows persisted: $TOTAL > 0"

# ── Step 10: assert exactly-once (zero duplicate natural keys) ────────────────
DISTINCT=$(pg_exec -tAc \
  "SELECT count(*) FROM (SELECT DISTINCT gpu_id, metric_name, timestamp FROM gpu_metrics) d;" \
  2>&1 | tr -d '[:space:]')
[[ "$DISTINCT" =~ ^[0-9]+$ ]] || fail "count(distinct) query returned non-integer: $DISTINCT"
[ "$TOTAL" -eq "$DISTINCT" ]   || \
  fail "Duplicate rows detected: count(*) $TOTAL != count(distinct) $DISTINCT (exactly-once violated)"
pass "Exactly-once: count(*) == count(distinct natural key) ($TOTAL rows, zero duplicates)"

# ── Step 11: assert UUID mapping (no ordinal gpu_id values) ──────────────────
# D-04 (models.FromProto): db.gpu_id must hold UUIDs like "GPU-5fd4f087-..."
# NOT the CSV ordinal values "0","1","2".
ORDINALS=$(pg_exec -tAc \
  "SELECT count(*) FROM gpu_metrics WHERE gpu_id IN ('0','1','2','3','4','5','6','7','8','9');" \
  2>&1 | tr -d '[:space:]')
[[ "$ORDINALS" =~ ^[0-9]+$ ]] || fail "ordinal check query returned non-integer: $ORDINALS"
[ "$ORDINALS" -eq 0 ]         || \
  fail "Ordinal gpu_id values found ($ORDINALS rows) — UUID mapping (D-04) failed; check models.FromProto"
pass "UUID mapping: gpu_id values are GPU UUIDs, not ordinals (D-04 verified)"

# ── Step 12: print summary ────────────────────────────────────────────────────
GPU_COUNT=$(pg_exec -tAc "SELECT count(distinct gpu_id) FROM gpu_metrics;" 2>&1 | tr -d '[:space:]')
echo "${GREEN}${BOLD}PASS${RST} — Phase 3 pipeline smoke"
echo "       Rows:          $TOTAL"
echo "       Distinct rows: $DISTINCT"
echo "       Distinct GPUs: $GPU_COUNT"
echo ""
echo "  Leave Postgres running for inspection:"
echo "    docker compose exec -T postgres psql -U vantage -d vantage -c 'SELECT gpu_id, metric_name, timestamp, value FROM gpu_metrics ORDER BY timestamp DESC LIMIT 10;'"
echo "  Stop pipeline background processes: kill $MQ_PID $COLLECTOR_PID $STREAMER_PID"
