#!/usr/bin/env bash
# Lint every Go module (golangci-lint, falling back to go vet). Used by `make lint`
# and the CI lint job. Modules with no packages yet are skipped.
set -euo pipefail

modules=(mq streamer collector apigateway)
have_lint=0; command -v golangci-lint >/dev/null 2>&1 && have_lint=1
fail=0

for m in "${modules[@]}"; do
  [ -f "$m/go.mod" ] || continue
  pkgs="$(cd "$m" && GOWORK=off go list ./... 2>/dev/null || true)"
  if [ -z "$pkgs" ]; then echo "• ${m}: no packages yet — skip"; continue; fi
  echo "== lint ${m} =="
  if [ "$have_lint" = 1 ]; then
    (cd "$m" && GOWORK=off golangci-lint run ./...) || fail=1
  else
    (cd "$m" && GOWORK=off go vet ./...) || fail=1
  fi
done

exit $fail
