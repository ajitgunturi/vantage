#!/usr/bin/env bash
# Phase 4 smoke check — API Gateway.
#
# Proves the gateway binary boots, serves a valid OpenAPI spec, and correctly
# handles API requests against a running Postgres dev stack:
#
#   1. GET /api/v1/gpus          → 200 + JSON array
#   2. GET /api/v1/gpus/<id>/telemetry → 200 + JSON array (known GPU)
#   3. GET /api/v1/gpus/GPU-does-not-exist/telemetry → 404 (unknown GPU)
#   4. GET /swagger/doc.json     → 200 + valid JSON spec
#
# Requires: go, docker, curl. Run via: make smoke-04
# Idempotent — safe to re-run. Leaves the dev Postgres running for inspection;
# kills only the gateway process on exit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

DEV_DSN="${VANTAGE_DB_DSN:-postgres://vantage:vantage@localhost:5432/vantage?sslmode=disable}"
GATEWAY_ADDR="${GATEWAY_ADDR:-:8080}"
GATEWAY_HOST="localhost${GATEWAY_ADDR}"   # e.g. localhost:8080

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
command -v curl   >/dev/null 2>&1 || fail "curl not found on PATH"

# ── pg_exec: run psql inside the running compose Postgres container ───────────
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
GATEWAY_PID=""

cleanup() {
  [ -n "$GATEWAY_PID" ] && kill "$GATEWAY_PID" 2>/dev/null || true
}
trap cleanup EXIT

echo "${BOLD}== Phase 4 smoke: API Gateway ==${RST}"

# ── Step 1: ensure Postgres is up ────────────────────────────────────────────
if ! pg_ready 2>/dev/null; then
  echo "dev stack not running — starting with make dev-up..."
  make dev-up || fail "make dev-up failed"
  for _ in $(seq 1 30); do pg_ready && break; sleep 1; done
  pg_ready || fail "Postgres did not become reachable after 30s"
fi
pass "Postgres reachable"

# ── Step 2: build the gateway binary ─────────────────────────────────────────
echo "building gateway..."
go build -o bin/gateway ./cmd/gateway || fail "go build cmd/gateway failed"
pass "gateway binary built"

# ── Step 3: kill any stale gateway process from a previous run ───────────────
pkill -f "$ROOT/bin/gateway" 2>/dev/null || true
sleep 0.5  # brief pause to release the port

# ── Step 4: ensure there are some GPU rows for the API to return ─────────────
# Seed a minimal GPU + metric row if the table is empty, so /gpus is non-empty.
ROW_COUNT=$(pg_exec -tAc "SELECT count(*) FROM gpu_metrics;" 2>&1 | tr -d '[:space:]')
if [[ "$ROW_COUNT" == "0" ]]; then
  echo "gpu_metrics is empty — seeding one row for smoke assertions..."
  pg_exec -c "
    INSERT INTO gpu_metrics (gpu_id, timestamp, metric_name, value)
    VALUES ('GPU-smoke-test-00000000', NOW(), 'DCGM_FI_DEV_GPU_UTIL', 42.0)
    ON CONFLICT DO NOTHING;" >/dev/null 2>&1
fi

# ── Step 5: discover a GPU UUID from the DB (no host psql required) ──────────
SEED_GPU=$(pg_exec -tAc "SELECT gpu_id FROM gpu_metrics LIMIT 1;" 2>&1 | tr -d '[:space:]')
[ -n "$SEED_GPU" ] || fail "could not discover a gpu_id from gpu_metrics — table may be empty"
pass "Found GPU for telemetry assertion: $SEED_GPU"

# ── Step 6: start gateway in background ──────────────────────────────────────
VANTAGE_DB_DSN="$DEV_DSN" GATEWAY_ADDR="$GATEWAY_ADDR" \
  ./bin/gateway &
GATEWAY_PID=$!
echo "gateway started (PID $GATEWAY_PID)"

# ── Step 7: wait for gateway to become ready (up to 15s) ─────────────────────
READY=0
for _ in $(seq 1 15); do
  if curl -sf "http://${GATEWAY_HOST}/api/v1/gpus" >/dev/null 2>&1; then
    READY=1; break
  fi
  sleep 1
done
[ "$READY" = 1 ] || fail "gateway did not become ready on $GATEWAY_HOST after 15s"
pass "gateway ready on $GATEWAY_HOST"

# ── Step 8: assert GET /api/v1/gpus → 200 + JSON array ───────────────────────
HTTP_CODE=$(curl -s -o /tmp/smoke04_gpus.json -w "%{http_code}" \
  "http://${GATEWAY_HOST}/api/v1/gpus")
[ "$HTTP_CODE" = "200" ] || fail "GET /api/v1/gpus: expected 200, got $HTTP_CODE"
# Validate response is a JSON array (not object or error).
python3 -c "
import json, sys
data = json.load(open('/tmp/smoke04_gpus.json'))
assert isinstance(data, list), f'Expected array, got {type(data)}'
" || fail "GET /api/v1/gpus: response is not a JSON array"
pass "GET /api/v1/gpus → 200 + JSON array"

# ── Step 9: assert GET /api/v1/gpus/<id>/telemetry → 200 + array ─────────────
HTTP_CODE=$(curl -s -o /tmp/smoke04_telem.json -w "%{http_code}" \
  "http://${GATEWAY_HOST}/api/v1/gpus/${SEED_GPU}/telemetry")
[ "$HTTP_CODE" = "200" ] || fail "GET /api/v1/gpus/${SEED_GPU}/telemetry: expected 200, got $HTTP_CODE"
python3 -c "
import json, sys
data = json.load(open('/tmp/smoke04_telem.json'))
assert isinstance(data, list), f'Expected array, got {type(data)}'
" || fail "GET /api/v1/gpus/${SEED_GPU}/telemetry: response is not a JSON array"
pass "GET /api/v1/gpus/${SEED_GPU}/telemetry → 200 + JSON array"

# ── Step 10: assert unknown GPU returns 404 ───────────────────────────────────
HTTP_CODE=$(curl -s -o /tmp/smoke04_404.json -w "%{http_code}" \
  "http://${GATEWAY_HOST}/api/v1/gpus/GPU-does-not-exist/telemetry")
[ "$HTTP_CODE" = "404" ] || fail "GET .../GPU-does-not-exist/telemetry: expected 404, got $HTTP_CODE"
pass "GET .../GPU-does-not-exist/telemetry → 404 (unknown GPU)"

# ── Step 11: assert GET /swagger/doc.json → 200 + valid JSON ─────────────────
HTTP_CODE=$(curl -s -o /tmp/smoke04_swagger.json -w "%{http_code}" \
  "http://${GATEWAY_HOST}/swagger/doc.json")
[ "$HTTP_CODE" = "200" ] || fail "GET /swagger/doc.json: expected 200, got $HTTP_CODE"
python3 -c "
import json, sys
spec = json.load(open('/tmp/smoke04_swagger.json'))
paths = spec.get('paths', {})
assert len(paths) >= 2, f'Expected >= 2 paths in spec, got {len(paths)}: {list(paths)}'
" || fail "GET /swagger/doc.json: spec is invalid or has < 2 paths"
pass "GET /swagger/doc.json → 200 + valid OpenAPI spec (>= 2 paths)"

# ── Step 12: print summary ────────────────────────────────────────────────────
GPU_COUNT=$(pg_exec -tAc "SELECT count(distinct gpu_id) FROM gpu_metrics;" 2>&1 | tr -d '[:space:]')
echo ""
echo "${GREEN}${BOLD}PASS${RST} — Phase 4 API Gateway smoke"
echo "       Gateway:       http://${GATEWAY_HOST}"
echo "       Distinct GPUs: $GPU_COUNT"
echo "       Swagger UI:    http://${GATEWAY_HOST}/swagger/"
echo ""
echo "  To inspect live:"
echo "    curl -s http://${GATEWAY_HOST}/api/v1/gpus | python3 -m json.tool"
echo "    curl -s 'http://${GATEWAY_HOST}/api/v1/gpus/${SEED_GPU}/telemetry?end_time=\$(date -u +%Y-%m-%dT%H:%M:%SZ)' | python3 -m json.tool"
echo "  Stop gateway: kill $GATEWAY_PID"
