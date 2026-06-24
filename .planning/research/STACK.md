# Stack Research

**Domain:** Custom durable message queue + GPU telemetry pipeline (Go, K8s) — *implementation-level* libraries/idioms within an already-locked stack
**Researched:** 2026-06-24
**Confidence:** HIGH (versions verified against the Go module proxy `proxy.golang.org` and current upstream releases; not training-data recall)

> **Scope guard.** The core stack is frozen by ADR-0001…0005 (custom segment-log MQ, gRPC streaming, PostgreSQL/pgx, Docker/K8s/Helm, kind, slog, testify, Prometheus client, gobco). This file does **not** re-litigate those. It picks the *implementation-level* libraries, APIs, and idioms **inside** that stack, and resolves the two OPEN tooling decisions (OpenAPI generator, HTTP router). For self-built subsystems (segment log, WAL), the "stack" is borrowed *patterns* from Kafka/etcd/NATS — not added dependencies (ADR-0001 forbids off-the-shelf broker code).

## Recommended Stack

### Core Technologies (verified versions)

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `google.golang.org/grpc` | **v1.81.1** | MQ transport (Produce/Consume/Commit) | Already locked (ADR-0004). Per-stream HTTP/2 flow control gives backpressure *for free* — `SendMsg` blocks when the peer's window is full. Current go.mod pins v1.71.0; bump to v1.81.1 for current keepalive/flow-control fixes. |
| `google.golang.org/protobuf` | **v1.36.11** | Message serialization | Locked. go.mod has v1.36.6 → bump to v1.36.11. |
| `github.com/jackc/pgx/v5` | **v5.10.0** | PostgreSQL driver + pool | Locked (ADR-0002). **Note the `/v5` module path** — the bare `github.com/jackc/pgx` path is the abandoned v3. Use `pgxpool` (not `database/sql`) for native batching/COPY and lower allocation overhead. |
| Go | **1.26** | Language/runtime | Locked. `log/slog`, `encoding/csv`, `os.File.Sync` are all stdlib — no deps for logging or CSV. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/prometheus/client_golang` | **v1.23.2** | Metrics exposition on every service | Locked. `promauto` + `promhttp.Handler()`. `*Vec` collectors are goroutine-safe — call directly from broker goroutines, no extra mutex. |
| `github.com/stretchr/testify` | **v1.x (latest)** | Assertions/mocks in tests | Locked. `require` for fatal preconditions, `assert` for soft checks. Pair with `synctest` (Go 1.25+ stdlib `testing/synctest`) for deterministic concurrency tests of the broker. |
| `github.com/go-chi/chi/v5` | **v5.3.0** | HTTP router for apigateway | **RESOLVES OPEN DECISION** (see below). Note `/v5` path. |
| `github.com/oapi-codegen/oapi-codegen/v2` | **v2.7.1** | OpenAPI spec → typed Go server | **RESOLVES OPEN DECISION** (see below). Note `/v2` path. |
| `golang.org/x/time/rate` | latest | Streamer produce-rate control | `rate.Limiter` (token bucket) is the idiomatic way to pace the CSV replay loop. Stdlib-adjacent, zero-risk. |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `buf` | proto lint + codegen | Already wired (`make proto`). Keep. |
| `oapi-codegen` (binary via `go tool` / `go run`) | Generate API server from `openapi.yaml` | Wire into a `make openapi` target with `//go:generate`. See Makefile integration below. |
| `golangci-lint v2`, `gobco` | lint + branch coverage | Locked. gobco drives the 100% branch gate on `internal/`. |

## OPEN DECISION 1 — OpenAPI generator: **oapi-codegen** (recommended)

**Recommendation: `oapi-codegen/v2` v2.7.1 (spec-first).** Confidence: HIGH.

| | oapi-codegen v2.7.1 | swaggo/swag |
|---|---|---|
| Approach | **Spec-first** — write `openapi.yaml`, generate typed server interface + models | **Code-first** — magic `// @...` comments on handlers, generate spec |
| OpenAPI version | **3.0 / 3.1** (current) | Stable v1.16.6 emits **only Swagger 2.0**. v2 (3.1) is still **release-candidate** (v2.0.0-rc5, Jan 2026) — *not stable as of June 2026* |
| Generated artifact | Server boilerplate + request/response types from the contract | Spec + Swagger-UI assets from comments |
| Fit for chi | First-class `chi-server` target | Framework-agnostic comments |

**Why oapi-codegen wins here:**
1. **The deliverable is the spec.** PROJECT.md requires an *auto-generated OpenAPI spec via a Makefile target*. With swaggo the spec is a derived byproduct of comments and pinned to the obsolete 2.0; with oapi-codegen the spec is the **source of truth**, hand-authored as modern OpenAPI 3.x, and the Go types/handlers are the generated artifact. That is the cleaner story for a grading rubric that explicitly values "auto-generated OpenAPI."
2. **swaggo stable is 2.0-only.** Shipping a 2.0 ("Swagger") spec in 2026 reads as dated. The 3.1 path is RC-quality.
3. **Tiny read-only surface.** Only two endpoints — hand-writing a ~60-line `openapi.yaml` is trivial and gives a contract you can publish directly. The spec-first overhead that hurts on huge APIs is negligible here.
4. **Type safety.** oapi-codegen generates a `ServerInterface` you implement; the compiler then guarantees handlers match the contract.

**Choose swaggo instead only if** you specifically want zero standalone spec file and are willing to accept Swagger 2.0 output. Not recommended for this project.

### Makefile integration (oapi-codegen)

```makefile
# apigateway: openapi.yaml is the source of truth; types+server are generated.
openapi:
	cd apigateway && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
	  -config oapi-codegen.yaml openapi.yaml
```

```yaml
# apigateway/oapi-codegen.yaml
package: api
generate:
  chi-server: true   # matches the chosen router
  models: true
  embedded-spec: true   # serve the spec at /openapi.yaml for the deliverable
output: gen.go
```

Pin the tool in `apigateway/go.mod` via the Go 1.24+ `tool` directive (`go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen`) so the version is reproducible in CI.

## OPEN DECISION 2 — HTTP router: **chi** (recommended)

**Recommendation: `go-chi/chi/v5` v5.3.0.** Confidence: HIGH.

| | chi v5.3.0 | gin v1.12.0 |
|---|---|---|
| Handler type | **stdlib `http.HandlerFunc`** | custom `gin.Context` |
| Weight | Thin router + composable middleware, ~no deps | Full framework (binding, validation, rendering) |
| Fit | Small **read-only** JSON API | Larger APIs needing form/JSON binding + validation |
| oapi-codegen target | native `chi-server` | native `gin-server` (also exists) |

**Why chi wins here:**
1. **stdlib-native handlers** — `func(w http.ResponseWriter, r *http.Request)`. No framework lock-in, trivial to test with `httptest`, and slog/Prometheus middleware compose as plain `http.Handler` wrappers.
2. **The API is read-only and tiny** — `GET /api/v1/gpus` and `GET /api/v1/gpus/{id}/telemetry` with `start_time`/`end_time` query params. gin's binding/validation/rendering machinery is dead weight for two GET handlers.
3. **Clean oapi-codegen pairing** — the `chi-server` generator emits a `ServerInterface` mounted on a `chi.Mux`; idiomatic and well-trodden.
4. **Middleware story** — chi's `middleware` package (RequestID, Recoverer, Timeout) plus a custom slog access-logger covers everything needed.

**Choose gin instead only if** the API later grows write endpoints with heavy request-body validation. Not the case in scope.

## Subsystem implementation guidance (within locked stack)

### 1. Durable append-only segment log + WAL (the core — ADR-0001)

Patterns to **borrow** (no deps added):

- **Segmentation + sparse offset index (Kafka model).** Each partition = a directory of fixed-size segments: `<baseOffset>.log` (records) + `<baseOffset>.index` (sparse offset→file-position map). Roll a new segment at a size/age threshold. Recovery = scan the **active** (last) segment forward, rebuild the in-memory index, truncate any torn trailing record. Older sealed segments are immutable — index them lazily.
- **Record framing.** Length-prefixed frames: `[u32 length][u32 crc32c][payload]`. CRC lets recovery detect a torn final write from a crash mid-fsync and truncate to the last intact record. This is how Kafka and etcd's WAL both guarantee a clean tail.
- **fsync strategy = the central durability knob.** PROJECT.md mandates "survive crash with no message loss," so the producer ack path must be **fsync-before-ack**: append → `file.Sync()` → assign offset → send `ProduceResponse`. Use `os.File.Sync()` (stdlib). Two levers to keep throughput sane under that constraint:
  - **Group commit / batched fsync:** accumulate appends from concurrent Produce streams, do **one** `Sync()` for the batch, then ack the whole batch. This is the single most important throughput optimization and directly mirrors etcd/Postgres group-commit. Amortizes the ~expensive fsync across many records.
  - Preallocate segment files (`file.Truncate(size)`) to avoid metadata fsyncs on growth.
- **mmap vs buffered IO — recommendation: buffered `os.File` + explicit `Sync()` for the write path; mmap only (optionally) for read/index lookup.** Rationale: mmap'd writes give you no control over *when* dirty pages hit disk (the kernel decides), which fights the fsync-before-ack durability contract and makes crash semantics murky (SIGBUS on I/O error mid-access). A `bufio.Writer` over `os.File` with an explicit `Sync()` is simpler, has deterministic durability, and is what Kafka effectively does (it relies on page cache + fsync, not mmap, for the log). Reserve mmap for the read-side index where random access dominates and durability isn't at stake.
- **etcd `wal` package** is the canonical Go reference for a CRC-framed, segmented, crash-safe WAL — read it for the framing/recovery *idioms*; do not import it (ADR-0001).
- **Offset/consumer-group state** is itself just another durable record stream — commit offsets to a small per-(topic,group) log (or a checkpoint file fsync'd on Commit), rebuilt on restart. Don't put it in Postgres (Out-of-Scope: no DB-backed queue).

Confidence: HIGH on patterns (well-established); the *implementation depth* (full WAL vs bounded) is a flagged build-time decision in PROJECT.md.

### 2. gRPC streaming for the broker (ADR-0004, contract exists)

- **Consume = server-streaming** (already in the proto: `Consume(ConsumeRequest) returns (stream ConsumeResponse)`). Correct choice — the broker pushes; the existing **separate** `Commit` unary RPC carries acks. Keep them split (don't fold acks into a bidi Consume) — it keeps at-least-once offset tracking explicit and matches the existing contract.
- **Produce = bidirectional** (already in the proto) — lets the client pipeline many records and receive per-record `(partition, offset)` acks without a round-trip per message.
- **Backpressure is automatic.** Don't build a custom credit system. gRPC-Go rides HTTP/2 per-stream flow control: when a slow consumer stops reading, the broker's `stream.Send()` blocks once the window fills. That blocking *is* the backpressure that makes producers-greater-than-consumers grow the **on-disk log** rather than broker memory — exactly the required behavior. The disk-growth part is your segment log; the memory-bound part is gRPC's window. Tune the window with `grpc.InitialStreamWindowSize` / `grpc.InitialConnWindowSize` if throughput testing shows the default 64 KB window is the bottleneck.
- **Keepalive** — set `keepalive.ServerParameters{Time, Timeout}` and a matching `keepalive.EnforcementPolicy{MinTime, PermitWithoutStream:true}` so idle long-lived Consume streams aren't reaped and clients don't get `ENHANCE_YOUR_CALM`/GOAWAY. This bites long-idle consumers if skipped.
- **Graceful shutdown** — `Server.GracefulStop()` on SIGTERM (K8s preStop) drains in-flight streams; guard with a timeout then `Stop()`. Critical for "no message loss on restart": flush+fsync the active segment in the shutdown path *before* GracefulStop returns.
- **Chunking** — DCGM rows are tiny; one row per `Message` is fine. Keep `grpc.MaxRecvMsgSize` default. No manual chunking needed.

Confidence: HIGH.

### 3. PostgreSQL idempotent bulk upsert with pgx (collector)

Conflict target is the natural key `(uuid, metric_name, ts)` — declare it as a `UNIQUE` constraint / PK on the `telemetry` fact table so `ON CONFLICT` has an arbiter.

- **Primary recommendation: `pgx.Batch` of parameterized `INSERT ... ON CONFLICT (uuid, metric_name, ts) DO UPDATE/DO NOTHING`.** One `SendBatch` pipelines the whole batch in a single network round-trip while preserving conflict handling. This is the idiomatic pgx pattern for idempotent bulk writes and directly satisfies the at-least-once → idempotent-collector contract. Use `DO UPDATE SET value = EXCLUDED.value` (last-write-wins) — re-delivery of the same row is a harmless no-op overwrite.
- **`CopyFrom` is faster but cannot do `ON CONFLICT`** (COPY protocol has no upsert). **Verified** against pgx issues #437/#992. If raw ingest throughput becomes the bottleneck under the 10×10 load test, use the **staging-table pattern**: `CopyFrom` into a `TEMP` table, then `INSERT INTO telemetry SELECT ... FROM staging ON CONFLICT DO NOTHING`. This keeps COPY speed *and* idempotency. Treat this as an optimization to reach for only if `pgx.Batch` doesn't hit the throughput target — start with Batch (simpler, already idempotent).
- **Pooling: `pgxpool`** (not `pgx.Conn` directly, not `database/sql`). Size `MaxConns` ≈ collector concurrency; each collector instance gets its own pool. `pgxpool` is concurrency-safe and acquires/releases per query.
- **GPU dimension upsert** — `gpus` table gets the same treatment: `INSERT ... ON CONFLICT (uuid) DO NOTHING` (or `DO UPDATE` to refresh hostname/model). Do it in the same batch as telemetry for the row's GPU.
- Wrap each batch in a transaction so a partial failure rolls back cleanly and is safely retried (at-least-once redelivery then re-runs the whole idempotent batch).

Confidence: HIGH.

### 4. CSV streaming + re-stamping (streamer)

- **`encoding/csv` (stdlib)** with `Reader.Read()` row-by-row (streaming) — **not** `ReadAll()`, which buffers the whole file. At 2,470 rows it'd fit, but row-by-row is the correct looping idiom and scales if the file grows. Set `Reader.ReuseRecord = true` to cut allocations on the hot loop. Set `FieldsPerRecord` to validate column count.
- **Loop semantics** — on EOF, `Seek(0,0)` and rebuild the reader to replay the file endlessly (or re-`Open`). Skip the header each pass.
- **Re-stamp at produce time** — discard CSV col 0; set the `Message.timestamp_unix_ns` / value timestamp to `time.Now().UnixNano()` at the moment of produce (PROJECT.md: processing time = telemetry timestamp).
- **Rate control** — `golang.org/x/time/rate.Limiter`; `limiter.Wait(ctx)` before each produce to pace throughput deterministically for the perf harness. Make the rate a flag so `(producers, consumers)` sweeps are reproducible.
- **Value encoding** — encode each row as protobuf or JSON into `Message.value`. Recommend a small protobuf `TelemetryRow` message (reuses the existing protobuf toolchain, smaller/faster than JSON, gives the collector a typed parse). JSON is acceptable if you want human-readable MQ payloads for debugging.

Confidence: HIGH.

### 5. Prometheus metrics + testify for concurrent broker code

- **`promauto` + `promhttp.Handler()`** on a `/metrics` endpoint per service. Distinct metrics ports per service (e.g. `:9090`).
- **Metric types for the rubric** — `Counter` (messages produced/consumed), `Gauge` (broker queue depth, consumer lag, active streams), `Histogram` (produce/end-to-end latency, fsync duration). `*Vec` variants labeled by `topic`/`partition`/`group`. **All `*Vec` collectors are internally synchronized — safe to increment from many broker goroutines without your own mutex.**
- **Testing concurrent broker code (testify):**
  - `testify/require` for setup invariants, `assert` for post-conditions.
  - For metric assertions use `prometheus/client_golang/prometheus/testutil`: `testutil.ToFloat64(counter)` and `testutil.CollectAndCompare(collector, expectedTextfile)`.
  - For deterministic concurrency tests prefer Go 1.25+ **`testing/synctest`** (stdlib) to drive goroutines/timers without flaky `time.Sleep`. Combine with `-race` (already in `make test`).
  - Use `testify/mock` or hand-rolled fakes for the gRPC stream interface when unit-testing broker handlers in isolation.

Confidence: HIGH.

## Installation

```bash
# apigateway module
cd apigateway
go get github.com/go-chi/chi/v5@v5.3.0
go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.7.1

# collector module
cd ../collector
go get github.com/jackc/pgx/v5@v5.10.0

# streamer module
cd ../streamer
go get golang.org/x/time/rate@latest

# shared across services (add per module that needs them)
go get github.com/prometheus/client_golang@v1.23.2
go get github.com/stretchr/testify@latest

# mq module — bump the already-present deps
cd ../mq
go get google.golang.org/grpc@v1.81.1
go get google.golang.org/protobuf@v1.36.11
```

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| chi/v5 (router) | gin | Only if the API grows write endpoints with heavy body binding/validation |
| oapi-codegen/v2 (spec-first) | swaggo/swag | Only if you insist on comment-driven specs and accept Swagger 2.0 output |
| `pgx.Batch` + ON CONFLICT | `CopyFrom` + TEMP staging table | When raw ingest throughput under 10×10 load beats Batch and you need COPY speed *with* idempotency |
| buffered `os.File` + `Sync()` (log writes) | mmap | Read-side index lookups only — never the durable write path |
| protobuf `Message.value` | JSON value | When human-readable MQ payloads aid debugging and payload size is irrelevant |
| `golang.org/x/time/rate` | hand-rolled ticker | Never — `rate.Limiter` is the idiom |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| `github.com/jackc/pgx` (bare path) | That's the abandoned **v3** (last tag 2020). | `github.com/jackc/pgx/v5` v5.10.0 |
| `database/sql` + pgx stdlib shim | Loses native `Batch`/`CopyFrom`, extra allocations, no pgx-native types | `pgxpool` directly |
| `CopyFrom` for the primary upsert path | COPY protocol has **no `ON CONFLICT`** → breaks idempotency on re-delivery (verified pgx #437/#992) | `pgx.Batch` of `INSERT ... ON CONFLICT`; staging-table COPY only as an opt |
| swaggo/swag stable | Emits **Swagger 2.0** only; v2/OpenAPI-3.1 is still RC in June 2026 | oapi-codegen/v2 (OpenAPI 3.x) |
| mmap for the durable log write path | No control over when dirty pages flush → fights fsync-before-ack; SIGBUS on I/O error | buffered `os.File` + explicit `Sync()` |
| `csv.Reader.ReadAll()` in the streamer loop | Buffers entire file; not a streaming idiom | `Read()` row-by-row + `ReuseRecord` |
| Custom gRPC credit/backpressure protocol | Reinvents HTTP/2 flow control already in gRPC-Go | Let `stream.Send()` block; tune window sizes |
| Importing Kafka/etcd/NATS broker code | ADR-0001 forbids off-the-shelf broker; assignment grades a **custom** MQ | Borrow the *patterns* (framing, group commit, sparse index) only |

## Version Compatibility

| Package | Compatible With | Notes |
|---------|-----------------|-------|
| pgx/v5 v5.10.0 | Go 1.26, PostgreSQL 13+ | pgxpool is the v5 pool package (`pgxpool.New`) |
| grpc v1.81.1 | protobuf v1.36.11, Go 1.26 | Bump both together; regenerate stubs with current protoc-gen-go via `make proto` |
| oapi-codegen/v2 v2.7.1 | chi/v5 v5.3.0 | `chi-server` generator target pairs natively |
| client_golang v1.23.2 | Go 1.26 | `testutil` subpackage for test assertions |
| chi/v5 v5.3.0 | stdlib `net/http`, Go 1.26 | Handlers are plain `http.Handler` |

## Sources

- `proxy.golang.org/@latest` (Go module proxy, authoritative) — pgx/v5 **v5.10.0**, chi/v5 **v5.3.0**, grpc **v1.81.1**, protobuf **v1.36.11**, client_golang **v1.23.2**, oapi-codegen/v2 **v2.7.1**, swaggo/swag **v1.16.6**, gin **v1.12.0** — confidence HIGH (canonical version source)
- WebSearch (Speakeasy Go OSS comparison; swaggo/swag GitHub releases & issues #386/#1766/#1898) — oapi-codegen = spec-first OpenAPI 3.x; swaggo stable = Swagger 2.0, v2/3.1 still RC (v2.0.0-rc5, Jan 2026) — confidence HIGH
- WebSearch (jackc/pgx issues #437, #992; Go pkgsite CopyUpsert) — `CopyFrom` has no `ON CONFLICT`; staging-table pattern for COPY+upsert — confidence HIGH
- Existing repo contract `mq/proto/mqv1/mq.proto` — confirmed Produce=bidi, Consume=server-stream, Commit=unary — confidence HIGH
- Established patterns: Kafka segment-log/sparse-index, etcd `wal` CRC framing + group commit, HTTP/2 per-stream flow control — confidence HIGH (widely documented, well-established)

---
*Stack research for: custom MQ + GPU telemetry pipeline (implementation-level, within locked stack)*
*Researched: 2026-06-24*
