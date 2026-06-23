#!/usr/bin/env bash
# Line-coverage gate. Fails if any given Go coverage profile is below the
# threshold. A profile that does not exist or has no statements (a module with
# no testable code yet) is SKIPPED, not failed — so the gate stays green until
# TDD adds the first covered line, then enforces from there.
#
# Usage: COVERAGE_THRESHOLD=90 scripts/coverage-gate.sh <profile...>
set -euo pipefail

THRESHOLD="${COVERAGE_THRESHOLD:-90}"
status=0

for prof in "$@"; do
  if [ ! -s "$prof" ]; then
    echo "• ${prof}: no profile (no code yet) — skip"
    continue
  fi
  data=$(grep -vc '^mode:' "$prof" || true)
  if [ "${data:-0}" -eq 0 ]; then
    echo "• ${prof}: no statements — skip"
    continue
  fi
  pct=$(go tool cover -func="$prof" | awk '/^total:/{gsub(/%/,"",$NF); print $NF}')
  pass=$(awk -v p="${pct:-0}" -v t="$THRESHOLD" 'BEGIN{print (p+0 >= t+0) ? "1" : "0"}')
  if [ "$pass" = "1" ]; then
    echo "• ${prof}: ${pct}% >= ${THRESHOLD}% ✓"
  else
    echo "• ${prof}: ${pct}% < ${THRESHOLD}% ✗"
    status=1
  fi
done

exit $status
