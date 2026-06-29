---
phase: 03-pipeline-streamer-collector-integration
plan: "03"
subsystem: pipeline
tags: [grpc, bidi-streaming, pgx, pgxpool, testcontainers, bufconn, exponential-backoff, at-least-once]

# Dependency graph
requires:
  - phase: 03-pipeline-streamer-collector-integration/03-01
    provides: pkg/models.GpuMetric, models.FromProto, models.InsertSQL (ON CONFLICT DO NOTHING)
  - phase: 03-pipeline-streamer-collector-integration/03-02
    provides: internal/streamer patterns (gRPC client, bufconn integration test shape)
  - phase: 01-mq-broker
    provides: MQ gRPC server, ADR-001 bidi Consume protocol, ConsumeClientMsg credit+ack handshake
provides:
  - Collector microservice (internal/collector): dialMQ, persistBatch, Consume (bidi stream), Run (reconnect loop)
  - cmd/collector/main.go: thin composition root — env config, DB migrate, pgxpool, errgroup wiring
  - Integration test suite (COLL-01/02/03/05): testcontainers Postgres + bufconn MQ, covering handshake, flush, idempotent upsert, reconnect
  - Unit tests for all error paths, config, and coverage floor (90.2%)
affects:
  - 03-04 (integration test: end-to-end Streamer → MQ → Collector → Postgres flow)
  - phase-06 (WAL persistence backend; Collector connect path unchanged)

# Tech tracking
tech-stack:
  added:
    - google.golang.org/grpc (bidi streaming client, bufconn)
    - github.com/jackc/pgx/v5 pgx.Batch + pgxpool.SendBatch
    - google.golang.org/grpc/test/bufconn (in-process gRPC for unit-integration tests)
    - keepalive.ClientParameters (Time=30s, Timeout=10s, PermitWithoutStream=true)
  patterns:
    - Two-goroutine bidi split: recv goroutine is sole stream.Recv caller; batch goroutine is sole stream.Send caller
    - ctx.Err() != nil as sole reconnect discriminant (not errors.Is) — avoids false-exit on gRPC server-side GracefulStop
    - pgx.Batch drain contract: defer br.Close() + loop exactly b.Len() times with br.Exec()
    - Exponential backoff base 100ms cap 5s reset on successful dial
    - testcontainers Snapshot/Restore for cheap per-test DB isolation

key-files:
  created:
    - internal/collector/collector.go
    - internal/collector/config.go
    - internal/collector/collector_test.go
    - internal/collector/config_test.go
    - internal/collector/run_test.go
    - cmd/collector/main.go
  modified: []

key-decisions:
  - "Two-goroutine bidi split for stream.Recv/stream.Send — prevents the gRPC concurrent-Send race condition documented in grpc-go"
  - "ctx.Err() != nil exclusively as the reconnect exit discriminant; gRPC may surface server-side codes.Canceled as stdlib context.Canceled, so errors.Is is unreliable here"
  - "grpcSrv.Stop() (not GracefulStop) in TestReconnect — GracefulStop blocks ~30s (drain interval) exceeding the run timeout, masking the reconnect path"
  - "ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING absorbs MQ at-least-once redeliveries idempotently — no dedup state needed in Collector"
  - "pgx.Batch drain must loop exactly b.Len() times — under-draining corrupts pool connections"
  - "TestBadProtoSkipped added (not in original plan) to cover persistBatch bad-proto log-and-continue path, lifting coverage from 89.7% to 90.2%"

patterns-established:
  - "Bidi gRPC client split: one goroutine owns Recv, caller goroutine owns Send — never cross-call"
  - "Reconnect loop uses ctx.Err() != nil, not errors.Is(err, context.Canceled)"
  - "pgx.Batch SendBatch drain: defer br.Close() then exactly b.Len() Exec calls"
  - "testcontainers + bufconn: postgres:17-alpine for DB, bufconn for MQ — no OS ports in tests"
  - "Credit >= BatchSize invariant: prevents stalling the broker's sliding window"

requirements-completed: [COLL-01, COLL-02, COLL-03, COLL-05]

coverage:
  - id: D1
    description: "Collector opens bidi Consume stream, sends credit handshake, receives 10 messages, persists all to Postgres (COLL-01)"
    requirement: COLL-01
    verification:
      - kind: integration
        ref: "internal/collector/collector_test.go#TestConsumeHandshake"
        status: pass
    human_judgment: false
  - id: D2
    description: "Collector auto-reconnects to MQ after stream drop with exponential backoff, consuming messages from both stream attempts (COLL-02)"
    requirement: COLL-02
    verification:
      - kind: integration
        ref: "internal/collector/collector_test.go#TestReconnect"
        status: pass
    human_judgment: false
  - id: D3
    description: "Collector flushes batch at BatchSize=50 boundary before ticker fires, all 120 messages land in Postgres (COLL-03)"
    requirement: COLL-03
    verification:
      - kind: integration
        ref: "internal/collector/collector_test.go#TestBatchFlush"
        status: pass
    human_judgment: false
  - id: D4
    description: "Idempotent upsert via ON CONFLICT DO NOTHING: two messages with same natural key (gpu_id, metric_name, timestamp) produce exactly 1 row (COLL-05)"
    requirement: COLL-05
    verification:
      - kind: integration
        ref: "internal/collector/collector_test.go#TestIdempotentUpsert"
        status: pass
    human_judgment: false
  - id: D5
    description: "cmd/collector/main.go thin composition root: env config, DB migrate, pgxpool, errgroup wiring"
    verification:
      - kind: unit
        ref: "make build — collector binary compiles"
        status: pass
    human_judgment: false
  - id: D6
    description: "Coverage gate: >=90% line coverage across internal/collector (actual: 90.2%)"
    verification:
      - kind: unit
        ref: "make coverage — 90.2% >= 90% threshold"
        status: pass
    human_judgment: false

# Metrics
duration: 27min
completed: 2026-06-29
status: complete
---

# Phase 03 Plan 03: Collector Microservice Summary

**Bidi gRPC Consume client with credit-based flow control, pgx.Batch upsert (ON CONFLICT DO NOTHING), and exponential-backoff reconnect loop — 90.2% coverage, all COLL-01/02/03/05 tests green**

## Performance

- **Duration:** 27 min
- **Started:** 2026-06-29T~19:39Z
- **Completed:** 2026-06-29T~20:06Z
- **Tasks:** 2 (RED + GREEN, TDD)
- **Files created:** 6

## Accomplishments

- `internal/collector/collector.go`: `dialMQ` (lazy gRPC with keepalive), `persistBatch` (pgx.Batch ON CONFLICT DO NOTHING), `Consume` (two-goroutine bidi split; credit handshake; size+time flush triggers; ack after persist), `Run` (exponential backoff reconnect loop)
- `internal/collector/config.go`: `Config` struct + `FromEnv` (4 COLLECTOR_* env vars with silent defaults)
- `cmd/collector/main.go`: thin composition root — DB migrate, pgxpool, errgroup, graceful shutdown via SIGTERM/SIGINT
- Integration test suite: TestConsumeHandshake, TestBatchFlush, TestIdempotentUpsert, TestBadProtoSkipped, TestReconnect — all passing with testcontainers Postgres + bufconn MQ
- Unit tests: 4 `config_test.go` tests (FromEnv defaults/overrides/invalid/partial), 2 `run_test.go` tests (pre-canceled ctx, stream-open error) — all passing without Docker
- All gates pass: `make build`, `go test -race` (integration + unit), `make coverage` (90.2%), `go vet`

## Task Commits

1. **Task 1: RED — integration test suite** - `555b5a8` (test)
2. **Task 2: GREEN — collector package implementation** - `f3ed191` (feat)

## Files Created/Modified

- `internal/collector/collector.go` — `dialMQ`, `persistBatch`, `Consume`, `Run`
- `internal/collector/config.go` — `Config` struct, `FromEnv`
- `internal/collector/collector_test.go` — integration tests (build tag: integration); testcontainers postgres:17-alpine + bufconn MQ
- `internal/collector/config_test.go` — unit tests for `FromEnv` (no Docker required)
- `internal/collector/run_test.go` — unit tests for `Run` and `Consume` error paths (no Docker required)
- `cmd/collector/main.go` — thin main: signal context, DB init, errgroup

## Decisions Made

- **Two-goroutine bidi split:** One goroutine exclusively owns `stream.Recv()`; the batch goroutine exclusively owns `stream.Send()`. Concurrent `Send` from two goroutines is a documented race in grpc-go that can panic the transport.
- **ctx.Err() != nil as sole exit discriminant in Run:** When the MQ server calls `GracefulStop()`, gRPC translates the server-side `codes.Canceled` to stdlib `context.Canceled` on the client side — but OUR context is still valid. Using `errors.Is(err, context.Canceled)` would incorrectly exit the reconnect loop. Checking `ctx.Err() != nil` exclusively is the only reliable discriminant.
- **ON CONFLICT DO NOTHING on (gpu_id, metric_name, timestamp):** MQ delivers at-least-once; Collector must be idempotent. Upsert absorbs redeliveries silently — no in-memory dedup state needed.
- **pgx.Batch drain is exact:** `defer br.Close()` plus exactly `b.Len()` calls to `br.Exec()`. Under-draining leaves a pending response in the pool connection, corrupting subsequent queries on that conn.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Reconnect discriminant used errors.Is causing false-exit**
- **Found during:** Task 2 (TestReconnect failing — collector exited instead of reconnecting)
- **Issue:** Initial implementation checked `errors.Is(err, context.Canceled)` to decide whether to exit the Run loop. When gRPC's server-side GracefulStop fires, the client receives `codes.Canceled` which grpc-go maps to stdlib `context.Canceled`. This caused Run to exit as if OUR context was cancelled, even though it was still active.
- **Fix:** Changed Run's exit check to `ctx.Err() != nil` exclusively. The actual stream error is logged but not used as the exit condition.
- **Files modified:** `internal/collector/collector.go` (Run function)
- **Verification:** TestReconnect passes (0.52s); collector successfully reconnects after stream drop
- **Committed in:** f3ed191

**2. [Rule 3 - Blocking] GracefulStop in TestReconnect blocks ~30s, exceeding runCtx timeout**
- **Found during:** Task 2 (TestReconnect timing out at 50.34s)
- **Issue:** Test used `grpcSrv1.GracefulStop()` to simulate MQ dropping. GracefulStop waits for all active handlers to finish. The MQ Consume handler is blocked in a `select` waiting for `ctx.Done()`, so GracefulStop blocks for the server's shutdown timeout (~30s). The 30s `runCtx` then expired before MQ2 could start.
- **Fix:** Changed `grpcSrv1.GracefulStop()` to `grpcSrv1.Stop()` in TestReconnect. `Stop()` immediately RSTs all connections. `stream.Recv()` returns with EOF/Unavailable in ~0ms. Also increased `runCtx` timeout from 30s to 60s for headroom.
- **Files modified:** `internal/collector/collector_test.go` (TestReconnect)
- **Verification:** TestReconnect passes in 0.52s
- **Committed in:** f3ed191

**3. [Rule 2 - Missing Critical] config.go FromEnv at 0% coverage**
- **Found during:** After GREEN implementation (`make coverage` returned 85.8%, gate failed)
- **Issue:** Plan said "set Config directly in tests" — no test ever called `FromEnv`, leaving the entire function uncovered.
- **Fix:** Added `internal/collector/config_test.go` with 4 unit tests: `TestFromEnvDefaults`, `TestFromEnvOverrides`, `TestFromEnvInvalidIntegers`, `TestFromEnvPartialOverride`. Uses `t.Setenv()` for isolation.
- **Files modified:** `internal/collector/config_test.go` (created)
- **Committed in:** f3ed191

**4. [Rule 2 - Missing Critical] Run and Consume error paths uncovered (66.7% / 81%)**
- **Found during:** After adding config_test.go, coverage still 88.4%
- **Issue:** `Run`'s pre-canceled-ctx early return and `Consume`'s stream-open error path were never exercised.
- **Fix:** Added `internal/collector/run_test.go` with `TestRunPreCanceledContext` (passes nil pool, pre-cancels ctx) and `TestConsumeStreamOpenError` (grpc.NewClient to port 1, expects error on stream open).
- **Files modified:** `internal/collector/run_test.go` (created)
- **Committed in:** f3ed191

**5. [Rule 2 - Missing Critical] persistBatch bad-proto path uncovered; coverage at 89.7%**
- **Found during:** After run_test.go added, still 89.7% (0.3% short)
- **Issue:** The `log.Printf("skip bad proto")` + `continue` branch in `persistBatch` was never hit.
- **Fix:** Added `TestBadProtoSkipped` integration test to `collector_test.go`: produces one message with an empty timestamp (causing `models.FromProto` to fail) and one valid message; asserts `rowCount=1` (bad proto skipped, valid one landed).
- **Files modified:** `internal/collector/collector_test.go`
- **Committed in:** f3ed191

---

**Total deviations:** 5 auto-fixed (1 bug, 2 blocking, 2 missing-critical)
**Impact on plan:** All fixes necessary for correctness and coverage gate. No scope creep.

## Issues Encountered

- `time` import missing from `run_test.go` initial write: `undefined: time` compile error. Added to imports. No separate commit needed (caught before committing).

## User Setup Required

None — no external service configuration required for the collector. Kubernetes deployment values (`COLLECTOR_MQ_ADDR`, `COLLECTOR_BATCH_SIZE`, `COLLECTOR_FLUSH_MS`, `COLLECTOR_CREDIT`) are configured in the Helm chart values.yaml in Phase 04.

## Next Phase Readiness

- Collector microservice complete end-to-end: dials MQ, consumes bidi stream, batch-upserts to Postgres, auto-reconnects
- Integrates with MQ (Phase 01) and models (Phase 03-01) without modification
- Ready for Phase 03-04: end-to-end integration test (Streamer → MQ → Collector → Postgres with all three live)
- No blockers

---
*Phase: 03-pipeline-streamer-collector-integration*
*Completed: 2026-06-29*
