# Project Research Summary

**Project:** vantage — Elastic GPU Telemetry Pipeline (custom in-memory MQ)
**Domain:** Go distributed systems / telemetry ingestion + custom message broker
**Researched:** 2026-06-27
**Confidence:** HIGH

## Executive Summary

This is a spec-driven, low-ambiguity distributed-systems project. The technology selection is mature
and uncontroversial; the entire difficulty is concentrated in **correct concurrent message delivery** —
each message delivered to exactly one consumer, with no loss and no duplication — through a custom
in-memory message queue built from native Go channels and sync primitives.

The recommended approach is strict dependency-driven phasing: the proto contract and MQ core come
first (and must be race-tested before anything connects to them), then the Streamer and Collector are
built against that core, then the API Gateway (which is off the critical path and only reads
PostgreSQL), with DevOps/Helm packaging in parallel. Proving the concurrency path early de-risks every
downstream integration.

The dominant risks are all in the MQ and its operations: data races, goroutine leaks on consumer
disconnect, deadlocks from holding a lock across a channel send, unbounded memory growth without a
drop policy, gRPC server-stream "silent death" without keepalives, and a split-brain if the
single-replica in-memory MQ is ever rolled rather than recreated. These translate directly into phase
exit criteria.

## Key Findings

### Recommended Stack

Standard, current Go ecosystem — no exotic dependencies. (Full detail + versions in `STACK.md`.)

**Core technologies:**
- **buf v1.71.0** (proto gen/lint via remote plugins) — over raw `protoc`; reproducible, no local plugin installs *(decision flag — current Makefile defaults to protoc)*
- **grpc-go v1.81.1 + google.golang.org/protobuf v1.36.11** — wire transport; never import deprecated `github.com/golang/protobuf`
- **jackc/pgx/v5 v5.10.0 + `pgxpool.CopyFrom`** — bulk insert via COPY protocol, 5–10× faster than `pgx.Batch`/multi-row INSERT
- **chi/v5 v5.3.0** for the API Gateway (path vars, net/http-compatible); stdlib `net/http` ServeMux for the MQ's single control-plane endpoint
- **swaggo/swag v1.16.6** (stable) — OpenAPI from annotations; NOT v2.0.0-rc5
- **testcontainers-go v0.43.0 + testify v1.11.1** — Postgres integration tests via `TestMain`
- **Docker:** `golang:1.26-alpine` builder → `distroless/static-debian12` final (CGO_ENABLED=0)
- **Helm:** Bitnami PostgreSQL chart via OCI pull (confirm appVersion at chart time)

### Expected Features

(Full breakdown in `FEATURES.md`.) Every required feature maps to an explicit line in `instructions.md` —
all are table stakes; nothing is deferred.

**Must have (table stakes):**
- MQ: `Produce` (unary), `Consume` (server-stream), thread-safe in-memory buffer, **unique delivery** to concurrent consumers (work-queue), `GET /api/v1/queue/inspect`
- Streamer: indefinite CSV loop, timestamp restamp (`time.Now().UTC()`), gRPC Produce client, up to 10 instances, DCGM 12-column parse
- Collector: long-lived Consume stream with reconnect, pgxpool batch insert, time-series schema, composite index `(gpu_id, timestamp DESC)`
- Gateway: `GET /api/v1/gpus`, `/gpus/{id}/telemetry`, `?start_time=&end_time=`, swag OpenAPI
- DevOps/QA: multi-stage Dockerfiles, Helm sub-charts, Makefile (proto/build/test/coverage/swagger), `go test -race`, ≥90% coverage gate

**Should have (differentiators, P2):** configurable drop policy, malformed-CSV skip-and-log, configurable batch size, K8s health probes, graceful drain, gRPC retry backoff, richer `/inspect` state.

**Out of scope (anti-features):** third-party brokers, MQ disk persistence, clustering, at-least-once ACK/NAK, consumer groups, replay, auth, hand-written OpenAPI, pagination, multi-region.

### Architecture Approach

(Full detail in `ARCHITECTURE.md`.) Single Go module; four service entrypoints under `cmd/`; the only
shared surface is `pkg/` (`pb` wire types shared by MQ/Streamer/Collector; `models` DB types shared by
Collector/Gateway). MQ runs dual-protocol in one process (gRPC data plane + `net/http` control plane
sharing the queue struct by pointer). API Gateway talks only to PostgreSQL.

**Major components:**
1. **MQ** — in-memory queue + gRPC Produce/Consume + HTTP inspect; the load-bearing concurrency core
2. **Streamer** — CSV → restamp → gRPC Produce client (horizontally scalable to 10)
3. **Collector** — gRPC Consume stream → `pgxpool.CopyFrom` batch insert
4. **API Gateway** — chi REST over PostgreSQL, swag-documented (off critical path)
5. **PostgreSQL** — single source of truth; `TIMESTAMPTZ` + composite index

### Critical Pitfalls

(20 catalogued in `PITFALLS.md`, with warning signs + verification per phase. Top items:)

1. **MQ data races / lost-or-duplicated messages** — use work-queue (channel) semantics; verify `go test -race -count=50` clean and N produced = N consumed across K consumers.
2. **Deadlock holding a lock across a channel send** — release locks before sending; separate buffer lock from subscriber-list lock; verify under `-race -timeout`.
3. **Goroutine leak on consumer disconnect** — honor stream context cancellation + `defer Unsubscribe()`; verify goroutine count stable.
4. **gRPC server-stream silent death** — configure keepalives on server AND client; verify a 10-min idle stream survives.
5. **`pgx.Batch` instead of `pgxpool.CopyFrom`** — use CopyFrom; verify throughput. Also: `TIMESTAMPTZ` not `TIMESTAMP`, and `EXPLAIN` must show the composite index used at scale.
6. **MQ `RollingUpdate` split-brain** — single in-memory replica must use Deployment `strategy: Recreate`, `replicas: 1`.

## Implications for Roadmap

Suggested 4-phase structure following the strict dependency chain `proto → MQ core → Streamer+Collector → Gateway / DevOps`.

### Phase 1: Foundation — Proto Contract + MQ Core
**Rationale:** The proto contract is the dependency root (nothing compiles without `pkg/pb`); the MQ is the load-bearing concurrency component everything chains onto.
**Delivers:** `api/proto/mq.proto` + generated stubs; MQ gRPC server (Produce unary, Consume server-stream); HTTP `/api/v1/queue/inspect`; race-tested in-memory queue.
**Avoids:** All Phase-1 concurrency pitfalls (races, leaks, deadlock, loss/dup, unbounded memory).
**Exit gate:** `go test -race -count=50` clean; delivery-count test (N produced = N consumed across K consumers); ≥90% coverage; MQ Docker image builds.

### Phase 2: Pipeline — Streamer + Collector + Storage
**Rationale:** Depends on Phase 1's contract + running MQ. Streamer and Collector are independent and built in parallel.
**Delivers:** Streamer (CSV loop, restamp, Produce client, rate limiter, 10-instance support); Collector (Consume stream, `pgxpool.CopyFrom`, graceful shutdown); PostgreSQL schema + composite index + migrations; end-to-end integration tests.
**Uses:** pgx/v5, pgxpool, testcontainers-go.
**Exit gate:** end-to-end CSV→MQ→Collector→Postgres test; `EXPLAIN` confirms index usage; concurrent-collector unique-delivery test; ≥90% coverage; images build.

### Phase 3: API Gateway + Docs
**Rationale:** Reads only PostgreSQL — off the critical path; can run in parallel with Phase 4 once the schema exists.
**Delivers:** 3 REST endpoints (list GPUs, telemetry by id, time-window filter); chi router; swag annotations → generated OpenAPI; integration tests against a seeded Postgres.
**Exit gate:** endpoints return correct payloads/status; `swag init` produces valid spec served by Swagger UI; queries use the index; ≥90% coverage.

### Phase 4: DevOps + Quality Gates
**Rationale:** Packaging/orchestration; parallelizable with Phase 3 after the pipeline exists.
**Delivers:** multi-stage Dockerfiles (4 services), Helm umbrella + 5 sub-charts (incl. Bitnami postgres), full Makefile, kind verification.
**Implements:** MQ `strategy: Recreate` + `replicas: 1`; per-service resource limits; distinct readiness/liveness probes; secrets via K8s Secret.
**Exit gate:** images small + build; `helm install` into kind succeeds, all pods Running; ~10-min soak stable; coverage gate ≥90%; no plaintext secrets.

### Phase Ordering Rationale

- Proto/MQ first because it is the import root and the only HIGH-complexity component; everything else is LOW–MEDIUM and chains onto it.
- Streamer + Collector are parallelizable within Phase 2 (independent client roles sharing only the contract).
- Gateway (Phase 3) and DevOps (Phase 4) parallelize once schema + services exist.
- Concurrency correctness is a Phase-1 exit criterion, not a later QA add-on — retrofitting race safety is far costlier.

### Research Flags

All four phases use well-documented, standard patterns. No phase has a research gap that blocks
planning — proceed directly to roadmap and per-phase planning. (Per-phase research is still enabled in
config and may add value for MQ concurrency design specifics in Phase 1.)

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | MEDIUM | Versions verified against pkg.go.dev / release pages (Jun 2026); Bitnami chart appVersion to confirm |
| Features | HIGH | Source is the authoritative brief; all table-stakes, no ambiguity |
| Architecture | HIGH | Go memory model + gRPC patterns are language/spec-guaranteed |
| Pitfalls | HIGH | 20 documented with phase mapping + verification methods |

**Overall confidence:** HIGH

### Gaps / Decisions to Address During Planning

- **MQ internal primitive:** buffered Go channel (competing-consumers, recommended by Architecture research) vs ring buffer + `sync.RWMutex` (suggested verbatim in `instructions.md`). Delivery semantics are **work-queue** (each message to exactly one consumer) — confirmed by the spec line "multiple Collectors … receive unique messages"; NOT pub-sub. Resolve in the Phase-1 plan.
- **Proto tooling:** buf (Stack research) vs raw protoc (current Makefile default). Resolve in Phase 1 / DevOps.
- **Produce backpressure on full queue:** return `ResourceExhausted` (non-blocking, recommended) vs block the caller. Decide before finalizing the Phase-1 plan.
- **Drop policy when buffer full:** drop oldest (recommended for telemetry) vs reject newest.
- **Collector batch size / flush interval & Streamer rate limit:** tune empirically against the DCGM CSV row rate in Phase 2 before fixing Helm values in Phase 4.

## Sources

### Primary (HIGH confidence)
- `instructions.md` — authoritative assignment brief (features, endpoints, constraints)
- Official docs (gRPC, jackc/pgx, swaggo/swag, PostgreSQL) verified during research

### Secondary (MEDIUM confidence)
- pkg.go.dev / GitHub release pages — library versions (verified Jun 2026)
- Community consensus on buf vs protoc, CopyFrom vs Batch, distroless base images

### Tertiary (LOW confidence)
- ArtifactHub Bitnami PostgreSQL chart version — appVersion to confirm at chart time

---
*Research completed: 2026-06-27*
*Ready for roadmap: yes*
