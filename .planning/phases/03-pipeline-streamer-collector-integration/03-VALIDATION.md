---
phase: 3
slug: pipeline-streamer-collector-integration
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-29
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Detailed validation architecture lives in `03-RESEARCH.md` (## Validation Architecture).

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (-race), testify, testcontainers-go (postgres:17-alpine), grpc/test/bufconn |
| **Config file** | none — Makefile drives (`make test`, `make coverage`) |
| **Quick run command** | `go test -race ./cmd/streamer/... ./cmd/collector/... ./pkg/models/...` |
| **Full suite command** | `make coverage` (with Rancher Docker env: `DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true`) |
| **Estimated runtime** | ~60–90 seconds (integration suite spins up postgres + bufconn MQ) |

---

## Sampling Rate

- **After every task commit:** Run the quick run command for the touched package
- **After every plan wave:** Run the full suite command
- **Before `/gsd-verify-work`:** Full suite must be green; coverage ≥ 90%
- **Max feedback latency:** ~90 seconds

---

## Per-Task Verification Map

*Populated by the planner / executors as tasks are defined. Maps each task → requirement (STREAM-01..05, COLL-01..05, QA-03) → automated command.*

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Status |
|---------|------|------|-------------|-----------|-------------------|--------|
| TBD | — | — | — | — | — | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/models/` package (GpuMetric + FromProto + InsertSQL) — shared write/read model, needed before Collector + Gateway tests
- [ ] `grpc/test/bufconn` in-process harness for the QA-03 E2E test (no new dependency — sub-package of existing grpc)

*Existing testcontainers + pkg/db Snapshot/Restore infrastructure covers the Postgres side.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end pipeline under the docker-compose dev stack | QA-03 (confidence) | Smoke harness exercises real broker + Postgres + restamp drift | `make smoke-03` (to be added this phase, container-sourced like smoke-02) |

*The exactly-once-under-concurrency property has automated coverage via the bufconn E2E test; the smoke run is a confidence check on the human-runnable harness.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
