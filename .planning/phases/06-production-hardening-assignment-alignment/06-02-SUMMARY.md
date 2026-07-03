---
phase: 06-production-hardening-assignment-alignment
plan: "02"
subsystem: gateway
tags: [gateway, pagination, health, api, swagger, API-05, OBS-01]
status: complete

dependency_graph:
  requires: []
  provides: [gateway.TelemetryPage, gateway.PaginationMeta, gateway.HealthzHandler, gateway.ReadyzHandler, db.Telemetry-offset]
  affects: [internal/gateway/handler.go, internal/gateway/server.go, pkg/db/read.go, pkg/docs]

tech_stack:
  added: []
  patterns: [limit+1-has_next-detection, pgx-bound-OFFSET-placeholder, handler-factory-health, swag-annotation-pagination]

key_files:
  created: []
  modified:
    - internal/gateway/handler.go
    - internal/gateway/server.go
    - internal/gateway/handler_test.go
    - internal/gateway/integration_test.go
    - pkg/db/read.go
    - pkg/docs/docs.go
    - pkg/docs/swagger.json
    - pkg/docs/swagger.yaml

decisions:
  - limit+1 sentinel row detection for has_next avoids a COUNT query (RESEARCH Pitfall 5 off-by-one)
  - limit and offset are parsed before nil-pool guard so unit tests with nil pool exercise 400 paths (T-06-03/T-06-04)
  - OFFSET bound as pgx $N placeholder in both simple ($3) and windowed ($5) SQL paths — never string-concatenated (T-06-03/ASVS V5)
  - ReadyzHandler returns generic "not ready" string only — no DSN, pg driver text, or version (T-06-05/ASVS V8)
  - Health routes registered at router root before api/v1 group; no swag annotations on health handlers (Pitfall 1 — excluded from documented API)
  - X-Truncated/X-Row-Limit response headers removed entirely — superseded by TelemetryPage.Pagination metadata

metrics:
  duration: "412s"
  completed: "2026-07-03T04:28:56Z"
  tasks_completed: 3
  files_changed: 8
  files_created: 0
---

# Phase 06 Plan 02: Gateway Pagination + Health Summary

**One-liner:** Added offset/limit pagination with a `TelemetryPage`/`PaginationMeta` envelope pushed into both SQL paths, plus `/healthz` and `/readyz` handlers excluded from the regenerated OpenAPI spec.

## What Was Built

- `TelemetryPage{Data []GpuMetricResponse, Pagination PaginationMeta}` and `PaginationMeta{Limit, Offset int; HasNext bool}` exported from the gateway package
- `GET /api/v1/gpus/{id}/telemetry?limit=N&offset=M` — `N` validated (≥1, ceiling `VANTAGE_GATEWAY_MAX_ROWS`), `M` validated (≥0), both return 400 on invalid input; `limit+1` sentinel used for has_next without COUNT query
- `db.Telemetry(ctx, pool, id, start, end, limit, offset int)` — `OFFSET $3` added to simple path, `OFFSET $5` to windowed path, both as pgx-bound placeholders (no string concatenation)
- `HealthzHandler()` — always returns 200 JSON `{"status":"ok"}` (liveness: process alive)
- `ReadyzHandler(pool)` — 2s timeout pool ping; nil pool or ping failure → 503 `{"error":"not ready"}` (no DSN leak); pool reachable → 200 `{"status":"ok"}`
- Routes `GET /healthz` and `GET /readyz` registered before `/api/v1` group in `NewRouter`
- Regenerated `pkg/docs` — `TelemetryPage` definition present, `limit`/`offset` params documented, `X-Truncated`/`X-Row-Limit` headers removed, health paths absent

## Tasks

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Failing tests for pagination envelope + gateway health (RED) | b66e222 | internal/gateway/handler_test.go, internal/gateway/integration_test.go |
| 2 | SQL OFFSET push-down + handler pagination envelope (GREEN) | c7309a6 | pkg/db/read.go, internal/gateway/handler.go, internal/gateway/integration_test.go |
| 3 | Gateway health handlers + swagger regeneration | 41e45cb | internal/gateway/handler.go, internal/gateway/server.go, pkg/docs/{docs.go,swagger.json,swagger.yaml} |

## TDD Gate Compliance

- RED (Task 1): new tests referencing `gateway.TelemetryPage` (undefined), and health/pagination 400 tests; `go test ./internal/gateway/...` produced FAIL — gate confirmed
- GREEN (Task 2): `TelemetryPage`, `PaginationMeta` added; `db.Telemetry` offset param added; pagination unit tests now pass
- REFACTOR: not needed (changes were scope-bounded)

## Verification

- `go test -race ./internal/gateway/... ./pkg/db/...` — PASS
- `make build && make test && make lint` — all PASS
- `grep -c 'healthz\|readyz' pkg/docs/swagger.json` == 0 (health excluded)
- `grep -c '"limit"' pkg/docs/swagger.json` == 2 (param + definition); offset similarly documented
- `make swagger && git diff --quiet pkg/docs` → SWAGGER-CLEAN (idempotent, drift-free)
- X-Truncated/X-Row-Limit headers absent from both spec and handler code

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated TestGetTelemetry_KnownGPUEmptyWindow to decode TelemetryPage**
- **Found during:** Task 2 implementation
- **Issue:** Integration test decoded the response body directly into `[]GpuMetricResponse` and asserted `JSONEq("[]", body)` — both would fail once handler returns `TelemetryPage` envelope instead of bare array
- **Fix:** Updated test to call `decodePage()` and assert on `page.Data` length and `page.Pagination.HasNext`
- **Files modified:** internal/gateway/integration_test.go
- **Commit:** c7309a6

## Threat Surface Scan

All three STRIDE threats from the plan's threat model were mitigated:

| Threat | Disposition | Verification |
|--------|-------------|--------------|
| T-06-03 (Tampering: limit/offset SQL injection) | mitigated | `grep -n 'OFFSET' pkg/db/read.go` shows $3 / $5 pgx placeholders only; no string concat |
| T-06-04 (DoS: large limit) | mitigated | Handler enforces ceiling at cfg.MaxRows; rejects limit<1 with 400 |
| T-06-05 (Info disclosure: readyz response) | mitigated | ReadyzHandler returns only `{"error":"not ready"}` — no DSN, driver, or version text |

No new network endpoints or auth paths beyond those specified in the plan.

## Self-Check: PASSED

- internal/gateway/handler.go: FOUND
- internal/gateway/server.go: FOUND
- pkg/db/read.go: FOUND
- pkg/docs/swagger.json: FOUND
- Commits b66e222, c7309a6, 41e45cb: FOUND in git log
