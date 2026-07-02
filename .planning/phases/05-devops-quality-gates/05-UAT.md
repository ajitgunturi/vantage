---
status: diagnosed
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
  root_cause: "Chicken-and-egg deadlock: migrate-job.yaml is a helm.sh/hook pre-install,pre-upgrade Job whose wait-for-postgres init container loops `until nc -z vantage-postgresql 5432`, but Helm applies pre-install hooks to completion BEFORE installing regular release manifests — including the Bitnami postgresql StatefulSet/Service. The Service the init container waits on is only created after the hook succeeds, so the hook can never complete. Helm prints nothing while waiting on hooks → silent hang until the default 5m timeout. The pre-upgrade path deadlocks identically (Postgres was never installed by the failed first attempt), so re-running cannot recover."
  artifacts:
    - path: "deployments/templates/migrate-job.yaml"
      issue: "pre-install/pre-upgrade hook annotation + wait-for-postgres init container = circular wait against the same release's Postgres (hook blocks the resources it depends on)"
    - path: "Makefile"
      issue: "helm-install target has no explicit --timeout; default 5m makes the failure present as a silent hang (secondary, not causal)"
  missing:
    - "Break the hook/dependency cycle so Postgres exists before the migration Job waits on it (e.g. post-install,post-upgrade hook, or a regular non-hook Job with services tolerating unmigrated schema via readiness/retry)"
    - "Loud failure mode: explicit helm --timeout and/or activeDeadlineSeconds on the migrate Job so a stuck migration fails visibly instead of hanging"
    - "Recovery note: live release 'vantage' is stranded in pending-upgrade/failed state with the migrate Job running — must be cleared (helm uninstall or make kind-down) before retesting"
  debug_session: .planning/debug/helm-install-hang.md
