---
phase: 1
slug: foundation-proto-contract-mq-core
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-27
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from `01-RESEARCH.md` (## Validation Architecture). Planner refines the per-task map.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (stdlib `testing`, `-race`) + testify (asserts) |
| **Config file** | none — Go modules; `make test` / `make coverage` drive it |
| **Quick run command** | `cd mq-or-root-module && go test -race ./internal/...` |
| **Full suite command** | `make coverage` (race + ≥90% gate on logic packages) |
| **Estimated runtime** | ~10–40s (race detector dominates) |

---

## Sampling Rate

- **After every task commit:** Run `go test -race ./internal/...`
- **After every plan wave:** Run `make coverage`
- **Before `/gsd-verify-work`:** Full suite green + coverage ≥90% on `internal/`
- **Max feedback latency:** ~40 seconds

---

## Per-Task Verification Map

Representative — planner aligns task IDs to plan waves. Every MQ requirement maps to an automated test.

| Task | Wave | Requirement | Secure Behavior | Test Type | Automated Command | Status |
|------|------|-------------|-----------------|-----------|-------------------|--------|
| proto gen | 0 | MQ-01/02 | N/A | build | `make proto && go build ./...` | ⬜ pending |
| ring store enqueue/dequeue | 1 | MQ-04 | bounded, no race | unit + race | `go test -race ./internal/queue/...` | ⬜ pending |
| drop-oldest on full | 1 | MQ-05 | oldest overwritten, dropped_total increments | unit | `go test -race -run DropOldest ./internal/queue/...` | ⬜ pending |
| unique delivery (N=N, K consumers) | 1 | MQ-03, QA-02 | each msg to exactly one consumer; dropped_total==0 | concurrency + race | `go test -race -count=50 -run UniqueDelivery ./internal/...` | ⬜ pending |
| Produce/Consume gRPC | 2 | MQ-01, MQ-02 | round-trip payload | integration | `go test -race ./internal/server/...` | ⬜ pending |
| consumer disconnect, no leak | 2 | MQ-07 | goroutine count stable after cancel | concurrency | `go test -race -run Disconnect ./internal/server/...` | ⬜ pending |
| inspect endpoint JSON | 2 | MQ-06 | returns depth/capacity/produced/consumed/dropped/consumers | httptest | `go test -race ./internal/http/...` | ⬜ pending |
| Store interface seam | 1 | MQ-08 | in-memory backend satisfies interface | unit | `go test -race ./internal/queue/...` | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Verify/install the `protoc` binary (NOT installed by `make tools` — only the Go plugins are) and confirm `make proto` generates `pkg/pb`
- [ ] Scope the coverage gate to logic packages (exclude generated `pkg/pb` and thin `cmd/`)
- [ ] Test scaffolding for `internal/queue`, `internal/server`, `internal/http`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| gRPC keepalive survives multi-minute idle behind NAT/LB | MQ-02 | True NAT/LB idle-timeout reproduction needs a cluster, not a unit test | In Phase 5 kind cluster, hold a `Consume` stream idle >6 min; assert it stays open. Unit-test the keepalive params are set at construction. |

---

## Validation Sign-Off

- [ ] All tasks have automated verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers protoc + coverage scoping
- [ ] No watch-mode flags
- [ ] Feedback latency < 40s
- [ ] `nyquist_compliant: true` set after planner aligns task IDs

**Approval:** pending
