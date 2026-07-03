---
phase: 6
slug: production-hardening-assignment-alignment
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-07-03
---

# Phase 6 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (testify; testcontainers for DB-touching tests) |
| **Config file** | Makefile (targets: test, coverage, lint) |
| **Quick run command** | `go test -race -count=1 ./<changed-package>/...` |
| **Full suite command** | `make test && make coverage && make lint` |
| **Estimated runtime** | ~120 seconds (full; testcontainers pulls cached) |

---

## Sampling Rate

- **After every task commit:** Run `go test -race -count=1 ./<changed-package>/...`
- **After every plan wave:** Run `make test && make coverage && make lint`
- **Before `/gsd-verify-work`:** Full suite must be green; `make build` + `make swagger` drift-free
- **Max feedback latency:** 180 seconds

---

## Per-Task Verification Map

*(Filled by planner — every task must map to a requirement and an automated command.)*

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| TBD | — | — | OBS-01/02, OPS-07/08/09, API-05, QA-07, DOC-02/03 | — | health endpoints leak no internals; DSN never logged (slog migration preserves ASVS V8) | unit/integration | `make test` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Existing infrastructure covers all phase requirements — go test + testify + testcontainers already in place; new tests slot into existing packages (`internal/http`, `internal/gateway`, `internal/streamer`, `internal/collector`, `pkg/db`).
- [ ] Helm chart changes verified via `helm template`/`helm lint` (no new framework needed).

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Probes gate pod readiness on kind | OPS-07 | Requires live cluster | `make deploy` then `kubectl get pods -n <ns>` — all Ready; kill MQ container, observe restart via liveness |
| HPA object created when enabled | OPS-09 | Requires metrics-server (absent on kind) | `helm template --set gateway.autoscaling.enabled=true` renders HPA; README documents metrics-server caveat |
| CI workflow green on push/PR | QA-07 | Requires GitHub runner | Push branch, observe Actions run all four make gates |
| AI_PROMPTS.md content honesty | DOC-02 | Editorial judgment | Owner reads for verbatim-prompt fidelity + failure-story candor |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 180s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
