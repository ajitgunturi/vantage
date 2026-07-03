---
phase: 06-production-hardening-assignment-alignment
plan: "01"
subsystem: logging
tags: [logging, slog, observability, OBS-02]
status: complete

dependency_graph:
  requires: []
  provides: [pkg/logger.New, slog-default-in-all-services]
  affects: [cmd/mq, cmd/gateway, cmd/collector, cmd/streamer, cmd/migrate, internal/streamer, internal/collector]

tech_stack:
  added: [log/slog (stdlib JSON handler via pkg/logger)]
  patterns: [shared-logger-helper, slog.SetDefault-in-main, Fatal→slog.Error+os.Exit(1)]

key_files:
  created:
    - pkg/logger/logger.go
    - pkg/logger/logger_test.go
  modified:
    - cmd/mq/main.go
    - cmd/gateway/main.go
    - cmd/collector/main.go
    - cmd/streamer/main.go
    - cmd/migrate/main.go
    - internal/streamer/streamer.go
    - internal/collector/collector.go

decisions:
  - pkglogger import alias avoids shadowing the slog package in cmd/*/main.go scope
  - log/slog stdlib only — no new dependency added (Go 1.26 project)
  - Fatal semantics preserved via slog.Error + os.Exit(1) on every former log.Fatal/log.Fatalf site
  - DSN never passed to slog (ASVS V8) — only error context strings, never the DSN value

metrics:
  duration: "480s"
  completed: "2026-07-03T04:17:44Z"
  tasks_completed: 3
  files_changed: 7
  files_created: 2
---

# Phase 06 Plan 01: Structured Logging (slog) Summary

**One-liner:** Replaced stdlib `log` with `log/slog` JSON handler across all 7 non-test production files via a shared `pkg/logger.New(service)` helper that attaches a `service` attribute and honors `LOG_LEVEL`.

## What Was Built

All four microservices and the migrate binary now emit structured JSON log lines to stderr with a `service` field identifying the emitter. Log verbosity is controlled by `LOG_LEVEL` (DEBUG/INFO/WARN/ERROR; default INFO). Every former `log.Fatal*` site terminates the process via `slog.Error` + `os.Exit(1)`.

## Tasks

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Create pkg/logger helper (TDD) | f0e024f | pkg/logger/logger.go, pkg/logger/logger_test.go |
| 2 | Migrate cmd/*/main.go to slog | a2c4c06 | cmd/{mq,gateway,collector,streamer,migrate}/main.go |
| 3 | Migrate internal packages to slog | a122e77 | internal/streamer/streamer.go, internal/collector/collector.go |

## TDD Gate Compliance

Task 1 followed full RED/GREEN cycle:
- RED: `logger_test.go` written first; build failed with "no non-test Go files"
- GREEN: `logger.go` implemented; `go test -race -count=1 ./pkg/logger/...` passed at 100% coverage
- REFACTOR: not needed (function is minimal)

## Verification

- `make build` — all four binaries compile cleanly
- `go test -race ./pkg/logger/... ./internal/streamer/... ./internal/collector/...` — all pass
- `grep -l '"log"$' cmd/*/main.go` — returns no matches (stdlib log removed from all five mains)
- DSN-leak audit: `grep -Rn 'DSN' cmd/ internal/ | grep -i 'slog\|log\.'` — returns nothing
- Coverage for pkg/logger: 100% (TestNew_ServiceAttr, TestNew_LevelFromEnv, TestNew_SetDefault)

## Deviations from Plan

None — plan executed exactly as written.

## Threat Surface Scan

No new network endpoints, auth paths, or schema changes introduced. Log output is the only new surface:
- T-06-01 mitigated: DSN never passed to any slog call confirmed by grep audit
- T-06-02 accepted: LOG_LEVEL unknown values default to INFO (no injection surface)

## Self-Check: PASSED

- pkg/logger/logger.go: FOUND
- pkg/logger/logger_test.go: FOUND
- Commits f0e024f, a2c4c06, a122e77: FOUND in git log
