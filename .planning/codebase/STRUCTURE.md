# Codebase Structure

**Analysis Date:** 2026-06-24

## Directory Layout

```
vantage/ (github.com/ajitgunturi/vantage)
├── go.work                          # Multi-module workspace (local dev)
├── Makefile                         # Build, test, lint, coverage, k8s targets
├── PROJECT.md                       # Full spec (scope, data model, design Q&A)
├── STATE.md                         # Live status (current phase, checklist, open items)
├── CLAUDE.md                        # Project conventions (Go style, modules, TDD, commits)
│
├── .claude/                         # Claude Code project config
│   └── settings.json                # Model, theme, permissions
│
├── .planning/
│   └── codebase/                    # Codebase map docs (ARCHITECTURE.md, STRUCTURE.md, ...)
│
├── .githooks/                       # Pre-commit hook (gofmt, golangci-lint)
│
├── docs/
│   ├── adr/                         # Architecture decisions (ADR-0001..0005)
│   │   ├── 0001-custom-mq-durable-segment-log.md     (Accepted)
│   │   ├── 0002-postgresql-for-collector-persistence.md (Accepted)
│   │   ├── 0003-multi-module-monorepo.md             (Accepted)
│   │   ├── 0004-grpc-streaming-transport.md          (Accepted)
│   │   ├── 0005-gpu-uuid-as-canonical-id.md          (Proposed)
│   │   └── README.md                # ADR index + status
│   ├── BRANCHING.md                 # Git workflow (PR to main, ephemeral branches)
│   └── PROMPT_HISTORY.md            # Tooling decisions (kind, buf, vantage name, ...)
│
├── scripts/
│   ├── coverage-gate.sh             # Enforce 90% line coverage
│   └── logic-coverage.sh            # Enforce 100% branch coverage (gobco)
│
├── k8s-infra/
│   ├── helm/
│   │   └── telemetry/               # Umbrella Helm chart (pending: values.yaml, Chart.yaml)
│   │       ├── Chart.yaml           # Pending
│   │       ├── values.yaml          # Pending
│   │       ├── charts/              # Sub-charts (streamer, collector, broker, apigateway)
│   │       │   ├── streamer/
│   │       │   ├── collector/
│   │       │   ├── mqbroker/
│   │       │   ├── apigateway/
│   │       │   └── postgres/        # Or Bitnami dependency
│   │       └── templates/           # Umbrella templates (NetworkPolicy, Ingress, etc.)
│   └── kind/
│       └── cluster.yaml             # Local dev kind cluster config (pending)
│
├── mq/
│   ├── go.mod                       # Module: github.com/ajitgunturi/vantage/mq
│   ├── buf.yaml                     # Protobuf config (buf, no code generation here — buf.gen.yaml next)
│   ├── buf.gen.yaml                 # Buf generation rules (Go + gRPC plugins)
│   ├── proto/
│   │   └── mqv1/
│   │       └── mq.proto             # gRPC service & message definitions
│   ├── gen/
│   │   └── mqv1/
│   │       ├── mq.pb.go             # Generated protobuf stubs (21k, auto)
│   │       └── mq_grpc.pb.go        # Generated gRPC stubs (11k, auto)
│   ├── cmd/
│   │   └── mqbroker/                # Broker binary entry point (PENDING)
│   │       └── main.go              # Stub or partial impl
│   ├── broker/                      # Broker service implementation (PENDING)
│   │   ├── broker.go                # Main broker struct, partition mgmt
│   │   ├── segment_log.go           # Append-only segment file I/O, fsync
│   │   ├── offset_index.go          # Partition offset ranges, recovery
│   │   ├── consumer_group.go        # Consumer offset tracking, rebalancing
│   │   └── broker_test.go           # TDD: partition ordering, crash recovery, fsync edge cases
│   └── client/                      # MQ client library (producer/consumer) (PENDING)
│       ├── producer.go              # Produce RPC wrapper, batching
│       ├── consumer.go              # Consume RPC wrapper, group management
│       ├── client_test.go           # TDD: streaming, offsets, retries
│       └── ...
│
├── streamer/
│   ├── go.mod                       # Module: github.com/ajitgunturi/vantage/streamer
│   ├── cmd/
│   │   └── streamer/                # Streamer binary entry point (PENDING)
│   │       └── main.go              # Parse env, init CSV source + MQ producer, loop
│   └── internal/
│       ├── source/                  # CSV data source (PENDING)
│       │   ├── source.go            # CSV reader, column parsing, iterate
│       │   └── source_test.go       # TDD: parse DCGM CSV format, cardinality
│       └── stream/                  # Streaming loop & produce logic (PENDING)
│           ├── stream.go            # Timestamp re-stamping, MQ produce, backpressure
│           └── stream_test.go       # TDD: latency, throughput, error handling
│
├── collector/
│   ├── go.mod                       # Module: github.com/ajitgunturi/vantage/collector
│   ├── cmd/
│   │   └── collector/               # Collector binary entry point (PENDING)
│   │       └── main.go              # Parse env, init MQ consumer + DB, consume loop
│   └── internal/
│       ├── consume/                 # MQ consumption & parsing (PENDING)
│       │   ├── consumer.go          # Consume RPC, group lifecycle, rebalance
│       │   ├── parser.go            # Message → telemetry struct
│       │   └── consume_test.go      # TDD: parse protobuf, offset handling
│       └── store/                   # PostgreSQL persistence (PENDING)
│           ├── store.go             # pgx pool, schema init, upsert logic
│           ├── models.go            # GPU, Telemetry structs
│           └── store_test.go        # TDD: idempotent upsert, tx handling
│
└── apigateway/
    ├── go.mod                       # Module: github.com/ajitgunturi/vantage/apigateway
    ├── cmd/
    │   └── apigateway/              # API binary entry point (PENDING)
    │       └── main.go              # Parse env, init HTTP router + DB, listen
    ├── docs/                        # OpenAPI/Swagger artifacts (auto-gen)
    │   ├── swagger.yaml             # Auto-generated via swag (pending)
    │   └── swagger.json             # Auto-generated (pending)
    └── internal/
        ├── api/                     # REST handlers (PENDING)
        │   ├── handler.go           # HTTP router (chi or gin), 3 endpoints
        │   ├── responses.go         # JSON response DTOs
        │   └── handler_test.go      # TDD: endpoint contracts, JSON marshaling
        └── store/                   # PostgreSQL query layer (PENDING)
            ├── store.go             # pgx queries, time-window filtering
            └── store_test.go        # TDD: query correctness, time bounds
```

## Directory Purposes

**Root-level config & docs:**
- Purpose: Monorepo entry point, build orchestration, project governance.
- Key files: `go.work` (local workspace), `Makefile` (build/test/k8s targets), `PROJECT.md` (spec), `STATE.md` (live status), `CLAUDE.md` (conventions).

**`docs/adr/`:**
- Purpose: Architecture decision records (ADR-0001 through ADR-0005).
- ADR bar: load-bearing design forks only (not tooling/naming). Process lives in BRANCHING.md or Makefile, not ADRs.
- Status: 5 ADRs; 4 Accepted, 1 Proposed (ADR-0005 GPU UUID pending explicit confirmation).

**`docs/` other:**
- `BRANCHING.md`: Simplified workflow (PR to main, ephemeral branches, no long-lived feature branches).
- `PROMPT_HISTORY.md`: Tooling choices (kind for local k8s, buf for protobuf, vantage project name).

**`mq/` — Custom Message Queue Module:**
- Purpose: Durable gRPC-based broker + client library.
- `proto/mqv1/mq.proto`: Service contract (Produce, Consume, Commit, CreateTopic, Health).
- `gen/mqv1/`: Auto-generated stubs from protobuf (do NOT edit; regenerate via `make proto`).
- `cmd/mqbroker/`: Broker service entry point (stub; TBD: main.go starts a gRPC server, loads partition state from PVC).
- `broker/`: Broker logic (TBD: segment log, offset index, consumer-group state; needs crash-recovery tests).
- `client/`: Producer/consumer client library (TBD: wrappers for Produce/Consume/Commit RPCs; used by streamer & collector).

**`streamer/` — Telemetry Producer:**
- Purpose: Read CSV, loop, timestamp-restamping, produce to MQ.
- `cmd/streamer/`: Entry point (stub; TBD: main.go initializes source + producer, runs loop).
- `internal/source/`: CSV parsing (TBD: load DCGM CSV, iterate rows).
- `internal/stream/`: Production loop (TBD: re-stamp ts, encode message, produce, handle backpressure).

**`collector/` — Telemetry Consumer & Persister:**
- Purpose: Consume from MQ, parse, idempotent upsert to Postgres.
- `cmd/collector/`: Entry point (stub; TBD: main.go initializes consumer + store, runs consume loop).
- `internal/consume/`: Consumption logic (TBD: join consumer group, stream messages, parse).
- `internal/store/`: Postgres persistence (TBD: pgx pool, schema init, idempotent upsert on (uuid, metric_name, ts)).

**`apigateway/` — REST API & OpenAPI:**
- Purpose: Serve `GET /api/v1/gpus` and `GET /api/v1/gpus/{uuid}/telemetry?...` against Postgres; generate OpenAPI spec.
- `cmd/apigateway/`: Entry point (stub; TBD: main.go initializes router + store, listens on :8080).
- `internal/api/`: HTTP handlers (TBD: endpoint implementations, response marshaling).
- `internal/store/`: Postgres query layer (TBD: pgx queries for GPU list & telemetry range).
- `docs/`: OpenAPI artifacts (swagger.yaml/json auto-generated by swag on `swag init -g cmd/apigateway/main.go`).

**`k8s-infra/` — Kubernetes & Helm:**
- Purpose: Local dev cluster config + production deployment templates.
- `kind/cluster.yaml`: Local kind cluster spec (pending; will define nodeCount, apiServerPort, extraPortMappings for services).
- `helm/telemetry/`: Umbrella Helm chart (pending; Chart.yaml, values.yaml, templates/).
  - Sub-charts (pending): `charts/streamer/`, `charts/collector/`, `charts/mqbroker/`, `charts/apigateway/`, `charts/postgres/` (or Bitnami dependency).
  - Templates (pending): NetworkPolicy, Ingress, PVC for broker log, ConfigMaps, Secrets.

**`scripts/`:**
- Purpose: Auxiliary build & validation scripts.
- `coverage-gate.sh`: Enforce 90% line coverage (Makefile target `make cover-check`).
- `logic-coverage.sh`: Enforce 100% branch coverage on business logic via gobco.

## Key File Locations

**Entry Points (all pending implementation):**
- `mq/cmd/mqbroker/main.go`: Start broker, listen on gRPC, initialize PVC mount for segments.
- `streamer/cmd/streamer/main.go`: Load CSV, start MQ producer, loop.
- `collector/cmd/collector/main.go`: Start MQ consumer group, initialize Postgres, consume loop.
- `apigateway/cmd/apigateway/main.go`: Start HTTP router, initialize Postgres, serve endpoints.

**Configuration & Spec:**
- `PROJECT.md`: Full functional & non-functional requirements, data model, 7 open design questions.
- `STATE.md`: Live checklist (monorepo scaffold: done; service stubs: NEXT; logic: TBD).
- `CLAUDE.md`: Go conventions, module map, Makefile usage, TDD/coverage, commit style.
- `docs/adr/*`: Architecture decisions (5 records, bar is load-bearing forks).

**Core Business Logic (all pending TDD):**
- `mq/broker/segment_log.go`: Append-only log, fsync, segment rolling.
- `mq/broker/offset_index.go`: Partition offset ranges, recovery from segments.
- `mq/broker/consumer_group.go`: Consumer offset tracking, rebalancing.
- `streamer/internal/source/source.go`: CSV parsing & iteration.
- `streamer/internal/stream/stream.go`: Timestamp re-stamping, MQ produce.
- `collector/internal/consume/consumer.go`: Consumer group lifecycle, message stream.
- `collector/internal/store/store.go`: Idempotent upsert (INSERT ... ON CONFLICT).
- `apigateway/internal/api/handler.go`: HTTP endpoints, JSON response.
- `apigateway/internal/store/store.go`: Postgres queries (GPU list, time-range telemetry).

**Generated & Committed:**
- `mq/gen/mqv1/mq.pb.go` (21k): Protobuf message stubs. **Auto-gen; do NOT edit. Regenerate via `make proto` after modifying `mq/proto/mqv1/mq.proto`.**
- `mq/gen/mqv1/mq_grpc.pb.go` (11k): gRPC service stubs. **Auto-gen; do NOT edit.**
- `apigateway/docs/swagger.json` (pending): Auto-gen via swag. **Auto-gen; do NOT edit. Regenerate via Makefile target (TBD) after modifying handler comments.**

**Testing (TDD-first):**
- `mq/broker/broker_test.go`: Partition ordering, crash recovery, fsync edge cases.
- `streamer/internal/source/source_test.go`: CSV parsing, cardinality.
- `streamer/internal/stream/stream_test.go`: Latency, throughput, backpressure.
- `collector/internal/consume/consume_test.go`: Message parsing, offset handling.
- `collector/internal/store/store_test.go`: Idempotent upsert, tx rollback.
- `apigateway/internal/api/handler_test.go`: Endpoint contracts, JSON marshaling.
- `apigateway/internal/store/store_test.go`: Query correctness, time bounds.

## Naming Conventions

**Files:**
- Entry points: `cmd/<service>/main.go` (e.g., `streamer/cmd/streamer/main.go`).
- Modules: lowercase (e.g., `broker.go`, `segment_log.go`).
- Test files: `_test.go` suffix (e.g., `store_test.go`).
- Protobuf: `<package>.proto` (e.g., `mqv1/mq.proto`).
- Generated stubs: `*.pb.go`, `*_grpc.pb.go` (auto-gen).

**Directories:**
- Services: `cmd/<service>/` (thin main wiring).
- Logic: `internal/<subsystem>/` (TDD, testable, non-exported outside module).
- Protobuf: `proto/<package>/` → generated to `gen/<package>/`.
- Helm: `helm/<chart>/` with substructure `charts/` (sub-charts) + `templates/` (manifest templates).
- Architecture docs: `docs/adr/` (ADR-XXXX-*.md).

**Go identifiers (idiomatic):**
- Packages: lowercase, short (e.g., `broker`, `store`, `api`).
- Types: CamelCase (e.g., `SegmentLog`, `ConsumerGroup`, `TelemetryRow`).
- Functions: CamelCase (e.g., `Produce`, `Consume`, `Commit`).
- Variables: camelCase (e.g., `brokerAddr`, `logDir`, `consumerID`).
- Constants: UPPER_SNAKE_CASE (e.g., `DEFAULT_PARTITION_COUNT`, `SEGMENT_SIZE_BYTES`).

## Where to Add New Code

**New Service (e.g., a scheduler):**
1. Create module: `<service>/go.mod`, `go.work` update.
2. Entry point: `<service>/cmd/<service>/main.go`.
3. Logic: `<service>/internal/<subsystem>/*.go` (test-first).
4. Helm: Add sub-chart `k8s-infra/helm/telemetry/charts/<service>/`.

**New Handler in API Gateway:**
1. Add HTTP handler: `apigateway/internal/api/handler.go` (new endpoint).
2. Add test: `apigateway/internal/api/handler_test.go` (test first).
3. Update swagger comments (swag parses Go doc comments); regenerate via Makefile.
4. Add query method: `apigateway/internal/store/store.go` if DB access needed.

**New Message Type in MQ:**
1. Add protobuf message: `mq/proto/mqv1/mq.proto`.
2. Regenerate stubs: `make proto` (updates `mq/gen/mqv1/`).
3. Update client/broker code to use new message type.
4. Test serialization in `mq/broker/broker_test.go` or `mq/client/client_test.go`.

**New Database Table (Collector):**
1. Add schema init: `collector/internal/store/store.go` (CREATE TABLE IF NOT EXISTS).
2. Add query method: `collector/internal/store/store.go` (e.g., `UpsertTelemetry`).
3. Add test: `collector/internal/store/store_test.go` (test upsert idempotence, FK constraints).

## Special Directories

**`mq/gen/`:**
- Purpose: Auto-generated protobuf & gRPC stubs.
- Generated: Yes (via `make proto` → `buf generate`).
- Committed: Yes (for reproducible builds; IDE support).
- **Do NOT edit** — regenerate after changing `mq/proto/mqv1/mq.proto`.

**`.planning/codebase/`:**
- Purpose: Codebase map documents (ARCHITECTURE.md, STRUCTURE.md, CONVENTIONS.md, TESTING.md, CONCERNS.md).
- Generated: Yes (by codebase mapper agent).
- Committed: Yes (reference for other GSD commands).

**`k8s-infra/helm/telemetry/`:**
- Purpose: Production & local-dev Kubernetes deployment.
- Generated: Partial (values, ConfigMaps auto-gen from config if implemented).
- Committed: Yes (Helm chart is source; instances auto-gen).

**`.githooks/`:**
- Purpose: Pre-commit hook (gofmt, golangci-lint).
- Generated: No.
- Committed: Yes. Installed via `make hooks` (sets `git config core.hooksPath .githooks`).

---

*Structure analysis: 2026-06-24*
