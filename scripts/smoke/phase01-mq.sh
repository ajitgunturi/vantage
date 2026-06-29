#!/usr/bin/env bash
# Phase 1 smoke check — custom in-memory MQ (data plane + control plane).
#
# Builds the MQ service, starts it on dedicated ports, exercises Produce/Consume
# via the mqprobe Go client, then verifies GET /api/v1/queue/inspect reflects the
# traffic. The MQ is in-memory — no broker or database is required.
#
# Requires: go, curl.  Run via: make smoke-01   (or: bash scripts/smoke/phase01-mq.sh)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# Dedicated ports so the smoke run never clashes with a dev instance on the
# defaults (:50051 / :8080). Override via the same env vars the service reads.
GRPC_ADDR="${MQ_GRPC_ADDR:-:55051}"
HTTP_ADDR="${MQ_HTTP_ADDR:-:58080}"
GRPC_HOST="127.0.0.1${GRPC_ADDR}"   # ":55051" -> "127.0.0.1:55051"
HTTP_HOST="127.0.0.1${HTTP_ADDR}"
N="${SMOKE_N:-20}"

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

echo "${BOLD}== Phase 1 smoke: custom MQ ==${RST}"

echo "building mq..."
go build -o "$TMP/mq" ./cmd/mq || fail "go build ./cmd/mq"

echo "starting mq (gRPC ${GRPC_ADDR}, HTTP ${HTTP_ADDR})..."
MQ_GRPC_ADDR="$GRPC_ADDR" MQ_HTTP_ADDR="$HTTP_ADDR" "$TMP/mq" >"$TMP/mq.log" 2>&1 &
MQ_PID=$!

# Wait for the HTTP control plane to answer.
ready=0
for _ in $(seq 1 50); do
  if curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect" >/dev/null 2>&1; then ready=1; break; fi
  kill -0 "$MQ_PID" 2>/dev/null || { cat "$TMP/mq.log"; fail "mq exited during startup"; }
  sleep 0.1
done
[ "$ready" = 1 ] || { cat "$TMP/mq.log"; fail "mq HTTP not ready after 5s"; }
pass "control plane up — GET /api/v1/queue/inspect"

# Data plane over the bidi at-least-once path: the consumer attaches first, sends
# its initial credit window, then receives AND acks each message by broker id
# (mqprobe -mode both). consumed_total now counts acks, not sends.
echo "producing/consuming ${N} messages via bidi mqprobe (consumer attached first, credit+ack)..."
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$N" -credit "$N" || fail "mqprobe bidi produce/consume failed"
pass "data plane — produced ${N}, consumed+acked ${N} over a bidi Consume stream"

# Late-join NO-LOSS path: producer publishes and disconnects, THEN a consumer
# attaches and reads FEWER than produced — the remainder must still be retrievable.
# Three separate mqprobe processes (produce-only, partial consume, drain-the-rest)
# prove the MQ retains unconsumed messages across producer disconnect and that a
# consumer reading K < N loses nothing: a follow-up consume drains the other N-K.
HALF=$(( N / 2 ))
REST=$(( N - HALF ))
echo "late join: producing ${N}, consuming ${HALF} (fewer than produced), then draining the remaining ${REST}..."
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$N"    -mode produce            || fail "mqprobe produce-only failed"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$HALF" -mode consume -credit 8  || fail "mqprobe partial consume failed"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$REST" -mode consume -credit 8  || fail "mqprobe drain-remainder failed (LOSS: ${REST} messages not retained)"
pass "late join no-loss — read ${HALF}/${N}, the remaining ${REST} were retained and drained (zero loss)"

# Credit-boundary path: a consumer whose FIRST credit message is <= 0 must NOT
# deadlock. The broker substitutes its own default window (MQ_CONSUME_CREDIT) and
# still delivers every message. mqprobe sends Credit verbatim, so -credit 0 puts a
# literal zero on the wire — exercising server.go's `if credit <= 0` substitution.
# A regression that dropped the default (granting an actual zero-token semaphore)
# would hang here until mqprobe's timeout, failing the smoke run instead of prod.
echo "credit boundary: producing ${N}, then consuming with -credit 0 (broker must substitute its default and drain all ${N})..."
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$N" -mode produce           || fail "mqprobe produce-only (credit boundary) failed"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$N" -mode consume -credit 0  || fail "mqprobe consume with credit 0 failed — broker did not substitute its default window (zero-credit DEADLOCK)"
pass "credit boundary — consumer requested 0, broker granted its default window and drained all ${N} (no deadlock)"

# Cross-check the at-least-once control-plane counters (sed keeps this jq-free).
# Parse the Plan-04 counter names: delivered_total (sends), consumed_total (acks),
# redelivered_total (re-enqueued on disconnect-with-unacked; 0 on the happy path).
BODY="$(curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect")" || fail "inspect curl failed"
echo "inspect: ${BODY}"
produced="$(printf '%s'    "$BODY" | sed -n 's/.*"produced_total":\([0-9]*\).*/\1/p')"
delivered="$(printf '%s'   "$BODY" | sed -n 's/.*"delivered_total":\([0-9]*\).*/\1/p')"
consumed="$(printf '%s'    "$BODY" | sed -n 's/.*"consumed_total":\([0-9]*\).*/\1/p')"
redelivered="$(printf '%s' "$BODY" | sed -n 's/.*"redelivered_total":\([0-9]*\).*/\1/p')"
[ "${produced:-0}"    -ge "$N" ] || fail "produced_total=${produced} < ${N}"
[ "${delivered:-0}"   -ge "$N" ] || fail "delivered_total=${delivered} < ${N} (broker did not send at least N)"
[ "${consumed:-0}"    -ge "$N" ] || fail "consumed_total=${consumed} < ${N} (acks did not arrive)"
[ "${redelivered:-0}" -ge 0    ] || fail "redelivered_total=${redelivered} is negative"
pass "inspect counters — produced_total=${produced} delivered_total=${delivered} consumed_total(acks)=${consumed} redelivered_total=${redelivered}"

echo "${GREEN}${BOLD}PASS${RST} — Phase 1 MQ smoke (bidi at-least-once)"
