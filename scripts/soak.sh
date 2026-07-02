#!/usr/bin/env bash
# Phase 5 soak — sustained pipeline load against the kind cluster (D-14).
#
# Scales the streamer Deployment to SOAK_STREAMERS replicas, runs for
# SOAK_DURATION seconds, then asserts:
#
#   1. Postgres gpu_metrics row count GREW over the window
#   2. MQ inspect counters reconcile: produced_total >= consumed_total (acks)
#   3. queue depth stays bounded (< capacity — no runaway growth / loss)
#
# Env overrides (defaults chosen for a quick local soak):
#   SOAK_DURATION=60   seconds to sustain load
#   SOAK_STREAMERS=3   streamer replicas (supports up-to-10 requirement)
#
# Requires: kubectl, curl, python3. Run via: make soak
# Assumes: make kind-up && make deploy already run (same guard as smoke-05).
# On exit: scales streamer back to 1 and kills its port-forwards (re-runnable).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RELEASE="vantage"
SOAK_DURATION="${SOAK_DURATION:-60}"
SOAK_STREAMERS="${SOAK_STREAMERS:-3}"
MQ_LOCAL_PORT=8082   # local 8082 → mq 8080 (gateway smoke uses 8081; Pitfall 8)

if [ -t 1 ]; then GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RST=$'\033[0m'
else GREEN=''; RED=''; BOLD=''; RST=''; fi
pass() { echo "${GREEN}✓${RST} $*"; }
fail() { echo "${RED}✗ $*${RST}"; exit 1; }

command -v kubectl >/dev/null 2>&1 || fail "kubectl not found on PATH"
command -v curl    >/dev/null 2>&1 || fail "curl not found on PATH"

# ── Cleanup: restore streamer replicas + kill port-forwards ──────────────────
PF_PID=""
cleanup() {
  [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null || true
  kubectl scale "deployment/${RELEASE}-streamer" --replicas=1 >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "${BOLD}== Phase 5 soak: ${SOAK_STREAMERS} streamer(s) for ${SOAK_DURATION}s ==${RST}"

# ── Step 1: cluster + deploy guard ───────────────────────────────────────────
kubectl get nodes >/dev/null 2>&1 || fail "kind cluster not running — run: make kind-up deploy"
kubectl get "deployment/${RELEASE}-streamer" >/dev/null 2>&1 \
  || fail "release '${RELEASE}' not deployed — run: make deploy"
pass "cluster reachable, release deployed"

# ── Step 2: row count helper (psql inside the Bitnami postgres pod) ──────────
row_count() {
  kubectl exec "${RELEASE}-postgresql-0" -- \
    env PGPASSWORD=vantage psql -U vantage -d vantage -tAc \
    "SELECT count(*) FROM gpu_metrics;" 2>/dev/null | tr -d '[:space:]'
}

# ── Step 3: MQ inspect helper via port-forward ────────────────────────────────
kubectl port-forward "svc/${RELEASE}-mq" "${MQ_LOCAL_PORT}:8080" >/dev/null 2>&1 &
PF_PID=$!
sleep 2

inspect_field() {  # inspect_field <json-field>
  curl -s "http://localhost:${MQ_LOCAL_PORT}/api/v1/queue/inspect" \
    | python3 -c "import json,sys; print(json.load(sys.stdin)['$1'])"
}

# ── Step 4: scale streamer up and sample the starting state ──────────────────
START_ROWS=$(row_count)
[ -n "$START_ROWS" ] || fail "could not read gpu_metrics row count from ${RELEASE}-postgresql-0"
CAPACITY=$(inspect_field capacity) || fail "MQ inspect unreachable on localhost:${MQ_LOCAL_PORT}"

kubectl scale "deployment/${RELEASE}-streamer" --replicas="$SOAK_STREAMERS" >/dev/null \
  || fail "kubectl scale streamer to ${SOAK_STREAMERS} failed"
kubectl rollout status "deployment/${RELEASE}-streamer" --timeout=120s >/dev/null \
  || fail "streamer did not reach ${SOAK_STREAMERS} replicas"
pass "streamer scaled to ${SOAK_STREAMERS} replicas (start rows: ${START_ROWS}, MQ capacity: ${CAPACITY})"

# ── Step 5: sustain and poll — depth must stay bounded the whole time ────────
MAX_DEPTH=0
INSPECT_FAILS=0
END=$(( $(date +%s) + SOAK_DURATION ))
while [ "$(date +%s)" -lt "$END" ]; do
  # Distinguish fetch failure from a real depth reading: a dead port-forward,
  # MQ pod restart, or JSON parse error must NOT be mapped to depth 0, or the
  # bounded-depth assertion passes vacuously while monitoring is blind.
  # Tolerate 2 consecutive blips (transient socket, pod restart), fail on the 3rd.
  if DEPTH=$(inspect_field depth); then
    INSPECT_FAILS=0
    [ "$DEPTH" -gt "$MAX_DEPTH" ] && MAX_DEPTH=$DEPTH
    if [ "$DEPTH" -ge "$CAPACITY" ]; then
      fail "queue depth ${DEPTH} hit capacity ${CAPACITY} — runaway growth (collector not keeping up)"
    fi
  else
    INSPECT_FAILS=$((INSPECT_FAILS + 1))
    [ "$INSPECT_FAILS" -ge 3 ] && fail "MQ inspect unreachable ${INSPECT_FAILS}x in a row — port-forward dead?"
  fi
  sleep 5
done
pass "depth stayed bounded (max ${MAX_DEPTH} / capacity ${CAPACITY})"

# ── Step 6: final reconciliation ──────────────────────────────────────────────
END_ROWS=$(row_count)
PRODUCED=$(inspect_field produced_total) \
  || fail "MQ inspect unreachable reading produced_total — port-forward dead?"
CONSUMED=$(inspect_field consumed_total) \
  || fail "MQ inspect unreachable reading consumed_total — port-forward dead?"

[ "$END_ROWS" -gt "$START_ROWS" ] \
  || fail "row count did not grow (start ${START_ROWS}, end ${END_ROWS}) — pipeline stalled"
pass "rows grew: ${START_ROWS} → ${END_ROWS} (+$(( END_ROWS - START_ROWS )))"

[ "$PRODUCED" -ge "$CONSUMED" ] \
  || fail "inspect counters inconsistent: produced ${PRODUCED} < consumed ${CONSUMED}"
pass "MQ counters reconcile: produced ${PRODUCED} >= acked ${CONSUMED}"

echo ""
echo "${GREEN}${BOLD}PASS${RST} — Phase 5 soak (${SOAK_STREAMERS} streamers, ${SOAK_DURATION}s)"
echo "       Rows:     ${START_ROWS} → ${END_ROWS}"
echo "       MQ:       produced=${PRODUCED} acked=${CONSUMED} max_depth=${MAX_DEPTH}"
echo "       Streamer scaled back to 1 replica on exit."
