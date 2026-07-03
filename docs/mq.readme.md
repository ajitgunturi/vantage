# MQ — Custom In-Memory Message Broker

← back to the [README](../README.md)

The broker is built from scratch in Go (`cmd/mq/`) — gRPC data plane (`Produce`/`Consume`) plus an
HTTP control plane (`/api/v1/queue/inspect`). It is in-memory — no database or external broker
needed to run it.

## Run the MQ

```sh
make build   # or just: go build -o bin/mq ./cmd/mq
./bin/mq
# mq: gRPC on :50051, HTTP on :8080, buffer 10000
```

## Configuration

Configuration is env-first (all optional):

| Env var | Default | Meaning |
|---|---|---|
| `MQ_GRPC_ADDR` | `:50051` | gRPC data-plane listen address (`Produce`, `Consume`) |
| `MQ_HTTP_ADDR` | `:8080` | HTTP control-plane listen address |
| `MQ_BUFFER_SIZE` | `10000` | Ring-buffer capacity (drop-oldest when full) |
| `MQ_CONSUME_CREDIT` | `20` | Broker-side **fallback** in-flight window, applied when a consumer's first credit message is ≤ 0. Non-positive/non-numeric values are ignored and the default is kept. |

## Delivery semantics — broker-side at-least-once

As of Phase 01.1 the MQ delivers **at-least-once** over a **bidirectional** `Consume` stream
(see [`ADR-001`](adr/ADR-001-bidi-at-least-once-delivery.md)):

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

Storage is **in-memory only** (a ring buffer behind the `Store` interface); crash durability is the
opt-in WAL backend designed for Phase 7 (deferred post-v1; see [`docs/FUTURE.md`](FUTURE.md)).

## Inspect the queue

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

## Produce & consume a message

The MQ speaks gRPC (`mq.v1.MQService`). The repo ships a tiny pure-Go probe so you don't need
`grpcurl`. Its `consume` path speaks the bidi protocol — it sends initial credit and **acks
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

## Verify (smoke-01)

**Phase 1 (`make smoke-01`)** builds the MQ on dedicated ports, starts it, then via the bidi
`mqprobe` (1) produces/consumes 20 messages over a real bidi `Consume` stream with credit + per-id
acks, and (2) runs a **late-join no-loss** scenario — produce 20, consume only 10, then drain the
remaining 10 in a third process — proving a consumer that reads fewer than produced loses nothing
across the producer's disconnect, and (3) runs a **credit-boundary** scenario — a consumer whose
first credit is `0` must not deadlock: the broker substitutes its default window and still drains
all 20. It cross-checks the at-least-once `GET /api/v1/queue/inspect` counters
(`delivered_total`, `consumed_total` = acks, `redelivered_total`) throughout — the redelivered
count goes positive exactly when the partial consumer disconnects holding unacked leases, proving
redelivery over the wire.

The MQ's correctness under concurrency is proven by race-detector tests in `internal/server` and
`internal/queue` (run at `-count=50`): broker-side at-least-once with **no loss** on consumer
disconnect, **no over-pull** beyond credit `C`, **redelivery** of unacked leases to survivors,
**unique** steady-state delivery, **safe** ack handling (unknown/double acks are no-ops), and no
goroutine leaks.
