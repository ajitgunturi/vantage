# vantage — Elastic GPU Telemetry Pipeline

A production-grade, horizontally-scalable pipeline that ingests live NVIDIA DCGM GPU telemetry,
moves it through a **custom message queue built from scratch in Go**, persists it to PostgreSQL,
and exposes it via a documented REST API. Four strictly independent microservices on Kubernetes.

```
CSV → Streamer →(gRPC Produce)→ MQ →(gRPC Consume stream)→ Collector → PostgreSQL → API Gateway → client
```

> This README grows **one phase at a time**. Each phase adds the commands to run and verify the
> components it ships, so you can always clone the current state and see what works end-to-end.

## Services

| Service | Entrypoint | Role | Status |
|---|---|---|---|
| **MQ** | `cmd/mq/` | Custom in-memory broker — gRPC data plane (`Produce`/`Consume`) + HTTP control plane (`/inspect`) | ✅ Phase 1 |
| **Streamer** | `cmd/streamer/` | Loops the DCGM CSV forever, restamps `now`, publishes to MQ | ⏳ Phase 3 |
| **Collector** | `cmd/collector/` | Consumes the MQ stream, batch-inserts to Postgres | ⏳ Phase 3 |
| **API Gateway** | `cmd/gateway/` | Read API over Postgres; OpenAPI auto-generated via `swag` | ⏳ Phase 4 |
| **PostgreSQL** | (Helm dep) | Time-series store; schema + connection pool in `pkg/db` | 🔜 Phase 2 |

Roadmap and per-phase plans live under [`.planning/`](.planning/); the authoritative spec is
[`instructions.md`](instructions.md).

## Prerequisites

- **Go 1.26+** (the only hard requirement to build, test, and smoke-test what exists today)
- **make**, **curl** — for the quickstart and smoke suite
- *Later phases:* `protoc` (regenerating proto), Docker + `docker compose` (Phase 2 dev stack),
  `kind` + Helm (Phase 5). Install dev tooling with `make tools`.

## Repository layout

```text
api/proto/      # shared gRPC contracts (mq.proto)
cmd/<svc>/      # independent service entrypoints (mq today; others land per phase)
internal/       # MQ service-private packages (queue, server, http, config)
pkg/            # the ONLY cross-service surface (pb/ generated code; db/, models/ later)
scripts/smoke/  # runnable manual smoke checks, one set per phase (+ the mqprobe gRPC client)
build/          # one multi-stage Dockerfile per service (Phase 5)
deployments/    # Helm charts (Phase 5)
Makefile        # build, test, coverage, smoke, proto, docker, k8s
```

`make help` lists every target.

## Quickstart

### Build

```sh
make build            # builds every service that exists (skips not-yet-created ones)
# or just the MQ:
go build -o bin/mq ./cmd/mq
```

### Run the MQ

The broker is in-memory — no database or external broker needed.

```sh
./bin/mq
# mq: gRPC on :50051, HTTP on :8080, buffer 10000
```

Configuration is env-first (all optional):

| Env var | Default | Meaning |
|---|---|---|
| `MQ_GRPC_ADDR` | `:50051` | gRPC data-plane listen address (`Produce`, `Consume`) |
| `MQ_HTTP_ADDR` | `:8080` | HTTP control-plane listen address |
| `MQ_BUFFER_SIZE` | `10000` | Ring-buffer capacity (drop-oldest when full) |

### Inspect the queue

```sh
curl -s localhost:8080/api/v1/queue/inspect
# {"capacity":10000,"depth":0,"produced_total":0,"consumed_total":0,"dropped_total":0,"active_consumers":0}
```

### Produce & consume a message

The MQ speaks gRPC (`mq.v1.MQService`). The repo ships a tiny pure-Go probe so you don't need
`grpcurl`:

```sh
# In one terminal: ./bin/mq
# In another:
go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:50051 -n 20
# mqprobe: OK — produced 20, consumed 20 via 127.0.0.1:50051
```

`mqprobe` has three modes via `-mode`:

| `-mode`   | Behaviour                                                            |
|-----------|---------------------------------------------------------------------|
| `both`    | *(default)* attach a `Consume` stream first, then produce N — the consumer-already-attached path |
| `produce` | produce N messages and exit, leaving them buffered in the MQ        |
| `consume` | attach a `Consume` stream and drain N messages, then exit           |

Running `produce` in one invocation and `consume` in a later one exercises the **late-join** path —
the producer publishes and disconnects, and a consumer that attaches afterwards still drains every
buffered message:

```sh
go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:50051 -n 20 -mode produce  # publish, then exit
go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:50051 -n 20 -mode consume  # join later, drain all 20
```

## Testing

### Automated (unit + concurrency + coverage)

```sh
make test       # go test -race across the module
make coverage   # enforces ≥90% line coverage on internal/ packages
make lint       # golangci-lint (falls back to go vet)
```

The MQ's correctness under concurrency (N produced = N consumed across K consumers, no duplication,
no goroutine leaks, and late-joining consumers still receiving every buffered message) is proven by
race-detector tests in `internal/server` and `internal/queue`.

### Manual smoke suite (watch each phase work end-to-end)

A runnable, dependency-light suite you can execute by hand to verify each phase's deliverables.
Each phase adds `scripts/smoke/phaseNN-*.sh`.

```sh
make smoke         # run every phase's smoke check shipped so far
make smoke-01      # run just Phase 1 (MQ)
```

**Phase 1 (`make smoke-01`)** builds the MQ on dedicated ports, starts it, then via `mqprobe`
(1) produces/consumes 20 messages over a real `Consume` stream, and (2) runs a **late-join**
scenario — produce 20 in one process, then consume them in a separate process that attaches after
the producer is gone. It cross-checks the `GET /api/v1/queue/inspect` counters throughout — proving
the data plane, the late-join buffering, and the control plane all work.

## Phase status & how to verify

| Phase | Delivers | Verify with |
|---|---|---|
| **1 — Foundation** ✅ | proto contract + custom in-memory MQ (gRPC + HTTP inspect) | `make test`, `make coverage`, `make smoke-01` |
| 2 — Storage | Postgres time-series schema + `pgxpool` in `pkg/db` | _(coming)_ `make dev-up`, `make smoke-02` |
| 3 — Pipeline | Streamer + Collector (CSV → MQ → Postgres) | _(coming)_ `make smoke-03` |
| 4 — API Gateway | REST read API + auto-generated OpenAPI | _(coming)_ `make smoke-04` |
| 5 — DevOps | Dockerfiles + Helm; runs on kind | _(coming)_ `make docker`, `make kind-up` |

---

Built phase-by-phase with the GSD framework. See [`CLAUDE.md`](CLAUDE.md) for conventions and the
hard constraints (custom MQ from scratch, ≥90% coverage, auto-generated OpenAPI, time-series schema).
