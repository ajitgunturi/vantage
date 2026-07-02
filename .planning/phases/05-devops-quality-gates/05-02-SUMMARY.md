---
phase: 05-devops-quality-gates
plan: 02
subsystem: infra
tags: [helm, kubernetes, bitnami, postgresql, oci, umbrella-chart, migration-hook, kind]

requires:
  - phase: 05-devops-quality-gates (plan 01)
    provides: "vantage/*:dev images + make dependency-update/deploy targets"
  - phase: 02-storage-foundation-schema-connection-pool
    provides: "cmd/migrate binary + golang-migrate migrations (payload of the hook Job)"
provides:
  - "deployments/ umbrella chart: 4 local sub-chart deps (condition: <svc>.enabled) + bitnami/postgresql 18.7.8 OCI dep"
  - "Pre-install/pre-upgrade migration hook Job with wait-for-postgres init container (busybox nc loop)"
  - "MQ sub-chart with HARDCODED replicas: 1 + strategy: Recreate (D-04/OPS-04)"
  - "Streamer fixture DCGM CSV ConfigMap mounted at /data/dcgm_metrics.csv"
  - "In-cluster DNS wiring: vantage-mq:50051, vantage-postgresql:5432, vantage-gateway:8080"
affects: [05-04 smoke and soak scripts, phase-6 WAL deploy]

tech-stack:
  added: ["bitnami/postgresql chart 18.7.8 (OCI)", "busybox:1.36 (init wait container)"]
  patterns: ["umbrella chart with local sub-chart deps + condition flags for OPS-03 independence", "Helm hook Job with init-container DB wait (Helm 4 hooks fail fast)"]

key-files:
  created:
    - deployments/Chart.yaml
    - deployments/values.yaml
    - deployments/templates/migrate-job.yaml
    - deployments/charts/mq/templates/deployment.yaml
    - deployments/charts/mq/templates/service.yaml
    - deployments/charts/streamer/templates/deployment.yaml
    - deployments/charts/streamer/templates/configmap.yaml
    - deployments/charts/collector/templates/deployment.yaml
    - deployments/charts/gateway/templates/deployment.yaml
    - deployments/charts/gateway/templates/service.yaml
  modified: []

key-decisions:
  - "Local sub-charts declared as Chart.yaml dependencies with condition: <svc>.enabled — required by Helm 4 lint and makes enabled flags Helm-native (belt-and-braces with in-template guards)"
  - "Sub-chart templates use .Values.image.* / .Values.enabled (Helm sub-chart scoping) — parent values.yaml keeps the planned <svc>.image.* layout"
  - "DSN hosts use {{ .Release.Name }}-postgresql / -mq so charts are release-name-correct (renders vantage-* with the vantage release)"

patterns-established:
  - "Service/Deployment names: {{ .Release.Name }}-<svc> → in-cluster DNS vantage-<svc>"
  - "MQ replicas/strategy are literals in the template, never values"

requirements-completed: [OPS-02, OPS-03, OPS-04]

coverage:
  - id: D1
    description: "Umbrella chart with 4 sub-charts + Bitnami PostgreSQL OCI dependency lints and templates cleanly; Chart.lock written"
    requirement: OPS-02
    verification:
      - kind: other
        ref: "helm dependency update deployments/ && helm lint deployments/ — 0 failed"
        status: pass
    human_judgment: false
  - id: D2
    description: "Per-service enabled + image.tag values allow independent deploy/disable of any single service"
    requirement: OPS-03
    verification:
      - kind: other
        ref: "helm template --set streamer.enabled=false omits vantage-streamer; each Deployment guarded + templated image tag"
        status: pass
    human_judgment: false
  - id: D3
    description: "MQ Deployment hardcodes replicas: 1 and strategy: Recreate as literals (not templated)"
    requirement: OPS-04
    verification:
      - kind: other
        ref: "helm template vantage deployments | grep 'replicas: 1' + 'type: Recreate'; grep literals in mq template; values.yaml has no replicas/strategy"
        status: pass
    human_judgment: false
  - id: D4
    description: "helm install brings all pods Running in a kind cluster (live E2E)"
    requirement: OPS-02
    verification: []
    human_judgment: true
    rationale: "Live kind-cluster install is exercised by 05-04 smoke-05; not run in this plan (chart tree + lint/template proofs only)"

duration: 9min
completed: 2026-07-02
status: complete
---

# Phase 5 Plan 02: Helm Umbrella + Sub-charts Summary

**Helm umbrella chart with Bitnami PostgreSQL 18.7.8 OCI dependency, four self-contained service sub-charts (MQ locked to single-replica/Recreate), pre-install migration hook Job with postgres-wait init container, and a streamer fixture-CSV ConfigMap**

## Performance

- **Duration:** 9 min
- **Started:** 2026-07-02T17:56:30Z
- **Completed:** 2026-07-02T18:05:30Z
- **Tasks:** 2
- **Files modified:** 16

## Accomplishments
- `helm dependency update deployments/` resolves postgresql 18.7.8 from oci://registry-1.docker.io/bitnamicharts (Assumption A1 verified live) and writes Chart.lock
- `helm lint deployments/` passes 0-failed; full `helm template` renders migrate hook Job, all four service Deployments, and both Services
- MQ Deployment renders literal `replicas: 1` + `strategy.type: Recreate`; values.yaml carries no replicas/strategy keys (T-05-03 mitigated)
- `--set <svc>.enabled=false` verifiably omits that service's objects (OPS-03 spot-checked at template level)
- Migration Job: pre-install/pre-upgrade hook, weight -5, before-hook-creation+hook-succeeded delete policy, busybox nc wait loop against vantage-postgresql:5432 (Pitfall 7 mitigation)

## Task Commits

1. **Task 1: Umbrella + OCI dep + migration hook** - `a535d37` (feat)
2. **Task 2: Four service sub-charts** - `b097a8c` (feat)

## Files Created/Modified
- `deployments/Chart.yaml` - umbrella; 4 local deps w/ conditions + postgresql OCI dep
- `deployments/values.yaml` - per-service enabled/image blocks, postgresql auth, migrate image
- `deployments/templates/migrate-job.yaml` - hook Job + init wait container
- `deployments/charts/{mq,streamer,collector,gateway}/` - self-contained sub-charts (D-03)
- `deployments/Chart.lock`, `deployments/charts/postgresql-18.7.8.tgz` - pinned dependency

## Decisions Made
- Helm 4 lint requires local sub-charts listed in dependencies — added with `condition: <svc>.enabled`, which is also the idiomatic mechanism for OPS-03 subset deploys
- Used `{{ .Release.Name }}` prefixes instead of hardcoded `vantage-` strings so the chart is correct under any release name

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Sub-chart value scoping corrected**
- **Found during:** Task 2 (sub-chart templates)
- **Issue:** Plan specified `{{ .Values.<svc>.image.repository }}` inside sub-chart templates; Helm scopes parent values under the sub-chart name, so that path would resolve to nil and break rendering
- **Fix:** Sub-chart templates use `.Values.image.*` / `.Values.enabled`; parent values.yaml keeps the exact planned `<svc>:` layout — rendered output matches every planned assertion
- **Files modified:** all four sub-chart templates
- **Verification:** helm lint + helm template render correct images/guards; disable spot-check passes
- **Committed in:** b097a8c

**2. [Rule 1 - Bug] Local sub-charts declared as umbrella dependencies**
- **Found during:** Task 2 (helm lint)
- **Issue:** Helm 4 lint fails with "chart metadata is missing these dependencies: collector,gateway,mq,streamer" when local charts/ members are undeclared
- **Fix:** Added the four local sub-charts to Chart.yaml dependencies with version 0.1.0 + condition flags
- **Files modified:** deployments/Chart.yaml
- **Verification:** helm dependency update + helm lint pass
- **Committed in:** b097a8c

---

**Total deviations:** 2 auto-fixed (both Rule 1 — Helm scoping/metadata corrections)
**Impact on plan:** Required for the charts to render at all; no scope creep. All planned assertions hold on rendered output.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Chart tree complete: `make deploy` can now build → kind-load → helm-install end-to-end (exercised by 05-04 smoke)
- Live kind install intentionally deferred to 05-04 smoke-05 (D-12/D-13)

---
*Phase: 05-devops-quality-gates*
*Completed: 2026-07-02*
