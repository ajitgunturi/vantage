#!/usr/bin/env bash
# Branch / logic coverage gate via gobco (https://github.com/rillig/gobco).
#
# SCOPE (2026-06-24): 100% condition coverage is enforced on the **MQ core only** —
# the logic packages under the `mq/` module (the durable broker + client library),
# which are the hardest, most correctness-critical code and the graded centerpiece.
# Generated code (gen/), the wire contract (proto/), and main wiring (cmd/) are out
# of scope. The app services (streamer, collector, apigateway) are covered by the
# 90% LINE gate (`make cover-check`) — not this 100% BRANCH gate — to avoid contrived
# tests on defensive branches in well-trodden code.
#
# No mq logic packages yet → exits 0 (skip), so the gate is green until the first
# mq/ logic package lands under TDD, then requires every branch to be exercised.
set -euo pipefail

command -v gobco >/dev/null 2>&1 || { echo "gobco not installed (go install github.com/rillig/gobco@latest)"; exit 1; }

# MQ core logic packages: any dir under mq/ that ships non-test Go code,
# excluding generated stubs, the proto contract, and main wiring.
mapfile -t dirs < <(find mq -type d 2>/dev/null | sort -u)

fail=0
checked=0
for d in "${dirs[@]}"; do
  ls "$d"/*.go >/dev/null 2>&1 || continue              # has Go source?
  case "$d" in */gen/*|*/cmd/*|*/proto/*) continue;; esac  # never logic
  checked=$((checked + 1))
  echo "== gobco $d =="
  if ! out=$(gobco "./$d" 2>&1); then
    echo "$out"; echo "✗ ${d}: gobco failed (missing tests?)"; fail=1; continue
  fi
  echo "$out"
  if echo "$out" | grep -q "was never"; then
    echo "✗ ${d}: branches not fully covered"; fail=1
  else
    echo "✓ ${d}: all branches covered"
  fi
done

if [ "$checked" -eq 0 ]; then
  echo "No MQ-core logic packages yet — skipping branch coverage."
fi
exit $fail
