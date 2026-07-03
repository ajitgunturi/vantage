# ADR-007: `pgx.Batch` with `ON CONFLICT DO NOTHING` over `CopyFrom`

**Status:** Accepted
**Date:** 2026-06-29
**Phase:** 3 — Streamer → MQ → Collector → Postgres Pipeline
**Backfilled:** 2026-07-03
**Related:** [ADR-001](ADR-001-bidi-at-least-once-delivery.md) (broker at-least-once source of duplicates),
[ADR-006](ADR-006-long-narrow-schema-natural-key.md) (the natural key this upsert targets)

## Context

The Collector receives a stream of `TelemetryMessage` records from the MQ and
must persist them to Postgres. The stack research (`.claude/CLAUDE.md`) recommends
`pgxpool.CopyFrom` as the fastest bulk-insert path — the PostgreSQL `COPY`
protocol is 5–10× faster than multi-row `INSERT` at scale.

However, two constraints make `CopyFrom` incompatible:

1. **Idempotency requirement (ADR-006):** The natural key uniqueness constraint
   `uq_gpu_metrics_natural_key` requires `INSERT ... ON CONFLICT (gpu_id,
   metric_name, timestamp) DO NOTHING`. `CopyFrom` uses the PostgreSQL `COPY`
   protocol, which cannot express `ON CONFLICT` clauses. Duplicate rows from
   MQ at-least-once redelivery (ADR-001) would cause `CopyFrom` to error rather
   than silently skip.

2. **Ack-on-persist contract (C-1 fix):** The correct at-least-once pipeline
   requires the Collector to ack messages to the MQ **only after** they are
   durably persisted to Postgres, not on receipt. This fix (quick task
   260702-ku8) means acks are tied to the success of the batch write. The batch
   semantics must be transactional so that a partial failure does not produce
   partial acks.

Phase 3 plan 03-03 established that the correct choice is `pgx.Batch` /
`SendBatch` with `INSERT ... ON CONFLICT ... DO NOTHING`.

Sources: plan 03-03, STATE.md ## Decisions (ON CONFLICT DO NOTHING, two-goroutine
bidi split, ctx.Err() reconnect discriminant), quick task 260702-ku8 (C-1
ack-on-persist data-loss fix), CLAUDE.md stack recommendation for CopyFrom.

## Decision

Use **`pgx.Batch` / `pool.SendBatch`** with per-row
`INSERT INTO gpu_metrics ... ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING`.

Key properties of this approach:

1. **Idempotent upsert** — `DO NOTHING` silently skips rows that already exist.
   This absorbs MQ at-least-once redeliveries without any in-process dedup state,
   completing the end-to-end exactly-once semantic:
   broker at-least-once (ADR-001) × idempotent upsert (ADR-006) = exactly-once
   delivered rows.

2. **pgx v5 implicit transaction semantics** — a `SendBatch` runs its statements
   inside a single implicit transaction. If the first `Exec` in the batch returns
   an error, the entire batch is rolled back. No partial inserts are committed;
   therefore no partial acks are sent to the MQ. The MQ redelivers the entire
   unacked batch on Collector reconnect — safe because the upsert is idempotent.

3. **Ack-on-persist (C-1)** — Acks are sent to the MQ _after_ `SendBatch` and
   `CloseBatch` return without error. If `SendBatch` errors, no acks are emitted
   and the MQ redelivers. This is the correct at-least-once contract.

4. **Batch by time window** — the Collector flushes every 500 ms or every 1000
   rows, whichever comes first, using a ticker + channel drain loop. This
   amortises the per-batch round-trip latency without accumulating unbounded
   backlog.

## Consequences

- **Positive:** End-to-end exactly-once row delivery achieved without any
  in-process dedup state — all idempotency logic lives in the DB constraint.
- **Positive:** Ack-on-persist (C-1) is correctly implemented: batch failure
  rolls back the entire batch, no acks emitted, MQ redelivers, next attempt
  is safe because upsert is idempotent.
- **Positive:** `pgx.Batch` is part of the `jackc/pgx/v5` library already
  required for `pgxpool` — no additional dependency.
- **Negative:** `pgx.Batch` / `SendBatch` is slower than `CopyFrom` at very
  high row volumes (no `COPY` protocol). For the fixture-scale DCGM CSV this
  is not a practical bottleneck. If the pipeline is ever wired to a real
  high-frequency DCGM sensor stream, the throughput ceiling should be revisited.
- **Negative:** The `ON CONFLICT` clause silently drops a row when a collision
  occurs. Diagnostics rely on comparing `RowsAffected` vs batch size rather than
  inspecting individual `DO NOTHING` outcomes.

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| `pgxpool.CopyFrom` (recommended in stack research) | Fastest bulk path (COPY protocol). Cannot express `ON CONFLICT` — duplicate rows from MQ redelivery cause errors, not silently skipped rows. Incompatible with the idempotency requirement (ADR-006). |
| Per-message transactions | Each row in its own `BEGIN` / `INSERT` / `COMMIT`. Correct but kills throughput: each message incurs a full round-trip. CopyFrom/Batch is transactional anyway; per-message transactions add cost with no benefit. |
| In-memory dedup in the Collector | Maintain a seen-set of `(gpu_id, metric_name, timestamp)` in the Collector process. Correct while the Collector runs, but stateful — a Collector restart loses the seen-set. The DB-enforced natural key (ADR-006) is stateless and survives any restart. |
| `ON CONFLICT DO UPDATE` (upsert with overwrite) | Would overwrite an existing row with the same metric value (idempotent in practice for this fixture). Unnecessary: the value never changes for a given `(gpu_id, metric_name, timestamp)` triple; `DO NOTHING` is cheaper and semantically cleaner. |
