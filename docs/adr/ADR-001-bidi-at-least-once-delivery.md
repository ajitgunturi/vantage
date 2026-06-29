# ADR-001: Bidirectional `Consume` with broker-side at-least-once delivery

**Status:** Accepted (owner-approved deviation from the original brief)
**Date:** 2026-06-28
**Phase:** 01.1 — MQ At-Least-Once Delivery — Bidi Consume + Ack
**Supersedes (in part):** the Phase-1 MQ delivery contract ("server-side
streaming `Consume`, exactly-once-to-one-consumer")

## Context

The original assignment brief (`instructions.md`) specifies the MQ `Consume`
endpoint as a **Server-Side Streaming RPC** and frames delivery as *"decoupled,
thread-safe queueing; multiple Collectors receive unique messages."* The derived
requirements (`.planning/REQUIREMENTS.md`) additionally placed a **per-message
ACK/NAK protocol out of scope**, routing the project's at-least-once guarantee
through Phase 6 WAL replay + idempotent downstream writes instead.

During manual smoke testing a defect was reproduced that exposed the limit of
the server-stream design:

> Produce 1000 messages, then consume with a short-lived client
> (`mqprobe -n 20 -mode consume`). The client read 20, but `consumed_total`
> jumped to **513** and a follow-up drain recovered only ~399 of the rest —
> **~493 messages silently lost.**

**Root cause:** the single dispatch goroutine eagerly drains the ring buffer into
a shared `workCh` (cap 1024), and `stream.Send` on a server-stream hands each
message to gRPC's flow-control window and returns *before* the client receives
it. When the consumer disconnects, every message already pulled from the queue
and pushed into the transport window is gone — there is no acknowledgement, so
nothing is re-queued. This is **at-most-once** delivery, and it loses the
in-flight window on consumer disconnect.

This loss does not occur for the intended *persistent* Collector (it streams
forever and never disconnects mid-window), and the project's planned at-least-once
strategy (persistent collector + Phase 6 WAL + idempotent writes) covers crash
recovery. The owner nonetheless elected to make the broker itself deliver
**at-least-once on the live path** as a showcase-quality property.

## Decision

Adopt **broker-side at-least-once delivery**, overriding the brief's server-side
streaming clause and the prior "no per-message ack" decision:

1. **`Consume` becomes a bidirectional streaming RPC.** Server → client streams
   delivered messages; client → server streams **credit + acks**.
2. **Client-driven credit flow control.** A consumer opens with an initial credit
   `C`; the broker keeps outstanding-unacked ≤ `C` per consumer and releases the
   next message only as acks free slots. This eliminates the eager over-pull at
   its root (no message leaves the queue's custody beyond the consumer's window).
3. **Lease + ack.** A delivered message is *leased* to the consumer (tracked in a
   per-consumer in-flight table) and removed from the broker only when the
   consumer **acks** it by broker-assigned id.
4. **Redelivery on disconnect.** If a consumer's stream closes with unacked
   leases, those messages are re-enqueued and redelivered (possibly to another
   consumer). Duplicates are therefore possible — the definition of at-least-once.
5. **Broker message id.** `TelemetryMessage` gains a broker-assigned monotonic
   `uint64 id` used by acks.

Steady-state **unique delivery across concurrent Collectors is preserved**: a
leased message is not delivered to a second consumer unless the first disconnects
without acking.

Out of scope for this phase (deferred): lease/visibility timeout for
connected-but-stalled consumers; dynamic credit windows; disk persistence (still
Phase 6).

## Consequences

**Positive**
- No silent loss on the live path, even under consumer churn.
- Client-driven credit gives natural backpressure; no unbounded in-flight window.
- The idempotency layer already planned for crash-replay (DB-04 unique constraint,
  COLL-05 `ON CONFLICT` upsert) now also absorbs disconnect-redelivery duplicates —
  earlier payoff for existing investment.

**Negative / risk**
- **Deviates from the canonical brief** (server-side streaming → bidi). Recorded
  here and annotated inline in `instructions.md`.
- **Reverses a prior decision** (per-message ack was explicitly out of scope).
- Rewrites the Phase-1 MQ engine, the `mq.proto` contract, the inspect counters,
  `mqprobe`, and the Phase-1 race tests. Phase 3's Collector is now written against
  bidi+ack from the start (acks after durable Postgres write = true end-to-end
  at-least-once).
- **Phase 6 WAL overlap:** Phase 6 was framed as "at-least-once via replay." With
  delivery-level at-least-once now owned here, Phase 6 narrows to **crash
  durability only** — reconciled when Phase 6 is planned (`/gsd-plan-phase 6`).

## Compliance

- `instructions.md` §1 (MQ) — clauses annotated with a deviation note pointing here.
- `.planning/REQUIREMENTS.md` — per-message-ack removed from Out of Scope; new
  requirements MQ-09 (at-least-once delivery) and MQ-10 (per-message ack + credit
  flow control) added and mapped to Phase 01.1; MQ-02/MQ-03 reframed.
- The in-memory / no-disk / single-replica / from-scratch constraints remain
  **unchanged and honored** — at-least-once here is achieved purely in memory via
  redelivery, not persistence.
