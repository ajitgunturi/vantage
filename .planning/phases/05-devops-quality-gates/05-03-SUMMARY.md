---
phase: 05-devops-quality-gates
plan: 03
subsystem: testing
tags: [testcontainers, docker-compose, e2e, build-tags, coverage-gate, rancher-desktop]

requires:
  - phase: 05-devops-quality-gates (plan 01)
    provides: "vantage/*:dev images + make test-harness target"
  - phase: 04-api-gateway-openapi-docs
    provides: "gateway REST endpoints the harness asserts against"
provides:
  - "docker-compose.full.yml — full five-image + Postgres stack (gateway host-mapped to 8090)"
  - "testdata/fixture.csv — 12-row DCGM 12-column fixture, distinct (uuid, metric_name) pairs"
  - "test/harness/harness_test.go — //go:build e2e testcontainers-compose harness, KEEP=1 escape hatch, kind-pointable via HARNESS_GATEWAY_BASE"
  - "QA-01 + QA-04 closed: make test green across internal/+pkg/, coverage gate 90.3% >= 90% with harness excluded"
affects: [05-04 README workflow section, phase-6 crash-resilience harness extension]

tech-stack:
  added: ["github.com/testcontainers/testcontainers-go/modules/compose v0.43.0"]
  patterns: ["e2e build tag isolates live-infra suite from make test and coverage", "hermetic up→test→down with t.Cleanup + KEEP=1 debug opt-out"]

key-files:
  created:
    - docker-compose.full.yml
    - testdata/fixture.csv
    - test/harness/harness_test.go
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "Postgres has NO host port mapping in docker-compose.full.yml — an exposed 5432 collides with the dev stack; harness asserts via gateway"
  - "HARNESS_GATEWAY_BASE env skips stack ownership entirely — same suite points at a kind/Helm deploy (D-16)"
  - "Collector/gateway compose deps gate on migrate service_completed_successfully — schema exists before consumers start"

patterns-established:
  - "test/ tree is outside the coverage-gate package list (./internal/... ./pkg/...) — new e2e suites live there"

requirements-completed: [QA-01, QA-04]

coverage:
  - id: D1
    description: "make test-harness owns the full five-image compose stack, proves CSV→streamer→MQ→collector→Postgres→gateway E2E, tears down hermetically"
    requirement: QA-01
    verification:
      - kind: e2e
        ref: "go test -tags=e2e ./test/harness/... — TestHarness green (16.3s), docker ps shows no residual harness containers"
        status: pass
    human_judgment: false
  - id: D2
    description: "Unit tests pass across all services (QA-01)"
    requirement: QA-01
    verification:
      - kind: unit
        ref: "make test — all internal/ and pkg/ packages green with -race"
        status: pass
    human_judgment: false
  - id: D3
    description: "Coverage gate holds at >= 90% with the e2e harness excluded (QA-04)"
    requirement: QA-04
    verification:
      - kind: integration
        ref: "make coverage — total 90.3% (min 90%); no test/harness package in output"
        status: pass
    human_judgment: false

duration: 14min
completed: 2026-07-02
status: complete
---

# Phase 5 Plan 03: Live-Infrastructure Harness + QA Gates Summary

**Testcontainers-compose E2E harness (//go:build e2e) that owns the full five-image stack, proves pipeline correctness through the gateway REST API with row-growth assertions, tears down hermetically with a KEEP=1 escape hatch — QA-01 green, QA-04 coverage gate holding at 90.3%**

## Performance

- **Duration:** 14 min
- **Started:** 2026-07-02T18:06:00Z
- **Completed:** 2026-07-02T18:20:00Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- `make test-harness` path proven live: TestHarness green in 16.3s against real containers — GPUs listed, telemetry rows returned, row count non-shrinking while the streamer loops
- Hermetic teardown verified (no vantage-harness containers after run); KEEP=1 leaves the stack up for debugging
- Tag isolation proven: `go test ./...` does not compile the harness; `make coverage` output contains no test/harness package
- QA-01 + QA-04 closed: make test green, coverage gate 90.3% >= 90%

## Task Commits

1. **Task 1: compose stack + fixture CSV** - `9216c36` (feat)
2. **Task 2: e2e harness + compose module + gate closure** - `0f89c0b` (feat)

## Files Created/Modified
- `docker-compose.full.yml` - six services (postgres, migrate one-shot, mq, streamer, collector, gateway@8090)
- `testdata/fixture.csv` - 12 rows, 3 GPUs x 4 DCGM metrics, distinct (uuid, metric_name)
- `test/harness/harness_test.go` - compose lifecycle + HTTP pipeline assertions
- `go.mod` / `go.sum` - modules/compose v0.43.0 pinned to core version

## Decisions Made
- Removed Postgres host port from the full stack after a live 5432 collision with the dev stack (see deviation) — harness never needed host DB access
- Added `service_completed_successfully` dependency on migrate for collector/gateway — deterministic schema-before-consumers ordering inside compose

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocker] Postgres host port collision with dev stack**
- **Found during:** Task 2 (first live harness run)
- **Issue:** Plan said to copy the postgres service verbatim from docker-compose.yml including `5432:5432`; with the dev stack running, compose up failed: "Bind for :::5432 failed: port is already allocated"
- **Fix:** Dropped the host port mapping from docker-compose.full.yml — services reach postgres over the compose network; harness assertions go through the gateway (8090)
- **Files modified:** docker-compose.full.yml
- **Verification:** compose config valid; harness green with dev stack still running
- **Committed in:** 0f89c0b

---

**Total deviations:** 1 auto-fixed (Rule 3 — blocking port conflict)
**Impact on plan:** Makes make test-harness robust regardless of dev-stack state. No scope creep.

## Issues Encountered
- First `go get` of the compose module was dropped by `go mod tidy` because no file imported it yet — resolved by writing harness_test.go first, then pinning (ordering note for future module additions)

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Harness is kind-pointable (HARNESS_GATEWAY_BASE) — 05-04's smoke/soak workflow can reference it
- Phase 6 can extend the harness with kill/restart resilience scenarios (explicitly deferred)

---
*Phase: 05-devops-quality-gates*
*Completed: 2026-07-02*
