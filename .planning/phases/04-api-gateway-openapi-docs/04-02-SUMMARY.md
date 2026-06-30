---
phase: 04-api-gateway-openapi-docs
plan: "02"
subsystem: api-gateway
status: complete
tags: [go, chi, pgx, tdd, telemetry, time-series, index-scan, nullable-columns]
dependency_graph:
  requires:
    - phase: 04-01
      provides: [internal/gateway (Config, NewRouter, ListGPUs, helpers), pkg/db.DistinctGPUIDs, chi/v5, testcontainers TestMain/Snapshot/Restore]
    - phase: 02
      provides: [gpu_metrics schema, idx_gpu_metrics_gpu_id_ts composite index, pkg/db.New/Migrate]
  provides:
    - db.GPUExists(ctx, pool, id) — SELECT EXISTS lookup for 404 gate
    - db.Telemetry(ctx, pool, id, start, end, limit) — two-query windowed read with COALESCE for nullable columns
    - gateway.GetTelemetry(pool, maxRows) http.HandlerFunc — full telemetry vertical slice
    - Route GET /api/v1/gpus/{id}/telemetry registered in NewRouter
  affects: [04-03 (swagger annotations now cover both endpoints), Plan 05 Helm values]
tech_stack:
  added: []
  patterns:
    - "Two-query approach: separate simple/windowed SQL paths keep planner index usage clean (RESEARCH Pattern 5 / A1)"
    - "COALESCE on nullable text columns in SELECT avoids *string scan panics without changing domain model"
    - "Parse time params before pool access — 400 fires even with nil pool; unit tests can assert it"
    - "GPUExists before Telemetry — single SELECT EXISTS call distinguishes 404 (unknown GPU) from 200 [] (known GPU, empty window)"
key_files:
  created: []
  modified:
    - pkg/db/read.go
    - internal/gateway/handler.go
    - internal/gateway/server.go
    - internal/gateway/handler_test.go
    - internal/gateway/integration_test.go
    - pkg/db/read_test.go
key-decisions:
  - "Two-query approach in db.Telemetry: no-bounds path uses simple WHERE gpu_id=$1 ORDER BY timestamp DESC LIMIT $2; bounded path uses nullable IS NULL OR predicates on $2/$3 for partial-bound support (OQ-3)"
  - "COALESCE on optional text columns in SELECT: converts DB NULLs to empty strings so *string scan never fails — production Collector always inserts empty strings but test helpers omit optional columns"
  - "GPUExists(EXISTS query) before Telemetry call: zero overhead on the missing-GPU path and correctly separates 404 (unknown) from 200-[] (empty window) per OQ-2"
  - "Time parse before nil-pool guard: ensures 400 fires for malformed time even in unit tests with nil pool, preserving the application/json Content-Type contract"
requirements-completed: [API-02, API-03]

coverage:
  - id: D1
    description: "GET /api/v1/gpus/{id}/telemetry returns rows ordered timestamp DESC (API-02)"
    requirement: API-02
    verification:
      - kind: integration
        ref: "internal/gateway/integration_test.go#TestGetTelemetry_NoFilter"
        status: pass
      - kind: integration
        ref: "pkg/db/read_test.go#TestTelemetry_NoFilter"
        status: pass
    human_judgment: false
  - id: D2
    description: "?start_time=&end_time= window filters rows using idx_gpu_metrics_gpu_id_ts (API-03, EXPLAIN proven)"
    requirement: API-03
    verification:
      - kind: integration
        ref: "internal/gateway/integration_test.go#TestGetTelemetry_TimeWindow"
        status: pass
      - kind: integration
        ref: "pkg/db/read_test.go#TestTelemetry_UsesCompositeIndex"
        status: pass
    human_judgment: false
  - id: D3
    description: "Unknown GPU returns 404 + ErrorResponse (OQ-2)"
    verification:
      - kind: integration
        ref: "internal/gateway/integration_test.go#TestGetTelemetry_UnknownGPU"
        status: pass
      - kind: integration
        ref: "pkg/db/read_test.go#TestGPUExists_False"
        status: pass
    human_judgment: false
  - id: D4
    description: "Known GPU with no rows in window returns 200 [] (OQ-2)"
    verification:
      - kind: integration
        ref: "internal/gateway/integration_test.go#TestGetTelemetry_KnownGPUEmptyWindow"
        status: pass
      - kind: integration
        ref: "pkg/db/read_test.go#TestTelemetry_EmptyResult"
        status: pass
    human_judgment: false
  - id: D5
    description: "Partial time bounds allowed (only start_time or only end_time) — OQ-3"
    verification:
      - kind: integration
        ref: "internal/gateway/integration_test.go#TestGetTelemetry_PartialBounds"
        status: pass
    human_judgment: false
  - id: D6
    description: "Malformed RFC3339 start_time/end_time returns 400 + ErrorResponse (OQ-4)"
    verification:
      - kind: unit
        ref: "internal/gateway/handler_test.go#TestGetTelemetry_BadTime_Unit"
        status: pass
      - kind: unit
        ref: "internal/gateway/handler_test.go#TestGetTelemetry_BadEndTime_Unit"
        status: pass
      - kind: integration
        ref: "internal/gateway/integration_test.go#TestGetTelemetry_BadTime"
        status: pass
    human_judgment: false
  - id: D7
    description: "Result capped at VANTAGE_GATEWAY_MAX_ROWS (OQ-1)"
    verification:
      - kind: integration
        ref: "internal/gateway/integration_test.go#TestGetTelemetry_ResultCap"
        status: pass
      - kind: integration
        ref: "pkg/db/read_test.go#TestTelemetry_Limit"
        status: pass
    human_judgment: false

duration: "558s"
completed: "2026-06-29"
tasks_completed: 2
files_changed: 6
---

# Phase 04 Plan 02: Telemetry Endpoint Summary

**`GET /api/v1/gpus/{id}/telemetry` with RFC3339 window filtering, composite index usage proven by EXPLAIN, and all OQ-1..4 edge cases handled.**

## Performance

- **Duration:** 558s (~9 min)
- **Started:** 2026-06-29T18:24:40Z
- **Completed:** 2026-06-29T18:33:58Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- `db.GPUExists` + `db.Telemetry` extend `pkg/db/read.go` — two-query design keeps index use clean for both no-filter and windowed calls; COALESCE handles nullable text columns defensively
- `gateway.GetTelemetry` handler parses RFC3339 time params (400 on failure), gates on GPUExists (404), fetches with limit cap, maps to `GpuMetricResponse` DTOs; swag annotations for Plan 03
- Route registered in `NewRouter`: `r.Get("/{id}/telemetry", GetTelemetry(pool, cfg.MaxRows))`
- 19 new tests: 3 unit (nil-pool, bad-time), 7 integration (gateway), 9 integration (pkg/db), including `TestTelemetry_UsesCompositeIndex` which seeds 100k rows + ANALYZE and asserts `idx_gpu_metrics_gpu_id_ts` appears in the EXPLAIN plan
- Full module `go test -race ./... -count=1` passes across all 11 packages

## TDD Gate Compliance

| Gate | Commit | Status |
|------|--------|--------|
| RED (test) | 9fe7e19 | test(04-02): add failing tests for GET /api/v1/gpus/{id}/telemetry (RED) |
| GREEN (feat) | 8526de4 | feat(04-02): implement GET /api/v1/gpus/{id}/telemetry slice (GREEN) |

Both RED and GREEN gate commits present in git history.

## Task Commits

1. **Task 1: Failing tests (RED)** — `9fe7e19` (test)
2. **Task 2: Implementation to GREEN** — `8526de4` (feat)

## Files Created/Modified

- `pkg/db/read.go` — added `GPUExists` + `Telemetry` (two-query, COALESCE)
- `internal/gateway/handler.go` — added `GetTelemetry` with swag annotations; chi import
- `internal/gateway/server.go` — registered `/{id}/telemetry` route in subrouter
- `internal/gateway/handler_test.go` — 3 unit tests (route, bad start_time, bad end_time)
- `internal/gateway/integration_test.go` — 7 integration tests + helper funcs
- `pkg/db/read_test.go` — 9 integration tests (GPUExists, Telemetry variants, EXPLAIN index proof)

## Decisions Made

- **Two-query approach:** No-filter path uses bare `WHERE gpu_id=$1 ORDER BY timestamp DESC LIMIT $2` (no IS NULL overhead). Bounded path uses `($2::timestamptz IS NULL OR timestamp >= $2)` pattern for partial bounds. Both paths exercise `idx_gpu_metrics_gpu_id_ts`.
- **COALESCE on nullable text columns:** Rather than change `GpuMetric` fields to `*string`, SELECT wraps optional columns in `COALESCE(col, '')`. This is consistent with the existing domain model and the production Collector's empty-string writes.
- **GPUExists before Telemetry:** A single `SELECT EXISTS` call cleanly separates the 404 (unknown GPU) from 200-[] (known GPU, empty window) cases per OQ-2, without over-fetching.
- **Time parse order:** RFC3339 parsing occurs before the nil-pool guard so unit tests with nil pool can trigger the 400 path without hitting pgxpool.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `cannot scan NULL into *string` for optional text columns**
- **Found during:** Task 2 (integration test run, `TestTelemetry_UsesCompositeIndex`)
- **Issue:** `seedRows` in `db_test.go` inserts only `(gpu_id, timestamp, metric_name, value)`, leaving `device`, `model_name`, etc. as `NULL`. Scanning into bare `string` fails with `cannot scan NULL into *string`.
- **Fix:** Added `COALESCE(col, '')` wrapping for all seven optional text columns in the SELECT list in `db.Telemetry`. Domain model unchanged.
- **Files modified:** `pkg/db/read.go`
- **Committed in:** 8526de4 (Task 2 GREEN commit)

**2. [Rule 1 - Bug] Body consumed before `w.Body.String()` check in `TestGetTelemetry_KnownGPUEmptyWindow`**
- **Found during:** Task 2 (integration test run)
- **Issue:** `decodeMetrics(t, w)` advances `w.Body` buffer; subsequent `w.Body.String()` returns empty string, causing `JSONEq("[]", "")` to panic.
- **Fix:** Replaced `decodeMetrics` call with inline `json.Unmarshal([]byte(body), &rows)`, capturing `body := w.Body.String()` first.
- **Files modified:** `internal/gateway/integration_test.go`
- **Committed in:** 8526de4 (Task 2 GREEN commit)

---

**Total deviations:** 2 auto-fixed (both Rule 1 — bugs found during GREEN verification)
**Impact on plan:** Both fixes necessary for correctness. No scope creep.

## Threat Surface Scan

All threats from `<threat_model>` mitigated:
- **T-04-03** (Tampering via `{id}`): `id` bound as `$1` in both `GPUExists` and `Telemetry` — no string concatenation
- **T-04-04** (Tampering via time params): `time.Parse(RFC3339)` extracts a `time.Time` value; only the typed value reaches SQL as `$2`/`$3` — raw string never touches SQL
- **T-04-05** (DoS via unbounded result): `LIMIT $4` = `maxRows` caps every response
- **T-04-06** (Info Disclosure via error strings): errors wrap with `fmt.Errorf("db: Telemetry: ...")` context only — DSN never formatted into any error

## Verification

```
go build ./internal/gateway/... ./pkg/db/...         ✓ BUILD OK
go vet ./internal/gateway/... ./pkg/db/...           ✓ VET OK
go test -race ./internal/gateway/... ./pkg/db/... -count=1   ✓ all unit tests pass
DOCKER_HOST=... TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration
  -run 'TestGetTelemetry|TestTelemetry|TestGPUExists'
  ./internal/gateway/... ./pkg/db/... -count=1       ✓ all 19 integration tests pass
    (incl. TestTelemetry_UsesCompositeIndex: 100k rows + ANALYZE → Index Scan on idx_gpu_metrics_gpu_id_ts)
go test -race ./... -count=1                         ✓ all 11 packages pass
```

## Self-Check: PASSED

Files verified on disk:
- `/Users/ajitg/workspace/vantage/pkg/db/read.go` — FOUND (GPUExists + Telemetry)
- `/Users/ajitg/workspace/vantage/internal/gateway/handler.go` — FOUND (GetTelemetry)
- `/Users/ajitg/workspace/vantage/internal/gateway/server.go` — FOUND (route registered)
- `/Users/ajitg/workspace/vantage/internal/gateway/handler_test.go` — FOUND (3 unit tests)
- `/Users/ajitg/workspace/vantage/internal/gateway/integration_test.go` — FOUND (7 integration tests)
- `/Users/ajitg/workspace/vantage/pkg/db/read_test.go` — FOUND (9 db integration tests)

Commits verified in git log:
- RED: `9fe7e19` — test(04-02)
- GREEN: `8526de4` — feat(04-02)

## Known Stubs

None — the telemetry endpoint is fully wired to the database. The swag annotations are present but `pkg/docs` is not yet generated (Plan 03 runs `make swagger`). This is the same documented stub as Plan 01.

## Next Phase Readiness

- Plan 03 can now generate OpenAPI docs via `swag init` — both `ListGPUs` and `GetTelemetry` have complete `@Param`/`@Success`/`@Failure`/`@Router` annotation blocks
- `cmd/gateway/main.go` thin composition root (Plan 03) will wire `db.New`, `gateway.FromEnv`, `gateway.NewRouter`, and the `_ "github.com/ajitg/vantage/pkg/docs"` side-effect import
- Phase 05 Helm values can reference `VANTAGE_GATEWAY_MAX_ROWS` to tune the row cap per environment
