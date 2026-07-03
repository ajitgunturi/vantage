---
phase: 06-production-hardening-assignment-alignment
plan: "05"
subsystem: observability
tags: [collector, health, readiness, http, kubernetes, probes, atomic, runner]

requires:
  - phase: 06-01
    provides: slog migration in internal/collector and cmd/collector

provides:
  - collector.Runner struct with NewRunner/Run/IsReady
  - Config.HealthAddr field (COLLECTOR_HEALTH_ADDR env, default :9001)
  - GET /healthz liveness and GET /readyz readiness on collector :9001
  - Backward-compatible package-level Run delegating to Runner.Run
  - consumeStream internal helper with nil-safe markReady callback

affects:
  - 06-07 (Helm probes — collector health port :9001 referenced in deployment.yaml)
  - 06-08 (CI — make build/lint gates exercised)

tech-stack:
  added: []
  patterns:
    - "Runner struct wrapping existing reconnect loop: atomic.Bool readiness + NewRunner(cfg, pool)/Run(ctx)/IsReady() API"
    - "consumeStream internal func with nil-safe markReady func(bool) — preserves exported Consume signature while enabling readiness tracking"
    - "errgroup three-goroutine pattern: primary service + health HTTP server + shutdown coordinator (mirrors streamer plan 06-04)"
    - "TDD RED/GREEN for structural additions: tests compile-fail (RED) then pass (GREEN)"

key-files:
  created: []
  modified:
    - internal/collector/config.go
    - internal/collector/collector.go
    - internal/collector/run_test.go
    - internal/collector/config_test.go
    - cmd/collector/main.go
    - pkg/db/read_test.go

key-decisions:
  - "consumeStream internal helper: Runner.Run passes r.ready.Store as markReady; Consume passes nil — avoids touching Consume exported signature"
  - "Readiness semantics: markReady(true) after client.Consume(ctx) succeeds (stream open); deferred markReady(false) when consumeStream returns — probe reflects actual stream connectivity"
  - "Package-level Run preserved: one-line delegate to NewRunner(cfg, pool).Run(ctx) — zero caller disruption for integration tests"
  - "Health bodies return only status field (T-06-10: no MQ addr, DSN, or batch config echoed)"
  - "http.ErrServerClosed treated as clean exit in both g.Wait filter and goroutine return"
  - "Rule 1 auto-fix: pkg/db/read_test.go missing offset arg in db.Telemetry calls (signature updated in plan 06-02 but tests not updated)"

requirements-completed: [OBS-01]

coverage:
  - id: D1
    description: "Config.HealthAddr field populated from COLLECTOR_HEALTH_ADDR env var with :9001 default"
    requirement: OBS-01
    verification:
      - kind: unit
        ref: "internal/collector/config_test.go#TestConfig_HealthAddrDefault"
        status: pass
      - kind: unit
        ref: "internal/collector/config_test.go#TestConfig_HealthAddrOverride"
        status: pass
    human_judgment: false
  - id: D2
    description: "Runner.IsReady() returns false before Run is called (pre-streaming state)"
    requirement: OBS-01
    verification:
      - kind: unit
        ref: "internal/collector/run_test.go#TestRunner_NotReadyBeforeStart"
        status: pass
    human_judgment: false
  - id: D3
    description: "Package-level Run delegates to NewRunner(cfg, pool).Run(ctx); existing Consume/Run seams unchanged"
    requirement: OBS-01
    verification:
      - kind: unit
        ref: "internal/collector/run_test.go#TestRunPreCanceledContext"
        status: pass
      - kind: unit
        ref: "internal/collector/run_test.go#TestConsumeStreamOpenError"
        status: pass
    human_judgment: false
  - id: D4
    description: "GET /healthz returns 200 JSON {status:ok}; GET /readyz returns 200 once stream open, 503 before"
    requirement: OBS-01
    verification:
      - kind: build
        ref: "make build && go vet ./cmd/collector/..."
        status: pass
    human_judgment: true
    rationale: "No cmd/collector integration test for health endpoints; /readyz true-positive requires live MQ + DB"

duration: 10min
completed: "2026-07-03"
status: complete
---

# Phase 06 Plan 05: Collector Health Endpoints Summary

**Collector HTTP health listener on :9001 with atomic readiness gate and backward-compatible Runner API for Kubernetes liveness and readiness probes (OBS-01)**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-07-03T04:55:01Z
- **Completed:** 2026-07-03T05:05:07Z
- **Tasks:** 2 (plus TDD RED/GREEN commits)
- **Files modified:** 6

## Accomplishments

- `collector.Runner` struct with `NewRunner(cfg Config, pool *pgxpool.Pool) *Runner`, `IsReady() bool`, `Run(ctx) error` — readiness via `atomic.Bool` set when MQ Consume stream opens, cleared on stream close
- `consumeStream` internal helper: nil-safe `markReady func(bool)` callback invoked after `client.Consume(ctx)` succeeds and deferred-cleared on return; preserves `Consume` exported signature unchanged
- `Config.HealthAddr` (`COLLECTOR_HEALTH_ADDR`, default `:9001`) parsed in `FromEnv` matching existing env var conventions
- Package-level `Run(ctx, cfg, pool)` preserved as one-liner wrapper — zero breakage to all existing tests
- `cmd/collector/main.go` upgraded to three-goroutine errgroup: primary runner goroutine + health HTTP server + shutdown coordinator
- Health bodies carry only `{"status":"ok"}` or `{"status":"not_ready"}` — T-06-10 information disclosure mitigation

## Task Commits

1. **test(06-05): RED — failing tests for Runner readiness and HealthAddr config** - `fd46359`
2. **feat(06-05): Runner struct with IsReady, HealthAddr config field** - `22c50a0`
3. **feat(06-05): add health HTTP listener goroutine in cmd/collector** - `85ff6ac`

## Files Created/Modified

- `internal/collector/config.go` — added `HealthAddr string` field and `COLLECTOR_HEALTH_ADDR` parsing
- `internal/collector/collector.go` — added `Runner` struct, `NewRunner`, `IsReady`, `Runner.Run`; refactored `Consume` body to internal `consumeStream` with `markReady func(bool)` callback; package-level `Run` is one-line delegate
- `internal/collector/run_test.go` — added `TestRunner_NotReadyBeforeStart` (TDD RED gate)
- `internal/collector/config_test.go` — added `TestConfig_HealthAddrDefault` and `TestConfig_HealthAddrOverride`
- `cmd/collector/main.go` — replaced direct `collector.Run` with `collector.NewRunner`; added health HTTP server goroutine; updated startup log with `health_addr`; error filter includes `http.ErrServerClosed`
- `pkg/db/read_test.go` — fixed 5 calls to `db.Telemetry` missing the `offset` argument (Rule 1 auto-fix)

## Decisions Made

- **consumeStream internal helper:** The exported `Consume` signature must remain unchanged (existing integration tests + E2E depend on it). Created internal `consumeStream(ctx, client, pool, cfg, markReady func(bool))` that both `Consume` (nil markReady) and `Runner.Run` (r.ready.Store) delegate to. This avoids any duplication or signature breakage.
- **Readiness semantics:** `markReady(true)` placed immediately after `client.Consume(ctx)` succeeds — the gRPC Consume RPC is open. Deferred `markReady(false)` fires when `consumeStream` returns (stream ended, reconnecting). This is more precise than the streamer's pattern (which sets ready before entering the loop) because the collector's connectivity is stream-driven.
- **Package-level Run preserved:** One-line delegate `return NewRunner(cfg, pool).Run(ctx)` keeps all existing call sites and all pre-existing Stream/Run tests working without change.
- **Status-only health bodies:** Response JSON contains only `{"status":"ok"}` or `{"status":"not_ready"}` — no MQ address, DSN, credit, batch, or config values echoed (T-06-10 information disclosure mitigation).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed pkg/db/read_test.go missing offset arg in db.Telemetry calls**
- **Found during:** Pre-task verification (make coverage showed build failure in pkg/db_test)
- **Issue:** Plan 06-02 added an `offset int` parameter to `db.Telemetry` but the 5 integration test call sites in `pkg/db/read_test.go` were not updated, causing `make coverage` to fail with "not enough arguments in call to db.Telemetry"
- **Fix:** Added `, 0` (zero offset = no pagination skip) to all 5 affected call sites: `TestTelemetry_NoFilter`, `TestTelemetry_WindowFilter`, `TestTelemetry_EmptyResult`, `TestTelemetry_Limit`, `TestTelemetry_UsesCompositeIndex`
- **Files modified:** `pkg/db/read_test.go`
- **Commit:** `fd46359` (included in TDD RED commit)

---

**Total deviations:** 1 auto-fixed (Rule 1: pre-existing bug in test file from plan 06-02 offset signature change)
**Impact on plan:** No scope creep. Fix unblocks `make coverage` from compile failure; tests verify correct offset=0 baseline behavior.

## Issues Encountered

None — plan executed cleanly. TDD RED/GREEN cycle applied for Task 1; Task 2 was a straightforward errgroup extension mirroring the streamer pattern from plan 06-04.

## Threat Surface Scan

No new security surface beyond the plan's threat model:
- `T-06-10` (information disclosure): health bodies return `{"status":"ok"}` or `{"status":"not_ready"}` only — no config echoed. Implemented.
- `T-06-11` (DoS): `ReadTimeout: 2s`, `WriteTimeout: 2s` bound connections; O(1) handlers. Implemented.

## User Setup Required

None — no external service configuration required for this plan.

## Next Phase Readiness

- Collector health endpoints ready for Helm probe configuration in Plan 06-07
- `cfg.HealthAddr` (`:9001`) must be referenced in `deployments/charts/collector/templates/deployment.yaml` as the `health` named port
- `runner.IsReady()` readiness gate wired and working; probe against `:9001/readyz` returns 503 until the first Consume stream opens, then 200

## Self-Check

- [x] `internal/collector/config.go` — modified (HealthAddr field + env parse)
- [x] `internal/collector/collector.go` — modified (Runner struct + NewRunner/IsReady/Run + consumeStream + wrapper)
- [x] `internal/collector/run_test.go` — modified (TestRunner_NotReadyBeforeStart)
- [x] `internal/collector/config_test.go` — modified (TestConfig_HealthAddrDefault, TestConfig_HealthAddrOverride)
- [x] `cmd/collector/main.go` — modified (health goroutine wired)
- [x] `pkg/db/read_test.go` — modified (offset arg fix)
- [x] Commits: fd46359 (RED), 22c50a0 (GREEN task 1), 85ff6ac (feat task 2)

## Self-Check: PASSED

---
*Phase: 06-production-hardening-assignment-alignment*
*Completed: 2026-07-03*
