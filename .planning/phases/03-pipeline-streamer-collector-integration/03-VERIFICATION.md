---
phase: 03-pipeline-streamer-collector-integration
verified: 2026-06-29T17:45:00Z
status: passed
score: 17/17
behavior_unverified: 0
overrides_applied: 0
gaps: []
---

# Phase 03: Pipeline — Streamer + Collector + Integration — Verification Report

**Phase Goal:** Live CSV telemetry flows end-to-end from the Streamer, through the MQ, into PostgreSQL via the Collector — correctly under concurrency.
**Verified:** 2026-06-29T17:45:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `models.FromProto` maps `proto.Uuid` (CSV col 4) to `GpuMetric.GpuID` — never `proto.GpuId` (ordinal) | ✓ VERIFIED | `pkg/models/telemetry.go:101` — `GpuID: msg.GetUuid()`; `TestFromProto_UUIDMapping` passes under `-race`; E2E assertion `count(distinct gpu_id)==10` proves UUID held end-to-end |
| 2 | `models.FromProto` parses RFC3339Nano timestamp into UTC `time.Time` with RFC3339 fallback, errors on unparseable | ✓ VERIFIED | `telemetry.go:90-96` — primary RFC3339Nano, fallback RFC3339, error + zero value on failure; `TestFromProto_RestampRFC3339Nano`, `TestFromProto_RFC3339Fallback`, `TestFromProto_BadTimestamp` all pass |
| 3 | `models.InsertSQL` targets the `(gpu_id, metric_name, timestamp)` natural key with `ON CONFLICT DO NOTHING` | ✓ VERIFIED | `telemetry.go:67-71` — exact SQL confirmed; `TestInsertSQL_Shape` asserts presence of conflict columns, `$11`, absence of `$12` |
| 4 | Streamer loops CSV indefinitely (seek-to-start after EOF) until context cancelled | ✓ VERIFIED | `streamer.go:108-148` — outer loop with `f.Seek(0, io.SeekStart)`; `TestStream_LoopsUntilCancel` passes: ≥6 messages from 3-row CSV proves ≥2 full passes |
| 5 | Each published record carries `time.Now().UTC().Format(time.RFC3339Nano)` — not RFC3339 second-granularity | ✓ VERIFIED | `streamer.go:70` — literal `time.RFC3339Nano` constant used; `TestRecordToProto_RestampRFC3339Nano` asserts timestamp ends in Z, parses as RFC3339Nano, differs from CSV value; `TestStreamProduce` (bufconn) verifies RFC3339Nano on messages received at broker |
| 6 | Malformed CSV rows (wrong column count, non-numeric value) are skipped and logged, never panic | ✓ VERIFIED | `streamer.go:117 FieldsPerRecord=12`; `streamer.go:128-132` — csv.ParseError → `log.Printf; continue`; `TestStream_SkipsMalformed`: exactly 2 messages published from 3-row CSV with 1 malformed 11-col row |
| 7 | Records published to running MQ via generated gRPC `Produce` client | ✓ VERIFIED | `streamer.go:138` — `client.Produce(ctx, &pb.ProduceRequest{Message: msg})`; `TestStreamProduce` (bufconn integration): exactly 3 messages consumed from the MQ after one Stream pass |
| 8 | ≤10 concurrent Streamer instances publish with no data race | ✓ VERIFIED | `Stream()` is stateless (own fd per call, no shared mutable state); `TestStream_Concurrent10`: 10 goroutines, total published == 30 (10×3), passes `go test -race` |
| 9 | Collector opens long-lived bidi `Consume` stream and sends initial credit handshake before any acks | ✓ VERIFIED | `collector.go:107-137` — `client.Consume(ctx)` then `stream.Send(&pb.ConsumeClientMsg{Credit: int32(cfg.Credit)})`; `TestConsumeHandshake` (integration): 10 rows land after credit handshake |
| 10 | When stream drops, Collector redials with exponential backoff (base 100ms, max 5s) and resumes consuming | ✓ VERIFIED | `collector.go:200-241` — `backoff:=100ms`, `maxBackoff:=5s`, `backoff=min(backoff*2,max)`; `TestReconnect` (integration): collector reconnects after `grpcSrv1.Stop()`, post-restart 5 rows persist → 10 total; test passes in 0.70s |
| 11 | Received messages batched and persisted to PostgreSQL via `pgxpool.SendBatch` on size or flush-interval trigger | ✓ VERIFIED | `collector.go:74,147-163` — `pool.SendBatch(ctx, b)`, size trigger at `len(batch) >= cfg.BatchSize`, time trigger via `time.NewTicker`; `TestBatchFlush` (integration): 120 rows, BatchSize=50, FlushMS=200 — all 120 land |
| 12 | Redelivered/duplicate message never creates a duplicate row (`ON CONFLICT DO NOTHING` on natural key) | ✓ VERIFIED | `collector.go:65` — `b.Queue(models.InsertSQL, ...)` uses ON CONFLICT DO NOTHING; `TestIdempotentUpsert` (integration): 2 messages with identical natural key → exactly 1 row; test passes in 10.21s |
| 13 | Exactly one goroutine calls `stream.Send` (credit + acks); recv goroutine only calls `stream.Recv` | ✓ VERIFIED | `collector.go:119-133` recv goroutine — sole `stream.Recv` caller; `collector.go:137,156` — both `stream.Send` calls are in the batch goroutine only (initial credit + ack-after-persist in `flush()`); passes `go test -race -tags=integration` |
| 14 | Automated E2E test drives CSV→Streamer→MQ→Collector→Postgres and asserts rows land | ✓ VERIFIED | `test/e2e/pipeline_test.go#TestEndToEnd_ExactlyOnce` (K=3 collectors, 200 rows, bufconn+testcontainers): ran and passed in 0.85s — "200 rows persisted" |
| 15 | With multiple concurrent collectors, each logical reading persisted exactly once (zero duplicates despite at-least-once redelivery) | ✓ VERIFIED | `pipeline_test.go:343-352` — `count(*)==count(DISTINCT gpu_id, metric_name, timestamp)` asserted; `count(distinct gpu_id)==10` asserted (UUID mapping end-to-end); test ran: 200 == 200 distinct, 10 gpu_ids |
| 16 | Human-runnable smoke check (`make smoke-03`) exercises the live pipeline over docker-compose dev stack | ✓ VERIFIED | `scripts/smoke/phase03-pipeline.sh` exists, executable, syntax-valid (`bash -n`); asserts count(*)>0, count(*)==count(distinct), no ordinal gpu_ids; discovered by existing Makefile `smoke-%` pattern rule |
| 17 | README has a Phase 3 section a reader can follow to run the pipeline and see rows | ✓ VERIFIED | README.md lines 207-293 — "Phase 3 — Pipeline (Streamer + Collector)" section; `make smoke-03` referenced; grep confirms 6 Phase 3 mentions |

**Score: 17/17 truths verified (0 present-behavior-unverified)**

---

### Required Artifacts

| Artifact | Status | Details |
|----------|--------|---------|
| `pkg/models/telemetry.go` | ✓ VERIFIED | 114 lines, substantive; imported by `internal/collector/collector.go` and `test/e2e/pipeline_test.go`; data flows via `persistBatch → b.Queue(models.InsertSQL, ...)` |
| `pkg/models/telemetry_test.go` | ✓ VERIFIED | 141 lines, 6 black-box tests, all passing under `-race` |
| `internal/streamer/streamer.go` | ✓ VERIFIED | 169 lines, substantive; imported by `cmd/streamer/main.go`, `internal/streamer/integration_test.go`, `test/e2e/pipeline_test.go` |
| `internal/streamer/config.go` | ✓ VERIFIED | 55 lines, substantive; imported by `cmd/streamer/main.go` |
| `internal/streamer/streamer_test.go` | ✓ VERIFIED | 309 lines, 12 in-package unit tests, all passing under `-race` |
| `internal/streamer/integration_test.go` | ✓ VERIFIED | 155 lines, `//go:build integration`; `TestStreamProduce` passes under `-race -tags=integration` |
| `cmd/streamer/main.go` | ✓ VERIFIED | 52 lines, thin wrapper — `streamer.FromEnv() + errgroup + Run()`; no business logic |
| `internal/collector/collector.go` | ✓ VERIFIED | 242 lines, substantive; imported by `cmd/collector/main.go`, `test/e2e/pipeline_test.go` |
| `internal/collector/config.go` | ✓ VERIFIED | Config struct + FromEnv; imported by `cmd/collector/main.go` |
| `internal/collector/collector_test.go` | ✓ VERIFIED | `//go:build integration`; 5 tests (COLL-01/02/03/05 + bad-proto), all passing under `-race -tags=integration` |
| `cmd/collector/main.go` | ✓ VERIFIED | 56 lines, thin wrapper — `db.FromEnv → db.Migrate → db.New → collector.Run`; no consume/persist logic |
| `test/e2e/pipeline_test.go` | ✓ VERIFIED | 370 lines, `//go:build integration`; `TestEndToEnd_ExactlyOnce` ran: PASS 0.85s, 200 rows, 200 distinct, 10 gpu_ids |
| `scripts/smoke/phase03-pipeline.sh` | ✓ VERIFIED | 166 lines, executable, syntax clean, 3 assertions (rows > 0, exactly-once, UUID mapping) |
| README.md (Phase 3 section) | ✓ VERIFIED | Phase 3 section present at line 207; run instructions, env vars table, `make smoke-03` |

---

### Key Link Verification

| From | To | Via | Status |
|------|----|-----|--------|
| `internal/streamer/streamer.go` | MQ gRPC `Produce` | `client.Produce(ctx, &pb.ProduceRequest{Message: msg})` | ✓ WIRED |
| `recordToProto` | `proto.Uuid` (not `proto.GpuId`) | `Uuid: record[4]` — CSV col 4 is the GPU UUID | ✓ WIRED |
| `internal/collector/collector.go` | `pkg/models` | `models.FromProto(msg)` + `b.Queue(models.InsertSQL, ...)` | ✓ WIRED |
| `collector.Consume` recv goroutine | `stream.Recv()` only | Goroutine only calls `stream.Recv()`; no `stream.Send` call path | ✓ WIRED |
| `collector.flush()` | ack only after persist | `persistBatch()` then `stream.Send(AckId)` sequentially in flush() | ✓ WIRED |
| `cmd/collector/main.go` | `pkg/db` migration | `db.FromEnv → db.Migrate → db.New` before `collector.Run` | ✓ WIRED |
| `test/e2e/pipeline_test.go` | `streamer.Stream` + `collector.Consume` | Imports both exported seams; drives real pipeline in-process | ✓ WIRED |

---

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `persistBatch` | `b *pgx.Batch` | `models.FromProto(msg)` on each `*pb.TelemetryMessage` | Yes — proto messages from live MQ stream | ✓ FLOWING |
| `Consume` → `flush()` | `batch []*pb.TelemetryMessage` | `msgCh` ← recv goroutine ← `stream.Recv()` from MQ | Yes — messages enqueued by Streamer | ✓ FLOWING |
| `InsertSQL` args | 11 positional params | `m.GpuID, m.Timestamp, m.MetricName, ...` from `GpuMetric` | Yes — all 11 fields populated by `FromProto` | ✓ FLOWING |

---

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| pkg/models unit tests (COLL-04, RFC3339Nano, InsertSQL shape) | `go test -race ./pkg/models/...` | PASS — 1.83s | ✓ PASS |
| Streamer unit tests (STREAM-01/02/04/05, 10x concurrency) | `go test -race -count=1 ./internal/streamer/...` | PASS — 1.62s | ✓ PASS |
| Collector unit tests (config, run error paths) | `go test -race -count=1 ./internal/collector/...` | PASS — 1.64s | ✓ PASS |
| `TestIdempotentUpsert` — ON CONFLICT DO NOTHING (COLL-05) | `go test -race -tags=integration ./internal/collector/... -run TestIdempotentUpsert` | PASS — 10.21s; 2 duplicate-key msgs → 1 row | ✓ PASS |
| `TestReconnect` — reconnect after stream drop (COLL-02) | `go test -race -tags=integration ./internal/collector/... -run TestReconnect` | PASS — 0.70s; collector reconnects, 10 total rows | ✓ PASS |
| `TestEndToEnd_ExactlyOnce` — QA-03 capstone | `go test -race -tags=integration ./test/e2e/... -run TestEndToEnd_ExactlyOnce` | PASS — 0.85s; 200 rows, 200 distinct, 10 gpu_ids | ✓ PASS |
| Coverage gate | `go test -race -covermode=atomic -tags=integration ./internal/... ./pkg/db ./pkg/models` | PASS — 90.2% ≥ 90% threshold | ✓ PASS |
| `go vet` on all phase packages | `go vet ./pkg/models/... ./internal/streamer/... ./internal/collector/...` | Clean | ✓ PASS |
| Smoke script syntax | `bash -n scripts/smoke/phase03-pipeline.sh` | SYNTAX OK | ✓ PASS |

---

### Requirements Coverage

| Requirement | Plan | Description | Status | Evidence |
|-------------|------|-------------|--------|----------|
| STREAM-01 | 03-02 | CSV loops indefinitely until context cancelled | ✓ SATISFIED | `TestStream_LoopsUntilCancel` — ≥6 msgs from 3-row CSV, exits on `context.Canceled` |
| STREAM-02 | 03-02 | RFC3339Nano restamp | ✓ SATISFIED | `time.RFC3339Nano` in `recordToProto`; `TestRecordToProto_RestampRFC3339Nano`; bufconn `TestStreamProduce` verifies |
| STREAM-03 | 03-02 | gRPC `Produce` client delivers to broker | ✓ SATISFIED | `TestStreamProduce` (bufconn): 3 produced → 3 consumed back from live MQ |
| STREAM-04 | 03-02 | 12-col strict parse; malformed skipped and logged | ✓ SATISFIED | `FieldsPerRecord=12`; `TestStream_SkipsMalformed`: 2 published, 1 malformed skipped |
| STREAM-05 | 03-02 | ≤10 concurrent instances with no data race | ✓ SATISFIED | `TestStream_Concurrent10`: 10 goroutines, no race, 30 total messages |
| COLL-01 | 03-03 | Long-lived bidi `Consume` stream with credit handshake | ✓ SATISFIED | `TestConsumeHandshake`: 10 rows land after handshake |
| COLL-02 | 03-03 | Auto-reconnect on stream drop | ✓ SATISFIED | `TestReconnect`: reconnects after `grpcSrv1.Stop()`, 10 rows total |
| COLL-03 | 03-03 | Batch-insert to PostgreSQL via `pgxpool` | ✓ SATISFIED | `TestBatchFlush`: 120 rows, size+ticker flush triggers |
| COLL-04 | 03-01 | Maps wire payload to DB model; `proto.Uuid → gpu_id` | ✓ SATISFIED | `TestFromProto_UUIDMapping`; E2E `count(distinct gpu_id)==10` end-to-end |
| COLL-05 | 03-03 | Idempotent upsert; no duplicate rows | ✓ SATISFIED | `TestIdempotentUpsert`: 2 duplicate-key msgs → 1 row |
| QA-03 | 03-04 | E2E: CSV→MQ→Collector→Postgres, exactly-once under concurrent collectors | ✓ SATISFIED | `TestEndToEnd_ExactlyOnce`: K=3, 200 rows, `count(*)==count(distinct)`==200, `count(distinct gpu_id)==10` |

---

### Anti-Patterns Found

None. Scanned `pkg/models/telemetry.go`, `internal/streamer/streamer.go`, `internal/streamer/config.go`, `internal/collector/collector.go`, `internal/collector/config.go`, `cmd/streamer/main.go`, `cmd/collector/main.go`, `test/e2e/pipeline_test.go` — no `TODO`, `FIXME`, `XXX`, `TBD`, `HACK`, `PLACEHOLDER`, or `return null/return []`/stub patterns found.

---

### Human Verification Required

None. All truths are verifiable programmatically and behavioral tests exercise all critical state transitions.

---

## Gaps Summary

None. All 17 must-haves are VERIFIED with behavioral evidence. Phase goal achieved.

---

_Verified: 2026-06-29T17:45:00Z_
_Verifier: Claude (gsd-verifier)_
