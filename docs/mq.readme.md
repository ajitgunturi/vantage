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
| `MQ_BUFFER_SIZE` | `10000` | **Capacity budget**: queued (main + retry) **plus in-flight** messages never exceed it. Overflow behavior is `MQ_OVERFLOW_POLICY`. |
| `MQ_CONSUME_CREDIT` | `20` | Broker-side **fallback** in-flight window, applied when a consumer's first credit message is ≤ 0. Non-positive/non-numeric values are ignored and the default is kept. |
| `MQ_OVERFLOW_POLICY` | `drop-oldest` | Enqueue behavior at a full budget: `drop-oldest` (default — telemetry freshness-first; evictions counted), `reject` (lossless backpressure — `Produce` returns `ResourceExhausted`), `block` (bounded producer wait). |
| `MQ_BLOCK_TIMEOUT_MS` | `1000` | Max producer wait under the `block` policy before `ResourceExhausted`. |
| `MQ_MAX_DELIVERIES` | `5` | Deliveries before a message is routed to the DLQ instead of retried. |
| `MQ_DLQ_CAPACITY` | `1000` | Dead-letter lane budget (separate from `MQ_BUFFER_SIZE`); DLQ overflow evicts its oldest entry (counted). |
| `MQ_RETRY_BACKOFF_BASE_MS` | `500` | Redelivery visibility delay after the 1st failed delivery; doubles per attempt. |
| `MQ_RETRY_BACKOFF_MAX_MS` | `30000` | Cap on the redelivery visibility delay. |
| `MQ_LEASE_TTL_MS` | `30000` | How long a delivery may stay unacked before the sweeper reclaims it for redelivery. |
| `MQ_DRAIN_TIMEOUT_MS` | `20000` | SIGTERM drain window: refuse producers, keep serving consumers, then exit. |

## Delivery semantics — broker-side at-least-once

As of Phase 01.1 the MQ delivers **at-least-once** over a **bidirectional** `Consume` stream
(see [`ADR-001`](adr/ADR-001-bidi-at-least-once-delivery.md)):

- The consumer opens the stream and first sends a **credit** message — its in-flight window `C`.
  The broker never has more than `C` unacked messages out to that consumer (**client-driven flow
  control**, no over-pull). If that first credit is **≤ 0**, the broker substitutes its own default
  (`MQ_CONSUME_CREDIT`, default `20`); any `C` above the **ceiling of `1000`** is clamped down so an
  over-large initial credit can't exhaust broker memory.
- The broker assigns each message a stable monotonic **id at Enqueue** (kept across redeliveries,
  so consumers can dedup by id) and **leases** it to the consumer, stamping `delivery_attempts`
  (1 on first delivery). A message leaves broker custody **only when the consumer acks that id**
  — `Consume{AckId: msg.id}`.
- If a consumer disconnects with **unacked** leases, those messages move to the **retry lane**
  and are redelivered to a survivor after an exponential **visibility backoff**
  (`MQ_RETRY_BACKOFF_BASE_MS × 2^(attempts−1)`, capped) — **no loss, and no eviction**: in-flight
  leases count against the capacity budget, so the requeue path always has guaranteed headroom.
  Redelivery can produce **duplicates**, which the (idempotent) Collector absorbs downstream.
- A **lease TTL** (`MQ_LEASE_TTL_MS`) covers the live-but-stuck consumer: leases unacked past the
  deadline are reclaimed by a background sweeper into the retry lane, and the stuck consumer's
  late acks become no-ops (its credit is revoked one slot per reclaimed lease).
- A message delivered `MQ_MAX_DELIVERIES` times routes to the bounded **dead-letter lane** instead
  of recirculating — see [Dead-letter queue](#dead-letter-queue).
- Steady state with all consumers acking is still **unique delivery** — each message goes to exactly
  one consumer.

### Overload semantics

The capacity budget (`MQ_BUFFER_SIZE`) counts queued **and** in-flight messages. At a full budget,
`MQ_OVERFLOW_POLICY` decides what `Produce` does:

- **`drop-oldest` (default) — freshness-first.** This is a telemetry pipeline: under overload the
  oldest reading is the least valuable, so the oldest *queued* message is evicted (never an
  in-flight lease) to admit the new one. Every eviction is counted in `dropped_overflow_total` —
  overload is a visible, alertable signal to scale consumers or the buffer, never silent. The
  planned follow-up is **adaptive sampling** under sustained overload (see
  [`docs/FUTURE.md`](FUTURE.md)): degrade resolution deliberately instead of tail-dropping blindly.
- **`reject` (opt-in) — lossless backpressure.** For workloads where every record matters more
  than freshness: `Produce` returns `ResourceExhausted` and the producer's retry-with-backoff
  (built into the Streamer) becomes real flow control. Refusals are counted in `rejected_total`.
- **`block` (opt-in)** — the producer waits up to `MQ_BLOCK_TIMEOUT_MS` for space, then
  `ResourceExhausted`.

The requeue path **cannot evict**: because leases count against the budget, releasing them always
has headroom. `dropped_requeue_total` exists as a tripwire and must stay `0`.

Storage is **in-memory only** (lanes + lease table behind the `Store` interface); crash durability
is the opt-in WAL backend designed for Phase 7 (deferred post-v1; see [`docs/FUTURE.md`](FUTURE.md)).
The **preStop drain** (`MQ_DRAIN_TIMEOUT_MS`) shrinks the rollout window: on SIGTERM the broker
refuses producers (readiness flips) while consumers keep acking, and exits once drained.

### Dead-letter queue

A message that exhausts `MQ_MAX_DELIVERIES` is parked in a bounded DLQ lane with its attempt count
and reason — a hot poison message can no longer wedge or starve the pipeline:

```sh
curl -s localhost:8080/api/v1/queue/dlq            # list entries (?limit=N; oldest first)
curl -s -X POST localhost:8080/api/v1/queue/dlq/replay   # re-enqueue as fresh work
```

Replay preserves each message's stable id, resets its attempts, and respects the capacity budget
(partial replay reports the remainder). DLQ overflow evicts the oldest dead-lettered entry
(`dlq_evicted`, bounded lane — the DLQ cannot grow without limit).

## Inspect the queue

```sh
curl -s localhost:8080/api/v1/queue/inspect
# {"capacity":10000,"depth":0,"retry_depth":0,"dlq_depth":0,"produced_total":40,
#  "rejected_total":0,"delivered_total":46,"consumed_total":40,"redelivered_total":6,
#  "dropped_total":0,"dropped_overflow_total":0,"dropped_requeue_total":0,
#  "dead_lettered_total":0,"lease_expired_total":0,"active_consumers":0,"in_flight":0}
```

Counter meanings (at-least-once, D-09): `produced_total` = accepted by Produce ·
`rejected_total` = Produce refusals under backpressure (**not** loss) · `delivered_total` =
messages **sent** to consumers · `consumed_total` = **acks** (confirmed deliveries — *not*
sends) · `redelivered_total` = requeued for redelivery (disconnect / failed send) ·
`lease_expired_total` = leases reclaimed by the TTL sweeper · `dead_lettered_total` = messages
routed to the DLQ · `in_flight` = currently sent-but-unacked. Loss counters split by cause:
`dropped_overflow_total` (opt-in drop-oldest policy) + `dropped_requeue_total` (structural zero —
tripwire) = `dropped_total`.

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
disconnect (in-flight leases count against the capacity budget, so the requeue path never
evicts — see [Overload semantics](#overload-semantics)),
**no over-pull** beyond credit `C`, **redelivery** of unacked leases to survivors,
**unique** steady-state delivery, **safe** ack handling (unknown/double acks are no-ops), and no
goroutine leaks.
