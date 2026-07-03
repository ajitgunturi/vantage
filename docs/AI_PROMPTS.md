# AI Prompt Log — vantage

**Purpose:** Verbatim log of the AI-assisted prompts used to build this project, with honest
notes on where prompts fell short and what manual intervention was required.

**Grading context:** The assignment requires "detailed information of all the prompts used and a
good description of where a prompt fell short that required manual intervention" across four
aspects: repo bootstrap, code, unit tests, and build environment.

**Framework:** Claude (Anthropic) + the GSD framework. Each `/gsd-discuss-phase`,
`/gsd-plan-phase`, and `/gsd-execute-phase` invocation is a traceable prompt; the resulting
PLAN.md task descriptions are the instructions the executor acts on. Source artifacts live in
`.planning/phases/*/`.

---

## Table of Contents

1. [Stage 1 — Repo Bootstrap](#stage-1--repo-bootstrap)
2. [Stage 2 — Code: MQ Core](#stage-2--code-mq-core)
3. [Stage 3 — Code: Pipeline (Streamer + Collector)](#stage-3--code-pipeline-streamer--collector)
4. [Stage 4 — Code: API Gateway](#stage-4--code-api-gateway)
5. [Stage 5 — Code: DevOps (Docker + Helm)](#stage-5--code-devops-docker--helm)
6. [Stage 6 — Unit Tests](#stage-6--unit-tests)
7. [Stage 7 — Build Environment / Toolchain](#stage-7--build-environment--toolchain)
8. [Stage 8 — Code Review + Bug Fix](#stage-8--code-review--bug-fix)
9. [Stage 9 — Production Hardening (Phase 6)](#stage-9--production-hardening-phase-6)

---

## Stage 1 — Repo Bootstrap

**Assignment aspect:** Repo bootstrap

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 1.1 | `/gsd-new-project` | "I have an assignment for building an elastic GPU telemetry pipeline. The brief is in instructions.md. Set up a GSD project and capture the context." | Created `.planning/PROJECT.md`, `ROADMAP.md`, `REQUIREMENTS.md`, `STATE.md`. Phase structure from 1–7 drafted. | The initial roadmap underestimated Phase 01.1 — at-least-once delivery was not in the plan at all. The `instructions.md` spec was accepted at face value before the at-most-once data-loss defect was reproduced. A manual reproduction step (human ran `mqprobe -n 1000 -mode produce` + short consume) was required to demonstrate the problem before the AI would acknowledge the gap (see Stage 2). |
| 1.2 | `/gsd-discuss-phase` (Phase 1) | "Discuss Phase 1 — proto contract and MQ core. The spec requires a custom in-memory MQ, gRPC Produce + Consume, and HTTP inspect endpoint." | Discussion log at `.planning/phases/01-foundation-proto-contract-mq-core/`. Decisions: buf generate over raw protoc; single Go module; chi for the gateway (later), ServeMux for MQ control plane. | No fall-short. Design choices were sound and held through to Phase 6. |
| 1.3 | Manual: `go mod init github.com/ajitg/vantage` | (Human) | go.mod + initial repo layout. | AI was not involved here — the human initialized the module manually before giving Claude the working directory. Not an AI failure; a boundary choice. |
| 1.4 | `/gsd-plan-phase` (Phase 1) | "Create detailed plans for Phase 1: proto definition, MQ core ring buffer + gRPC wiring, HTTP inspect + smoke test." | Three PLAN.md files (01-01, 01-02, 01-03). Task descriptions became the executor prompts. | Plans were technically correct. One missed assumption: `buf generate` with remote BSR plugins requires a network call to `buf.build`; CI did not exist yet and the buf BSR call failed silently in the first executor run until the `buf.gen.yaml` remote plugin versions were pinned. Fixed in Stage 7. |
| 1.5 | `/gsd-execute-phase` (Phase 1 plans 1–3) | (Executed 01-01-PLAN.md task by task) | Ring buffer (`internal/queue`), gRPC server (`internal/server`), HTTP inspect, smoke script. Build and unit tests passing. | No fall-short in this pass. Coverage gate (≥90%) was met. |

---

## Stage 2 — Code: MQ Core

**Assignment aspect:** Code

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 2.1 | `/gsd-discuss-phase` (Phase 01.1) | "We reproduced a data-loss defect: produce 1000, consume 20, then drain — ~493 messages are silently lost. The root cause is eager dispatch into `workCh` with gRPC flow control: messages die on consumer disconnect (at-most-once). I want at-least-once delivery now, not in Phase 6." | Discussion session captured in `.planning/phases/01.1-mq-at-least-once-delivery-bidi-consume-and-ack/01.1-DISCUSSION-LOG.md`. Decided: bidi stream, per-message ack with broker-side ack+requeue, client-driven credit. ADR-001 drafted. | Claude's initial recommendation was to _not_ do this: it recommended documenting the at-most-once behavior and deferring to Phase 6 WAL. The owner overrode — this is documented explicitly in the DISCUSSION-LOG as "User's choice: Override spec — deliberate, documented deviation." The AI underestimated how much the data-loss problem mattered to the grader. |
| 2.2 | `/gsd-plan-phase` (Phase 01.1) | "Plan the bidi Consume + broker-side at-least-once delivery redesign across 6 plans: proto update, lease store, send loop redesign, ack handler, reconnect-requeue, credit model + mqprobe update." | Six PLAN.md files (01.1-01 through 01.1-06). | Phase 01.1-06 (credit model) had a sizing error in the initial draft — the initial credit ceiling was set to 100 but the smoke test drove 1000 messages and a ceiling too low caused a stall. Corrected in the plan before execution. |
| 2.3 | `/gsd-execute-phase` (Phase 01.1, all plans) | (Executed 01.1-01-PLAN.md through 01.1-06-PLAN.md) | Bidi `Consume` stream, lease store with per-consumer tracking, requeue-on-disconnect, credit window, `mqprobe` bidi protocol update. `go test -race` green at `-count=50`. | **M-2 — Missed wakeup race (manual fix required after the fact):** The initial executor implementation captured `s.notifyChan()` _after_ `TryDequeue()`. A Produce in the gap closed the old notify channel and installed a new one; the consumer then waited on the new (never-notified) channel, stalling indefinitely on the last message of a burst. The race detector caught this later (during the quick-ku8 review in Stage 8) — it was _not_ caught during Phase 01.1 execution or its test suite. The fix moved `ch := s.notifyChan()` before `TryDequeue()`. |
| 2.4 | `/gsd-execute-phase` (Phase 01.1, all plans) | (same batch) | (same batch) | **M-3 — GracefulStop hang (manual fix required after the fact):** The `shutdownCh` field in `MQServer` was write-only dead code — nothing in the send loop read from it. `GracefulStop()` (which the main goroutine called on SIGTERM) does not cancel server-side stream contexts; the infinite `Consume` stream kept running, hanging the process until SIGKILL. Added in quick-ku8: `case <-s.shutdownCh` to both blocking selects in the send loop, plus a 5s timeout racing `GracefulStop` against `Stop()` in `cmd/mq/main.go`. Again, this was not caught during Phase 01.1 — it required the Stage 8 mid-assignment code review. |

---

## Stage 3 — Code: Pipeline (Streamer + Collector)

**Assignment aspect:** Code

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 3.1 | `/gsd-discuss-phase` (Phase 2) | "Phase 2: storage foundation. PostgreSQL time-series schema with composite index (gpu_id, timestamp DESC), pgxpool connection pool in pkg/db, golang-migrate for versioned DDL." | `.planning/phases/02-storage-foundation-schema-connection-pool/02-DISCUSSION-LOG.md`. Decisions: UUID in gpu_id (not ordinal), golang-migrate embedded migrations, docker-compose dev stack. | No fall-short at design stage. |
| 3.2 | `/gsd-execute-phase` (Phase 2) | (Executed 02-01, 02-02 plans) | Schema DDL, migration tool, pgxpool config, `pkg/db.FromEnv()`, `make smoke-02`. | No fall-short. |
| 3.3 | `/gsd-plan-phase` (Phase 3) | "Phase 3: Streamer reads the DCGM CSV forever, restamps `now`, publishes to MQ via gRPC Produce. Collector consumes the bidi MQ stream, acks each message after batching into Postgres via pgxpool CopyFrom." | Four PLAN.md files (03-01 through 03-04). | The plans correctly specified `ON CONFLICT DO NOTHING` for idempotent upserts. However, the collector ack-on-persist bug (C-1) was NOT caught at planning time — the plan said "ack after persist" but the implementation silently acked on every batch regardless of DB outcome (see Stage 8 for the fix). |
| 3.4 | `/gsd-execute-phase` (Phase 3) | (Executed 03-01 through 03-04 plans) | Streamer + Collector binaries, E2E test, `make smoke-03`. | **C-1 — Collector ack-on-persist data loss (most severe failure):** `persistBatch()` in `internal/collector/collector.go` had no non-nil return path. `br.Exec()` errors were logged and continued; the function ended `return nil`. Every batch was acked regardless of whether the DB write succeeded. A 30-second Postgres outage caused silent data loss — messages acked, broker discarded them, gone. This violated the project's core "no message loss" claim. The bug was present from initial implementation and was not caught by unit tests (which mocked the DB). It was discovered during the mid-assignment 4-agent code review (Stage 8). |
| 3.5 | Manual: MVCC false-fail in Phase 3 smoke | (Human observation) | Phase 3 smoke-03 initially failed on the exactly-once check: `count(*) != count(DISTINCT ...)`. | **MVCC two-query smoke false-failure:** The smoke check ran two separate SQL queries — one for `count(*)` and one for `count(DISTINCT ...)`. Between the two queries, Postgres's MVCC committed new rows (the Streamer was still running during the check). The counts diverged not because of duplicates but because the snapshot differed between queries. Human spotted this, added a `WHERE timestamp < NOW()` boundary to freeze the snapshot window. Not a bug in the application — a test harness design flaw that required human observation to diagnose. |

---

## Stage 4 — Code: API Gateway

**Assignment aspect:** Code

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 4.1 | `/gsd-plan-phase` (Phase 4) | "Phase 4: API Gateway in Go using chi router. Endpoints: GET /api/v1/gpus (sorted UUID list) and GET /api/v1/gpus/{id}/telemetry (newest-first, time-window filter, row cap). OpenAPI auto-generated by swag from annotations." | Three PLAN.md files (04-01 through 04-03). | Swag runtime version dependency was not anticipated. The initial plan used `swag v2.0.0-rc5` (the latest at the time the research was written); the stable library was `v1.16.4`. The `http-swagger/v2` UI handler was compatible only with the v1 runtime. Discovered during executor build — required manual version correction in `go.mod` before the swagger UI served. |
| 4.2 | `/gsd-execute-phase` (Phase 4) | (Executed 04-01, 04-02, 04-03 plans) | Gateway binary, chi router, `/api/v1/gpus` + `/telemetry` handlers, swag annotations, `make swagger`, Swagger UI. `make smoke-04` passing. | **M-4 — HTTP header ordering bug (silent until code review):** In `internal/gateway/handler.go`, the `X-Truncated: true` and `X-Row-Limit: N` headers were set _after_ `w.WriteHeader(http.StatusOK)`. In Go's `net/http`, calling `w.Header().Set(...)` after `WriteHeader` is silently ignored — the headers never reached the client. The bug was invisible in tests because test code decoded the body without checking headers. Discovered during mid-assignment review (Stage 8); fixed by moving header writes before the `WriteHeader` call. |

---

## Stage 5 — Code: DevOps (Docker + Helm)

**Assignment aspect:** Build environment

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 5.1 | `/gsd-discuss-phase` (Phase 5) | "Phase 5: Dockerfiles (multi-stage, distroless final), Helm umbrella chart with 4 sub-charts + Bitnami PostgreSQL, schema migration as a Helm hook Job, kind local cluster, make deploy workflow." | `.planning/phases/05-devops-quality-gates/05-DISCUSSION-LOG.md`. Decisions: `IfNotPresent` pull policy, `make deploy` composite, `pre-install,pre-upgrade` hook (initial), distroless final stage. | The initial discussion decided `pre-install,pre-upgrade` for the migrate Job hook. This was the direct cause of the deadlock (see 5.3). |
| 5.2 | `/gsd-execute-phase` (Phase 5, plans 01–04) | (Executed 05-01 through 05-04 plans) | Four Dockerfiles (multi-stage, distroless), Helm umbrella + sub-charts, `make kind-up / helm-install / kind-down`, `make smoke-05`. | No per-plan fall-short. The hook deadlock manifested at deploy time (Stage 5.3), not during plan execution. |
| 5.3 | Human: `make deploy` (kind cluster) | (Human-initiated) | **Helm install silently hung for 5 minutes and timed out.** | **Phase 5 pre-install hook deadlock (blocking human intervention required):** The migrate Job was annotated `helm.sh/hook: pre-install,pre-upgrade`. Helm runs pre-install hooks _before_ creating any regular release resource. The Job's `wait-for-postgres` init container polled `vantage-postgresql:5432` — but the Bitnami PostgreSQL StatefulSet and Service are regular release resources, so they did not exist when the pre-install hook ran. Circular wait: hook waiting for Postgres, Postgres waiting for hook to complete. `helm install` silently hung until its default 5-minute timeout. The human had to `make kind-down && make kind-up` to recover the cluster. AI then diagnosed and fixed by moving the hook to `post-install,post-upgrade` (Plan 05-05). The fix also added `activeDeadlineSeconds: 120` on the Job for bounded loud failure. |
| 5.4 | `/gsd-execute-phase` (Phase 5, plan 05) | "Fix the pre-install hook deadlock: move migrate Job to post-install,post-upgrade so Postgres Service exists before the init container polls it. Add activeDeadlineSeconds: 120 and --timeout 3m to make helm-install." | `deployments/templates/migrate-job.yaml` hook changed to `post-install,post-upgrade`; `Makefile` updated. Human re-ran `make deploy` and verified no hang. | No fall-short after the diagnosis was made. The human checkpoint (UAT) confirmed the fix. |

---

## Stage 6 — Unit Tests

**Assignment aspect:** Unit tests

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 6.1 | `/gsd-execute-phase` (all phases, TDD tasks) | (Each plan contained TDD task descriptions: "write failing tests first, then implement to make them pass") | MQ ring buffer tests (`internal/queue`), MQ server concurrency tests at `-count=50` (`internal/server`), Collector persistence tests, Gateway handler + integration tests, Streamer retry tests. ≥90% coverage maintained throughout. | TDD was followed for all new code. However, the C-1 bug (ack-on-persist) was not caught by unit tests because the mock DB returned nil from `SendBatch` by design — the test did not exercise the error path. A "test passes when it should catch the bug" situation that required a higher-level review to surface. |
| 6.2 | `/gsd-execute-phase` (Phase 01.1, MQ concurrency tests) | "Write -race -count=50 tests for the ring buffer and server: no-loss on disconnect, no over-pull beyond credit, unique steady-state delivery, safe ack handling (unknown ids are no-ops), no goroutine leaks." | Comprehensive concurrency test suite at `internal/server/server_test.go`. `-race` always green. | M-2 (missed-wakeup) was present in the codebase during this phase but was not caught by the concurrency tests written at the time. The tests exercised the happy path with no inter-arrival delay between Produce and Consume; the missed-wakeup only manifested when a Produce happened in the specific window between `TryDequeue` and `notifyChan()`. A targeted regression test `TestMQ_MissedWakeup_SingleConsumer` (produce after the consumer is blocked waiting, assert delivery) was added in Stage 8. |
| 6.3 | `/gsd-execute-phase` (quick-260702-ku8, Task 1 TDD) | "RED test for C-1: inject a DB error into persistBatch, assert that no ack is sent and the error propagates." | `TestPersistBatchError_NoAcks` written as RED, then persistBatch fixed to return error on Exec failure (GREEN). | **Context panic in RED test (auto-fixed):** The initial RED test used `context.WithTimeout` for both testcontainers setup and the `Consume` call. After the timeout expired, cleanup ran and `testCtr.MustConnectionString` panicked. Fix: `context.Background()` for `restoreDB` setup, a separate `consumeCtx` (3s) only for the Consume call. Handled by the executor under Rule 1 (auto-fix). |
| 6.4 | `/gsd-execute-phase` (quick-260702-ku8, Task 2 TDD) | "RED test for M-3: start MQ server, open a Consume stream, call srv.Shutdown(), assert the server exits within a bounded time." | `TestMQ_ShutdownUnderActiveStream` added. | **M-3 test hung on recv goroutine (auto-fixed):** After the send loop returned via `<-s.shutdownCh`, the test's recv goroutine was stuck on `stream.Recv()` (Background context, no cancellation). Production `grpcSrv.GracefulStop()` cancels stream contexts; the unit test tested in isolation without that. Fix: cancellable stream context in the test; cancel immediately after `srv.Shutdown()`. Handled by executor under Rule 3 (blocking issue). |
| 6.5 | `/gsd-execute-phase` (Phase 6, health + logger tests) | "Write tests for /healthz + /readyz handlers; pkg/logger; streamer/collector Runner struct readiness." | Health handler tests in `internal/http/`, `internal/gateway/`, `internal/streamer/`, `internal/collector/`. Logger test in `pkg/logger/`. | No fall-short. Coverage gate maintained at ≥90%. |

---

## Stage 7 — Build Environment / Toolchain

**Assignment aspect:** Build environment

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 7.1 | `/gsd-plan-phase` (Phase 1) | "Set up Makefile with targets: proto (buf generate), build (go build all services), test (-race -cover), coverage (≥90% gate), swagger (swag init), lint (golangci-lint), docker, kind-up/kind-down, helm-install." | Makefile with all targets. `make help` lists them. | **`make lint` silently swallowed errors:** The initial Makefile used `golangci-lint run ./... 2>/dev/null || go vet ./...`. Any non-zero exit from lint (including real findings) fell back to `go vet` silently. This masked 5 pre-existing `errcheck` issues that only became visible in Stage 8 when the lint fallback was fixed. Fix: `if command -v golangci-lint >/dev/null; then golangci-lint run ./...; else go vet ./...; fi`. |
| 7.2 | `/gsd-plan-phase` (Phase 1 + Phase 4) | "Proto toolchain via buf with remote BSR plugins. Swag toolchain: swag init -g cmd/gateway/main.go -o pkg/docs. Generated files committed." | `api/proto/buf.gen.yaml`, `make proto`, `make swagger`. Generated `pkg/pb/` and `pkg/docs/` committed. | **buf remote plugin call fails with no network:** During the first `make proto` run, `buf generate` tried to contact `buf.build` to pull remote plugins. In an offline environment (or rate-limited), this failed silently. Fallback: committed `pkg/pb/` generated output so CI does not need `make proto` at all. |
| 7.3 | Manual: Docker socket config | (Human) | Set `DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock` and `TESTCONTAINERS_RYUK_DISABLED=true` in `.env` for Rancher Desktop. | The Makefile's `.env` auto-creation logic (create empty `.env` if absent) interacted badly with `check-env`: CI that didn't pre-create `.env` got an empty `DOCKER_HOST` and failed `check-env`. **Phase 6 CI workflow** (`.github/workflows/ci.yml`) added a "Prepare .env for CI" step before the first `make` call to pre-create `.env` with `DOCKER_HOST=/var/run/docker.sock`. |
| 7.4 | `/gsd-execute-phase` (Phase 5, Dockerfiles) | "Write multi-stage Dockerfiles for each service: golang:1.26-alpine builder, distroless/static-debian12 final, CGO_ENABLED=0." | Four service Dockerfiles + `build/migrate.Dockerfile`. `make docker` builds all five images. | No fall-short. The distroless base meant no shell in the final image — this is intentional. The kind image load (`make kind-load`) is separate from build on purpose. |
| 7.5 | `/gsd-execute-phase` (Phase 6, Plan 07) | "Create .github/workflows/ci.yml: push + PR trigger; install golangci-lint via go install; Prepare .env step BEFORE make build; run make build, make test, make coverage, make lint." | `.github/workflows/ci.yml`. | No fall-short at plan execution. The `GITHUB_PATH` append for golangci-lint was added inline by the executor to ensure the binary was on PATH for `make lint`. |

---

## Stage 8 — Code Review + Bug Fix

**Assignment aspect:** Code + Unit tests

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 8.1 | `/gsd-quick` (260702-ku8 — 4-agent review) | "Run a mid-assignment 4-agent code review of Phases 1–4 against the assignment brief. One agent per subsystem (MQ, pipeline, gateway, deliverables). Each agent produces findings; orchestrate, deduplicate, and prioritize." | Findings document at `.planning/quick/260702-ku8-*/260702-ku8-REVIEW-FINDINGS.md`. 1 CRITICAL, 6 MAJOR, 3 MEDIUM, 9 MINOR findings. | The review found C-1 (already missed by Phase 3 and 01.1 execution) and M-2, M-3, M-4 (already missed by their respective phase executions). The AI execution passes did not catch these because: (a) unit tests mocked the error paths, (b) the race condition required a specific timing gap, (c) the shutdown hang only appeared under SIGTERM/GracefulStop, and (d) the header bug was silent in the response body. The 4-agent review found them through static analysis and specification comparison. |
| 8.2 | `/gsd-execute-phase` (quick-260702-ku8, Task 1) | "Fix C-1: drain all Exec calls in persistBatch capturing firstErr; return error without acking; flush treats the stream as failed on error. Fix M-6: streamer retry with exponential backoff. Fix G-1: collector backoff reset only after Consume survives >5s. TDD for C-1 first." | C-1 fixed; streamer retry added; collector backoff corrected. Tests: `TestPersistBatchError_NoAcks`, `TestStream_RetryOnProduceError`. | **Go compiler: defer references `ack` before declaration (auto-fixed):** The initial fix for disconnect-cleanup reorder used `defer ack(id)` but `ack` was a closure declared later in the function body. Compiler rejected it. Fix: inline the ack logic directly in the defer. Auto-fixed by executor under Rule 3. |
| 8.3 | `/gsd-execute-phase` (quick-260702-ku8, Task 2) | "Fix M-2: move ch := s.notifyChan() BEFORE TryDequeue(). Fix M-3: add case <-s.shutdownCh to both blocking selects; race GracefulStop against 5s timeout in main. Reorder disconnect cleanup (drain ackCh first, then requeue). TDD for M-2 and M-3." | M-2 and M-3 fixed. Tests: `TestMQ_MissedWakeup_SingleConsumer`, `TestMQ_ShutdownUnderActiveStream`. `go test -race ./internal/server/... -count=50` green. | No additional fall-short beyond the recv-goroutine hang in the M-3 test (documented in Stage 6.4 above). |
| 8.4 | `/gsd-execute-phase` (quick-260702-ku8, Task 3) | "Fix M-4: set X-Truncated + X-Row-Limit headers BEFORE WriteHeader; add @Header swag annotations; regenerate make swagger. Create ADR-002 for M-5 natural-key collision. Create docs/AI_USAGE.md for G-4." | M-4 fixed; ADR-002 created; `docs/AI_USAGE.md` created (summary-level AI usage doc). | **G-2 lint unmasking (auto-fixed):** The G-2 lint fix made `golangci-lint` errors visible. Five existing `defer close()` patterns across unrelated files failed `errcheck`. Added `//nolint:errcheck` — standard Go pattern for cleanup resources where errors are non-actionable. |

---

## Stage 9 — Production Hardening (Phase 6)

**Assignment aspect:** Code, build environment, unit tests

| Step | Tool / Command | Representative Prompt | Outcome | Where it fell short / Manual intervention |
|------|---------------|----------------------|---------|-------------------------------------------|
| 9.1 | `/gsd-plan-phase` (Phase 6) | "Phase 6: close nine audit findings — health endpoints on all services, Helm probes/resources/HPA, pagination, CI workflow, structured logging (log/slog), README accuracy, AI prompt log." | Eight PLAN.md files (06-01 through 06-08). Wave-based: slog first (06-01), then health endpoints (06-02 gateway, 06-03 MQ, 06-04 streamer, 06-05 collector), then Helm (06-06), CI (06-07), docs (06-08). | No fall-short. All nine findings had clear enough specs. |
| 9.2 | `/gsd-execute-phase` (Phase 6, Plan 01 — slog) | "Migrate all stdlib log.* calls to log/slog with JSON handler, level via LOG_LEVEL env, per-service `service` attribute. Add pkg/logger helper. Keep DSN-never-logged rule." | `pkg/logger`, `slog.SetDefault` in all four cmd mains, 27 log calls migrated. | No fall-short. The slog.SetDefault bridge (forwarding stdlib log.* to the slog handler) was correctly applied as a safety net for any missed migration sites. |
| 9.3 | `/gsd-execute-phase` (Phase 6, Plan 02 — gateway pagination + health) | "Add limit/offset params to GET /api/v1/gpus/{id}/telemetry with TelemetryPage envelope and PaginationMeta. Health endpoints /healthz + /readyz on gateway chi router. OFFSET pushed into both SQL paths. Regenerate swagger." | Gateway pagination + health working. `pkg/docs` regenerated. Tests updated. | No fall-short. The limit+1 sentinel-row approach for `has_next` detection was correctly implemented without an off-by-one. |
| 9.4 | `/gsd-execute-phase` (Phase 6, Plans 03/04/05 — MQ/streamer/collector health) | "MQ: register /healthz + /readyz on existing HTTP ServeMux. Streamer: add Runner struct with atomic readiness + STREAMER_HEALTH_ADDR listener. Collector: add Runner struct with markReady callback + COLLECTOR_HEALTH_ADDR listener." | Health endpoints on all four services. Ports: MQ :8080, gateway :8080, streamer :9000, collector :9001. | Rule 1 auto-fix in Plan 05: `pkg/db/read_test.go` had calls to `db.Telemetry` without the new `offset` arg (signature updated in Plan 02 but tests not updated). Auto-fixed by executor. |
| 9.5 | `/gsd-execute-phase` (Phase 6, Plan 06 — Helm probes/resources/HPA) | "Wire livenessProbe/readinessProbe in all four sub-chart deployment templates. Resource requests/limits per service. Gateway values-gated autoscaling/v2 HPA (default off). MQ replicas:1 + Recreate stays hardcoded." | All four charts updated. HPA template created. `helm template` renders valid YAML. | No fall-short. |
| 9.6 | `/gsd-execute-phase` (Phase 6, Plan 07 — CI) | "Create .github/workflows/ci.yml with push + PR trigger, golangci-lint install, Prepare .env step, make build/test/coverage/lint." | CI workflow created. | No fall-short. |

---

## Failure Summary (Where Prompts Fell Short)

| ID | Phase | Failure | How Detected | Manual Intervention |
|----|-------|---------|-------------|---------------------|
| C-1 | Phase 3 (Collector) | `persistBatch` returned nil on DB error — every batch acked regardless of persist outcome. Silent data loss. | Mid-assignment 4-agent code review (Stage 8) | Human-organized code review; human verified fix in test + gate |
| M-2 | Phase 01.1 (MQ) | `notifyChan()` captured after `TryDequeue()` — missed wakeup race: last message of a burst stalled. | Race detector output in Stage 8 review | Review findings doc; targeted regression test |
| M-3 | Phase 01.1 (MQ) | `shutdownCh` was write-only dead code; `GracefulStop()` hung on live streams. | Stage 8 code review (specification comparison) | Race against 5s timeout + `Stop()` fallback |
| M-4 | Phase 4 (Gateway) | `X-Truncated`/`X-Row-Limit` headers set after `WriteHeader` — silently dropped. | Stage 8 code review | Move header writes before `WriteHeader`; verified by test |
| Phase-5 Hook | Phase 5 (DevOps) | Pre-install hook circular wait: migrate Job polled Postgres Service before it existed. Silent 5-min hang. | Human ran `make deploy` and observed hang | Human recovery (`make kind-down && make kind-up`); moved to `post-install,post-upgrade` |
| MVCC Smoke | Phase 3 | Smoke exactly-once check false-failed due to MVCC two-query snapshot divergence. | Human observed smoke output | Human added `WHERE timestamp < NOW()` freeze boundary |
| Lint Swallow | Phase 1 | `make lint` silently fell back to `go vet` on any lint error. | Stage 8 review (G-2) | Changed branch condition to check binary presence not exit code |

---

*Sources: `.planning/phases/*/` PLAN.md, SUMMARY.md, DISCUSSION-LOG.md files; `.planning/quick/260702-ku8-*/`; `docs/adr/ADR-001-bidi-at-least-once-delivery.md`; `docs/adr/ADR-002-natural-key-microsecond-collision.md`.*

*See also: [`docs/AI_USAGE.md`](AI_USAGE.md) for scope, oversight practices, and model details.*
