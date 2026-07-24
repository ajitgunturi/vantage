#!/usr/bin/env bash
# Phase 7 smoke check — throughput & observability boundaries.
#
#   1. Batch publish: one ProduceBatch RPC admits N messages in order; under
#      the reject policy a full budget yields the partial-accept contract
#      (accepted prefix + rejected suffix), with zero loss.
#   2. MQ Prometheus exposition agrees with the inspect counters.
#
# Requires: go, curl.  Run via: make smoke-07  (or: bash scripts/smoke/phase07-scale.sh)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

GRPC_ADDR="${MQ_GRPC_ADDR:-:55077}"
HTTP_ADDR="${MQ_HTTP_ADDR:-:58097}"
GRPC_HOST="127.0.0.1${GRPC_ADDR}"
HTTP_HOST="127.0.0.1${HTTP_ADDR}"

if [ -t 1 ]; then GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RST=$'\033[0m'
else GREEN=''; RED=''; BOLD=''; RST=''; fi
pass() { echo "${GREEN}✓${RST} $*"; }
fail() { echo "${RED}✗ $*${RST}"; exit 1; }

command -v go   >/dev/null 2>&1 || fail "go not found on PATH"
command -v curl >/dev/null 2>&1 || fail "curl not found on PATH"

TMP="$(mktemp -d)"
MQ_PID=""
cleanup() {
  if [ -n "$MQ_PID" ]; then kill "$MQ_PID" 2>/dev/null || true; wait "$MQ_PID" 2>/dev/null || true; fi
  rm -rf "$TMP"
}
trap cleanup EXIT

counter() { printf '%s' "$1" | sed -n "s/.*\"$2\":\([0-9]*\).*/\1/p"; }

start_mq() {
  env "$@" MQ_GRPC_ADDR="$GRPC_ADDR" MQ_HTTP_ADDR="$HTTP_ADDR" "$TMP/mq" >"$TMP/mq.log" 2>&1 &
  MQ_PID=$!
  for _ in $(seq 1 50); do
    curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect" >/dev/null 2>&1 && return
    kill -0 "$MQ_PID" 2>/dev/null || { cat "$TMP/mq.log"; fail "mq exited during startup"; }
    sleep 0.1
  done
  cat "$TMP/mq.log"; fail "mq HTTP not ready after 5s"
}
stop_mq() {
  if [ -n "$MQ_PID" ]; then kill "$MQ_PID" 2>/dev/null || true; wait "$MQ_PID" 2>/dev/null || true; MQ_PID=""; fi
}

echo "${BOLD}== Phase 7 smoke: throughput & observability boundaries ==${RST}"
echo "building mq..."
go build -o "$TMP/mq" ./cmd/mq || fail "go build ./cmd/mq"

# ── Scenario 1: batch publish, happy path ────────────────────────────────────
echo "scenario 1: one ProduceBatch RPC admits 50 messages..."
start_mq MQ_BUFFER_SIZE=100
OUT=$(go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 50 -mode produce-batch) || fail "produce-batch failed: $OUT"
echo "$OUT" | grep -q "accepted 50, rejected 0" || fail "batch must fully accept: $OUT"
BODY=$(curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect")
[ "$(counter "$BODY" produced_total)" -eq 50 ] || fail "produced_total must be 50: $BODY"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 50 -mode consume -credit 50 \
  || fail "draining the batch failed"
pass "batch publish — 50 messages in one RPC, all delivered and acked"
stop_mq

# ── Scenario 2: partial accept at a full budget (reject policy) ──────────────
echo "scenario 2: batch of 15 into a 10-slot ring under reject policy..."
start_mq MQ_BUFFER_SIZE=10 MQ_OVERFLOW_POLICY=reject
OUT=$(go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 15 -mode produce-batch) || fail "produce-batch failed: $OUT"
echo "$OUT" | grep -q "accepted 10, rejected 5" || fail "partial-accept contract violated: $OUT"
BODY=$(curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect")
[ "$(counter "$BODY" produced_total)" -eq 10 ] || fail "accepted prefix must be 10: $BODY"
[ "$(counter "$BODY" rejected_total)" -eq 1 ]  || fail "one refusal event, not one per message: $BODY"
[ "$(counter "$BODY" dropped_total)"  -eq 0 ]  || fail "partial accept must not lose anything: $BODY"
pass "partial accept — accepted prefix 10, rejected suffix 5, one refusal event, zero loss"

# ── Scenario 3: Prometheus exposition agrees with inspect ────────────────────
echo "scenario 3: /metrics mirrors the inspect counters..."
METRICS=$(curl -sf "http://${HTTP_HOST}/metrics") || fail "GET /metrics failed"
printf '%s' "$METRICS" | grep -q '^mq_produced_total 10$' || fail "mq_produced_total must be 10:
$(printf '%s' "$METRICS" | grep mq_produced)"
printf '%s' "$METRICS" | grep -q '^mq_rejected_total 1$'  || fail "mq_rejected_total must be 1"
printf '%s' "$METRICS" | grep -q '^mq_queue_depth 10$'    || fail "mq_queue_depth must be 10"
printf '%s' "$METRICS" | grep -q 'mq_dropped_total{cause="overflow"} 0' || fail "dropped{overflow} must be 0"
printf '%s' "$METRICS" | grep -q 'mq_dropped_total{cause="requeue"} 0'  || fail "dropped{requeue} must be 0"
pass "MQ /metrics — gauges and cause-split counters agree with inspect"
stop_mq

echo "${GREEN}${BOLD}PASS${RST} — Phase 7 throughput & observability smoke (batch publish, partial accept, metrics)"
