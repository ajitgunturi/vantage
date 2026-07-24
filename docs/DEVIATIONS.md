# Deliberate deviations from the brief

The brief (`instructions.md`) is preserved verbatim as delivered. The system as built departs
from it in the two places recorded below. These were **author-decided** departures — made during
implementation, documented here and in the linked ADRs so a reviewer can see exactly what was
*asked* versus what was *built*, and why. Affected clauses in `instructions.md` are annotated
inline with **[DEVIATION → ADR-NNN]**; the brief's original wording is untouched.

## 1. MQ `Consume` is a bidirectional streaming RPC with broker-side at-least-once delivery

- **What the brief asked for:** a server-side-streaming `Consume` RPC with no per-message
  acknowledgement.
- **Trigger:** a reproduced silent-message-loss defect — a short-lived consumer disconnecting
  mid-stream lost the entire gRPC flow-control window (~493 of 1000 messages in the repro),
  because messages handed to the transport were never re-queued.
- **Decision:** rebuild `Consume` as a bidirectional stream — client sends initial credit and
  per-message acks; the broker leases messages and re-enqueues unacked leases on disconnect
  (at-least-once; duplicates absorbed by the idempotent Collector). Built in Phase 01.1. The
  in-memory / no-disk / single-replica / from-scratch constraints are unchanged.
- **Record:** [`ADR-001`](adr/ADR-001-bidi-at-least-once-delivery.md) (including its
  guarantee-boundaries amendment).

## 2. Opt-in WAL persistence backend (deferred; seam shipped, backend not built)

- **What the brief asked for:** a purely in-memory MQ with no disk persistence.
- **Decision:** keep in-memory as the default, but place storage behind a `Store` interface so
  an opt-in WAL backend (batched group-commit fsync + replay-on-restart) can add crash
  durability without changing the default path. Deferred post-v1 (2026-07-03, renumbered
  Phase 7): the interface seam shipped, the backend was not built.
- **Record:** [`ADR-009`](adr/ADR-009-opt-in-wal-extension.md) and [`FUTURE.md`](FUTURE.md).
