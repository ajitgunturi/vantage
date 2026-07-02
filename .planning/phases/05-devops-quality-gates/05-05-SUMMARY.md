---
phase: 05-devops-quality-gates
plan: "05"
subsystem: infra
tags: [helm, kubernetes, migrate-job, hook, kind, e2e]

requires:
  - phase: 05-devops-quality-gates
    provides: Helm umbrella chart, migrate-job, Makefile deploy targets, smoke-05 test suite

provides:
  - post-install,post-upgrade migrate-job hook that resolves instead of deadlocking
  - activeDeadlineSeconds:120 on migrate Job spec for bounded loud failure
  - explicit --timeout 3m on helm-install Makefile target
  - restored OPS-02 (make deploy succeeds) and OPS-03 (targeted mq upgrade) on a live kind cluster

affects: [phase-6-wal-durability, phase-5-soak]

tech-stack:
  added: []
  patterns:
    - "Helm post-install,post-upgrade hook ordering: regular resources (Bitnami postgresql Service) are applied BEFORE post-install hooks, so a wait-for-postgres init container resolves"
    - "before-hook-creation,hook-succeeded delete-policy re-creates the Job on each release op, making helm upgrade safe for Jobs with fixed names"
    - "Job activeDeadlineSeconds bounds migration stalls to a loud, named failure within 2 min instead of a silent helm-default 5m hang"

key-files:
  created: []
  modified:
    - deployments/templates/migrate-job.yaml
    - Makefile

key-decisions:
  - "Deliberate deviation from locked decision D-09: migrate hook phase moved from pre-install,pre-upgrade to post-install,post-upgrade. Intent preserved — a dedicated Job still runs once per release op and Helm still blocks until it completes. Services tolerate the brief post-Postgres/pre-migration window (collector self-migrates via db.Migrate at startup; gateway returns 500 not crash until schema exists). Reviewed and accepted by the owner in 05-UAT.md / .planning/debug/helm-install-hang.md."
  - "activeDeadlineSeconds placed on Job.spec (sibling of template, NOT inside pod spec) — this is the field Kubernetes uses to bound the total Job wall-clock duration"
  - "--timeout 3m added to helm-install without --wait: --wait additionally blocks on service pod readiness during the transient pre-migration window (collector may crashloop briefly before schema exists), which is unnecessary and would cause false failures"

patterns-established:
  - "Post-install hook ordering: if a hook init container must contact a Helm-managed Service, it must be a post-install (not pre-install) hook"
  - "Bounded loud failure: every deploy-critical Job should carry activeDeadlineSeconds + an explicit helm --timeout on the Makefile target"

requirements-completed: [OPS-02, OPS-03]

coverage:
  - id: D1
    description: "migrate-job hook annotation changed from pre-install,pre-upgrade to post-install,post-upgrade — Postgres Service exists before hook runs, resolving the deadlock"
    requirement: OPS-02
    verification:
      - kind: other
        ref: "helm template vantage deployments -f deployments/values.yaml --show-only templates/migrate-job.yaml | grep post-install,post-upgrade"
        status: pass
      - kind: other
        ref: "helm template ... | grep activeDeadlineSeconds: 120"
        status: pass
      - kind: other
        ref: "helm template ... | grep pre-install (must be absent)"
        status: pass
    human_judgment: false
  - id: D2
    description: "helm-install Makefile target carries --timeout 3m (no --wait)"
    requirement: OPS-02
    verification:
      - kind: other
        ref: "grep -E 'helm upgrade --install vantage deployments.*--timeout 3m' Makefile"
        status: pass
    human_judgment: false
  - id: D3
    description: "Live kind E2E: make kind-down && make kind-up && make deploy completed without hanging; post-install migrate Job ran to completion; make smoke-05 passed end-to-end including OPS-03 targeted mq upgrade"
    requirement: OPS-03
    verification:
      - kind: manual_procedural
        ref: "Human approval 2026-07-03: make deploy returned within 3m timeout, make smoke-05 PASS"
        status: pass
    human_judgment: true
    rationale: "Live kind cluster with Rancher Docker socket required; cannot be run headless by the executor. Human confirmed both make deploy (no hang) and make smoke-05 (PASS) on 2026-07-03."

duration: gap-closure (Tasks 1-2 ~20 min automated; Task 3 human checkpoint)
completed: "2026-07-03"
status: complete
---

# Phase 5 Plan 05: Helm Migrate-Job Hook Deadlock Fix Summary

**Broke the pre-install hook circular-wait deadlock by moving the migrate Job to post-install,post-upgrade, added activeDeadlineSeconds:120 + helm --timeout 3m for bounded loud failure, and re-proved the live kind E2E path (make deploy no hang, make smoke-05 PASS — OPS-02 and OPS-03 restored).**

## Performance

- **Duration:** gap-closure plan (automated tasks ~20 min; Task 3 human checkpoint)
- **Started:** 2026-07-03T00:00:00Z
- **Completed:** 2026-07-03
- **Tasks:** 3 (2 automated + 1 human-verify checkpoint)
- **Files modified:** 2

## Root Cause

`deployments/templates/migrate-job.yaml` was annotated as `helm.sh/hook: pre-install,pre-upgrade`. Helm runs pre-install hooks to completion **before** creating any regular release resource. The migrate Job's `wait-for-postgres` init container polls `nc -z vantage-postgresql 5432` — but the Bitnami postgresql StatefulSet/Service is a regular release resource, so it does not exist yet when the pre-install hook runs. Result: circular wait → silent hang → 5-minute default helm timeout failure. The pre-upgrade path deadlocked identically.

## Accomplishments

- Moved `helm.sh/hook` annotation from `pre-install,pre-upgrade` to `post-install,post-upgrade`: post-install hooks run after all regular release manifests are applied, so the Bitnami postgresql Service is reachable by the time the init container polls.
- Added `activeDeadlineSeconds: 120` on the Job's `spec` (sibling of `template`, NOT inside the pod spec): any migration that stalls now fails loudly within 2 minutes with a named Job timeout error instead of hanging to Helm's default 5-minute wall clock.
- Added `--timeout 3m` to the `helm-install` Makefile target (without `--wait`): `--wait` would stall on service pod readiness during the transient pre-migration window, causing false failures. `--timeout` alone delivers the bounded-failure requirement.
- Human verified live kind E2E (2026-07-03): `make kind-down && make kind-up && make deploy` returned within the 3-minute timeout with no hang; post-install migrate Job ran to completion; `make smoke-05` passed end-to-end including the OPS-03 targeted mq-only upgrade assertion.
- OPS-02 (helm install brings all pods Running) and OPS-03 (targeted independent upgrade) restored; Phase 5 UAT Test 1 passes and Test 2 (soak) is unblocked.

## Task Commits

1. **Task 1: Move migration Job to post-install,post-upgrade hook + add bounded loud failure** - `4bf43e8` (fix)
2. **Task 2: Add --timeout 3m to helm-install target** - `59d20b4` (fix)
3. **Task 3: Live kind E2E verify** — human checkpoint (no code commit; verified by human 2026-07-03)

## Files Created/Modified

- `deployments/templates/migrate-job.yaml` — hook annotation changed to `post-install,post-upgrade`; `activeDeadlineSeconds: 120` added on Job spec; init-container comment updated to reflect corrected ordering
- `Makefile` — `helm-install` target: `--timeout 3m` added

## Decisions Made

- **Deliberate deviation from locked decision D-09** (hook phase `pre-install,pre-upgrade`): moved to `post-install,post-upgrade`. The hook PHASE changes; the intent is preserved — a dedicated migration Job still runs once per release operation, and Helm still blocks until it completes. This deviation was explicitly diagnosed and approved in 05-UAT.md and `.planning/debug/helm-install-hang.md`.
- `activeDeadlineSeconds` placed on `Job.spec` (sibling of `template`): this is the Kubernetes field that bounds total Job wall-clock time. Inner pod spec `activeDeadlineSeconds` would only bound individual pod attempts.
- `--timeout 3m` without `--wait`: `--wait` blocks on all pod readiness (including the collector, which may crashloop briefly before the migration schema exists). Only `--timeout` is needed for the loud-failure requirement.

## Deviations from Plan

### Deliberate Corrective Deviation (owner-approved, not an auto-fix rule)

**D-09 hook phase: pre→post (approved direction from diagnosis)**
- **Found during:** Root-cause analysis documented in 05-UAT.md / `.planning/debug/helm-install-hang.md`
- **Issue:** Locked decision D-09 specified `pre-install,pre-upgrade`, which is the direct cause of the deadlock.
- **Fix:** Hook annotation changed to `post-install,post-upgrade` — the owner-approved fix direction from the diagnosis session.
- **Intent preserved:** Dedicated migration Job, runs once per release op, Helm blocks until completion. Services tolerate the transient pre-migration window (collector self-migrates via `db.Migrate`; gateway returns 500 until schema exists — not a crash).
- **Files modified:** `deployments/templates/migrate-job.yaml`
- **Committed in:** `4bf43e8`

---

**Total deviations:** 1 deliberate corrective (owner-approved direction, not an unplanned auto-fix)
**Impact on plan:** Required to achieve the plan objective. No scope creep.

## Issues Encountered

- Phase 5 UAT Test 1 (kind E2E) failed with a silent hang during the initial UAT session; the stranded release was recovered via `make kind-down && make kind-up` (clean cluster) before re-verification.

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced. Threat mitigations T-05-05-01 (deadlock → root fix + defense-in-depth `activeDeadlineSeconds`) and T-05-05-02 (silent hang → explicit `--timeout`) are fully applied. T-05-05-03 (migration DDL re-run on post-upgrade hook) is accepted: golang-migrate is idempotent.

## Next Phase Readiness

- Phase 5 is fully verified: OPS-02, OPS-03 restored; UAT Test 2 (soak) is unblocked.
- Phase 6 (MQ Durability — opt-in WAL persistence) may proceed. No blockers from this gap-closure plan.

---
*Phase: 05-devops-quality-gates*
*Completed: 2026-07-03*

## Self-Check: PASSED

- `05-05-SUMMARY.md` — FOUND at `.planning/phases/05-devops-quality-gates/05-05-SUMMARY.md`
- Commit `4bf43e8` (Task 1: migrate-job hook fix) — FOUND
- Commit `59d20b4` (Task 2: helm --timeout) — FOUND
- Commit `7161f64` (docs: SUMMARY + STATE + ROADMAP) — FOUND
