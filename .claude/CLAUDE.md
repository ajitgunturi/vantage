<!-- GSD:project-start source:PROJECT.md -->

## Project

**vantage — Elastic GPU Telemetry Pipeline with Custom Message Queue**

An elastic, scalable, restart-safe telemetry pipeline for an AI/GPU cluster (many hosts, 1+ GPUs
each), built in idiomatic Go and deployed via Docker + Kubernetes + Helm. A **Streamer** loops a
DCGM telemetry CSV and produces re-stamped datapoints over a **custom-built message queue**; a
**Collector** consumes and persists them idempotently into PostgreSQL; an **API Gateway** exposes
the data over REST with an auto-generated OpenAPI spec. The MQ is hand-built (no Kafka/RabbitMQ/
ZeroMQ) with its own durable append-only segment log so it survives broker restarts.

**Core Value:** The **custom message queue** must be durable, scalable, and correct under load — it is the heart of
the exercise. Everything else (streamer, collector, API) exists to exercise and demonstrate the MQ
across producer/consumer ratios up to 10×10. If the MQ loses or corrupts messages, the project fails.

### Constraints

- **Tech stack**: Custom MQ only — no off-the-shelf brokers — *core grading criterion; "custom" must stay honest (no DB-backed queue)*.
- **Scale**: ≤ 10 streamers, ≤ 10 collectors — *exercise ceiling; partition count must still be ≥ 10 for consumer parallelism*.
- **Deployment**: All components deployable to Kubernetes via Helm — *assignment deliverable*.
- **Durability**: MQ must survive broker crash/restart with no message loss — *user-added hard requirement driving the segment-log design*.
- **Language/quality**: idiomatic Go, graceful error handling + memory management, `slog` logging — *grading criterion*.
- **Testing**: unit tests required (system tests bonus); coverage via Makefile; OpenAPI auto-generated via Makefile — *deliverable + grading criterion*.
- **Documentation**: comprehensive README + AI-usage doc with exact prompts and where they fell short — *explicit deliverable*.

<!-- GSD:project-end -->

<!-- GSD:stack-start source:codebase/STACK.md -->

## Technology Stack

## Languages

- Go 1.26 - All four service modules (mq, streamer, collector, apigateway); single-language monorepo

## Runtime

- Go 1.26 runtime (specified in all `go.mod` files and `go.work`)
- Go modules (`go.mod` per module + `go.work` for local multi-module development)
- Lockfile: No `go.sum` files committed; modules are in early scaffold stage with minimal dependencies

## Frameworks & Build Tools

- **gRPC** (v1.71.0) — Service-to-service RPC for the custom message queue broker
- **Protocol Buffers** (v1.36.6) — Data serialization and gRPC service definitions
- **buf** (latest via `make tools`) — Protocol buffer build system and linter
- **golangci-lint** (v2, latest via `make tools`) — Multi-linter for Go
- **gobco** (latest via `make tools`) — Branch/condition coverage analyzer
- **kind** (latest via `make tools`) — Kubernetes in Docker for local development

## Key Dependencies

- `google.golang.org/grpc` v1.71.0 — gRPC runtime and streaming transport
- `google.golang.org/protobuf` v1.36.6 — Protobuf message serialization and gRPC code generation support
- Transitive: `golang.org/x/net`, `golang.org/x/sys`, `golang.org/x/text`, `google.golang.org/genproto/googleapis/rpc` (indirect)
- `github.com/jackc/pgx` — PostgreSQL client driver (for collector persistence)
- `log/slog` — Structured logging (Go stdlib, no dependency)
- `github.com/stretchr/testify` — Assertion/mocking library for tests
- `prometheus/client_golang` — Prometheus metrics exposition
- `chi` OR `gin` — HTTP router for apigateway (final selection TBD per ADR-0006, still *Proposed*)

## Infrastructure & Deployment

- Docker (referenced in Makefile targets `kind-load`; Dockerfiles not yet created)
- Deployment target: Kubernetes via **kind** (local dev)
- **Kubernetes** (kind cluster target, local 1.2x default)
- **Helm** (auto-assumed for templating; umbrella chart structure pre-scaffolded)
- **PostgreSQL** — Primary data store for collector's telemetry persistence
- **Kubernetes Persistent Volumes (PVCs)** — MQ broker segment log durability

## Configuration

- Go modules resolved via local `go.work` (dev) or per-module `replace` directives (Docker)
- Multi-module structure via `go.work` (development); Docker builds use `GOWORK=off` (per `make build`)
- Protocol buffer config: `mq/buf.yaml` (linting), `mq/buf.gen.yaml` (code generation)
- No .env or dotenv files (all secrets/config deferred until service implementation)
- `make tools` — Install buf, protoc plugins (protoc-gen-go, protoc-gen-go-grpc), gobco, golangci-lint, kind
- `make proto` — Regenerate gRPC stubs from `mq/proto/mqv1/mq.proto`
- `make build` — Build all binaries (GOWORK=off for Docker parity)
- `make test` — Run unit tests with race detector and coverage
- `make cover` / `make cover-check` — Test coverage gates (90% line coverage threshold)
- `make cover-logic` — Branch coverage enforcement (100% on internal/ packages via gobco)
- `make lint` — Run golangci-lint across modules
- `make kind-up` / `make kind-down` — Manage local Kubernetes cluster
- `make helm-install` — Deploy umbrella chart to kind

## Platform Requirements

- Go 1.26
- Docker (for `kind`)
- kubectl (implied by kind cluster management)
- Helm 3.x (for chart deployment)
- buf, protoc-gen-go, protoc-gen-go-grpc (installed via `make tools`)
- golangci-lint, gobco, kind (installed via `make tools`)
- Bash (for Makefile wrappers in `scripts/`)
- Kubernetes 1.20+ (target: kind cluster locally; production on any K8s)
- Helm 3.x
- PostgreSQL 13+ (via Bitnami Helm subchart or managed service)
- Docker images for mq, streamer, collector, apigateway (Dockerfiles pending)

## Code Coverage & Quality Gates

- Threshold: 90% (enforced by `make cover-check` via `scripts/coverage-gate.sh`)
- Skipped: Generated code (`gen/`), thin wiring (`cmd/`)
- Early-stage modules without testable code skip this gate
- Threshold: 100% on `internal/` packages (enforced by `make cover-logic` via gobco)
- Skipped: `gen/`, `cmd/`, modules with no logic packages yet
- Green until first `internal/` package lands under TDD
- Tool: golangci-lint v2 (fallback: `go vet`)
- Run: `make lint` (wrapper: `scripts/lint.sh`)
- Pre-commit hook configured via core.hooksPath = `.githooks` (install via `make hooks`)

## Status

- ✓ Go 1.26, go.work + 4 service modules
- ✓ mq/proto + buf config + generated stubs (mq.pb.go, mq_grpc.pb.go)
- ✓ Makefile with tools/build/test/lint/kind/helm targets
- ✗ Service binaries (cmd/*/main.go): not yet created
- ✗ Dockerfiles: not yet created
- ✗ Helm charts: scaffold only (directories exist, manifests pending)
- ✗ Actual business logic: no internal/ packages yet
- pgx, slog, testify, prometheus/client_golang, chi|gin (declared in PROJECT.md, added as modules need them)

<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->

## Conventions

## Naming Patterns

- Go source: lowercase with underscores for multi-word names (e.g., `segment_log.go`, `message_queue_test.go`)
- Command binaries: lowercase (e.g., `cmd/mqbroker/main.go`)
- Proto files: lowercase with underscores, `.proto` extension (e.g., `mq/proto/mqv1/mq.proto`)
- Test files: `*_test.go` suffix (standard Go convention)
- Idiomatic Go: PascalCase for exported functions, camelCase for unexported
- Convention evident from gRPC stubs generated by `mq/gen/mqv1/mq_grpc.pb.go`: exported service methods follow `Produce()`, `Consume()`, `Commit()` pattern
- Test functions: `Test<Function|Behavior>` (standard Go testing convention)
- Short, meaningful names in functions (e.g., `msg`, `offset`, `uuid`)
- Constants: UPPER_SNAKE_CASE for module-level constants
- Interface receivers: single-letter convention (e.g., `(b *Broker)`, `(s *Streamer)`)
- Exported: PascalCase (e.g., `Broker`, `Message`, `Topic`)
- Unexported: camelCase (e.g., `segmentLog`, `offsetManager`)
- Interfaces: descriptive names ending in `-er` pattern (standard Go, e.g., `Producer`, `Consumer`, `Writer`)
- One-word lowercase names where possible (`mq`, `client`, `broker`)
- Generated proto packages: `mqv1` (versioned)
- Internal packages: `internal/consume`, `internal/store`, `internal/source`, `internal/stream` (per module structure)

## Code Style

- **Tool:** gofmt (Go's built-in standard formatter)
- **Enforcement:** Pre-commit hook (`.githooks/pre-commit`) runs `gofmt -l` on staged Go files; commit fails if any file is not formatted
- **Key settings:** Default gofmt behavior (4-space indentation via tabs, single newline between top-level declarations)
- **Setup:** Automatic via `make hooks` (sets `core.hooksPath=.githooks`)
- **Tool:** golangci-lint v2 (latest via `make tools`)
- **Scope:** All modules via `scripts/lint.sh` (runs per module with `GOWORK=off`)
- **Fallback:** `go vet` if golangci-lint not installed
- **Integration:** Runs in pre-commit hook (`.githooks/pre-commit` line 32) and CI (`scripts/lint.sh`, invoked by `make lint`)
- **CI enforcement:** `.github/workflows/ci.yml` includes explicit `gofmt` and `golangci-lint` checks on every PR to `main`
- **No config file:** golangci-lint runs with default settings (no `.golangci.yml` exists; uses built-in ruleset)

## Import Organization

- Blank imports for side effects must be grouped separately with a comment explaining why
- Multi-module monorepo uses `go.work` for local development (no import aliases needed)
- Modules reference each other via full import path: `github.com/ajitgunturi/vantage/[module]`
- Docker builds use per-module `go.mod` with `replace` directives (documented in each module's `go.mod`, currently commented out pending first cross-module import)

## Error Handling

- Idiomatic Go error returns: functions that can fail return `(T, error)` as the last return value
- Error checking: `if err != nil { /* handle */ }` on every non-error-ignorable call
- Wrapping: Use `fmt.Errorf("context: %w", err)` to chain errors (Go 1.13+)
- Panics: Reserved for truly unrecoverable initialization errors only; business logic must never panic
- Logging errors: Use structured logging via `slog` (planned in PROJECT.md § 6); include context and error details

## Logging

- Planned in PROJECT.md § 6 as "idiomatic Go: `slog` for logging"
- Not yet implemented (no business logic to log yet)
- Future convention: structured key-value logging with `.Info()`, `.Warn()`, `.Error()` levels
- When added: Initialize a global or per-module logger via `slog.New()`; log significant state changes, errors, and performance events

## Comments

- **Package-level:** Every package should have a comment explaining its purpose (line before `package` declaration)
- **Exported types and functions:** Must have a comment starting with the name (Go doc convention)
- **Complex logic:** Explain the "why," not the "what" — code shows what it does
- **Caveats and gotchas:** e.g., "Must be called under lock" or "Non-nil check happens at call site"
- **Avoid:** Over-commenting obvious code (e.g., `x := x + 1  // increment x`)
- Not applicable (Go uses Go Doc format, not JSDoc)
- Go doc comments are single-line (`//`) or multi-line (`/* */`) preceding declarations
- Generated documentation via `godoc` or pkg.go.dev

## Function Design

- Aim for small, single-responsibility functions (15–30 lines typical)
- If a function grows beyond 50 lines, consider extracting helper functions
- Long functions are acceptable only if they are straightforward sequences (e.g., initialization)
- Prefer function parameters over global state
- Limit to 3–4 parameters; use struct parameter if more are needed
- Receivers should be pointers for methods that modify state, values for read-only
- Prefer (T, error) tuple for fallible functions; no exceptions/panic except initialization
- Multiple return values: max 3–4 before considering a struct return (e.g., `(result, offset, committed bool, error)` might use a struct)

## Module Design

- Export only what is part of the public API; keep most code unexported (in `internal/`)
- Exported types should have a dedicated comment explaining their purpose
- Not used in this codebase; each package exports directly
- Consider simple `doc.go` files for package-level documentation if needed
- Commands: `cmd/` (thin wiring, main() only)
- Business logic: `internal/` (the real implementation)
- Generated code: `gen/` (excluded from coverage and linting)
- Tests: `*_test.go` alongside source files (prefer co-location)

## Concurrency

- Use for streaming, long-lived consumers, background tasks
- Always provide a way to stop (context.Context or channel close)
- Avoid goroutine leaks — ensure all started goroutines exit cleanly
- Prefer channels for inter-goroutine communication (idiomatic Go)
- Use `sync.Mutex` / `sync.RWMutex` for protected shared state
- Comment any lock-guarded sections clearly

## Idiomatic Go Touchstones

- **nil is valid**: Functions should handle nil receivers gracefully or document the requirement
- **Interfaces over types**: Define minimal interfaces for mocks and abstraction
- **Defer for cleanup**: Use `defer` for resource cleanup (file close, unlock, etc.)
- **io.Reader/Writer**: Use standard interfaces for I/O
- **No setters/getters**: Direct field access for simple types; methods only when computation is needed

## Conventional Commits

- `feat` — New feature (e.g., "feat(mq): add segment log persistence")
- `fix` — Bug fix (e.g., "fix(collector): handle nil UUID in parse")
- `chore` — Build, dependencies, tooling (e.g., "chore: add gobco to coverage gates")
- `docs` — Documentation (e.g., "docs: add contributor guide")
- `refactor` — Code refactoring without behavior change (e.g., "refactor(streamer): extract CSV parsing")
- `test` — Test additions or fixes (e.g., "test(collector): add upsert idempotency tests")
- `ci` — CI/CD pipeline changes (e.g., "ci: add logic-coverage gate to CI")
- Module or subsystem name (e.g., `mq`, `collector`, `broker`, `api`)
- Lowercase, imperative mood ("add", not "added" or "adds")
- Under 50 characters
- No period at end
- Explain the "why" and any breaking changes
- Wrap at 72 characters
- Separate from subject line with a blank line
- **IMPORTANT:** No `Co-Authored-By` trailer (per project CLAUDE.md, line 29)
- Additional footers may include `Closes #123`, `Refs #456`

## Branch Naming

- `feat/` — Feature work (e.g., `feat/segment-log-persistence`)
- `fix/` — Bug fix (e.g., `fix/collector-upsert-idempotency`)
- `chore/` — Maintenance (e.g., `chore/bump-golangci-lint`)
- Lowercase, hyphens for word separation
- Example: `feat/postgres-schema` not `feat/PostgreSQL_schema`
- Create from `main`: `git checkout -b feat/my-feature main`
- One branch per logical change
- Delete after merge: ephemeral branches only
- No long-lived branches other than `main`

## Integration with CI/CD

- Runs automatically on `git commit` (installed via `make hooks`)
- Checks: `gofmt` on staged Go files, then `golangci-lint` per touched module
- Failures prevent commit; bypass with `git commit --no-verify` in emergencies only
- Ensures code is formatted and linted BEFORE it reaches a branch push
- Triggers on pull requests to `main` and pushes to `main`
- Runs: build + test + gofmt + golangci-lint + helm-lint + logic-coverage
- Gate: `ci-success` job depends on all checks passing
- Branch protection: `main` requires `ci-success` check to merge (no force-push)

<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->

## Architecture

## System Overview

```text

```

## Component Responsibilities

| Component | Responsibility | File(s) |
|-----------|----------------|---------|
| **Streamer** | Read DCGM CSV, loop continuously, re-stamp timestamps, produce over gRPC MQ with key=uuid for ordering | `streamer/cmd/streamer`, `streamer/internal/source`, `streamer/internal/stream` |
| **Collector** | Consume from MQ (gRPC server-push), parse telemetry, idempotent upsert to Postgres on (uuid, metric_name, ts) | `collector/cmd/collector`, `collector/internal/consume`, `collector/internal/store` |
| **MQ Broker** | Manage durable partitioned append-only log on PVC; track consumer-group offsets; deliver at-least-once via gRPC streaming | `mq/cmd/mqbroker`, `mq/broker/` (pending), `mq/gen/mqv1` (stubs) |
| **API Gateway** | Serve `GET /api/v1/gpus`, `GET /api/v1/gpus/{uuid}/telemetry?start_time=...&end_time=...` against Postgres; auto-gen OpenAPI spec | `apigateway/cmd/apigateway`, `apigateway/internal/api`, `apigateway/internal/store` |

## Pattern Overview

- **Durable, partitioned, ordered queue** — append-only segment log on PVC with at-least-once semantics and consumer-group offset tracking (ADR-0001, ADR-0004).
- **Idempotent collectors** — upsert pattern on natural key `(uuid, metric_name, ts)` tolerates at-least-once redelivery (ADR-0002).
- **Canonical GPU identity = UUID** — globally unique, stable across host reboots; used as partition key to order all metrics for one GPU (ADR-0005, *Proposed* — confirm before freezing schema).
- **Scalable consumer/producer** — partitions enable collector scale-out (≥10 partitions for ≥10 collectors); stress patterns tested (3 ratios: producers>consumers, producers<consumers, balanced).
- **gRPC streaming transport** — HTTP/2 multiplexing, backpressure, typed client lib (ADR-0004).
- **TDD + coverage gates** — 90% line coverage, 100% branch coverage on business logic (internal/ packages).

## Layers

- Purpose: Streamer reads CSV and produces telemetry; Collector consumes and persists; API Gateway queries persisted data.
- Location: `streamer/`, `collector/`, `apigateway/` modules.
- Contains: `cmd/` (thin main wiring) + `internal/` (business logic, TDD-covered).
- Depends on: MQ client lib (`mq/client`), PostgreSQL client (`pgx`), gRPC stubs (`mq/gen/mqv1`).
- Used by: Deployed as independent k8s services.
- Purpose: Durable, ordered, partitioned message broker with at-least-once delivery and consumer-group offset tracking.
- Location: `mq/` module: `cmd/mqbroker/` (service entry), `broker/` (segment log + offset index), `client/` (producer/consumer lib).
- Contains: gRPC service implementation, segment-log WAL, consumer-group state, partition assignment.
- Depends on: gRPC server/client, protobuf (`mq/gen/mqv1`), filesystem (PVC mount).
- Used by: Streamer (Produce), Collector (Consume, Commit), API (optional liveness queries).
- Purpose: Durable storage for queue state (segments/offsets on PVC) and telemetry (Postgres).
- Location: PVC mounts (broker logs); `postgresql` (managed via Helm subchart in k8s-infra).
- Contains: Segment files (`<baseOffset>.seg`, `<baseOffset>.segidx`), consumer offsets; `gpus` + `telemetry` tables.
- Used by: Broker (on restart, rebuilds in-memory indexes from segments), Collector (writes), API (reads).
- Purpose: Kubernetes deployment, networking, volume provisioning, Helm templating.
- Location: `k8s-infra/helm/telemetry/` (umbrella chart), `k8s-infra/kind/` (local dev cluster config).
- Contains: Helm sub-charts for broker, streamer, collector, apigateway; Postgres subchart or Bitnami dependency; PVC specs.
- Used by: `make kind-up`, `helm upgrade --install`.

## Data Flow

### Primary Request Path (Telemetry Stream)

### State Management

- **Broker partition offsets:** Persisted per-partition in segment directory; rebuilt at startup.
- **Consumer group offsets:** Persisted separately (proto-defined CommitRequest); durable across restart.
- **Streamer state:** Stateless; only loops CSV. No checkpoints needed (CSV is fully read each cycle).
- **Collector state:** Stateless; offset tracking is the broker's job. Idempotency via DB upsert.
- **API state:** Read-only; queries Postgres.

## Key Abstractions

- Purpose: Represent a queued unit and its position.
- Pattern: `Message{key, value, timestamp_ns}` (proto in `mq/proto/mqv1/mq.proto` lines 8–12). Key routes to partition via hash; monotonic offset assigned per partition.
- Examples: `streamer/internal/source` encodes a telemetry row into a Message.
- Purpose: Logical namespace for messages; consumer-group enables parallel consumption and offset tracking.
- Pattern: Topic name (e.g., "telemetry"); group name (e.g., "collector-group"); consumer_id (e.g., "collector-0"). Offset committed per group/partition.
- Examples: Broker creates "telemetry" topic with ≥10 partitions; collector joins as "collector-group" member.
- Purpose: Ensure at-least-once redelivery doesn't create duplicates.
- Pattern: Natural key `(uuid, metric_name, ts)` + INSERT ... ON CONFLICT DO UPDATE in `collector/internal/store`.
- Examples: Collector receives same message twice → both upserts succeed, second is a no-op.
- Purpose: Durable, append-only storage for queue messages.
- Pattern: Sequence of files `<baseOffset>.seg` (messages) + `<baseOffset>.segidx` (offset index); fsync on write.
- Examples: Broker writes ProduceRequest to current segment, fsync, then replies; on restart reads segments and rebuilds partition offset ranges.

## Entry Points

- Location: `streamer/cmd/streamer` (pending – stub)
- Triggers: `make build`; `streamer` Pod started in k8s.
- Responsibilities: Load CSV, loop, produce to MQ, handle gRPC backpressure.
- Env vars: `MQ_BROKER_ADDR`, `MQ_TOPIC` (TBD at impl).
- Location: `collector/cmd/collector` (pending – stub)
- Triggers: `make build`; `collector` Pod started in k8s.
- Responsibilities: Consume from MQ, parse, upsert to Postgres, commit offsets.
- Env vars: `MQ_BROKER_ADDR`, `MQ_TOPIC`, `MQ_GROUP`, `DATABASE_URL` (TBD at impl).
- Location: `mq/cmd/mqbroker` (pending – stub)
- Triggers: `make build`; `mqbroker` Pod started in k8s.
- Responsibilities: Listen on gRPC, manage partitions, fsync to log on PVC, serve at-least-once semantics.
- Env vars: `BROKER_LISTEN_ADDR`, `LOG_DIR` (PVC mount), `PARTITION_COUNT` (TBD at impl).
- Location: `apigateway/cmd/apigateway` (pending – stub)
- Triggers: `make build`; `apigateway` Pod started in k8s.
- Responsibilities: Serve REST endpoints, query Postgres, gen/serve OpenAPI spec.
- Env vars: `API_LISTEN_ADDR`, `DATABASE_URL` (TBD at impl).

## Architectural Constraints

- **Threading:** Go's goroutines; gRPC server handles concurrent requests. MQ broker must use mutexes or channels to serialize partition writes (fsync is not concurrent-safe).
- **Global state:** Broker's in-memory partition offset ranges and consumer-group state; initialized at startup from segment files. Streamer/Collector/API stateless.
- **Circular imports:** None expected in multi-module layout; `mq` is imported by streamer/collector/apigateway, not vice versa.
- **Partitioning strategy:** All messages for a UUID land on the same partition (key=uuid); enables ordering per GPU. ≥10 partitions for ≥10 collectors.
- **At-least-once semantics:** Broker acks after fsync; no explicit deduplication. Collectors idempotent via upsert. **NOT exactly-once yet** — Postgres upsert + at-least-once = effective exactly-once at rest, but in-flight redelivery possible.
- **Consumer scaling ceiling:** 10 collectors (exercise limit); partition count ≥10 (one per consumer worst-case).

## Anti-Patterns

### **Out-of-order messages per GPU**

### **Collectors not idempotent**

### **Unbounded queue growth under backpressure**

## Error Handling

- **Streamer CSV load failure:** Log error, exit (no retry — CSV is static). K8s restarts the Pod.
- **Streamer → MQ network error:** Retry with exponential backoff (TBD at impl; gRPC built-in retry policies).
- **Collector → Postgres write failure:** Log, continue (next message may succeed). Commit offset only after successful upsert (avoid offset drift).
- **Broker fsync failure:** Log fatally, exit. K8s restarts; PVC persists data; recovery from segments.
- **API query timeout:** Return HTTP 500 to client; log slow query time (SLA/monitoring).

## Cross-Cutting Concerns

- Streamer: validate CSV row (uuid non-empty, metric_name in allowed set, value numeric).
- Collector: validate message format (protobuf parse + field presence).
- API: validate time-range params (start < end, reasonable bounds).
- Streamer: `streamer_messages_produced_total`, `streamer_produce_latency_seconds`.
- Broker: `broker_messages_persisted_total`, `broker_fsync_latency_seconds`, `broker_partition_offset_max`.
- Collector: `collector_messages_consumed_total`, `collector_upsert_latency_seconds`, `collector_offset_committed_total`.
- API: `api_requests_total`, `api_query_latency_seconds`.

<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->

## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->

## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:

- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->

<!-- GSD:profile-start -->

## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
