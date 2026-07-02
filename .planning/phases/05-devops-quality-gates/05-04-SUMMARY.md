---
phase: 05-devops-quality-gates
plan: 04
subsystem: infra
tags: [smoke, soak, kind, kubectl, helm, readme, port-forward]

requires:
  - phase: 05-devops-quality-gates (plan 01)
    provides: "make smoke-05 / soak targets + deploy chain"
  - phase: 05-devops-quality-gates (plan 02)
    provides: "deployed Helm release the scripts assert against"
provides:
  - "scripts/smoke/phase05-kind.sh — kind E2E smoke: migration/pods/pipeline assertions + OPS-03 independent-rollout demo (gateway port-forward on 8081)"
  - "scripts/soak.sh — SOAK_DURATION/SOAK_STREAMERS sustained load: row growth, produced>=acked, bounded depth (MQ inspect on 8082)"
  - "README Phase-5 section: deploy/smoke/soak/test-harness workflow + env prerequisites"
affects: [phase-6 deploy verification, verify-work UAT]

tech-stack:
  added: []
  patterns: ["smoke scripts guard on cluster+release and fail fast with the fix command (D-13)", "generation-capture assertion for single-service Helm rollouts"]

key-files:
  created:
    - scripts/smoke/phase05-kind.sh
    - scripts/soak.sh
  modified:
    - README.md

key-decisions:
  - "Migration-Job check tolerates hook-succeeded deletion: absent Job counts as success (hook delete policy removes it), completeness proven via pods + served rows"
  - "OPS-03 assertion captures Deployment generations before/after the targeted upgrade — streamer/collector/gateway must be unchanged"
  - "Soak fails hard if depth reaches capacity (runaway growth), tracks max depth across the window"

patterns-established:
  - "Port allocation: 8081 gateway smoke, 8082 MQ inspect soak — never 8080 locally (Pitfall 8)"

requirements-completed: [OPS-03]

coverage:
  - id: D1
    description: "make smoke-05 proves full E2E through kind and demonstrates independent MQ rollout"
    requirement: OPS-03
    verification:
      - kind: other
        ref: "bash -n scripts/smoke/phase05-kind.sh; greps for 8081:8080, reuse-values, cluster guard — all pass"
        status: pass
    human_judgment: true
    rationale: "Static checks pass; the live kind-cluster run (helm install → pods Running → rows served) requires the operator to run make kind-up deploy smoke-05 — deliberate per D-13 (smoke assumes cluster exists)"
  - id: D2
    description: "make soak sustains configurable load and asserts growth + no loss"
    verification:
      - kind: other
        ref: "bash -n scripts/soak.sh; greps for env defaults, kubectl scale, inspect poll, restore trap — all pass"
        status: pass
    human_judgment: true
    rationale: "Same as D1 — endurance run needs the live cluster; script logic statically verified"
  - id: D3
    description: "README documents the Phase-5 operator workflow (DOC-01 cadence)"
    verification:
      - kind: other
        ref: "grep smoke-05/make deploy/test-harness/ImagePullBackOff in README.md; existing sections intact"
        status: pass
    human_judgment: false

duration: 10min
completed: 2026-07-02
status: complete
---

# Phase 5 Plan 04: kind Smoke + Soak + README Summary

**kind E2E smoke script with migration/pod/pipeline assertions and a generation-based OPS-03 independent-rollout proof, a configurable soak script (streamer scaling, row growth, MQ counter reconciliation, bounded-depth watchdog), and the README Phase-5 operator workflow section**

## Performance

- **Duration:** 10 min
- **Started:** 2026-07-02T18:22:00Z
- **Completed:** 2026-07-02T18:32:00Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- smoke-05: cluster/release guard (fail-fast with "run: make kind-up deploy"), migration Job wait (hook-deletion tolerant), four-Deployment Available wait, gateway port-forward on 8081, /gpus + /telemetry row assertions, and the OPS-03 demo via before/after Deployment generations around `helm upgrade --reuse-values --set mq.image.tag=dev`
- soak: SOAK_DURATION (60) / SOAK_STREAMERS (3) env overrides, kubectl scale + rollout wait, psql row-count sampling inside vantage-postgresql-0, 5s inspect polling with hard-fail on depth==capacity, produced>=acked reconciliation, trap restores 1 replica + kills port-forwards
- README: Phase-5 section (deploy workflow, IfNotPresent/kind-load caveat, OPS-03 examples, smoke/soak/test-harness docs, kind+Rancher prerequisites) inserted before ## Testing; status table row flipped to shipped

## Task Commits

1. **Task 1: phase05-kind.sh smoke** - `6b0681c` (feat)
2. **Task 2: soak.sh + README** - `50227fb` (feat)

## Files Created/Modified
- `scripts/smoke/phase05-kind.sh` - kind E2E smoke, discovered by make smoke-05
- `scripts/soak.sh` - sustained-load endurance run, invoked by make soak
- `README.md` - Phase-5 workflow section + updated status table

## Decisions Made
- Migration Job absence treated as success: the hook's delete-policy removes it after completion, so `kubectl wait` on a missing Job would false-fail every re-run
- OPS-03 proof uses metadata.generation comparison rather than rollout watching — robust when the targeted upgrade is a no-op (same tag)

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Full operator loop shipped: make kind-up → deploy → smoke-05 → soak; live-cluster UAT is the remaining human step (D-13 by design)
- Phase 6 (WAL durability) can reuse the smoke/soak conventions and extend the harness with crash scenarios

---
*Phase: 05-devops-quality-gates*
*Completed: 2026-07-02*
