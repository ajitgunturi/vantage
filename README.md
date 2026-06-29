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

### Delivery semantics — broker-side at-least-once

As of Phase 01.1 the MQ delivers **at-least-once** over a **bidirectional** `Consume` stream
(see [`docs/adr/ADR-001`](docs/adr/ADR-001-bidi-at-least-once-delivery.md)):

- The consumer opens the stream and first sends a **credit** message — its in-flight window `C`.
  The broker never has more than `C` unacked messages out to that consumer (**client-driven flow
  control**, no over-pull).
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
across the producer's disconnect. It cross-checks the at-least-once `GET /api/v1/queue/inspect`
counters (`delivered_total`, `consumed_total` = acks, `redelivered_total`) throughout — the
redelivered count goes positive exactly when the partial consumer disconnects holding unacked
leases, proving redelivery over the wire.

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
