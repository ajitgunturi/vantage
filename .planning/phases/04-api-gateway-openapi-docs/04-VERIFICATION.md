---
phase: 04-api-gateway-openapi-docs
verified: 2026-06-30T22:20:00Z
status: passed
score: 3/3 must-haves verified
behavior_unverified: 0
overrides_applied: 0
human_items_resolved: 2026-06-30T22:25:00Z
human_items_resolution: |
  All three human-verification items were resolved by the orchestrator after the static-analysis pass:
  1. Composite-index EXPLAIN proof — PASSED. Ran `DOCKER_HOST=unix://$HOME/.rd/docker.sock
     TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration
     -run TestTelemetry_UsesCompositeIndex ./pkg/db/... -count=1 -v` against a live testcontainers
     Postgres (100k seeded rows). Test PASS (36.07s); planner uses idx_gpu_metrics_gpu_id_ts (Index Scan).
  2. End-to-end smoke — PASSED. `make dev-up && make build && make smoke-04` ran all four curl
     assertions green (GET /gpus 200+array, 257 distinct GPUs; GET /gpus/{id}/telemetry 200+array;
     unknown GPU 404; /swagger/doc.json 200 + valid OpenAPI, >=2 paths).
  3. swag v1.16.4 vs CLAUDE.md-pinned v1.16.6 — RESOLVED by developer decision: accept v1.16.4.
     Aligned .claude/CLAUDE.md pin references to v1.16.4 and pinned `make tools` to swag@v1.16.4.
behavior_unverified_items:
  - truth: "GET /api/v1/gpus/{id}/telemetry filters by time window using the composite index idx_gpu_metrics_gpu_id_ts"
    test: "Run `DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration -run TestTelemetry_UsesCompositeIndex ./pkg/db/... -count=1 -v`"
    expected: "EXPLAIN plan text contains both idx_gpu_metrics_gpu_id_ts and Index Scan; exit code 0"
    why_human: "TestTelemetry_UsesCompositeIndex seeds 100k rows and runs EXPLAIN ANALYZE against a testcontainers Postgres instance. Cannot run without a Docker daemon — grep/presence checks confirm the ORDER BY timestamp DESC LIMIT pattern is correct but cannot observe the actual planner output."
human_verification:
  - test: "Confirm composite index is used by EXPLAIN"
    expected: "go test -race -tags=integration -run TestTelemetry_UsesCompositeIndex ./pkg/db/... exits 0 and prints idx_gpu_metrics_gpu_id_ts with Index Scan"
    why_human: "Requires Docker / testcontainers. Cannot run without a live daemon."
  - test: "Run make smoke-04 against dev stack"
    expected: "All four curl assertions pass: GET /api/v1/gpus 200+array; GET /api/v1/gpus/<id>/telemetry 200+array; GET /api/v1/gpus/GPU-does-not-exist/telemetry 404; GET /swagger/doc.json 200+valid JSON >=2 paths"
    why_human: "Smoke script requires a running Postgres (make dev-up) and a compiled gateway binary. End-to-end smoke cannot run in a static analysis pass."
  - test: "Assess swag v1.16.4 vs CLAUDE.md-pinned v1.16.6 deviation"
    expected: "Developer confirms v1.16.4 is acceptable, OR upgrades go.mod and make tools target to v1.16.6"
    why_human: "CLAUDE.md explicitly pins github.com/swaggo/swag@v1.16.6. v1.16.6 exists and is available (confirmed). The executor reported v1.16.4 as 'closest available' — this is inaccurate. The spec is still auto-generated and tests pass, so the functional goal is met, but the pin violation requires a developer call on whether to accept it or correct it."
---

# Phase 04: API Gateway + OpenAPI Docs Verification Report

**Phase Goal:** Clients can query stored GPU telemetry over a documented REST API backed directly by PostgreSQL.
**Verified:** 2026-06-30T22:20:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | GET /api/v1/gpus returns the unique list of GPU IDs from PostgreSQL (API-01) | ✓ VERIFIED | `DistinctGPUIDs` issues `SELECT DISTINCT gpu_id FROM gpu_metrics ORDER BY gpu_id`; `ListGPUs` handler calls it and JSON-encodes the result; route registered in `NewRouter`; integration tests cover seeded + empty cases |
| 2 | GET /api/v1/gpus/{id}/telemetry returns telemetry ordered by time, and ?start_time/end_time filters by window using composite index (API-02, API-03) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | Endpoint, routing, RFC3339 parsing, 404/400/200 paths, result cap, ORDER BY timestamp DESC all VERIFIED by code inspection and unit tests. The specific "composite index is used" invariant requires EXPLAIN ANALYZE output — `TestTelemetry_UsesCompositeIndex` is the behavioral test but requires Docker/testcontainers to run |
| 3 | swag init regenerates a valid OpenAPI spec entirely from code annotations and serves it via Swagger UI (API-04) | ✓ VERIFIED | `pkg/docs/swagger.json` is generated ("DO NOT EDIT"); 2 paths documented (`/gpus`, `/gpus/{id}/telemetry`); `cmd/gateway/main.go` carries `_ pkg/docs` side-effect import; `TestSwaggerUI` PASSES: `/swagger/doc.json` → 200 + valid JSON + 2 paths; `/swagger/index.html` → 200 |

**Score:** 2/3 truths verified (behavior_unverified: 1)

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/gateway/config.go` | Config struct + FromEnv() | ✓ VERIFIED | Config{Addr, MaxRows}; FromEnv reads GATEWAY_ADDR (default ":8080") and VANTAGE_GATEWAY_MAX_ROWS (default 1000); rejects non-positive values |
| `internal/gateway/handler.go` | ListGPUs + GetTelemetry handlers with swag annotations | ✓ VERIFIED | Both handlers implemented with full annotation blocks; GpuMetricResponse + ErrorResponse DTOs; writeJSON/writeError helpers |
| `internal/gateway/server.go` | NewRouter with all three routes | ✓ VERIFIED | chi.NewRouter with Logger+Recoverer; `/api/v1/gpus` subrouter with `GET /` and `GET /{id}/telemetry`; `/swagger/*` handler |
| `pkg/db/read.go` | DistinctGPUIDs + GPUExists + Telemetry | ✓ VERIFIED | Three functions with real parameterized queries; two-query approach for Telemetry; non-nil empty slices; no DSN in errors |
| `cmd/gateway/main.go` | Composition root with errgroup lifecycle | ✓ VERIFIED | Signal context, db.Migrate, db.New, gateway.NewRouter, errgroup with ListenAndServe+Shutdown; DSN never logged; `_ pkg/docs` import |
| `pkg/docs/swagger.json` | Auto-generated OpenAPI spec with >=2 paths | ✓ VERIFIED | 2 paths: `/gpus` (GET) and `/gpus/{id}/telemetry` (GET with path/query params and 400/404/500 responses); generated by swag |
| `internal/gateway/integration_test.go` | Testcontainers integration tests | ✓ VERIFIED | 7 integration tests (ListGPUs_TwoGPUs, ListGPUs_Empty, GetTelemetry_NoFilter, GetTelemetry_TimeWindow, GetTelemetry_PartialBounds, GetTelemetry_UnknownGPU, GetTelemetry_KnownGPUEmptyWindow, GetTelemetry_BadTime, GetTelemetry_ResultCap) |
| `scripts/smoke/phase04-gateway.sh` | Executable curl smoke check | ✓ VERIFIED | Executable; bash -n syntax OK; 4 curl assertions present; pg_exec pattern; cleanup on EXIT |
| `README.md` | Gateway quickstart section | ✓ VERIFIED | Contains `/api/v1/gpus` and swagger references; curl examples; Swagger UI URL; make smoke-04 description |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/gateway/handler.go:ListGPUs` | `pkg/db:DistinctGPUIDs` | `db.DistinctGPUIDs(r.Context(), pool)` call | ✓ WIRED | Direct function call; pool flows from NewRouter closure |
| `internal/gateway/handler.go:GetTelemetry` | `pkg/db:GPUExists` + `pkg/db:Telemetry` | Sequential calls with id/$start/$end/maxRows | ✓ WIRED | 404 gate then fetch; all params are pgx-bound, not string-concatenated |
| `internal/gateway/server.go:NewRouter` | `handler.ListGPUs` + `handler.GetTelemetry` | `r.Get("/", ...)` + `r.Get("/{id}/telemetry", ...)` inside `r.Route("/api/v1/gpus", ...)` | ✓ WIRED | Both routes registered in chi subrouter; cfg.MaxRows passed to GetTelemetry |
| `cmd/gateway/main.go` | `pkg/docs` (generated spec) | `_ "github.com/ajitg/vantage/pkg/docs"` side-effect import | ✓ WIRED | Blank import registers spec on init(); test imports same pattern |
| `Makefile:coverage` | exclusion of `pkg/docs` | `grep -v '/pkg/pb\|/pkg/docs'` | ✓ WIRED | Both generated packages excluded from the >=90% gate |

---

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `handler.ListGPUs` | `ids []string` | `db.DistinctGPUIDs` → `SELECT DISTINCT gpu_id FROM gpu_metrics ORDER BY gpu_id` | Yes — live pgxpool query | ✓ FLOWING |
| `handler.GetTelemetry` | `metrics []models.GpuMetric` | `db.Telemetry` → `SELECT ... FROM gpu_metrics WHERE gpu_id=$1 ORDER BY timestamp DESC LIMIT $N` | Yes — parameterized query with COALESCE on nullable columns | ✓ FLOWING |

---

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| OpenAPI spec served at /swagger/doc.json with >=2 paths | `go test -race -run TestSwaggerUI ./internal/gateway/... -count=1` | PASS: spec_json_served ✓ (2 paths), swagger_ui_index ✓ (200) | ✓ PASS |
| Module builds cleanly | `go build ./...` | Exit 0, no output | ✓ PASS |
| Unit tests pass | `go test -race ./internal/gateway/... ./pkg/db/... -count=1` | PASS: gateway 2.466s, db 1.769s | ✓ PASS |
| Coverage gate >=90% | `make coverage` | 90.4% >= 90% (pkg/pb + pkg/docs excluded) | ✓ PASS |
| Composite index used for telemetry window queries | `go test -race -tags=integration -run TestTelemetry_UsesCompositeIndex ./pkg/db/... -count=1` | SKIP — requires Docker/testcontainers | ? SKIP |
| Smoke script syntax | `bash -n scripts/smoke/phase04-gateway.sh` | Syntax OK | ✓ PASS |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| API-01 | 04-01-PLAN.md | GET /api/v1/gpus returns unique list of GPU IDs | ✓ SATISFIED | DistinctGPUIDs query + ListGPUs handler + route + integration tests |
| API-02 | 04-02-PLAN.md | GET /api/v1/gpus/{id}/telemetry returns ordered telemetry | ✓ SATISFIED | Telemetry() ORDER BY timestamp DESC + GetTelemetry handler + tests |
| API-03 | 04-02-PLAN.md | Time-window filtering using composite index | ✓ SATISFIED (index behavior unverified) | Two-query approach with nullable-bound predicate + TestTelemetry_UsesCompositeIndex exists but requires Docker |
| API-04 | 04-03-PLAN.md | OpenAPI spec auto-generated from swag annotations | ✓ SATISFIED | pkg/docs generated files; make swagger target; TestSwaggerUI PASSES |

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `go.mod` | indirect dep | `github.com/swaggo/swag v1.16.4` vs CLAUDE.md pin `v1.16.6` | ⚠️ WARNING | v1.16.6 exists and is available. Executor reported "closest available" which is inaccurate. The auto-generation works correctly and v1.16.4 is stable, but this violates the explicit project constraint. Requires developer decision. |
| `Makefile` | line 23 | `make tools` installs `swag@latest` instead of `@v1.16.6` | ⚠️ WARNING | Non-reproducible tool install; CLAUDE.md convention is to pin dev tools. Not a phase-4-specific file, but modified by this phase (coverage target change) and the deviation is now documented. |

No `TBD`, `FIXME`, or `XXX` markers found in any phase-04 modified files. No stub implementations, no empty returns where real data is expected, no hardcoded fixtures masquerading as live data.

---

### Human Verification Required

#### 1. Composite Index Usage by EXPLAIN (API-03 behavior-dependent invariant)

**Test:** Run `DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration -run TestTelemetry_UsesCompositeIndex ./pkg/db/... -count=1 -v`

**Expected:** Test exits 0; output contains "idx_gpu_metrics_gpu_id_ts" and "Index Scan" in the EXPLAIN plan. The test seeds 100k rows, calls ANALYZE, then runs `EXPLAIN (ANALYZE, FORMAT TEXT)` on the windowed query and asserts the plan string contains the index name.

**Why human:** The behavioral invariant (planner actually selects idx_gpu_metrics_gpu_id_ts over a seq scan) can only be observed from live EXPLAIN output against a real Postgres instance. Code structure (ORDER BY timestamp DESC LIMIT on composite index leading column) is correct, and the test mechanism is well-formed, but the actual planner decision requires Docker to observe.

#### 2. End-to-End Smoke Check (make smoke-04)

**Test:** `make dev-up && make build && make smoke-04`

**Expected:** All four curl assertions succeed: `GET /api/v1/gpus` → 200 + JSON array; `GET /api/v1/gpus/<id>/telemetry` → 200 + JSON array; `GET /api/v1/gpus/GPU-does-not-exist/telemetry` → 404; `GET /swagger/doc.json` → 200 + valid JSON with >=2 paths.

**Why human:** Requires a running Postgres dev stack, a compiled gateway binary, and network connectivity. Static analysis confirms the smoke script is syntactically correct and the assertions are logically valid.

#### 3. swag Version Deviation (WARNING — developer decision required)

**Test:** Review whether `github.com/swaggo/swag v1.16.4` in `go.mod` is acceptable or should be corrected to `v1.16.6`.

**Expected:** Developer either (a) accepts v1.16.4 as equivalent and documents the deviation, or (b) runs `go get github.com/swaggo/swag@v1.16.6` and updates `make tools` to install `@v1.16.6` instead of `@latest`.

**Why human:** CLAUDE.md explicitly pins v1.16.6. v1.16.6 exists and was available during execution (confirmed). The executor's claim that v1.16.4 was "closest available" is inaccurate. Functionally the spec auto-generation works identically, but the constraint violation needs an explicit accept-or-correct decision. This is a WARNING, not a BLOCKER — the phase goal is achieved regardless.

---

## Gaps Summary

No gaps. All three observable truths are either VERIFIED or PRESENT_BEHAVIOR_UNVERIFIED (code present and wired, behavioral test exists, requires Docker to run). The swag version deviation is a WARNING that does not block the phase goal.

---

_Verified: 2026-06-30T22:20:00Z_
_Verifier: Claude (gsd-verifier)_
