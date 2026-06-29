---
phase: 3
slug: pipeline-streamer-collector-integration
status: validated
nyquist_compliant: true
wave_0_complete: true
created: 2026-06-29
validated: 2026-06-29
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Detailed validation architecture lives in `03-RESEARCH.md` (## Validation Architecture).

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (-race), testify, testcontainers-go (postgres:17-alpine), grpc/test/bufconn |
| **Config file** | none — Makefile drives (`make test`, `make coverage`) |
| **Quick run command** | `go test -race ./internal/streamer/... ./internal/collector/... ./pkg/models/...` |
| **Full suite command** | `make coverage` (with Rancher Docker env: `DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true`) |
| **E2E command** | `DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration ./test/e2e/... -run TestEndToEnd` |
| **Estimated runtime** | ~60–90 seconds (integration suite spins up postgres + bufconn MQ) |

---

## Sampling Rate

- **After every task commit:** Run the quick run command for the touched package
- **After every plan wave:** Run the full suite command
- **Before `/gsd-verify-work`:** Full suite must be green; coverage ≥ 90%
- **Max feedback latency:** ~90 seconds

---

## Per-Task Verification Map

*Reconstructed from executed PLAN/SUMMARY artifacts. Every Phase 3 requirement maps to a
requirement-tagged automated test (or, for the cross-cutting doc/smoke pair, a manual-confidence
check). All commands assume the Rancher Docker env for `-tags=integration` runs.*

| Requirement | Plan | Wave | Test Type | Test (function / artifact) | Automated Command | Status |
|-------------|------|------|-----------|----------------------------|-------------------|--------|
| STREAM-01 (loop CSV forever) | 03-02 | 1 | unit (-race) | `TestStream_LoopsUntilCancel` | `go test -race ./internal/streamer/...` | ✅ green |
| STREAM-02 (RFC3339Nano restamp) | 03-02 | 1 | unit + integ | `TestRecordToProto_RestampRFC3339Nano`, `TestStreamProduce` | `go test -race -tags=integration ./internal/streamer/...` | ✅ green |
| STREAM-03 (gRPC Produce client) | 03-02 | 1 | integration (bufconn) | `TestStreamProduce` | `go test -race -tags=integration ./internal/streamer/... -run TestStreamProduce` | ✅ green |
| STREAM-04 (12-col parse, skip malformed) | 03-02 | 1 | unit | `TestStream_SkipsMalformed` | `go test -race ./internal/streamer/...` | ✅ green |
| STREAM-05 (10 concurrent instances) | 03-02 | 1 | unit (-race) | `TestStream_Concurrent10` | `go test -race ./internal/streamer/...` | ✅ green |
| COLL-01 (long-lived Consume stream) | 03-03 | 2 | integration | `TestConsumeHandshake` | `go test -race -tags=integration ./internal/collector/...` | ✅ green |
| COLL-02 (auto-reconnect on drop) | 03-03 | 2 | integration | `TestReconnect` | `go test -race -tags=integration ./internal/collector/...` | ✅ green |
| COLL-03 (pgxpool batched writes) | 03-03 | 2 | integration | `TestBatchFlush` | `go test -race -tags=integration ./internal/collector/...` | ✅ green |
| COLL-04 (wire→DB UUID mapping) | 03-01 | 1 | unit + e2e | `TestFromProto_UUIDMapping`, `TestEndToEnd_ExactlyOnce` (distinct-gpu_id assert) | `go test -race ./pkg/models/...` | ✅ green |
| COLL-05 (idempotent ON CONFLICT upsert) | 03-03 | 2 | integration | `TestIdempotentUpsert` | `go test -race -tags=integration ./internal/collector/...` | ✅ green |
| QA-03 (end-to-end CSV→MQ→Collector→PG, exactly-once) | 03-04 | 3 | e2e (bufconn + testcontainers) | `TestEndToEnd_ExactlyOnce` | `go test -race -tags=integration ./test/e2e/... -run TestEndToEnd` | ✅ green |
| QA-06 (manual smoke harness) | 03-04 | 3 | manual smoke | `scripts/smoke/phase03-pipeline.sh` | `make smoke-03` | ✅ green (manual) |
| DOC-01 (living README) | 03-04 | 3 | doc / manual | README Phase 3 section | `grep -q "Phase 3" README.md` | ✅ green (manual) |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

**Coverage gate:** `make coverage` = **90.2%** across `./internal/... ./pkg/...` (floor 90%, `pkg/pb` excluded).

---

## Wave 0 Requirements

- [x] `pkg/models/` package (GpuMetric + FromProto + InsertSQL) — shared write/read model (delivered plan 03-01; `pkg/models` coverage 100%)
- [x] `grpc/test/bufconn` in-process harness for the QA-03 E2E test (used by `test/e2e/pipeline_test.go` and `internal/streamer/integration_test.go`; zero new dependency)

*Existing testcontainers + pkg/db Snapshot/Restore infrastructure covers the Postgres side (reused verbatim in the E2E TestMain).*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end pipeline under the docker-compose dev stack | QA-06 (confidence) | Smoke harness exercises real broker + Postgres + restamp drift over live binaries | `make smoke-03` — last run: 403,072 rows, 0 duplicates, 257 distinct GPU UUIDs |
| README clone→run→see-it-work narrative | DOC-01 | Documentation correctness is a human-readability judgement | Follow README "Phase 3 — Pipeline" section end-to-end |

*The exactly-once-under-concurrency property itself has automated coverage via the bufconn E2E test (`TestEndToEnd_ExactlyOnce`, K=3 concurrent collectors); the smoke run is a confidence check on the human-runnable harness, not the sole proof.*

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 90s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** validated 2026-06-29 — all 13 Phase 3 requirements have automated coverage (11 functional + QA-03 e2e) or a manual-confidence check (DOC-01, QA-06); coverage gate 90.2% green; E2E exactly-once green under `-race`.

---

## Validation Audit 2026-06-29

| Metric | Count |
|--------|-------|
| Requirements audited | 13 |
| COVERED (automated) | 11 (STREAM-01..05, COLL-01..05, QA-03) |
| Manual-only (confidence) | 2 (DOC-01, QA-06) |
| Gaps found | 0 |
| Resolved | 0 |
| Escalated | 0 |

Reconstructed the Per-Task Verification Map from the four executed plans (03-01..03-04). No gaps
required the nyquist auditor: every functional requirement already carries a requirement-tagged,
green automated test. Confirmed live: `make coverage` = 90.2%, `TestEndToEnd_ExactlyOnce` green
under `-race`.
