# vantage — Elastic GPU Telemetry Pipeline

A production-grade, horizontally-scalable pipeline that ingests live NVIDIA DCGM GPU telemetry,
moves it through a **custom message queue built from scratch in Go**, persists it to PostgreSQL,
and exposes it via a documented REST API. Four strictly independent microservices on Kubernetes.

```
CSV → Streamer →(gRPC Produce)→ MQ →(gRPC Consume bidi stream: msgs ↓ / credit+acks ↑)→ Collector → PostgreSQL → API Gateway → client
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
| **PostgreSQL** | (Helm dep) | Time-series store; schema + connection pool in `pkg/db` | ✅ Phase 2 |

Roadmap and per-phase plans live under [`.planning/`](.planning/); the authoritative spec is
[`instructions.md`](instructions.md).

## Prerequisites

- **Go 1.26+** (the only hard requirement to build, test, and smoke-test what exists today)
- **make**, **curl** — for the quickstart and smoke suite
- **Docker + `docker compose`** — required for Phase 2: brings up the local Postgres dev stack (`make dev-up`); `psql` is NOT required on the host — `make smoke-02` runs all SQL assertions through the `postgres:17-alpine` container bundled in the compose stack
- *Later phases:* `protoc` (regenerating proto), `kind` + Helm (Phase 5). Install dev tooling with `make tools`.

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
| `MQ_CONSUME_CREDIT` | `20` | Broker-side **fallback** in-flight window, applied when a consumer's first credit message is ≤ 0. Non-positive/non-numeric values are ignored and the default is kept. |

### Delivery semantics — broker-side at-least-once

As of Phase 01.1 the MQ delivers **at-least-once** over a **bidirectional** `Consume` stream
(see [`docs/adr/ADR-001`](docs/adr/ADR-001-bidi-at-least-once-delivery.md)):

- The consumer opens the stream and first sends a **credit** message — its in-flight window `C`.
  The broker never has more than `C` unacked messages out to that consumer (**client-driven flow
  control**, no over-pull). If that first credit is **≤ 0**, the broker substitutes its own default
  (`MQ_CONSUME_CREDIT`, default `20`); any `C` above the **ceiling of `1000`** is clamped down so an
  over-large initial credit can't exhaust broker memory.
- The broker assigns each message a monotonic **id** and **leases** it to the consumer. A message
  leaves broker custody **only when the consumer acks that id** — `Consume{AckId: msg.id}`.
- If a consumer disconnects with **unacked** leases, those messages are **re-enqueued at the front
  and redelivered** to a surviving consumer — **no loss**. Redelivery can produce **duplicates**,
  which the (idempotent) Collector absorbs downstream.
- Steady state with all consumers acking is still **unique delivery** — each message goes to exactly
  one consumer.

Storage is still **in-memory only** (a ring buffer behind the `Store` interface); crash durability
is the opt-in WAL backend planned for Phase 6.

### Inspect the queue

```sh
curl -s localhost:8080/api/v1/queue/inspect
# {"capacity":10000,"depth":0,"produced_total":40,"delivered_total":46,"consumed_total":40,
#  "redelivered_total":6,"dropped_total":0,"active_consumers":0,"in_flight":0}
```

Counter meanings (at-least-once, D-09): `produced_total` = accepted by Produce ·
`delivered_total` = messages **sent** to consumers · `consumed_total` = **acks** (confirmed
deliveries — *not* sends) · `redelivered_total` = re-enqueued after a disconnect-with-unacked ·
`in_flight` = currently sent-but-unacked. The identity `delivered = consumed + redelivered +
in_flight` holds at rest.

### Produce & consume a message

The MQ speaks gRPC (`mq.v1.MQService`). The repo ships a tiny pure-Go probe so you don't need
`grpcurl`. Its `consume` path now speaks the bidi protocol — it sends initial credit and **acks
every message by broker id**:

```sh
# In one terminal: ./bin/mq
# In another:
go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:50051 -n 20 -credit 20
# mqprobe: OK — produced 20, consumed 20 via 127.0.0.1:50051
```

`mqprobe` flags: `-mode` selects the scenario, `-credit` (default `20`) sets the initial bidi
flow-control window for the consume side.

| `-mode`   | Behaviour                                                            |
|-----------|---------------------------------------------------------------------|
| `both`    | *(default)* attach a bidi `Consume` stream first (send credit), then produce N — receive **and ack** each |
| `produce` | produce N messages and exit, leaving them buffered in the MQ        |
| `consume` | attach a bidi `Consume` stream, receive N messages and **ack each by id**, then exit |

Running `produce` in one invocation and `consume` in a later one exercises the **late-join** path —
the producer publishes and disconnects, and a consumer that attaches afterwards still drains (and
acks) every buffered message. Reading **fewer** than produced leaves the rest retrievable — zero
loss:

```sh
go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:50051 -n 20 -mode produce            # publish, then exit
go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:50051 -n 10 -mode consume -credit 8  # join later, read+ack 10
go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:50051 -n 10 -mode consume -credit 8  # the other 10 are still there
```

## Phase 2 — Storage Foundation

### Bring up local Postgres

`docker-compose.yml` provides a `postgres:17-alpine` instance for local development and smoke
testing. The credentials (`vantage`/`vantage` on `localhost:5432/vantage`) are **local dev
defaults only** — they exist in the repo for developer convenience and are never used in
production. Production DSNs are always supplied via `VANTAGE_DB_DSN`.

```sh
make dev-up    # start Postgres (waits for healthcheck)
make dev-down  # stop Postgres
```

### Apply the schema

The shared `pkg/db` library ships the versioned migration embedded in the binary. `cmd/migrate`
is a one-shot runner — it reads `VANTAGE_DB_DSN`, calls `pkg/db.Migrate`, and exits:

```sh
export VANTAGE_DB_DSN=postgres://vantage:vantage@localhost:5432/vantage?sslmode=disable
go run ./cmd/migrate
# migrate: schema up to date
```

`pkg/db.Migrate` is idempotent (`migrate.ErrNoChange` is treated as success), so it is safe to
call on every service startup or restart.

### Storage environment variables

| Env var | Required | Default | Meaning |
|---|---|---|---|
| `VANTAGE_DB_DSN` | yes | — | Full `postgres://` connection string; **never logged** |
| `VANTAGE_DB_MAX_CONNS` | no | 0 (pgxpool default) | Maximum pool connections |

Both env vars are read by `pkg/db.FromEnv()`, imported by Collector (Phase 3) and Gateway (Phase 4).

### GPU identity convention (D-04)

`gpu_id` stores the GPU **UUID** (e.g. `GPU-5fd4f087-...`), not the ordinal index (`"0"`).
The column is named `gpu_id` to match the spec's mandated composite index expression and the
`/api/v1/gpus/{id}` API route — but the value stored is always the UUID. The GPU ordinal,
device name, model, hostname, and pod/container metadata are stored as descriptive columns,
not as identity.

### Verify the storage foundation

```sh
make smoke-02
```

`make smoke-02` runs `scripts/smoke/phase02-postgres.sh`, which:

1. Starts the dev stack (`make dev-up`) if Postgres is not already running
2. Applies the schema via `go run ./cmd/migrate`
3. Asserts that `gpu_metrics` exists with the expected columns
4. Asserts that both indexes exist: `idx_gpu_metrics_gpu_id_ts` (composite) and
   `uq_gpu_metrics_natural_key` (unique)
5. Seeds 100,000 rows (`10 GPUs × 10 metrics × 1,000 timestamps`) and runs `ANALYZE`
6. Runs `EXPLAIN` on a selective single-GPU 1-hour range query and asserts `Index Scan`
   (not `Seq Scan`) — proving the planner uses the composite index at representative scale

## Testing

### Automated (unit + concurrency + coverage)

```sh
make test       # go test -race across the module
make coverage   # enforces ≥90% line coverage on internal/ packages
make lint       # golangci-lint (falls back to go vet)
```

The MQ's correctness under concurrency is proven by race-detector tests in `internal/server` and
`internal/queue` (run at `-count=50`): broker-side at-least-once with **no loss** on consumer
disconnect, **no over-pull** beyond credit `C`, **redelivery** of unacked leases to survivors,
**unique** steady-state delivery, **safe** ack handling (unknown/double acks are no-ops), and no
goroutine leaks.

### Manual smoke suite (watch each phase work end-to-end)

A runnable, dependency-light suite you can execute by hand to verify each phase's deliverables.
Each phase adds `scripts/smoke/phaseNN-*.sh`.

```sh
make smoke         # run every phase's smoke check shipped so far
make smoke-01      # run just Phase 1 (MQ)
```

**Phase 1 (`make smoke-01`)** builds the MQ on dedicated ports, starts it, then via the bidi
`mqprobe` (1) produces/consumes 20 messages over a real bidi `Consume` stream with credit + per-id
acks, and (2) runs a **late-join no-loss** scenario — produce 20, consume only 10, then drain the
remaining 10 in a third process — proving a consumer that reads fewer than produced loses nothing
across the producer's disconnect, and (3) runs a **credit-boundary** scenario — a consumer whose
first credit is `0` must not deadlock: the broker substitutes its default window and still drains
all 20. It cross-checks the at-least-once `GET /api/v1/queue/inspect`
counters (`delivered_total`, `consumed_total` = acks, `redelivered_total`) throughout — the
redelivered count goes positive exactly when the partial consumer disconnects holding unacked
leases, proving redelivery over the wire.

## Phase status & how to verify

| Phase | Delivers | Verify with |
|---|---|---|
| **1 — Foundation** ✅ | proto contract + custom in-memory MQ (gRPC + HTTP inspect) | `make test`, `make coverage`, `make smoke-01` |
| **2 — Storage** ✅ | Postgres time-series schema + `pgxpool` in `pkg/db` | `make dev-up`, `go run ./cmd/migrate`, `make smoke-02` |
| 3 — Pipeline | Streamer + Collector (CSV → MQ → Postgres) | _(coming)_ `make smoke-03` |
| 4 — API Gateway | REST read API + auto-generated OpenAPI | _(coming)_ `make smoke-04` |
| 5 — DevOps | Dockerfiles + Helm; runs on kind | _(coming)_ `make docker`, `make kind-up` |

---

Built phase-by-phase with the GSD framework. See [`CLAUDE.md`](CLAUDE.md) for conventions and the
hard constraints (custom MQ from scratch, ≥90% coverage, auto-generated OpenAPI, time-series schema).
