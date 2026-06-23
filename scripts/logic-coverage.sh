#!/usr/bin/env bash
# Branch / logic coverage gate via gobco (https://github.com/rillig/gobco).
# Enforces 100% condition coverage on "logic" packages: any package under an
# internal/ directory that ships non-test Go code. Generated code (gen/), main
# wiring (cmd/), and modules without logic packages are out of scope.
#
# No logic packages yet → exits 0 (skip), so the gate is green until the first
# internal/ package lands under TDD, then requires every branch to be exercised.
set -euo pipefail

command -v gobco >/dev/null 2>&1 || { echo "gobco not installed (go install github.com/rillig/gobco@latest)"; exit 1; }

mapfile -t dirs < <(find mq streamer collector apigateway -type d -path '*/internal/*' 2>/dev/null | sort -u)

fail=0
checked=0
for d in "${dirs[@]}"; do
  ls "$d"/*.go >/dev/null 2>&1 || continue          # has Go source?
  case "$d" in */gen/*|*/cmd/*) continue;; esac      # never logic
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
  echo "No logic packages yet — skipping branch coverage."
fi
exit $fail
