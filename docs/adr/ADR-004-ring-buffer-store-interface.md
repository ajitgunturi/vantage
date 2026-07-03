# ADR-004: Bounded Ring Buffer Behind a `Store` Interface

**Status:** Accepted
**Date:** 2026-06-27
**Phase:** 1 — MQ Foundation
**Backfilled:** 2026-07-03
**Related:** [ADR-009](ADR-009-opt-in-wal-extension.md) (WAL backend slots behind this same interface)

## Context

The MQ brief requires a custom, from-scratch, in-memory message queue implemented
with native Go concurrency primitives (channels, `sync.RWMutex`, ring buffers).
No third-party broker may be used. The queue must be safe for concurrent producers
and consumers, and it must expose an inspection endpoint showing depth and
throughput counters.

Two design choices had downstream implications that required explicit decisions:

1. **`sync.Mutex` vs `sync.RWMutex`** for the ring buffer.
2. **What `Enqueue` returns** — `bool` (dropped/accepted) vs `error`.

Additionally, a future WAL persistence backend was on the roadmap (ADR-009), so
the in-memory implementation needed an interface seam that a disk-backed backend
could satisfy without changing call sites.

Sources: plans 01-02 and 01.1-02, STATE.md ## Decisions (sync.Mutex, Enqueue
bool, Inspect by value, drop-newest Requeue, Phase 01 RESOLVED).

## Decision

Implement the MQ storage layer as a **bounded ring buffer** in `internal/queue`,
behind a `Store` interface. Key design choices:

1. **`sync.Mutex` (not `sync.RWMutex`)** — Every operation — `Enqueue`,
   `TryDequeue`, `Requeue`, `Inspect` — mutates internal state (`head`, `tail`,
   `count`, counters). `TryDequeue` advances `head` and decrements `count` on
   every call. There are no read-only paths that benefit from a shared read lock;
   using `sync.RWMutex` would acquire write locks on every path anyway, adding
   overhead with no throughput gain.

2. **`Enqueue` returns `bool`, not `error`** — The in-memory backend is
   unconditional: the only failure mode is a full buffer (drop-oldest). Returning
   a `bool` (true = enqueued, false = dropped) avoids forcing callers to handle
   `error` values that the implementation can never produce. It also keeps the
   `Store` interface compatible with a WAL backend where `error` would carry disk
   I/O failures; the `bool` layer above can translate.

3. **Drop-oldest on `Enqueue` when full** — the producer path evicts the oldest
   unconsumed message to make room. This provides backpressure signalling — a
   signal that the buffer is full, letting the caller observe the drop without
   the producer goroutine ever blocking — via the `bool` return value.

4. **Drop-newest (tail eviction) on `Requeue` when full** — `Requeue` re-enqueues
   a message that was in-flight and lost a consumer (at-least-once redelivery,
   ADR-001). When the buffer is also full, prioritise the in-flight message being
   returned (tail eviction keeps the oldest messages) over the newly arrived
   producer messages. This avoids starvation of redelivery under sustained
   overload — though under severe overload some requeued messages can still be
   lost (accepted: in-memory-only, no durability guarantee until ADR-009).

5. **`Inspect()` returns `StoreStats` by value** — a snapshot copy. Returning a
   pointer to internal state would allow callers to race on the counters; a value
   copy is safe to read after the mutex releases.

6. **`Store` interface seam** — the interface is shaped so the WAL backend
   (ADR-009) can be swapped in behind a config flag without changing any call site
   in the MQ server.

## Consequences

- **Positive:** `go test -race ./cmd/mq/...` cleanly exercises all paths; the
  race detector has never flagged the ring buffer implementation.
- **Positive:** The `Store` interface allows the WAL backend (ADR-009) to be
  introduced in Phase 7 without touching the MQ server logic.
- **Positive:** `Enqueue` returning `bool` keeps error handling simple at
  call sites; dropped messages are observable via `Inspect` counters.
- **Negative:** `sync.Mutex` serialises all ring buffer operations. Under very
  high concurrency (hundreds of goroutines), this is a potential bottleneck.
  For the single-replica MQ serving up to 10 Streamers and a small number of
  Collectors this is not a practical concern.
- **Negative:** Drop-oldest on `Enqueue` means a sustained overload silently
  discards the oldest messages. The `bool` return and `Inspect` counters make
  this observable but do not prevent it.

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| `sync.RWMutex` | Gains nothing: every `TryDequeue` mutates `head`+`count`, so all hot paths acquire the write lock anyway. Adds complexity with no throughput benefit. |
| `Enqueue` returns `error` | Forces callers to check an error that can never occur in the in-memory implementation. The `ErrBufferFull` sentinel value (a special marker used as a signal rather than representing a real I/O failure) would be a valid `error` type but makes call sites more verbose than needed for a bool decision. |
| Unbounded queue (slice/linked list) | No backpressure; memory grows without bound under a slow consumer. Incompatible with the "ring buffer" constraint in the brief. |
| Channel-only (no struct) | Go channels are bounded and goroutine-safe, but expose no inspection interface and cannot satisfy the `Inspect` method; implementing the message-lease semantics (ADR-001) — where a message is temporarily held by a consumer and returned to the queue if the consumer disconnects without acknowledging it — on top of a channel requires a separate struct anyway. |
