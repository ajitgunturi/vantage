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

echo "producing/consuming ${N} messages via mqprobe (consumer attached first)..."
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$N" || fail "mqprobe produce/consume failed"
pass "data plane — produced ${N}, consumed ${N} over a Consume stream"

# Late-join path: producer publishes and disconnects, THEN a consumer attaches.
# Separate mqprobe invocations (separate processes) prove the MQ buffers messages
# for a consumer that joins after the producer is gone — not just live fan-out.
echo "producing ${N} messages, then consuming them in a later invocation (late join)..."
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$N" -mode produce || fail "mqprobe produce-only failed"
go run ./scripts/smoke/mqprobe -grpc "$GRPC_HOST" -n "$N" -mode consume || fail "mqprobe late-join consume failed"
pass "late join — ${N} messages buffered before any consumer attached, then drained"

# Cross-check the control-plane counters (sed keeps this jq-free).
BODY="$(curl -sf "http://${HTTP_HOST}/api/v1/queue/inspect")" || fail "inspect curl failed"
echo "inspect: ${BODY}"
produced="$(printf '%s' "$BODY" | sed -n 's/.*"produced_total":\([0-9]*\).*/\1/p')"
consumed="$(printf '%s' "$BODY" | sed -n 's/.*"consumed_total":\([0-9]*\).*/\1/p')"
[ "${produced:-0}" -ge "$N" ] || fail "produced_total=${produced} < ${N}"
[ "${consumed:-0}" -ge "$N" ] || fail "consumed_total=${consumed} < ${N}"
pass "inspect counters — produced_total=${produced} consumed_total=${consumed}"

echo "${GREEN}${BOLD}PASS${RST} — Phase 1 MQ smoke"
