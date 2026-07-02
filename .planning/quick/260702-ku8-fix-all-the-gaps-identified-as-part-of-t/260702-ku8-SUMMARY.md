---
phase: quick
plan: 260702-ku8
subsystem: all
tags: [correctness, liveness, gateway, docs, tdd]
status: complete
decisions:
  - "C-1: pgx v5 SendBatch runs whole batch in one implicit transaction; drain all Exec calls capturing firstErr; do not ack on persist failure"
  - "G-1: grpc.NewClient is lazy — reset backoff only after Consume survives >5s"
  - "M-3: shutdownCh cases added to BOTH blocking selects; test simulates GracefulStop stream-cancel as second step"
  - "M-5: nanosecond collision under concurrent Streamers accepted by design (ADR-002)"
  - "G-2: lint errors now surface (no more 2>/dev/null swallow); 5 pre-existing nolint:errcheck added"
metrics:
  duration: ~90min
  completed: "2026-07-02"
  tasks_completed: 3
  files_changed: 22
key-files:
  created:
    - docs/adr/ADR-002-natural-key-microsecond-collision.md
    - docs/AI_USAGE.md
  modified:
    - internal/collector/collector.go
    - internal/collector/collector_test.go
    - internal/streamer/streamer.go
    - internal/streamer/streamer_test.go
    - internal/server/server.go
    - internal/server/server_test.go
    - internal/gateway/handler.go
    - internal/gateway/integration_test.go
    - pkg/db/db.go
    - pkg/db/read.go
    - pkg/docs/docs.go
    - pkg/docs/swagger.json
    - pkg/docs/swagger.yaml
    - cmd/mq/main.go
    - test/e2e/pipeline_test.go
    - Makefile
    - README.md
    - internal/collector/run_test.go
    - scripts/smoke/mqprobe/main.go
---

# Phase quick Plan 260702-ku8: Mid-Assignment Review Fixes Summary

Closes every CRITICAL/MAJOR/MEDIUM finding and cheap MINOR items from the 4-agent mid-assignment review of phases 1–4.

## One-liner

Fixes C-1 silent data loss on DB fail, M-2 missed-wakeup race, M-3 shutdown hang, M-4 silent truncation header, M-5 collision ADR, M-6 streamer retry, G-1 backoff never escalates, G-4 AI usage docs, plus all reviewed MINORs.

## Tasks Completed

| Task | Description | Commit |
|---|---|---|
| 1 | Pipeline correctness: C-1, M-6, G-1, M-1, MINORs | cd41b2b |
| 2 | MQ liveness: M-2, M-3, MQ MINORs | da60983 |
| 3 | Gateway/docs/build: M-4, M-5, G-4, MINORs | 33783bb |
| - | Lint nolint fixes (G-2 un-masked pre-existing errcheck) | a8b8faa |

## Findings Closed

### CRITICAL
- **C-1 (collector):** `persistBatch` always returned `nil` even when `br.Exec()` failed. Fixed: drain all Exec calls capturing `firstErr`; return error; `flush` does not ack on failure (messages requeued at-least-once). Tests: `TestPersistBatchError_NoAcks`.

### MAJOR
- **M-1 (e2e):** e2e asserted `count > 0`; changed to `count == 200` exactly.
- **M-2 (server):** `notifyChan()` captured AFTER `TryDequeue()` — missed-wakeup window. Fixed: capture `ch := s.notifyChan()` BEFORE `TryDequeue()`. Tests: `TestMQ_MissedWakeup_SingleConsumer`.
- **M-3 (server/cmd/mq):** `shutdownCh` was write-only dead code in send-loop selects. Fixed: added `case <-s.shutdownCh` to both blocking selects (credit-acquire and park); `cmd/mq/main.go` races `GracefulStop` against 5s timeout. Tests: `TestMQ_ShutdownUnderActiveStream`.
- **M-4 (gateway):** Truncated response had no signal. Fixed: `X-Truncated: true` + `X-Row-Limit: <n>` headers set before body write when `len(metrics) == maxRows`; `@Header` swag annotations added; `pkg/docs` regenerated.
- **M-5 (docs):** No ADR for natural-key nanosecond collision. Fixed: `ADR-002` created; README references it near exactly-once section.
- **M-6 (streamer):** No retry on Produce failure — one blip killed the instance. Fixed: `produceWithRetry` with exponential backoff (100ms → 5s cap, ctx-aware). Tests: `TestStream_RetryOnProduceError`.

### MEDIUM
- **G-1 (collector):** Backoff reset unconditionally after `grpc.NewClient` (which is lazy — never evidence MQ is up). Fixed: reset only after `Consume` survives > 5s.
- **G-2 (Makefile):** `golangci-lint run ./... 2>/dev/null || go vet ./...` swallowed all lint errors. Fixed: `if command -v golangci-lint; then golangci-lint run ./...; else go vet ./...; fi`.
- **G-3 (Makefile/README):** Docker env vars for integration tests undocumented. Fixed: added comment block at top of Makefile; added `make e2e` target; updated README.
- **G-4 (docs):** No AI usage documentation. Fixed: `docs/AI_USAGE.md` created; README "AI-Assisted Development" section added.

### MINOR (all closed)
- `pkg/db/db.go`: `postgresql://` scheme not handled in `Migrate()` — fixed with explicit `HasPrefix` switch.
- `internal/server/server.go`: Disconnect cleanup reordered (drain ackCh first, then requeue); `io.EOF` translated to nil; `creditCeiling` function doc improved.
- `pkg/db/read.go`: Verbatim-duplicate godoc block on `Telemetry` removed.
- `internal/gateway/handler.go`: `context.WithTimeout(10s)` added around all DB calls in both handlers.
- `internal/gateway/integration_test.go`: `fmt.Printf` diagnostic removed; window assertion strengthened to `rows[0].Timestamp == t3` exactly.
- `internal/collector/collector.go`: `context.WithCancel` added to prevent recv goroutine leak on error return.
- `internal/streamer/streamer.go`: Loop delay changed to ctx-aware select.
- Pre-existing `errcheck` lint issues surfaced by G-2 fix: `nolint:errcheck` added to 5 `defer close` patterns across collector, streamer, db, mqprobe.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Context panic in C-1 RED test**
- **Found during:** Task 1 TDD
- **Issue:** Initial test used `context.WithTimeout` for both the testcontainers setup and the `Consume` call. Cleanup ran after the timeout expired, causing `testCtr.MustConnectionString` to panic.
- **Fix:** Used `context.Background()` for `restoreDB` and a separate `consumeCtx` (3s) only for the Consume call.
- **Commit:** cd41b2b (in initial test)

**2. [Rule 3 - Blocking] Go compiler: defer references `ack` before declaration**
- **Found during:** Task 2 M-3 / cleanup reorder fix
- **Issue:** Initial cleanup defer called `ack(id)` but `ack` is a closure declared AFTER the defer in the function body. Go compiler rejected it.
- **Fix:** Inlined the ack logic directly in the defer (identical semantics, same atomics).
- **Commit:** da60983

**3. [Rule 3 - Blocking] M-3 test hung — recv goroutine stuck on stream.Recv()**
- **Found during:** Task 2 M-3 GREEN
- **Issue:** After the send loop returned via `<-s.shutdownCh`, the defer's `wg.Wait()` blocked because the mock recv goroutine was stuck on `stream.Recv()` (Background context, no way to cancel). In production, `grpcSrv.GracefulStop()` cancels the stream context, unblocking Recv. The test tested Shutdown in isolation.
- **Fix:** Updated test to use cancellable stream context; cancel it immediately after `srv.Shutdown()` (accurately models the production GracefulStop sequence). Accept codes.Unavailable or context.Canceled.
- **Commit:** da60983

**4. [Rule 2 - Missing] G-2 lint fix surfaced 5 pre-existing errcheck issues**
- **Found during:** Final gate (`make lint`)
- **Issue:** The G-2 fix made `golangci-lint` errors visible. 5 existing `defer close()` patterns across unrelated files failed errcheck.
- **Fix:** Added `//nolint:errcheck` — standard Go pattern for close on cleanup resources where errors are non-actionable.
- **Commit:** a8b8faa

## Gates

| Gate | Result |
|---|---|
| `make build` | PASS |
| `make test` | PASS (all packages) |
| `make coverage` (integration, Docker) | PASS — 90.3% |
| `make lint` | PASS — 0 issues |

## Self-Check: PASSED

- All 4 task commits verified in git log
- SUMMARY.md written to canonical path
- All named files exist in working tree
