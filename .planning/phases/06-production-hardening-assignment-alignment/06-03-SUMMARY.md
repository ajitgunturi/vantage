---
phase: 06-production-hardening-assignment-alignment
plan: "03"
subsystem: mq-health
tags: [mq, health, observability, kubernetes]
status: complete

dependency_graph:
  requires: ["06-01"]
  provides: ["mq-healthz", "mq-readyz", "MQServer.IsShuttingDown"]
  affects: ["cmd/mq"]

tech_stack:
  added: []
  patterns:
    - "handler-factory (func returning http.HandlerFunc, matching inspect.go)"
    - "non-blocking channel select for lock-free state read"
    - "nolint:errcheck on response Write (consistent with inspect.go)"

key_files:
  created:
    - internal/http/health.go
    - internal/http/health_test.go
  modified:
    - internal/server/server.go
    - cmd/mq/main.go

decisions:
  - "IsShuttingDown uses non-blocking channel select — no mutex, no allocation; O(1) on hot HTTP path"
  - "Response bodies contain only {status:...} string — no queue depth, version, DSN (T-06-06)"
  - "healthz/readyz share the existing httpSrv on :8080 alongside /api/v1/queue/inspect — no new port"

metrics:
  duration_seconds: 114
  completed_date: "2026-07-03"
  tasks_completed: 2
  tasks_total: 2
  files_created: 2
  files_modified: 2
---

# Phase 06 Plan 03: MQ Health Endpoints Summary

MQ control-plane now exposes `/healthz` (liveness) and `/readyz` (readiness) on `:8080` alongside `/api/v1/queue/inspect`; readiness flips to 503 atomically when `Shutdown()` is called, closing the Kubernetes invisible-broker gap (OBS-01 / finding F-02).

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (RED) | Failing tests for health handlers + IsShuttingDown | 5bd5fe3 | internal/http/health_test.go |
| 1 (GREEN) | MQServer.IsShuttingDown + mqhttp health handlers | 62004a5 | internal/server/server.go, internal/http/health.go |
| 2 | Register healthz/readyz on MQ control-plane mux | 92fc999 | cmd/mq/main.go |

## Verification Results

- `go test -race -count=1 ./internal/http/... ./internal/server/...` — PASS
- `make build` — PASS (all four service binaries)
- `make test` — PASS
- `make lint` — 0 issues
- `grep -Ei 'version|dsn|config' internal/http/health.go` — no leaky fields (T-06-06 mitigated)
- `grep -c 'HandleFunc("GET /healthz"\|HandleFunc("GET /readyz"' cmd/mq/main.go` — 2 routes present
- Coverage for `internal/http`: 100%

## Deviations from Plan

None — plan executed exactly as written. TDD gate followed: RED commit → GREEN commit.

## Known Stubs

None.

## Threat Flags

No new threat surface introduced beyond the planned T-06-06 / T-06-07 mitigations already in the threat register. Response bodies contain only `{"status":"ok"}` or `{"status":"shutting_down"}` — no broker internals exposed.

## Self-Check: PASSED

- internal/http/health.go — FOUND
- internal/http/health_test.go — FOUND
- internal/server/server.go — FOUND
- Commit 5bd5fe3 (RED) — FOUND
- Commit 62004a5 (GREEN) — FOUND
- Commit 92fc999 (Task 2) — FOUND
