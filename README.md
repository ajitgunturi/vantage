# vantage — Elastic GPU Telemetry Pipeline

A production-grade, horizontally-scalable pipeline that ingests live NVIDIA DCGM GPU telemetry,
moves it through a **custom message queue built from scratch in Go**, persists it to PostgreSQL,
and exposes it via a documented REST API. Four strictly independent microservices on Kubernetes.

```
CSV → Streamer →(gRPC Produce)→ MQ →(gRPC Consume bidi stream: msgs ↓ / credit+acks ↑)→ Collector → PostgreSQL → API Gateway → client
```

## Services

| Service | Entrypoint | Role | Status |
|---|---|---|---|
| **MQ** | `cmd/mq/` | Custom in-memory broker — gRPC data plane (`Produce`/`Consume`) + HTTP control plane | ✅ Phase 1 |
| **Streamer** | `cmd/streamer/` | Loops the DCGM CSV forever, restamps `now`, publishes to MQ | ✅ Phase 3 |
| **Collector** | `cmd/collector/` | Consumes the MQ stream, batch-inserts to Postgres | ✅ Phase 3 |
| **API Gateway** | `cmd/gateway/` | Read API over Postgres; OpenAPI auto-generated via `swag` | ✅ Phase 4 |
| **PostgreSQL** | (Helm dep) | Time-series store; schema + connection pool in `pkg/db` | ✅ Phase 2 |

## Prerequisites

- **Go 1.26+** — the only requirement for `make build`, `make test`, and local smoke checks
- **make**, **curl**
- **Docker + `docker compose`** — required for Postgres dev stack (`make dev-up`), integration tests, and image builds
- **kind + Helm** (for Kubernetes deploy) — install via `make tools`
- **protoc** (only if regenerating proto) — install via `brew install protobuf` then `make proto`

## Make commands

| Target | Purpose |
|---|---|
| `make help` | List all targets |
| `make tools` | Install dev tools: protoc plugins, swag, golangci-lint, kind |
| `make proto` | Compile `api/proto/*.proto` → `pkg/pb` |
| `make build` | Build all four service binaries into `bin/` |
| `make test` | `go test -race ./...` — unit + integration tests |
| `make coverage` | Enforce ≥ 90% line coverage on `internal/` and `pkg/` |
| `make e2e` | End-to-end pipeline tests (requires Docker) |
| `make swagger` | Auto-generate OpenAPI spec from gateway code annotations |
| `make lint` | golangci-lint (fallback: `go vet`) |
| `make tidy` | `go mod tidy` |
| `make clean` | Remove `bin/` and coverage artifacts |
| `make smoke` | Run every phase's smoke check |
| `make smoke-NN` | Run one phase's smoke check (e.g. `make smoke-05`) |
| `make dev-up` | Start local Postgres via docker compose |
| `make dev-down` | Stop local Postgres |
| `make docker` | Build all five service images |
| `make docker-<svc>` | Build one service image |
| `make kind-up` | Create the local kind cluster |
| `make helm-install` | Install/upgrade the umbrella Helm chart |
| `make kind-down` | Delete the kind cluster |
| `make kind-load` | Load all five `vantage/*:dev` images into kind |
| `make deploy` | Full deploy: docker build → kind-load → helm-install |
| `make dependency-update` | Pull Helm chart dependencies (Bitnami PostgreSQL OCI) |
| `make soak` | Sustained pipeline soak (`SOAK_DURATION=60`, `SOAK_STREAMERS=3`) |
| `make test-harness` | Live-infrastructure E2E harness (requires Docker) |

## Deploy & Try It

Full kind cluster walkthrough — everything needed to evaluate the running system.

### 1. Stand up the cluster

```sh
make kind-up          # create a single-node kind cluster
make deploy           # docker build (5 images) → kind-load → helm-install
make smoke-05         # prove the pipeline is flowing end-to-end
```

`make deploy` builds `vantage/{mq,streamer,collector,gateway,migrate}:dev`, loads them into kind,
and installs the Helm release `vantage`. A migration hook Job applies the schema before service
pods roll.

### 2. Query the API Gateway

```sh
kubectl port-forward svc/vantage-gateway 8081:8080
```

```sh
# List GPU IDs
curl -s http://localhost:8081/api/v1/gpus | python3 -m json.tool

# All telemetry for a GPU (capped, newest-first)
curl -s http://localhost:8081/api/v1/gpus/GPU-5fd4f087-.../telemetry | python3 -m json.tool

# Time-window filter (RFC3339)
curl -s 'http://localhost:8081/api/v1/gpus/GPU-5fd4f087-.../telemetry?start_time=2024-01-01T00:00:00Z&end_time=2024-01-02T00:00:00Z'

# Pagination (cursor-free offset; response includes total + has_next)
curl -s 'http://localhost:8081/api/v1/gpus/GPU-5fd4f087-.../telemetry?limit=100&offset=0'
curl -s 'http://localhost:8081/api/v1/gpus/GPU-5fd4f087-.../telemetry?limit=100&offset=100'
```

Swagger UI (live interactive docs): `http://localhost:8081/swagger/`

### 3. Inspect the MQ

```sh
kubectl port-forward svc/vantage-mq 8082:8080
curl -s localhost:8082/api/v1/queue/inspect
```

### 4. Horizontal scaling

```sh
# Scale Streamers and Collectors independently (stateless services)
kubectl scale deployment/vantage-streamer --replicas=3
kubectl scale deployment/vantage-collector --replicas=2

# The MQ stays at 1 replica — it is a single-replica in-memory broker by design (ADR-001)
```

### 5. Tear down

```sh
make kind-down
```

### Local (no Kubernetes)

See [docs/development.readme.md](docs/development.readme.md) for `make dev-up`, per-service
startup commands, and the smoke suite without kind.

## Submission Checklist

| Graded item | Where to find it |
|---|---|
| Build gate (`make build`) | `Makefile` |
| Test gate (`go test -race`, `make test`) | `Makefile` target `test` |
| ≥ 90% coverage gate (`make coverage`) | `Makefile` target `coverage` |
| Lint gate (`make lint`) | `Makefile` target `lint` |
| kind E2E deploy (`make deploy`, `make smoke-05`) | `Makefile`; `scripts/smoke/phase05-kind.sh` |
| Soak (10 concurrent Streamers; `make soak`) | `Makefile` target `soak`; `scripts/soak.sh` |
| Live E2E harness (`make test-harness`) | `Makefile` target `test-harness`; `test/harness/` |
| Auto-generated OpenAPI (`make swagger`, `/swagger/`) | `Makefile`; `pkg/docs/`; gateway annotations |
| ADR design records | [`docs/adr/README.md`](docs/adr/README.md) |
| AI usage disclosure | [`docs/AI_USAGE.md`](docs/AI_USAGE.md) |
| AI prompt log | [`docs/AI_PROMPTS.md`](docs/AI_PROMPTS.md) |
| Future enhancements (WAL + multi-schema) | [`docs/FUTURE.md`](docs/FUTURE.md) |
| CI workflow | `.github/workflows/ci.yml` |

## Documentation

| Document | Contents |
|---|---|
| [docs/mq.readme.md](docs/mq.readme.md) | MQ config, delivery semantics, inspect counters, mqprobe |
| [docs/streamer.readme.md](docs/streamer.readme.md) | Streamer config, CSV prereqs, restamping |
| [docs/collector.readme.md](docs/collector.readme.md) | Collector config, exactly-once semantics |
| [docs/gateway.readme.md](docs/gateway.readme.md) | Gateway endpoints, pagination, Swagger |
| [docs/development.readme.md](docs/development.readme.md) | Repo layout, storage, smoke suite, testing, DevOps, CI |
| [docs/FUTURE.md](docs/FUTURE.md) | Designed-but-deferred: opt-in WAL + multi-schema messages |
| [docs/adr/README.md](docs/adr/README.md) | All ten architectural decision records |
| [docs/AI_USAGE.md](docs/AI_USAGE.md) | Scope, model, oversight, known limitations |
| [docs/AI_PROMPTS.md](docs/AI_PROMPTS.md) | Verbatim prompt log across all phases |
| [instructions.md](instructions.md) | Authoritative project spec |
| [CLAUDE.md](CLAUDE.md) | Conventions and hard constraints |

---

Built phase-by-phase with the GSD framework. This is the final v1 consolidation — Phases 1–6
complete on branch `adr-backfill`; Phase 7 (WAL durability) deferred as an optional post-v1
enhancement (see [`docs/FUTURE.md`](docs/FUTURE.md)).
