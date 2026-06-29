---
phase: "03"
plan: "02"
subsystem: streamer
status: complete
tags: [streamer, grpc, csv, tdd, rfc3339nano, pipeline]
dependency_graph:
  requires:
    - pkg/pb (generated gRPC stubs — mq.proto Produce/Consume)
    - internal/server (bufconn integration test: NewMQServer)
    - internal/queue (bufconn integration test: NewRingStore)
  provides:
    - internal/streamer (Stream, Run, Config, FromEnv — CSV-to-MQ producer)
    - cmd/streamer (binary entrypoint)
  affects:
    - make build (streamer slot now filled — no more skip msg)
    - make coverage (internal/streamer adds to coverage pool)
tech_stack:
  added: []
  patterns:
    - CSV infinite loop with file.Seek + csv.NewReader (FieldsPerRecord=12)
    - RFC3339Nano restamp (Phase-2 lock-in — never RFC3339)
    - exported Stream seam (once=true) for fast unit + bufconn tests
    - grpc.NewClient with keepalive (lazy connect — no network I/O at dial)
    - fakeProducer + cancelProducer (mutex-guarded fake MQServiceClient for -race)
    - bufconn in-process gRPC server (no Docker, no OS port)
key_files:
  created:
    - internal/streamer/config.go
    - internal/streamer/streamer.go
    - internal/streamer/streamer_test.go
    - internal/streamer/integration_test.go
    - cmd/streamer/main.go
  modified: []
decisions:
  - "RFC3339Nano restamp is non-negotiable (Phase-2 lock-in): RFC3339 collapses same-GPU/metric readings onto the same natural key, causing only 1 row to survive per GPU/metric pair per loop pass"
  - "Stream exported seam with once=true enables deterministic unit and bufconn tests without the infinite production loop"
  - "Logic lives in internal/streamer (not cmd/streamer) so the coverage gate (./internal/...) captures it and plan 03-04 E2E tests can import the exported Stream seam"
  - "grpc.NewClient (lazy) vs grpc.Dial (deprecated) — NewClient used throughout per CLAUDE.md stack constraint"
  - "Extra coverage tests added (TestFromEnv_*, TestDialMQ_ReturnsConn, TestRun_*) via deviation Rule 2 to keep total coverage above 90%"
metrics:
  duration: "~4.5 minutes"
  completed: "2026-06-29"
  tasks_completed: 3
  tasks_total: 3
  files_created: 5
  files_modified: 0
---

# Phase 03 Plan 02: Streamer Microservice Summary

**One-liner:** CSV-to-MQ producer with RFC3339Nano restamp, FieldsPerRecord=12 skip-and-log, and bufconn-proven gRPC Produce client.

## What Was Built

The complete Streamer vertical slice: an internal package (`internal/streamer`) + thin binary entrypoint (`cmd/streamer`).

- **`internal/streamer/config.go`** — `Config{MQAddr, CSVPath, LoopDelayMS}` + `FromEnv()` reading `STREAMER_MQ_ADDR` / `STREAMER_CSV_PATH` / `STREAMER_LOOP_DELAY_MS` with sane defaults.
- **`internal/streamer/streamer.go`** — four functions:
  - `dialMQ(addr)` — `grpc.NewClient` with insecure credentials + keepalive params matching the MQ server enforcement policy (Time=30s, Timeout=10s, PermitWithoutStream=true). Lazy connect — no network I/O at construction.
  - `recordToProto(record)` — maps 12 CSV columns to `TelemetryMessage`. Restamps at `time.RFC3339Nano` (col 0 discarded). Maps col 4 (uuid, GPU UUID) to `proto.Uuid` and col 2 (ordinal) to `proto.GpuId`. Returns error for non-numeric value column (col 10).
  - `Stream(ctx, client, csvPath, loopDelayMS, once)` — exported test seam. Opens the CSV once; loops: seek → create csv.Reader(FieldsPerRecord=12) → read header → inner read loop (skip+log malformed, recordToProto, Produce, optional sleep). `once=true` returns after one full pass; `once=false` loops until `ctx.Err()`. Thread-safe — each call owns its own file handle.
  - `Run(ctx, cfg)` — validates CSVPath, dials MQ, calls `Stream(..., false)`.
- **`cmd/streamer/main.go`** — thin `package main`: signal.NotifyContext + errgroup wiring; no business logic.

## Test Results

| Suite | Command | Result |
|-------|---------|--------|
| Unit (RED → GREEN) | `go test -race ./internal/streamer/...` | PASS |
| Integration | `go test -race -tags=integration ./internal/streamer/... -run TestStreamProduce` | PASS |
| Build | `make build` | PASS (mq + streamer) |
| Coverage | `make coverage` | **92.6%** (gate: ≥90%) |
| Lint/vet | `make lint` / `go vet` | PASS |

### Coverage Detail for `internal/streamer`

| File | Function | Coverage |
|------|----------|----------|
| config.go | FromEnv | 100% |
| streamer.go | dialMQ | 75% (error branch requires grpc.NewClient to fail — impractical in unit tests) |
| streamer.go | recordToProto | 100% |
| streamer.go | Stream | 80% (loopDelayMS>0 sleep branch not covered in unit tests) |
| streamer.go | Run | 87.5% (dialMQ error return path not covered) |
| **Total internal/streamer** | | **85.5%** |
| **Total ./internal/... ./pkg/...** | | **92.6%** |

## Requirements Satisfied

| Req ID | Behavior | Proven By |
|--------|----------|-----------|
| STREAM-01 | CSV loops indefinitely (seek after EOF), exits on ctx cancel | TestStream_LoopsUntilCancel |
| STREAM-02 | RFC3339Nano restamp; original timestamp discarded | TestRecordToProto_RestampRFC3339Nano, TestStreamProduce (timestamp check) |
| STREAM-03 | Produce client delivers records to live MQ broker | TestStreamProduce (bufconn end-to-end) |
| STREAM-04 | 11-field rows skipped+logged; never panic | TestStream_SkipsMalformed |
| STREAM-05 | 10 concurrent instances, no race | TestStream_Concurrent10 (under -race) |

## Deviations from Plan

### Auto-added (Rule 2 — missing critical functionality for coverage)

**[Rule 2 - Coverage] Extra unit tests for Config, dialMQ, and Run**
- **Found during:** Task 2 (GREEN implementation)
- **Issue:** Config.FromEnv, dialMQ, and Run were not covered by the plan's mandatory tests. Estimated coverage without these tests: ~70-75% for internal/streamer, risking total coverage falling below the 90% gate.
- **Fix:** Added `TestFromEnv_Defaults`, `TestFromEnv_Override`, `TestFromEnv_InvalidDelayKeptAsDefault`, `TestDialMQ_ReturnsConn`, `TestRun_RequiresCSVPath`, `TestRun_ErrorsOnMissingCSV` in the RED test commit (they were part of the failing compile suite from the start).
- **Files modified:** `internal/streamer/streamer_test.go`
- **Result:** Total coverage 92.6% (passes gate).

No other deviations. Plan executed exactly as written.

## Known Stubs

None. The Streamer produces real gRPC Produce calls with real restamped proto payloads. No placeholder data in any path that flows to the wire.

## Threat Surface Scan

No new network endpoints beyond those specified in the plan (gRPC Produce client — outbound only). No new auth paths. No new file access patterns beyond what the plan specifies (reads one CSV file path from env). No schema changes. Plan threat model fully implemented.

## Self-Check: PASSED

- All 5 files created and present on disk.
- All 3 commits exist: 57e2cb8 (RED), 8922f97 (GREEN), a567351 (integration test).
- `make build && make test && make coverage && make lint` all pass.
- `TestStreamProduce` (STREAM-03 bufconn) passes with `-race -tags=integration`.
