---
status: partial
phase: 05-devops-quality-gates
source: [05-VERIFICATION.md]
started: 2026-07-02T18:14:53Z
updated: 2026-07-03T00:05:00Z
---

## Current Test

[testing complete]

## Tests

### 1. kind E2E deploy + smoke
expected: make kind-up && make deploy && make smoke-05 passes end-to-end — pods Running, real rows served, independent MQ rollout demonstrated
result: issue
reported: "seems stuck in - helm upgrade --install vantage deployments -f deployments/values.yaml / Release \"vantage\" does not exist. Installing it now."
severity: major

### 2. Sustained soak
expected: make soak (defaults 60s / 3 streamers; optionally SOAK_STREAMERS=10) — row count grows, produced_total >= consumed_total, queue depth stays below capacity, streamer restored to 1 replica on exit
result: blocked
blocked_by: prior-phase
reason: "make deploy hangs at helm upgrade --install (same hang as Test 1) — kind loads and helm dependency update complete, then no further output; soak cannot run without a deployed release"

## Summary

total: 2
passed: 0
issues: 1
pending: 0
skipped: 0
blocked: 1

## Gaps

- truth: "make kind-up && make deploy && make smoke-05 passes end-to-end — migration hook completes, all four Deployments Available, gateway serves real rows, targeted mq upgrade rolls only MQ"
  status: failed
  reason: "User reported: seems stuck in - helm upgrade --install vantage deployments -f deployments/values.yaml / Release \"vantage\" does not exist. Installing it now."
  severity: major
  test: 1
  artifacts: []  # Filled by diagnosis
  missing: []    # Filled by diagnosis
