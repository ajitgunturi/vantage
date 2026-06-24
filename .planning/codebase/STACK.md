# Technology Stack

**Analysis Date:** 2026-06-24

## Languages

**Primary:**
- Go 1.26 - All four service modules (mq, streamer, collector, apigateway); single-language monorepo

## Runtime

**Environment:**
- Go 1.26 runtime (specified in all `go.mod` files and `go.work`)

**Package Manager:**
- Go modules (`go.mod` per module + `go.work` for local multi-module development)
- Lockfile: No `go.sum` files committed; modules are in early scaffold stage with minimal dependencies

## Frameworks & Build Tools

**Core Frameworks:**
- **gRPC** (v1.71.0) — Service-to-service RPC for the custom message queue broker
  - Package: `google.golang.org/grpc` in `mq/go.mod`
  - Used for: Broker Produce/Consume/Commit/CreateTopic/Health streaming RPC endpoints defined in `mq/proto/mqv1/mq.proto`

**Protocol Buffers:**
- **Protocol Buffers** (v1.36.6) — Data serialization and gRPC service definitions
  - Package: `google.golang.org/protobuf` in `mq/go.mod`
  - Used for: Message encoding in the custom MQ (defined in `mq/proto/mqv1/mq.proto`)

**Build & Code Generation:**
- **buf** (latest via `make tools`) — Protocol buffer build system and linter
  - Config: `mq/buf.yaml` (STANDARD lint rules) and `mq/buf.gen.yaml` (code generation)
  - Used for: Regenerating gRPC stubs from proto via `make proto` target
  - Generates: `mq/gen/mqv1/mq.pb.go` and `mq/gen/mqv1/mq_grpc.pb.go`

- **golangci-lint** (v2, latest via `make tools`) — Multi-linter for Go
  - Run: `make lint` (wrapper at `scripts/lint.sh`)
  - Enforces: Code style and correctness across all modules

- **gobco** (latest via `make tools`) — Branch/condition coverage analyzer
  - Run: `make cover-logic` (wrapper at `scripts/logic-coverage.sh`)
  - Enforces: 100% branch coverage on `internal/` packages (TDD gate)

- **kind** (latest via `make tools`) — Kubernetes in Docker for local development
  - Used for: `make kind-up`, `make kind-load`, `make kind-down`
  - Config template: `k8s-infra/kind/cluster.yaml` (referenced, not yet created)

## Key Dependencies

**Critical (currently declared in mq/go.mod):**
- `google.golang.org/grpc` v1.71.0 — gRPC runtime and streaming transport
- `google.golang.org/protobuf` v1.36.6 — Protobuf message serialization and gRPC code generation support
- Transitive: `golang.org/x/net`, `golang.org/x/sys`, `golang.org/x/text`, `google.golang.org/genproto/googleapis/rpc` (indirect)

**Planned (documented in PROJECT.md § 6, not yet added via TDD):**
- `github.com/jackc/pgx` — PostgreSQL client driver (for collector persistence)
- `log/slog` — Structured logging (Go stdlib, no dependency)
- `github.com/stretchr/testify` — Assertion/mocking library for tests
- `prometheus/client_golang` — Prometheus metrics exposition
- `chi` OR `gin` — HTTP router for apigateway (final selection TBD per ADR-0006, still *Proposed*)

## Infrastructure & Deployment

**Container Runtime:**
- Docker (referenced in Makefile targets `kind-load`; Dockerfiles not yet created)
- Deployment target: Kubernetes via **kind** (local dev)

**Kubernetes & Package Management:**
- **Kubernetes** (kind cluster target, local 1.2x default)
- **Helm** (auto-assumed for templating; umbrella chart structure pre-scaffolded)
  - Chart root: `k8s-infra/helm/telemetry/`
  - Subdirectories: `charts/` (subchart stubs), `templates/` (manifests TBD)
  - Install: `make helm-install` (wires Helm into kind cluster)

**Persistent Storage:**
- **PostgreSQL** — Primary data store for collector's telemetry persistence
  - Client: `pgx` (declared in PROJECT.md; not yet in `collector/go.mod`)
  - Deployment: Bitnami Helm subchart (planned in umbrella chart)
- **Kubernetes Persistent Volumes (PVCs)** — MQ broker segment log durability
  - Expected in: broker Helm chart, mq/charts/broker/ (not yet created)

## Configuration

**Environment:**
- Go modules resolved via local `go.work` (dev) or per-module `replace` directives (Docker)
- Multi-module structure via `go.work` (development); Docker builds use `GOWORK=off` (per `make build`)

**Build Configuration:**
- Protocol buffer config: `mq/buf.yaml` (linting), `mq/buf.gen.yaml` (code generation)
- No .env or dotenv files (all secrets/config deferred until service implementation)

**Makefile Targets (toolchain):**
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

**Development:**
- Go 1.26
- Docker (for `kind`)
- kubectl (implied by kind cluster management)
- Helm 3.x (for chart deployment)
- buf, protoc-gen-go, protoc-gen-go-grpc (installed via `make tools`)
- golangci-lint, gobco, kind (installed via `make tools`)
- Bash (for Makefile wrappers in `scripts/`)

**Production / Deployment:**
- Kubernetes 1.20+ (target: kind cluster locally; production on any K8s)
- Helm 3.x
- PostgreSQL 13+ (via Bitnami Helm subchart or managed service)
- Docker images for mq, streamer, collector, apigateway (Dockerfiles pending)

## Code Coverage & Quality Gates

**Line Coverage:**
- Threshold: 90% (enforced by `make cover-check` via `scripts/coverage-gate.sh`)
- Skipped: Generated code (`gen/`), thin wiring (`cmd/`)
- Early-stage modules without testable code skip this gate

**Branch Coverage:**
- Threshold: 100% on `internal/` packages (enforced by `make cover-logic` via gobco)
- Skipped: `gen/`, `cmd/`, modules with no logic packages yet
- Green until first `internal/` package lands under TDD

**Linting:**
- Tool: golangci-lint v2 (fallback: `go vet`)
- Run: `make lint` (wrapper: `scripts/lint.sh`)
- Pre-commit hook configured via core.hooksPath = `.githooks` (install via `make hooks`)

## Status

**Current state:** Monorepo scaffold complete.
- ✓ Go 1.26, go.work + 4 service modules
- ✓ mq/proto + buf config + generated stubs (mq.pb.go, mq_grpc.pb.go)
- ✓ Makefile with tools/build/test/lint/kind/helm targets
- ✗ Service binaries (cmd/*/main.go): not yet created
- ✗ Dockerfiles: not yet created
- ✗ Helm charts: scaffold only (directories exist, manifests pending)
- ✗ Actual business logic: no internal/ packages yet

**Pending dependencies (TDD-driven):**
- pgx, slog, testify, prometheus/client_golang, chi|gin (declared in PROJECT.md, added as modules need them)

---

*Stack analysis: 2026-06-24*
