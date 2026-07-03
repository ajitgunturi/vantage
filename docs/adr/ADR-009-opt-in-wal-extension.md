# ADR-009: Opt-In WAL Persistence Backend Behind the `Store` Interface

**Status:** Accepted (implementation pending — Phase 7)
**Date:** 2026-06-27 (decided at roadmap creation; Phase renumbered 6 → 7 on 2026-07-03)
**Phase:** roadmap-level / Phase 7 — WAL Durability
**Backfilled:** 2026-07-03
**Related:** [ADR-001](ADR-001-bidi-at-least-once-delivery.md) (delivery at-least-once that narrows WAL scope),
[ADR-004](ADR-004-ring-buffer-store-interface.md) (the `Store` interface this backend implements)

## Context

The assignment brief (`instructions.md`) mandates an **in-memory-only** MQ — no
disk, no clustering. This is the specification baseline and the default deployment
mode.

A broker crash in the in-memory design loses all un-consumed messages that had not
yet been dequeued by a Collector. For the assignment's fixture-replay scenario
(looping DCGM CSV), this is tolerable — the Streamer simply picks up where it left
off. For a production deployment with real sensor streams, losing the in-flight
window on a broker restart is a correctness issue.

At the same time, Phase 01.1 (ADR-001) moved delivery-level at-least-once into
the live path: the broker now redelivers unacked messages on consumer disconnect,
without persistence. This narrows the WAL's responsibility: the WAL is not needed
for live-path at-least-once (already solved); it is needed only for **crash
durability** — preserving un-consumed messages across MQ process restarts.

The roadmap originally placed the WAL (write-ahead log — a disk-resident journal
that records each incoming message before it is consumed, so the queue can replay
those messages after a crash) as Phase 6. Phase 6 was consumed by Production
Hardening + Assignment Alignment (audit-driven work: health probes, pagination,
CI, slog, resources). The WAL was renumbered to Phase 7 on 2026-07-03 (STATE.md
Roadmap Evolution).

The `Store` interface seam was placed in Phase 1 (ADR-004) specifically to allow
a WAL backend to be introduced later without changing the MQ server logic.

Sources: PROJECT.md Key Decisions (WAL opt-in row) + Active requirement (Store
interface + WAL), STATE.md Key Decisions (WAL Phase 7 renumber), STATE.md Roadmap
Evolution, CLAUDE.md §Hard constraints (MQ in-memory default + opt-in WAL).

## Decision

Add an **opt-in WAL persistence backend** behind the existing `Store` interface
(ADR-004) as a deliberate, documented extension of the brief's in-memory-only
baseline.

Design constraints (fixed):

1. **In-memory remains the default** — the WAL backend is selected only via an
   explicit config flag (e.g., `VANTAGE_MQ_WAL_ENABLED=true`). The byte-for-byte
   in-memory behaviour is unchanged when the flag is absent.
2. **Single-replica only** — the WAL persists to the local filesystem of the
   single MQ pod. There is no distributed WAL, no replication, no cluster. The
   MQ `replicas: 1` Helm constraint (ADR-008) remains non-negotiable.
3. **At-least-once semantics** — the WAL writes messages using a group-commit
   strategy. Rather than calling `fsync` (the OS call that forces buffered data
   from memory to stable storage on disk) after every individual message, the MQ
   accumulates several messages and issues a single `fsync` for the whole batch.
   This amortises the per-write disk latency across many messages. After an MQ
   restart, the WAL is replayed into the ring buffer before the MQ accepts new
   connections. Duplicates from replay are absorbed by the Collector's idempotent
   upsert (ADR-007).
4. **Interface compatibility** — the WAL backend must satisfy the `Store` interface
   (ADR-004). No call site in the MQ server changes when the WAL backend is
   selected.

Scope of crash durability: messages in the WAL that had been ACKed by the
Collector before the crash are not replayed (the Collector already persisted
them). Only un-acked messages are replayed — preserving them at the cost of
possible duplicates (at-least-once, not exactly-once; absorbed by ADR-007).

## Consequences

- **Positive:** Crash durability without changing the MQ public interface, the
  proto contract, or any Collector / Streamer code.
- **Positive:** The `Store` interface seam (ADR-004) makes the WAL backend
  swappable without touching the MQ server's business logic.
- **Positive:** Batched group-commit fsync amortises the fsync overhead across
  multiple messages — much cheaper than per-message fsync.
- **Negative:** WAL persistence is to local disk — a pod eviction or node failure
  loses the WAL file. This is Kubernetes-specific: a `PersistentVolumeClaim` could
  survive a pod restart but not a node replacement. For the single-node dev
  cluster this is acceptable; a production cluster would mount a durable PVC.
- **Negative:** Replay-on-restart can produce duplicate messages to the Collector.
  This is safe because the Collector's idempotent upsert (ADR-007) absorbs
  duplicates, but it adds transient read amplification on the DB at restart time.
- **Negative:** The WAL adds fsync latency to the `Enqueue` hot path (even with
  group-commit). Under very high produce rates this may become a bottleneck
  compared to the pure in-memory backend.
- **Neutral:** Delivery-level at-least-once (ADR-001) already covers the live
  path; the WAL's scope is crash durability only. Operators who do not need crash
  durability pay zero cost with the default in-memory mode.

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| Mandatory disk persistence (WAL always on) | Violates the brief's in-memory-only baseline as the default mode. The spec is explicit: in-memory is the default. |
| No durability at all (pure in-memory, no WAL phase) | Acceptable for the fixture scenario; but a broker crash silently loses un-consumed messages. The assignment's `instructions.md` notes that WAL as an opt-in extension is a desirable showcase property. |
| External broker (Kafka, NATS, Redis Streams) | Forbidden by spec. The MQ must be built from scratch; using an external broker nullifies the assignment's primary artifact. |
| Per-message fsync (no group-commit) | Correct but slow: each `Enqueue` would block until the OS confirms the write. Group-commit batches multiple messages in a single fsync, drastically improving throughput at the cost of a small durability window (messages in the unflushed batch on a crash). |
