#!/usr/bin/env bash
# Phase 6 smoke check — delivery-semantics hardening boundaries.
#
# Boots dedicated MQ instances with boundary-sized knobs and drives each
# hardening guarantee over the real wire:
#   1. Backpressure: a full ring REFUSES producers (ResourceExhausted),
#      drops nothing, and admits again after a drain.
#   2. Lease TTL → retry → DLQ: a live-but-stuck consumer (no acks)
#      loses its leases to the TTL sweeper; after MQ_MAX_DELIVERIES the
#      messages dead-letter; GET /dlq lists them; POST /dlq/replay returns
#      them to circulation and an acking consumer drains all of them.
#   3. preStop drain: SIGTERM refuses producers but serves consumers;
#      the process exits cleanly once the backlog clears.
#
# Requires: go, curl.  Run via: make smoke-06  (or: bash scripts/smoke/phase06-hardening.sh)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

GRPC_ADDR="${MQ_GRPC_ADDR:-:55057}"
HTTP_ADDR="${MQ_HTTP_ADDR:-:58087}"
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

# counter <json> <field> — extract an integer counter from an inspect body.
counter() { printf '%s' "$1" | sed -n "s/.*\"$2\":\([0-9]*\).*/\1/p"; }

start_mq() { # start_mq [extra env as VAR=VAL ...]
  env "$@" MQ_GRPC_ADDR="$GRPC_ADDR" MQ_HTTP_ADDR="$HTTP_ADDR" "$TMP/mq" >"$TMP/mq.log" 2>&1 &
  MQ_PID=$!
  local ready=0
  for _ in $(seq 1 50); do
    if curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect" >/dev/null 2>&1; then ready=1; break; fi
    kill -0 "$MQ_PID" 2>/dev/null || { cat "$TMP/mq.log"; fail "mq exited during startup"; }
    sleep 0.1
  done
  [ "$ready" = 1 ] || { cat "$TMP/mq.log"; fail "mq HTTP not ready after 5s"; }
}

stop_mq() {
  if [ -n "$MQ_PID" ]; then kill "$MQ_PID" 2>/dev/null || true; wait "$MQ_PID" 2>/dev/null || true; MQ_PID=""; fi
}

echo "${BOLD}== Phase 6 smoke: delivery-hardening boundaries ==${RST}"
echo "building mq..."
go build -o "$TMP/mq" ./cmd/mq || fail "go build ./cmd/mq"

# ── Scenario 1: backpressure at a full ring ──────────────────────────
echo "scenario 1: backpressure — ring of 10, reject policy (default)..."
start_mq MQ_BUFFER_SIZE=10

go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 10 -mode produce \
  || fail "filling the ring (10) must succeed"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 3 -mode produce-expect-reject \
  || fail "producing into a FULL ring must be refused with ResourceExhausted"
BODY="$(curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect")" || fail "inspect curl failed"
[ "$(counter "$BODY" rejected_total)" -ge 1 ] || fail "rejected_total not incremented: ${BODY}"
[ "$(counter "$BODY" dropped_total)"  -eq 0 ] || fail "reject policy must not drop: ${BODY}"
[ "$(counter "$BODY" depth)"          -eq 10 ] || fail "depth must still be 10: ${BODY}"
pass "full ring refused the producer (rejected_total>=1), dropped_total=0, all 10 retained"

go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 10 -mode consume -credit 10 \
  || fail "draining the ring failed"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 1 -mode produce \
  || fail "post-drain produce must be admitted again (backpressure released)"
pass "after drain, production is admitted again — backpressure is flow control, not loss"
stop_mq

# ── Scenario 2: lease TTL → retry → DLQ → replay ──────────────────
echo "scenario 2: stuck consumer → TTL reclaim → DLQ at max deliveries → replay..."
start_mq MQ_BUFFER_SIZE=100 MQ_MAX_DELIVERIES=2 MQ_LEASE_TTL_MS=300 \
         MQ_RETRY_BACKOFF_BASE_MS=50 MQ_RETRY_BACKOFF_MAX_MS=100

go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 5 -mode produce || fail "produce 5 failed"

# Delivery attempt 1: stuck consumer holds 5 leases past the 300ms TTL.
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 5 -mode consume-noack -hold 1200ms \
  || fail "consume-noack (attempt 1) failed"
BODY="$(curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect")"
[ "$(counter "$BODY" lease_expired_total)" -ge 5 ] \
  || fail "TTL sweeper must reclaim the 5 stuck leases: ${BODY}"
pass "lease TTL — 5 leases reclaimed from a live-but-stuck consumer (lease_expired_total>=5)"

# Delivery attempt 2 (max): stuck again → attempts hit MQ_MAX_DELIVERIES → DLQ.
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 5 -mode consume-noack -hold 1200ms \
  || fail "consume-noack (attempt 2) failed"
DLQ_OK=0
for _ in $(seq 1 30); do
  BODY="$(curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect")"
  if [ "$(counter "$BODY" dlq_depth)" -eq 5 ]; then DLQ_OK=1; break; fi
  sleep 0.2
done
[ "$DLQ_OK" = 1 ] || fail "after ${BODY} — 5 messages must dead-letter at max deliveries"
[ "$(counter "$BODY" dead_lettered_total)" -eq 5 ] || fail "dead_lettered_total must be 5: ${BODY}"
pass "max deliveries — all 5 poison-pattern messages routed to the DLQ (dlq_depth=5)"

DLQ_BODY="$(curl -sf "http://${HTTP_HOST}/api/v1/queue/dlq")" || fail "GET /dlq failed"
echo "dlq: $(printf '%s' "$DLQ_BODY" | head -c 200)..."
printf '%s' "$DLQ_BODY" | grep -q '"reason":"max delivery attempts exceeded (2)"' \
  || fail "DLQ entries must carry the dead-letter reason: ${DLQ_BODY}"
pass "GET /api/v1/queue/dlq — entries listed with attempts + reason"

REPLAY_BODY="$(curl -sf -X POST "http://${HTTP_HOST}/api/v1/queue/dlq/replay")" || fail "POST /dlq/replay failed"
printf '%s' "$REPLAY_BODY" | grep -q '"replayed":5' || fail "replay must return 5: ${REPLAY_BODY}"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 5 -mode consume -credit 5 \
  || fail "replayed messages must be consumable (fresh attempts)"
BODY="$(curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect")"
[ "$(counter "$BODY" dlq_depth)" -eq 0 ]      || fail "DLQ must be empty after replay: ${BODY}"
[ "$(counter "$BODY" dropped_total)" -eq 0 ]  || fail "zero loss through the whole cycle: ${BODY}"
pass "replay — 5 messages returned to circulation, acked by a healthy consumer, zero loss end-to-end"
stop_mq

# ── Scenario 3: preStop drain ──────────────────────────────────────────
echo "scenario 3: SIGTERM drain — producers refused, consumers drain, clean exit..."
start_mq MQ_BUFFER_SIZE=100 MQ_DRAIN_TIMEOUT_MS=10000

go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 5 -mode produce || fail "produce 5 failed"
kill -TERM "$MQ_PID"
sleep 0.3   # let the drain phase engage
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 3 -mode produce-expect-reject \
  || fail "a draining broker must refuse producers (Unavailable)"
pass "drain phase — producers refused after SIGTERM"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n 5 -mode consume -credit 5 \
  || fail "consumers must still drain the backlog DURING the drain phase"
pass "drain phase — consumer drained all 5 queued messages during shutdown"

EXITED=0
for _ in $(seq 1 100); do
  if ! kill -0 "$MQ_PID" 2>/dev/null; then EXITED=1; break; fi
  sleep 0.1
done
[ "$EXITED" = 1 ] || fail "mq must exit promptly once the backlog is drained"
wait "$MQ_PID" 2>/dev/null; RC=$?
MQ_PID=""
[ "$RC" -eq 0 ] || { cat "$TMP/mq.log"; fail "mq must exit 0 after a clean drain (got $RC)"; }
grep -q "drain complete" "$TMP/mq.log" || fail "mq log must record the completed drain"
pass "clean exit — drain completed with an empty broker (exit 0)"

echo "${GREEN}${BOLD}PASS${RST} — Phase 7 delivery-hardening smoke (backpressure, TTL→DLQ→replay, preStop drain)"
