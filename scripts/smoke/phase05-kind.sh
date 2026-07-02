#!/usr/bin/env bash
# Phase 5 smoke check — kind cluster E2E.
#
# Proves the full pipeline through kind (D-12): migration hook completed →
# all service pods Available → streamer publishing → port-forwarded gateway
# serves real rows:
#
#   1. kind cluster + deploy present (fails fast otherwise, D-13)
#   2. migrate hook Job completed (or already deleted on success per hook policy)
#   3. all four Deployments Available
#   4. GET /api/v1/gpus            → 200 + non-empty JSON array
#   5. GET /api/v1/gpus/<id>/telemetry → 200 + rows
#   6. OPS-03: helm upgrade --reuse-values --set mq.image.pullPolicy=Never
#      (a REAL pod-template change vs the IfNotPresent default) rolls ONLY mq:
#      mq's generation must increment AND the others must stay unchanged;
#      reverts to IfNotPresent afterwards so re-runs keep a change to make
#
# Requires: kubectl, helm, curl. Run via: make smoke-05
# Assumes: make kind-up && make deploy have already been run.
# Idempotent — safe to re-run.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

RELEASE="vantage"
LOCAL_PORT=8081   # local 8081 → gateway 8080 (avoids MQ HTTP 8080 clash, Pitfall 8)

if [ -t 1 ]; then GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RST=$'\033[0m'
else GREEN=''; RED=''; BOLD=''; RST=''; fi
pass() { echo "${GREEN}✓${RST} $*"; }
fail() { echo "${RED}✗ $*${RST}"; exit 1; }

command -v kubectl >/dev/null 2>&1 || fail "kubectl not found on PATH"
command -v helm    >/dev/null 2>&1 || fail "helm not found on PATH"
command -v curl    >/dev/null 2>&1 || fail "curl not found on PATH"

# ── PID tracking for cleanup ──────────────────────────────────────────────────
PF_PID=""
cleanup() { [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null || true; }
trap cleanup EXIT

echo "${BOLD}== Phase 5 smoke: kind E2E ==${RST}"

# ── Step 1: cluster + deploy guard (D-13) ────────────────────────────────────
kubectl get nodes >/dev/null 2>&1 || fail "kind cluster not running — run: make kind-up deploy"
helm status "$RELEASE" >/dev/null 2>&1 || fail "release '$RELEASE' not installed — run: make deploy"
pass "kind cluster reachable, release '$RELEASE' installed"

# ── Step 2: migration hook Job completed ─────────────────────────────────────
# The hook uses delete-policy hook-succeeded: on success the Job is deleted.
if kubectl get job "${RELEASE}-migrate" >/dev/null 2>&1; then
  kubectl wait --for=condition=complete "job/${RELEASE}-migrate" --timeout=120s \
    || fail "migration Job exists but did not complete"
  pass "migration Job completed"
else
  pass "migration Job absent — deleted on success (hook-succeeded policy)"
fi

# ── Step 3: all service Deployments Available ────────────────────────────────
for d in mq streamer collector gateway; do
  kubectl wait --for=condition=available "deployment/${RELEASE}-${d}" --timeout=120s >/dev/null \
    || fail "deployment ${RELEASE}-${d} did not become Available"
done
pass "all four service Deployments Available"

# ── Step 4: port-forward gateway to local ${LOCAL_PORT} ──────────────────────
kubectl port-forward "svc/${RELEASE}-gateway" 8081:8080 >/dev/null 2>&1 &
PF_PID=$!
sleep 2  # allow the port-forward to bind

# ── Step 5: GET /api/v1/gpus → 200 + non-empty array (streamer → collector) ──
GPU_ID=""
for _ in $(seq 1 30); do
  HTTP_CODE=$(curl -s -o /tmp/smoke05_gpus.json -w "%{http_code}" \
    "http://localhost:${LOCAL_PORT}/api/v1/gpus" 2>/dev/null || echo 000)
  if [ "$HTTP_CODE" = "200" ]; then
    GPU_ID=$(python3 -c "
import json
data = json.load(open('/tmp/smoke05_gpus.json'))
assert isinstance(data, list)
print(data[0] if data else '')" 2>/dev/null || echo "")
    [ -n "$GPU_ID" ] && break
  fi
  sleep 2
done
[ -n "$GPU_ID" ] || fail "GET /api/v1/gpus never returned a non-empty array — pipeline not flowing"
pass "GET /api/v1/gpus → 200 + non-empty array (found $GPU_ID)"

# ── Step 6: GET telemetry for that GPU → 200 + rows ──────────────────────────
HTTP_CODE=$(curl -s -o /tmp/smoke05_telem.json -w "%{http_code}" \
  "http://localhost:${LOCAL_PORT}/api/v1/gpus/${GPU_ID}/telemetry")
[ "$HTTP_CODE" = "200" ] || fail "GET telemetry: expected 200, got $HTTP_CODE"
python3 -c "
import json
data = json.load(open('/tmp/smoke05_telem.json'))
assert isinstance(data, list) and len(data) > 0, 'telemetry array empty'
" || fail "telemetry response is not a non-empty JSON array"
pass "GET /api/v1/gpus/${GPU_ID}/telemetry → 200 + rows"

# ── Step 7: OPS-03 — targeted upgrade rolls ONLY the mq Deployment ───────────
# Capture the generation of every Deployment, force a REAL mq pod-template
# change (pullPolicy IfNotPresent → Never; NOT Always — vantage/*:dev images
# live only inside the kind node, a registry pull would ImagePullBackOff),
# then assert BOTH directions: mq's generation incremented AND
# streamer/collector/gateway generations are unchanged. A no-op --set (like
# the values-default tag) would render byte-identical manifests and pass
# vacuously — the mq increment assertion is what makes this check real.
gen() { kubectl get "deployment/${RELEASE}-$1" -o jsonpath='{.metadata.generation}'; }
GEN_MQ=$(gen mq)
GEN_STREAMER=$(gen streamer); GEN_COLLECTOR=$(gen collector); GEN_GATEWAY=$(gen gateway)

helm upgrade --reuse-values --set mq.image.pullPolicy=Never "$RELEASE" deployments >/dev/null \
  || fail "helm upgrade --reuse-values --set mq.image.pullPolicy=Never failed"
kubectl rollout status "deployment/${RELEASE}-mq" --timeout=120s >/dev/null \
  || fail "mq rollout did not complete after targeted upgrade"

[ "$(gen mq)" -gt "$GEN_MQ" ] || fail "mq did not roll (generation stuck at ${GEN_MQ}) — OPS-03 check is a no-op"
[ "$(gen streamer)"  = "$GEN_STREAMER"  ] || fail "streamer rolled during mq-only upgrade (OPS-03 violated)"
[ "$(gen collector)" = "$GEN_COLLECTOR" ] || fail "collector rolled during mq-only upgrade (OPS-03 violated)"
[ "$(gen gateway)"   = "$GEN_GATEWAY"   ] || fail "gateway rolled during mq-only upgrade (OPS-03 violated)"
pass "OPS-03: mq rolled (gen ${GEN_MQ} → $(gen mq)); streamer/collector/gateway untouched"

# Revert to the values default so repeated runs start from IfNotPresent and
# the Never toggle above stays a real change on every run.
helm upgrade --reuse-values --set mq.image.pullPolicy=IfNotPresent "$RELEASE" deployments >/dev/null \
  || fail "revert upgrade (mq.image.pullPolicy=IfNotPresent) failed"
kubectl rollout status "deployment/${RELEASE}-mq" --timeout=120s >/dev/null \
  || fail "mq rollout did not complete after pullPolicy revert"

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "${GREEN}${BOLD}PASS${RST} — Phase 5 kind E2E smoke"
echo "       Gateway (port-forward): http://localhost:${LOCAL_PORT}"
echo "       Sample GPU:             ${GPU_ID}"
echo ""
echo "  To inspect live:"
echo "    kubectl get pods"
echo "    curl -s http://localhost:${LOCAL_PORT}/api/v1/gpus | python3 -m json.tool"
