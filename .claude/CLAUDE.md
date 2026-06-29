<!-- GSD:project-start source:PROJECT.md -->

## Project

**vantage — Elastic GPU Telemetry Pipeline**

A production-grade, horizontally-scalable pipeline that ingests live GPU telemetry (NVIDIA DCGM
metrics), moves it through a **custom-built message queue**, persists it to PostgreSQL, and exposes
it via a documented REST API. It is built from scratch in idiomatic Go as four strictly independent
microservices on Kubernetes, intended as a reference-quality distributed-systems implementation.

**Core Value:** The end-to-end telemetry flow must work reliably under concurrency:
`CSV → Streamer → custom MQ → Collector → PostgreSQL → API Gateway → client`,
with **no message loss or duplication** across horizontally-scaled producers and consumers.

### Constraints

- **Tech stack**: Go (idiomatic), PostgreSQL via `jackc/pgx/v5` (`pgxpool`), gRPC (Protobuf v3) + HTTP/1.1 JSON — fixed by spec.
- **Architecture**: Strictly independent microservices; only shared surface is `pkg/` (proto contracts + DB models).
- **MQ implementation**: Native Go concurrency only (channels, `sync.RWMutex`, ring buffers). No brokers, no disk.
- **Quality**: ≥90% line coverage enforced via Makefile gate; `go test -race` for MQ concurrency. TDD-first.
- **Deployment**: Docker multi-stage builds; Kubernetes + Helm; each service deploys independently.
- **Docs**: OpenAPI auto-generated from `swag` annotations only.
- **Process**: Built with the GSD framework, phase by phase, Vertical-MVP structure.

<!-- GSD:project-end -->

<!-- GSD:stack-start source:research/STACK.md -->

## Technology Stack

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Go | 1.26.4 | Runtime for all four microservices | Latest stable; `min go 1.22` in go.mod minimum for path-variable ServeMux. 1.26.x adds range-over-func and other ergonomics with no migration cost. |
| google.golang.org/grpc | v1.81.1 | gRPC runtime, server-streaming for MQ Consume + Produce | The only maintained Go gRPC implementation; v1.81.1 is May 2026. Pin this in go.mod — minor releases have behavioral changes. |
| google.golang.org/protobuf | v1.36.11 | Protobuf v3 runtime / generated code | Replaces the deprecated `github.com/golang/protobuf`. v1.36.11 is Dec 2025; fixes a JSON unmarshaling CVE present in <v1.33. |
| github.com/jackc/pgx/v5 | v5.10.0 | PostgreSQL driver + connection pool (pgxpool) | De-facto standard; v5 rewrote pgxpool for correct concurrency. pgx.CopyFrom uses the PostgreSQL COPY protocol — the fastest bulk-insert path, 5-10x faster than multi-row INSERT at scale. |
| github.com/go-chi/chi/v5 | v5.3.0 | HTTP router for API Gateway + MQ control plane | 100% net/http compatible — uses `http.Handler`/`http.ResponseWriter`/`*http.Request` with no custom context type. Supports path variables (`{id}`) needed for `/gpus/{id}/telemetry`. Zero external dependencies. |
| github.com/swaggo/swag | v1.16.6 | OpenAPI spec generation from code annotations | Stable release (Jul 2025). The spec mandates fully auto-generated docs from annotations; swag init parses `// @Param`, `// @Success`, etc. and emits `docs/swagger.json`. Use v1.16.6, NOT v2.0.0-rc5 (RC, not stable). |
| bufbuild/buf | v1.71.0 | Protobuf toolchain (linting, code gen, breaking-change detection) | Replaces raw `protoc` + manual plugin management. `buf generate` with remote BSR plugins eliminates local plugin installs; buf lint enforces proto style; buf breaking protects API contracts. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/swaggo/http-swagger/v2 | v2.0.2 | Embeds Swagger UI as an HTTP handler | Add to the API Gateway at `/swagger/*` to serve live docs alongside the API. Pairs with swag v1.16.6. |
| github.com/stretchr/testify | v1.11.1 | Test assertions and suite helpers | Every package. Use `assert` for non-fatal and `require` for fatal assertions. Use `testify/mock` only if behavior verification is needed; prefer stub structs for interface fakes. |
| github.com/testcontainers/testcontainers-go | v0.43.0 | Spin up real Docker containers in tests | Collector and db integration tests only. Starts a real `postgres:17-alpine` container per test package via `TestMain`; pgxpool connects to its `ConnectionString()`. |
| github.com/testcontainers/testcontainers-go/modules/postgres | v0.43.0 | Postgres-specific helpers for testcontainers | Same version as parent module. Provides `postgres.Run(ctx, image, postgres.WithDatabase(...), postgres.BasicWaitStrategies())` and `Snapshot()`/`Restore()` for cheap test isolation. |

### Protobuf Code Generation (buf.gen.yaml)

# api/proto/buf.gen.yaml

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| buf CLI v1.71.0 | Proto linting, code generation, breaking-change detection | `brew install bufbuild/buf/buf` or `go install github.com/bufbuild/buf/cmd/buf@v1.71.0`. Run `buf lint` in CI; `buf breaking --against '.git#branch=main'` to prevent accidental API breaks. |
| swag CLI v1.16.6 | Generate docs/ from gateway annotations | `go install github.com/swaggo/swag/cmd/swag@v1.16.6`. Add `make swagger` target: `swag init -g cmd/gateway/main.go --output docs/`. |
| kind (latest) | Local Kubernetes cluster | `go install sigs.k8s.io/kind@latest`. Single-node cluster; load images with `kind load docker-image`. No VM required — runs clusters in Docker containers. |
| Helm v3 | Package manager for Kubernetes manifests | Used to deploy all four services + PostgreSQL sub-charts under `deployments/`. |
| Bitnami PostgreSQL chart | Helm chart for PostgreSQL in-cluster | Chart version 18.7.8. Pull via OCI: `helm install postgres oci://registry-1.docker.io/bitnamicharts/postgresql`. OCI pull works without the Bitnami repo (avoids the Aug 2025 commercial-access restriction). |

## Installation

# Runtime dependencies (go.mod)

# Test dependencies

# CLI tools (not in go.mod — install globally or pin in a tools.go)

## Alternatives Considered

| Category | Recommended | Alternative | Why Not |
|----------|-------------|-------------|---------|
| Proto toolchain | buf CLI v1.71.0 | raw protoc | protoc requires managing separate binaries per OS, no linting, no breaking-change detection. buf solves all three and is now the community standard. |
| HTTP router (Gateway) | chi/v5 v5.3.0 | gin v1 | Gin uses its own `*gin.Context` type, breaking compatibility with standard `http.Handler` middleware. Chi is idiomatic Go — any net/http middleware works without adaptation. |
| HTTP router (MQ control plane) | net/http ServeMux | chi | MQ has exactly one HTTP endpoint (`GET /api/v1/queue/inspect`). No path variables, no middleware chain needed. ServeMux is zero-dependency and sufficient. |
| PostgreSQL bulk insert | pgxpool.CopyFrom | pgx.Batch / SendBatch | CopyFrom uses PostgreSQL COPY protocol — the fastest available path, 5-10x faster at volume. pgx.Batch/SendBatch is for mixed-operation batches or when partial failure per-row matters. |
| OpenAPI generation | swag v1.16.6 | swag v2.0.0-rc5 | v2 is a release candidate (last: RC5, Jan 2026); not suitable for a production codebase. Revisit when v2.0.0 stable ships. |
| Integration testing | testcontainers-go | dockertest | testcontainers-go is more actively maintained, has first-class Postgres module with Snapshot/Restore, and the API is cleaner. |
| Base image (final stage) | distroless/static-debian12 | alpine | Go binaries compile statically by default (CGO_ENABLED=0). Distroless drops shell, package manager, and libc — significantly smaller attack surface than alpine. Use alpine in builder stage only. |
| Local k8s | kind | minikube | kind runs clusters inside Docker with no hypervisor. Faster to start, easier to script in CI, simpler image loading. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| Kafka / NATS / RabbitMQ / Redis Streams | Explicitly out of scope per spec; the MQ is the assignment artifact — using a broker nullifies it | Custom in-memory MQ with Go channels + sync.RWMutex behind gRPC |
| github.com/golang/protobuf | Deprecated; replaced by google.golang.org/protobuf. Still appears in old tutorials and transitively required by some packages, but do not import it directly | google.golang.org/protobuf v1.36.11 |
| pgx v4 (github.com/jackc/pgx/v4) | v5 has breaking API changes and a redesigned pgxpool; mixing v4 and v5 in the same module causes type conflicts | jackc/pgx/v5 v5.10.0 |
| swag v2.0.0-rc5 | Release candidate; OpenAPI 3.1.0 support is not yet stable. Breaking changes possible before final release | swag v1.16.6 (produces Swagger 2.0 / OpenAPI 2.0 — fully sufficient for the spec requirement) |
| gin | Custom context type breaks standard middleware ecosystem; over-engineered for lightweight microservices | chi/v5 for the gateway; net/http for the MQ control plane |
| Alpine as final Docker image | Alpine uses musl libc; even though Go binaries are statically linked with CGO_ENABLED=0, any CGO dependency would break silently. Alpine is also larger and has more CVE surface than distroless. | gcr.io/distroless/static-debian12 for final stage; golang:1.26-alpine as builder stage |
| go:embed for proto files | Proto files are compile-time artifacts; embedding them in binaries serves no runtime purpose | Generate code into pkg/pb/ at build time via `make proto` |

## Stack Patterns by Variant

- Define service in `api/proto/mq.proto` with `Produce(MetricPayload) returns (Ack)` and `Consume(ConsumeRequest) returns (stream MetricPayload)`
- Generate with `buf generate` from `api/proto/`
- Implement `grpc.NewServer()` on one port (default 50051); `net/http` ServeMux on a second port (8080) for the control plane
- Keep gRPC server and HTTP server in separate goroutines; use `errgroup.Group` from `golang.org/x/sync/errgroup` to manage both
- Open pgxpool once at startup: `pgxpool.New(ctx, connString)` with `pgxpool.ParseConfig` to set `MaxConns`
- Use `pool.CopyFrom(ctx, pgx.Identifier{"gpu_metrics"}, colNames, pgx.CopyFromRows(rows))` per batch
- Batch by time window (e.g., flush every 500ms or every 1000 rows, whichever comes first) using a ticker + channel drain loop
- Do NOT open per-message transactions — COPY is transactional by nature
- Every handler must have a full swag comment block: `// @Summary`, `// @Tags`, `// @Produce json`, `// @Param`, `// @Success`, `// @Failure`, `// @Router`
- `swag init` must point at `cmd/gateway/main.go` (the file with `// @title`, `// @version`, `// @BasePath`)
- Mount swagger UI: `r.Get("/swagger/*", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))`

# Builder stage — full Go toolchain

# Final stage — distroless, no shell

- Run `go test -race ./cmd/mq/...` — the race detector catches concurrent map/slice access in the ring buffer
- Also run `go test -race ./pkg/...` for any shared data structures
- The Makefile `test` target should always pass `-race`; `-count=1` disables caching to catch flaky tests

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|-----------------|-------|
| google.golang.org/grpc@v1.81.1 | google.golang.org/protobuf@v1.36.11 | grpc-go depends on protobuf; go mod tidy resolves; do not mix with github.com/golang/protobuf |
| github.com/swaggo/swag@v1.16.6 | github.com/swaggo/http-swagger/v2@v2.0.2 | swag v1 + http-swagger/v2 is the supported pairing; http-swagger/v2 added support for newer Swagger UI versions |
| github.com/testcontainers/testcontainers-go@v0.43.0 | testcontainers-go/modules/postgres@v0.43.0 | Always pin both to the same version — the modules/postgres package is part of the same release cycle |
| github.com/jackc/pgx/v5@v5.10.0 | testcontainers-go postgres@v0.43.0 | testcontainers-go returns a connection string; feed it to pgxpool.New() directly |
| buf.build/protocolbuffers/go:v1.36.11 | buf.build/grpc/go:v1.6.2 | Remote BSR plugins are versioned independently of the local Go libraries; versions match their local equivalents |

## Sources

- pkg.go.dev/google.golang.org/grpc — verified v1.81.1, published May 13, 2026 (MEDIUM confidence)
- pkg.go.dev/github.com/jackc/pgx/v5 — verified v5.10.0, published Jun 3, 2026 (MEDIUM confidence)
- pkg.go.dev/github.com/go-chi/chi/v5 — verified v5.3.0, published May 22, 2026 (MEDIUM confidence)
- pkg.go.dev/github.com/swaggo/swag — v1.16.6 stable Jul 2025; v2.0.0-rc5 Jan 2026 is RC only (MEDIUM confidence)
- pkg.go.dev/github.com/testcontainers/testcontainers-go — verified v0.43.0, published Jun 19, 2026 (MEDIUM confidence)
- pkg.go.dev/github.com/stretchr/testify — verified v1.11.1, published Aug 27, 2025 (MEDIUM confidence)
- github.com/bufbuild/buf/releases — verified v1.71.0, published Jun 16, 2026 (MEDIUM confidence)
- buf.build/docs/generate/tutorial — buf.gen.yaml v2 remote plugin syntax verified (MEDIUM confidence)
- artifacthub.io/packages/helm/bitnami/postgresql — Bitnami chart 18.7.8 (LOW confidence; chart version not app version)
- WebSearch: Go router comparison, CopyFrom vs Batch benchmarks, distroless pattern, kind local dev (LOW confidence; cross-checked across multiple sources)
- goldlapel.com/grounds/go-postgres/pgx-bulk-insert-benchmarks — CopyFrom vs SendBatch benchmark analysis (LOW confidence)

<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->

## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->

## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
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
