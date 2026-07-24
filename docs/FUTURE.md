# Future Enhancements

This file tracks work that has been **intentionally deferred but already designed**. It exists so
a reviewer can see the system's extension path without those extensions being part of the v1
scope. Both items below are **document-only** — neither is implemented in v1, and nothing in the
running system depends on them.

---

## 1. Opt-in WAL persistence backend (deferred Phase 7)

A durable MQ storage backend selected by a config flag. When enabled, the broker appends each
produced message to a write-ahead log on disk, issuing one `fsync` per batch of messages (group
commit) rather than one per message, and replays un-consumed messages back into the ring buffer
on restart — giving **at-least-once** crash durability. When the flag is absent, the in-memory
ring buffer remains the byte-for-byte default, exactly as the assignment brief specifies.

The extension is safe by construction: the Phase 2 natural-key unique constraint
(`uq_gpu_metrics_natural_key`) plus the idempotent Collector (`ON CONFLICT DO NOTHING`) already
absorb redelivery duplicates, so replay-produced duplicates converge to the same PostgreSQL state.
The `Store` interface seam was placed in Phase 1 specifically so this backend can be introduced
without touching the MQ server logic, the proto contract, or any consumer code.

**Design record:** [ADR-009 — Opt-In WAL Persistence Backend Behind the `Store` Interface](adr/ADR-009-opt-in-wal-extension.md).
The frozen phase design (goal, success criteria, dependencies) is retained under
**Phase 7** in [`.planning/ROADMAP.md`](../.planning/ROADMAP.md).

**Status:** designed, deferred (optional post-v1 enhancement) — not implemented in v1.

---

## 2. Multi-schema message support in the same broker

Today the broker carries exactly one message type — the DCGM telemetry payload defined in
`api/proto/mq.proto`. Supporting additional message kinds (say, job events or node health
records) through the same broker is a design sketch only; no part of it exists in v1.

### Envelope / proto evolution

Three viable shapes, each with a real tradeoff — no winner is picked here:

- **`oneof` payload** — a wrapper message whose `oneof` field enumerates every supported payload
  type. Strongly typed end to end, but every new message kind is a proto change that all
  producers and consumers must regenerate against.
- **Opaque `bytes` payload + `schema_id`/`content_type` fields** — the broker carries raw bytes
  tagged with a schema identifier; only endpoints that care about a kind need to understand it.
  Maximally decoupled, but type safety moves from the compiler to runtime dispatch.
- **Topic-per-schema** — the broker grows a topic concept and each message kind gets its own
  queue. Clean isolation and per-kind backpressure, but it is the largest broker change of the
  three.

### Broker-side impact

The ring buffer and the `Store` interface are already **payload-agnostic** — they move bytes and
never inspect message contents. An opaque-payload envelope therefore needs **no core broker
change**. The real broker-side decision is queue topology: per-topic queues give per-kind
capacity, drop policy, and credit windows, while a single queue with a type tag keeps the broker
untouched but interleaves all kinds through one buffer and one flow-control window.

### Consumer-side dispatch

The Collector (or per-kind consumers) would route on the schema tag. The unresolved policy
question is what to do with a message whose schema the consumer does not recognise:
**dead-letter** it (park it for inspection, at the cost of a dead-letter store the broker
currently doesn't have) or **skip-and-log** (simple, but silently discards data if a producer
ships a new kind before its consumer deploys).

### Database impact

The long/narrow schema ([ADR-006](adr/ADR-006-long-narrow-schema-natural-key.md)) already absorbs
new *metric names* with no DDL change — that flexibility was the point of the narrow shape. But a
new message *kind* is not a new metric name: it would need its own table with its own natural key
and idempotency constraint, mirroring how `uq_gpu_metrics_natural_key` makes telemetry ingestion
idempotent.

### OpenAPI / gateway impact

New read shapes mean new gateway endpoints, each with full `swag` annotations, and a regenerated
OpenAPI spec (`make swagger`) — the spec stays auto-generated, never hand-written.

**Status:** designed, deferred, not scheduled — revisit post-v1.

---

## Adaptive sampling under producer overload

**Status: planned (not designed in detail).** ADR-011 made overload an explicit policy: the
default `drop-oldest` keeps the freshest telemetry and counts every eviction
(`dropped_overflow_total`). That is the right *failure* behavior for a telemetry pipeline — but
sustained overload deserves a smarter *response* than blind tail-drop.

### Direction

**Degrade resolution deliberately instead of dropping arbitrarily.** When overload is sustained
(non-zero eviction rate over a window, or queue depth above a high-watermark), reduce the
sampling rate at the source rather than letting the ring decide which readings survive:

- **Streamer-side (preferred):** on sustained `ResourceExhausted`/eviction signal, publish every
  Nth reading per `(gpu_id, metric_name)` stream — uniform stride sampling preserves per-series
  coverage, unlike tail-drop which can blackhole whole series behind a hot producer. Back off N
  as pressure clears.
- **Broker-side (fallback):** at a high-watermark, admit-by-stride per series instead of
  evicting — same effect for producers that don't implement client-side sampling.

### Why sampling and not a bigger queue

Telemetry tolerates lower resolution; it does not tolerate staleness. For workloads where every
record matters, the `reject` policy already provides lossless backpressure — and the counters
(`dropped_overflow_total`, `rejected_total`, queue depth) are the operator signal to scale
consumers, raise `MQ_BUFFER_SIZE`, or add partitions instead. A Prometheus `/metrics` export of
these counters (also future work) is the natural trigger for that scaling loop; sampling is the
*automated* response for the freshness-first default.
