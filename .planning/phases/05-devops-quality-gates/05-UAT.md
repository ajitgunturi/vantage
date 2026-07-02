---
status: testing
phase: 05-devops-quality-gates
source: [05-VERIFICATION.md]
started: 2026-07-02T18:14:53Z
updated: 2026-07-02T18:14:53Z
---

## Current Test

number: 1
name: kind E2E deploy + smoke
expected: |
  make kind-up && make deploy && make smoke-05 —
  migration hook completes, all four Deployments Available, port-forwarded
  gateway (localhost:8081) returns a non-empty GPU list and telemetry rows,
  and the targeted mq upgrade rolls ONLY the MQ Deployment (OPS-03).
awaiting: user response

## Tests

### 1. kind E2E deploy + smoke
expected: make kind-up && make deploy && make smoke-05 passes end-to-end — pods Running, real rows served, independent MQ rollout demonstrated
result: [pending]

### 2. Sustained soak
expected: make soak (defaults 60s / 3 streamers; optionally SOAK_STREAMERS=10) — row count grows, produced_total >= consumed_total, queue depth stays below capacity, streamer restored to 1 replica on exit
result: [pending]

## Summary

total: 2
passed: 0
issues: 0
pending: 2
skipped: 0
blocked: 0

## Gaps
