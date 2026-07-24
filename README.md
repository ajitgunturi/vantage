# vantage — Elastic GPU Telemetry Pipeline

A production-grade, horizontally-scalable pipeline that ingests live NVIDIA DCGM GPU telemetry,
moves it through a **custom message queue built from scratch in Go**, persists it to PostgreSQL,
and exposes it via a documented REST API. Four strictly independent microservices on Kubernetes.

```
CSV → Streamer →(gRPC ProduceBatch)→ MQ →(gRPC Consume bidi stream: msgs ↓ / credit+acks ↑)→ Collector → PostgreSQL → API Gateway → client
```

## Services

| Service | Entrypoint | Role | Status |
|---|---|---|---|
| **MQ** | `cmd/mq/` | Custom in-memory broker — gRPC data plane (`ProduceBatch`/`Produce`/`Consume`) + HTTP control plane (inspect, DLQ, `/metrics`) | ✅ |
| **Streamer** | `cmd/streamer/` | Loops the DCGM CSV forever, restamps `now`, publishes batches to MQ | ✅ |
| **Collector** | `cmd/collector/` | Consumes the MQ stream, batch-inserts to Postgres; scales on queue backlog | ✅ |
| **API Gateway** | `cmd/gateway/` | Read API over Postgres (keyset + offset pagination); OpenAPI auto-generated via `swag` | ✅ |
| **PostgreSQL** | (Helm dep) | Time-series store; daily-partitioned `gpu_metrics` with retention CronJob | ✅ |

All four services expose Prometheus metrics on `GET /metrics` plus `/healthz` (and `/readyz` on
the MQ). See [Monitoring & autoscaling](#monitoring--autoscaling) below.

## Prerequisites

- **Go 1.26+** — the only requirement for `make build`, `make test`, and local smoke checks
- **make**, **curl**
- **Docker + `docker compose`** — required for the Postgres dev stack (`make dev-up`),
  integration tests, and image builds. Rancher Desktop works; `DOCKER_HOST` and
  testcontainers/Ryuk notes live in `.env.example`.
- **kind + Helm v3** (for Kubernetes deploy) — install via `make tools`
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
| `make smoke` | Run every phase's smoke check, then leave a running local stack for manual testing (`SMOKE_NO_STACK=1` to skip) |
| `make smoke-NN` | Run one phase's smoke check (e.g. `make smoke-05`) |
| `make stack-up` | Start all four services locally against dev Postgres (gateway :8080, MQ :8081/:50051, streamer :9000, collector :9001) — import `Insomnia_Collection.yaml` for ready-made requests |
| `make stack-down` | Stop the local stack (Postgres stays up; `make dev-down` stops it) |
| `make kind-forward` | Port-forward the kind cluster onto the same local ports — the Insomnia collection works unchanged against the cluster |
| `make kind-unforward` | Stop the kind port-forwards |
| `make dev-up` | Start local Postgres via docker compose |
| `make dev-down` | Stop local Postgres |
| `make docker` | Build all five service images |
| `make docker-<svc>` | Build one service image |
| `make kind-up` | Create the local kind cluster |
| `make helm-install` | Install/upgrade the umbrella Helm chart |
| `make kind-down` | Delete the kind cluster |
| `make kind-load` | Load all five `vantage/*:dev` images into kind |
| `make deploy CSV=<path>` | Full deploy: docker build → kind-load → helm-install (telemetry CSV **required** — prompts on a TTY) |
| `make dependency-update` | Pull Helm chart dependencies (Bitnami PostgreSQL OCI) |
| `make soak` | Sustained pipeline soak (`SOAK_DURATION=60`, `SOAK_STREAMERS=3`) |
| `make test-harness` | Live-infrastructure E2E harness (requires Docker) |

## Deploy & Try It

Full kind cluster walkthrough — everything needed to evaluate the running system.

### 1. Stand up the cluster

```sh
make kind-up                        # create a single-node kind cluster
make deploy CSV=/path/to/your.csv   # docker build (5 images) → kind-load → helm-install
make smoke-05                       # prove the pipeline is flowing end-to-end
```

`make deploy` builds `vantage/{mq,streamer,collector,gateway,migrate}:dev`, loads them into kind,
and installs the Helm release `vantage`. A migration hook Job applies the schema before service
pods roll.

### Bring your own CSV (mandatory)

`CSV=<path>` is **required** — there is no silent fixture fallback. The pipeline streams whatever
DCGM telemetry you give it: `make deploy CSV=/path/to/your.csv` bakes exactly that file into the
streamer image at `/data/dcgm_metrics.csv`. Any filename works, and the path may live anywhere on
disk (it is staged into the Docker build context automatically).

- **No `CSV=` on an interactive terminal?** `make deploy` prompts for the path before building.
- **Scripted / CI / piped stdin?** `make deploy` fails loudly with usage — the streamer image never
  bakes demo data implicitly; choosing the telemetry is intentional by design.
- The file must exist and be readable, or deploy aborts with a clear error before any build.

Dev/test flows need no CSV: `make docker`, docker compose, and the e2e/smoke suites build a
CSV-less image and mount the single test fixture `testdata/fixture.csv` at runtime.

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
```

**Pagination** — two modes, both return the same envelope:

```sh
# Offset mode (includes pagination.total)
curl -s 'http://localhost:8081/api/v1/gpus/GPU-5fd4f087-.../telemetry?limit=100&offset=0'

# Keyset (cursor) mode: pass the previous page's next_cursor verbatim.
# O(page) per page and stable under live ingest; mutually exclusive with offset.
curl -s 'http://localhost:8081/api/v1/gpus/GPU-5fd4f087-.../telemetry?limit=100&cursor=<next_cursor>'
```

Response envelope:

```json
{
  "data": [ ... ],
  "pagination": {
    "limit": 100,
    "offset": 0,
    "total": 12345,
    "has_next": true,
    "next_cursor": "opaque-token"
  }
}
```

`next_cursor` is emitted in **both** modes whenever another page exists, so offset clients can
switch to keyset walks mid-way. `total` appears in offset mode only — cursor mode skips the
COUNT(*) scan by design (see [ADR-010](docs/adr/ADR-010-telemetrypage-pagination-envelope.md)).

Swagger UI (live interactive docs): `http://localhost:8081/swagger/`

### 3. Inspect the MQ

```sh
kubectl port-forward svc/vantage-mq 8082:8080

# Queue depths, in-flight, retry/DLQ lane counters
curl -s localhost:8082/api/v1/queue/inspect

# Broker dead-letter queue: messages that exhausted redelivery attempts
curl -s localhost:8082/api/v1/queue/dlq

# Re-enqueue everything in the broker DLQ
curl -s -X POST localhost:8082/api/v1/queue/dlq/replay
```

### 4. Metrics

Every service exposes Prometheus metrics:

```sh
curl -s localhost:8082/metrics          # MQ (queue depth, retry/DLQ lanes, in-flight)
curl -s localhost:8081/metrics          # Gateway (port-forwarded above)
# Streamer / Collector: port-forward their pods' :9000 / :9001 health ports
```

### 5. Horizontal scaling

```sh
# Scale Streamers manually (stateless; up to 10 concurrent instances supported)
kubectl scale deployment/vantage-streamer --replicas=3

# Collector: backlog-driven HPA — scales on the broker's queue backlog
# (external metric mq_queue_backlog = mq_queue_depth + mq_retry_depth).
# Requires the monitoring stack; see deployments/monitoring/README.md.
helm upgrade vantage deployments --set collector.autoscaling.enabled=true

# Gateway: CPU-based HPA (request-bound service — CPU is the right signal there)

# The MQ stays at 1 replica — it is a single-replica in-memory broker by design (ADR-001)
```

### 6. Tear down

```sh
make kind-down
```

### Local (no Kubernetes)

See [docs/development.readme.md](docs/development.readme.md) for `make dev-up`, per-service
startup commands, and the smoke suite without kind. `make stack-up` runs the full stack locally:

- Gateway `http://localhost:8080` — `/api/v1/gpus`, `/swagger/`, `/metrics`
- MQ `http://localhost:8081` — `/api/v1/queue/inspect`, `/api/v1/queue/dlq`, `/metrics` (gRPC `:50051`)
- Streamer `http://localhost:9000`, Collector `http://localhost:9001` — `/healthz`, `/metrics`

## Monitoring & autoscaling

The [deployments/monitoring/](deployments/monitoring/README.md) stack wires the services'
`/metrics` endpoints into Kubernetes autoscaling: kube-prometheus-stack scrapes the pods,
prometheus-adapter surfaces `mq_queue_depth + mq_retry_depth` as the external metric
`mq_queue_backlog`, and the Collector HPA scales on it (target backlog per replica is tunable
via `collector.autoscaling.targetBacklogPerReplica`). See
[ADR-014](docs/adr/ADR-014-prometheus-metrics-queue-depth-hpa.md) for the design rationale.

## Data retention

`gpu_metrics` is partitioned by UTC day (migration 000003). A nightly CronJob
(`deployments/templates/retention-cronjob.yaml`) drops whole partitions past the retention
window — O(1), no vacuum churn — and pre-creates upcoming partitions so the write path never
waits on DDL. Tunables live under the `retention.*` Helm values (`enabled`, `days`,
`precreateDays`, `schedule`). See
[ADR-012](docs/adr/ADR-012-daily-partitioning-retention.md).

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
| [docs/mq.readme.md](docs/mq.readme.md) | MQ config, delivery semantics, overload/DLQ, inspect counters, mqprobe |
| [docs/streamer.readme.md](docs/streamer.readme.md) | Streamer config, CSV prereqs, restamping, batch publish |
| [docs/collector.readme.md](docs/collector.readme.md) | Collector config, effectively-once persistence, poison-row DLQ |
| [docs/gateway.readme.md](docs/gateway.readme.md) | Gateway endpoints, pagination (offset + keyset), Swagger |
| [docs/development.readme.md](docs/development.readme.md) | Repo layout, storage, smoke suite, testing, DevOps, CI |
| [deployments/monitoring/README.md](deployments/monitoring/README.md) | Prometheus + adapter setup, backlog-driven Collector HPA |
| [docs/FUTURE.md](docs/FUTURE.md) | Designed-but-deferred work + known operational gaps |
| [docs/adr/README.md](docs/adr/README.md) | All architectural decision records |
| [docs/DEVIATIONS.md](docs/DEVIATIONS.md) | Deliberate deviations from the brief |
| [docs/AI_USAGE.md](docs/AI_USAGE.md) | Scope, model, oversight, known limitations |
| [docs/AI_PROMPTS.md](docs/AI_PROMPTS.md) | Verbatim prompt log across all phases |
| [instructions.md](instructions.md) | Authoritative project spec |
| [CLAUDE.md](CLAUDE.md) | Conventions and hard constraints |

---

Built phase-by-phase with the GSD framework. Phases 1–6 complete, plus post-v1 hardening:
delivery-semantics (backpressure, retry/DLQ lanes, lease TTL — PR #13) and observability & scale
(Prometheus metrics, backlog HPA, batch publish, keyset pagination, partitioned retention —
PR #14). Phase 7 (WAL durability) remains deferred as an optional enhancement (see
[`docs/FUTURE.md`](docs/FUTURE.md)).
