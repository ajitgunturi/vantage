---
phase: 04-api-gateway-openapi-docs
plan: "01"
subsystem: api-gateway
status: complete
tags: [go, chi, pgx, swagger, tdd, api-gateway]
dependency_graph:
  requires: [pkg/db, pkg/models, internal/collector (testcontainers pattern)]
  provides: [internal/gateway (Config, NewRouter, ListGPUs), pkg/db.DistinctGPUIDs, chi/v5, http-swagger/v2]
  affects: [go.mod, go.sum, Makefile (future coverage gate update in plan 03)]
tech_stack:
  added: [github.com/go-chi/chi/v5@v5.3.0, github.com/swaggo/http-swagger/v2@v2.0.2]
  patterns: [TDD RED→GREEN, testcontainers Snapshot/Restore, chi subrouter, pgxpool.Query DISTINCT, nil-pool guard]
key_files:
  created:
    - internal/gateway/config.go
    - internal/gateway/handler.go
    - internal/gateway/server.go
    - pkg/db/read.go
    - internal/gateway/handler_test.go
    - internal/gateway/integration_test.go
    - pkg/db/read_test.go
  modified:
    - go.mod
    - go.sum
decisions:
  - "nil pool guard in ListGPUs: panics in pgxpool propagate through chi Recoverer as text/plain 500; a nil-pool check returns application/json 500 maintaining the API content-type contract (Rule 2 auto-fix)"
  - "chi route structure: r.Route('/api/v1/gpus') + r.Get('/') so Plan 02 can add /{id}/telemetry in the same subrouter without touching server.go structure"
  - "DistinctGPUIDs returns make([]string, 0) not nil: empty table encodes as [] not null per API-01"
metrics:
  duration: "356s"
  completed: "2026-06-29"
  tasks_completed: 2
  files_changed: 9
requirements_delivered: [API-01]
---

# Phase 04 Plan 01: Gateway Skeleton + GET /api/v1/gpus Summary

Delivered a working `GET /api/v1/gpus` endpoint returning sorted distinct GPU UUID strings from PostgreSQL, served by a chi v5 router over the shared `pkg/db` pgxpool, proven by testcontainers integration tests (TDD GREEN).

## What Was Built

- **`pkg/db/read.go`** — `DistinctGPUIDs(ctx, pool)` queries `SELECT DISTINCT gpu_id FROM gpu_metrics ORDER BY gpu_id`; returns non-nil empty slice on empty table; never embeds DSN in errors (ASVS V8)
- **`internal/gateway/config.go`** — `Config{Addr string, MaxRows int}` + `FromEnv()`; GATEWAY_ADDR defaults to `:8080`; VANTAGE_GATEWAY_MAX_ROWS defaults to 1000 (OQ-1 safety ceiling, non-pagination)
- **`internal/gateway/handler.go`** — `ListGPUs(pool)` http.HandlerFunc with swag annotations for Plan 03; `GpuMetricResponse` + `ErrorResponse` DTOs; `writeJSON`/`writeError` helpers
- **`internal/gateway/server.go`** — `NewRouter(pool, cfg)` builds chi router with Logger+Recoverer middleware, mounts `/api/v1/gpus` subrouter + `/swagger/*` stub
- **`internal/gateway/handler_test.go`** — unit tests (no build tag): route registration not-404, Content-Type application/json
- **`internal/gateway/integration_test.go`** — integration tests: TestListGPUs_TwoGPUs (seeds 3 rows/2 UUIDs, asserts sorted), TestListGPUs_Empty (asserts `[]`)
- **`pkg/db/read_test.go`** — integration tests for DistinctGPUIDs under `//go:build integration`
- **`go.mod`/`go.sum`** — added chi/v5 v5.3.0 and http-swagger/v2 v2.0.2

## TDD Gate Compliance

| Gate | Commit | Status |
|------|--------|--------|
| RED (test) | 05ad3df | test(04-01): add failing tests for GET /api/v1/gpus (RED) |
| GREEN (feat) | 836cd4b | feat(04-01): implement GET /api/v1/gpus slice — chi router + pgx read (GREEN) |

Both RED and GREEN gate commits are present in git history. Plan executed exactly as required by TDD protocol.

## Verification

```
go build ./internal/gateway/...       ✓ BUILD OK
go test -race ./internal/gateway/... ./pkg/db/... -count=1   ✓ all pass
DOCKER_HOST=... TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration
  -run 'TestListGPUs|TestDistinctGPUIDs' ./internal/gateway/... ./pkg/db/...   ✓ all pass
go test -race ./... -count=1          ✓ all 11 packages pass
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing null check] Nil pool causes panic that bypasses application/json Content-Type**
- **Found during:** Task 2 (GREEN phase unit test execution)
- **Issue:** With nil pool, `pgxpool.(*Pool).Query` panics. Chi's `middleware.Recoverer` intercepts the panic and calls `http.Error()` which sets `Content-Type: text/plain`, overwriting the JSON content-type contract. The unit test `TestNewRouter_RouteRegistration` (which uses nil pool to test route registration) was failing because the Content-Type header was not `application/json`.
- **Fix:** Added nil pool guard at the top of `ListGPUs`: returns 500 `ErrorResponse{Error: "database not available"}` with `Content-Type: application/json` before any pgxpool call. This also prevents panics in production if the pool is nil due to DI misconfiguration.
- **Files modified:** `internal/gateway/handler.go`
- **Commit:** 836cd4b

## Known Stubs

- `/swagger/*` route is registered but the `pkg/docs` generated package does not exist yet. The http-swagger handler serves a blank/minimal spec. The side-effect import `_ "github.com/ajitg/vantage/pkg/docs"` is NOT in `server.go` (correctly deferred to `cmd/gateway/main.go` in Plan 03). This stub is intentional — Plan 03 generates `pkg/docs` via `make swagger` and wires the import.

## Threat Surface Scan

No new security surface beyond what the plan's threat model covers:
- `db.DistinctGPUIDs` error path: context-only wrapping, no DSN leakage (T-04-01 mitigated)
- chi router: no user input reaches SQL in the GPU list endpoint (parameterless DISTINCT query)

## Self-Check: PASSED

All 7 created files verified on disk. Both commits (05ad3df RED, 836cd4b GREEN) found in git log.
