---
status: resolved
trigger: "helm upgrade --install vantage deployments -f deployments/values.yaml hangs indefinitely during Phase 5 UAT (kind E2E deploy). Prints 'Release \"vantage\" does not exist. Installing it now.' then no further output."
created: 2026-07-03T00:03:00+05:30
updated: 2026-07-03T00:00:00Z
---

## Current Focus

hypothesis: CONFIRMED — pre-install/pre-upgrade hook deadlock. Migration Job hook must complete before Helm creates any regular release resources (including the Bitnami Postgres StatefulSet/Service), but the Job's init container waits for that very Postgres service. Circular wait -> hook never completes -> helm blocks silently until timeout.
test: complete
expecting: complete
next_action: Return ROOT CAUSE FOUND diagnosis (goal: find_root_cause_only — no fix applied).

## Symptoms

expected: make kind-up && make deploy && make smoke-05 passes end-to-end — migration hook Job completes, all four Deployments (mq, streamer, collector, gateway) become Available, port-forwarded gateway (localhost:8081) returns non-empty GPU list and telemetry rows, targeted mq image upgrade rolls only the MQ Deployment.
actual: Deploy hangs at `helm upgrade --install vantage deployments -f deployments/values.yaml`. All five kind load image loads complete, helm dependency update completes (pulls bitnamicharts/postgresql:18.7.8), helm prints 'Release "vantage" does not exist. Installing it now.' and hangs with no further output. Reproduced twice.
errors: None printed — silent hang.
reproduction: Test 1 in UAT — make kind-up && make deploy on local kind cluster (Rancher Desktop docker). Cluster still live.
started: Discovered during Phase 5 UAT (first-ever live kind deploy of these charts). Charts authored this phase — commits a535d37 (umbrella + Bitnami OCI dep + pre-install migration hook Job) and b097a8c (four service sub-charts).

## Eliminated

## Evidence

- timestamp: 2026-07-03T00:02
  checked: Live kind cluster — kubectl get pods,jobs -A; kubectl get events -A; helm list -A
  found: |
    - pod/vantage-migrate-scx94 stuck in Init:0/1 (init container running, main container never started), age ~5m; a prior pod vantage-migrate-9d84z ran its wait-for-postgres init container for ~5m before being killed ("Stopping container wait-for-postgres") when a second helm attempt superseded the first.
    - job.batch/vantage-migrate Running 0/1 — never completes.
    - NO postgres pod/StatefulSet exists in any namespace. Zero release resources besides the migrate Job.
    - helm list: release "vantage" revision 2, status "pending-upgrade" (first attempt left pending-install; user re-ran, creating a pending-upgrade).
    - Events show busybox:1.36 pulled fine (init image pull is NOT the problem). Init container "wait-for-postgres" is running — waiting on Postgres.
  implication: Helm is blocked waiting for the hook Job to complete, and the hook Job is waiting for Postgres — which Helm has not created and will not create until the hook completes. Classic pre-install hook deadlock. Rules out image-pull and Docker Hub slowness for the migrate job's init image.

- timestamp: 2026-07-03T00:05
  checked: deployments/templates/migrate-job.yaml (lines 10-34) and Makefile helm-install target (line 122-123)
  found: |
    - Job annotated `"helm.sh/hook": pre-install,pre-upgrade`, hook-weight -5, hook-delete-policy before-hook-creation,hook-succeeded.
    - Job's initContainer `wait-for-postgres` (busybox) loops `until nc -z {{ .Release.Name }}-postgresql 5432` forever.
    - Makefile: `helm upgrade --install vantage deployments -f deployments/values.yaml` — no --wait, no --timeout (default 5m applies; helm ALWAYS blocks on hook completion regardless of --wait).
  implication: Helm hook semantics — pre-install hooks must complete before ANY regular chart resource (including the Bitnami postgresql sub-chart StatefulSet + Service) is applied. The hook waits on a Service that Helm will only create after the hook succeeds. Circular dependency.
- timestamp: 2026-07-03T00:07
  checked: kubectl logs vantage-migrate pod init container; helm version; helm history vantage; kubectl get svc,statefulset -A | grep postgres
  found: |
    - Init container logs loop: "nc: bad address 'vantage-postgresql'" — the Service does not exist (DNS lookup fails).
    - No postgres Service or StatefulSet exists in any namespace (grep returned nothing).
    - Helm v4.0.5.
    - helm history: revision 1 FAILED "Release \"vantage\" failed: context canceled" (user Ctrl-C'd the perceived hang); revision 2 FAILED "pre-upgrade hooks failed: resource not ready, name: vantage-migrate, kind: Job, status: InProgress" (default 5m timeout expired).
  implication: Direct proof of the deadlock: the hook Job is alive and waiting on vantage-postgresql; the Service is absent; helm blocks silently (no progress output during hook wait) until 5m timeout or Ctrl-C — perceived as an indefinite hang. The pre-UPGRADE path has the identical deadlock because Postgres was never installed by the failed first attempt.

## Resolution

root_cause: |
  Chicken-and-egg deadlock between the Helm migration hook and the in-release Postgres dependency.
  deployments/templates/migrate-job.yaml declares `helm.sh/hook: pre-install,pre-upgrade`. Helm runs
  pre-install hooks to completion BEFORE applying any regular release manifests — which includes the
  Bitnami postgresql sub-chart's StatefulSet and Service. The hook Job's wait-for-postgres init
  container polls `nc -z vantage-postgresql 5432` forever, but that Service can only be created after
  the hook succeeds. The hook can never complete; helm waits silently (no output during hook wait)
  until its default 5m timeout ("pre-upgrade hooks failed: resource not ready ... Job ... InProgress")
  or user cancellation ("context canceled") — perceived as an indefinite silent hang. The safety init
  container added for "Pitfall 7" (don't false-fail while Postgres starts) converted what would have
  been a fast, diagnosable migration failure into a silent deadlock.
fix: (not applied — diagnose-only mode)
verification: (n/a)
files_changed: []
