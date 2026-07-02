---
status: complete
phase: 05-devops-quality-gates
source: [05-VERIFICATION.md]
started: 2026-07-02T18:14:53Z
updated: 2026-07-02T19:50:44Z
---

## Current Test

[testing complete]

## Tests

### 1. kind E2E deploy + smoke
expected: make kind-up && make deploy && make smoke-05 passes end-to-end — pods Running, real rows served, independent MQ rollout demonstrated
result: passed
note: "Re-verified 2026-07-03 after gap-closure plan 05-05 (migrate Job moved to post-install,post-upgrade hook + activeDeadlineSeconds 120; helm-install --timeout 3m). Human confirmed on clean kind cluster: make deploy returned within 3m, make smoke-05 PASS including mq-only targeted upgrade."

### 2. Sustained soak
expected: make soak (defaults 60s / 3 streamers; optionally SOAK_STREAMERS=10) — row count grows, produced_total >= consumed_total, queue depth stays below capacity, streamer restored to 1 replica on exit
result: passed
note: "Human confirmed 2026-07-02: make soak ran against the deployed kind cluster (after fixing machine-local DOCKER_HOST scheme in .env) — rows grew, produced_total >= consumed_total, depth below capacity, streamer restored to 1 replica."

## Summary

total: 2
passed: 2
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps

- truth: "make kind-up && make deploy && make smoke-05 passes end-to-end — migration hook completes, all four Deployments Available, gateway serves real rows, targeted mq upgrade rolls only MQ"
  status: resolved
  resolved_by: 05-05
  resolution: "Hook phase moved pre-install,pre-upgrade → post-install,post-upgrade (Postgres now exists before wait-for-postgres polls it); activeDeadlineSeconds: 120 on the Job and --timeout 3m on helm-install bound any residual stall to a loud failure. Human re-verified live on clean kind cluster 2026-07-03: deploy returns, smoke-05 passes."
  reason: "User reported: seems stuck in - helm upgrade --install vantage deployments -f deployments/values.yaml / Release \"vantage\" does not exist. Installing it now."
  severity: major
  test: 1
  root_cause: "Chicken-and-egg deadlock: migrate-job.yaml is a helm.sh/hook pre-install,pre-upgrade Job whose wait-for-postgres init container loops `until nc -z vantage-postgresql 5432`, but Helm applies pre-install hooks to completion BEFORE installing regular release manifests — including the Bitnami postgresql StatefulSet/Service. The Service the init container waits on is only created after the hook succeeds, so the hook can never complete. Helm prints nothing while waiting on hooks → silent hang until the default 5m timeout. The pre-upgrade path deadlocks identically (Postgres was never installed by the failed first attempt), so re-running cannot recover."
  artifacts:
    - path: "deployments/templates/migrate-job.yaml"
      issue: "pre-install/pre-upgrade hook annotation + wait-for-postgres init container = circular wait against the same release's Postgres (hook blocks the resources it depends on)"
    - path: "Makefile"
      issue: "helm-install target has no explicit --timeout; default 5m makes the failure present as a silent hang (secondary, not causal)"
  debug_session: .planning/debug/resolved/helm-install-hang.md
