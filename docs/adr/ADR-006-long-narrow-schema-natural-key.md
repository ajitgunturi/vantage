# ADR-006: Long/Narrow Schema with Natural Composite Key

**Status:** Accepted
**Date:** 2026-06-29
**Phase:** 2 — Storage Foundation
**Backfilled:** 2026-07-03
**Related:** [ADR-002](ADR-002-natural-key-microsecond-collision.md) (microsecond collision consequence),
[ADR-007](ADR-007-pgx-batch-on-conflict-over-copyfrom.md) (the upsert that uses this key)

## Context

Phase 2 (plan 02-01) defined the `gpu_metrics` Postgres table — the single source
of truth for all ingested telemetry. Two fundamental schema shapes were possible:

- **Wide table** — one column per DCGM metric name (`sm_active`, `fb_used`,
  `power_draw`, …). Each GPU × timestamp occupies one wide row.
- **Long/narrow table** — one row per `(gpu_id, metric_name, timestamp)` triple;
  the metric value is a single `NUMERIC` column.

The DCGM CSV format (`dcgm_metrics_*.csv`) contains heterogeneous metric names
for the same GPU at the same timestamp. The project must remain flexible to
new/changed metric names from the CSV without DDL changes.

Three additional constraints drove the key design:

1. **Idempotency requirement:** The Collector must be idempotent — processing the
   same message twice must produce no extra rows in the database — against MQ
   at-least-once redeliveries (ADR-001). A natural key is the set of real-world
   identifying columns that together uniquely describe a measurement, as opposed to
   a generated surrogate ID. Here the natural key is `(gpu_id, metric_name,
   timestamp)`. A `UNIQUE` constraint on this natural key enables
   `INSERT ... ON CONFLICT DO NOTHING` without any in-process dedup state.
2. **GPU identity (planning decision D-04 — use the GPU's UUID, not its DCGM ordinal
   index):** The GPU is identified by its **UUID** (from `msg.GetUuid()` in the
   protobuf message), not by the DCGM ordinal integer. UUIDs are stable across
   host reboots; ordinals are not.
3. **Restamp precision:** The Streamer restamps each record with
   `time.Now().UTC()` at `RFC3339Nano` (nanosecond) precision. Using second
   granularity would collapse multiple same-second readings from a single Streamer
   pass onto the same natural key, causing spurious `ON CONFLICT` silences.

Sources: plan 02-01, STATE.md ## Decisions (uq_gpu_metrics_natural_key,
RFC3339Nano locked, GpuID from uuid, D-04).

## Decision

Adopt a **long/narrow schema**: one row per `(gpu_id, metric_name, timestamp)`.

Key schema choices:

1. **`gpu_id TEXT`** — stores the GPU UUID string from `msg.GetUuid()`. TEXT is
   preferable to a dedicated UUID type here because the DCGM UUID format is
   treated as an opaque string at the collector layer.
2. **`metric_name TEXT`** — the DCGM metric name verbatim from the CSV. New
   metric names are absorbed automatically without a schema migration.
3. **`timestamp TIMESTAMPTZ`** — microsecond precision in Postgres; RFC3339Nano
   from the Streamer provides nanosecond-level distinguishability (capped at
   microsecond in storage, which is sufficient).
4. **`uq_gpu_metrics_natural_key UNIQUE (gpu_id, metric_name, timestamp)`** —
   the uniqueness constraint that makes `ON CONFLICT DO NOTHING` correct and
   efficient.
5. **Composite index `(gpu_id, timestamp DESC)`** — a database index spanning two
   columns at once, optimised for the read path of
   `GET /api/v1/gpus/{id}/telemetry?start_time=…&end_time=…`. EXPLAIN verified
   index scan at 100k rows (Phase 2 acceptance criterion).
6. **RFC3339Nano restamp is locked** — using second-granularity (RFC3339) would
   collapse multiple readings within the same second onto the same natural key,
   silently treating them as duplicates. The restamp precision cannot be loosened
   without a schema + uniqueness-constraint change.

## Consequences

- **Positive:** New DCGM metric names are ingested without DDL changes — the
  narrow schema is metric-agnostic.
- **Positive:** `uq_gpu_metrics_natural_key` makes the Collector unconditionally
  idempotent: MQ at-least-once redeliveries, concurrent-Streamer duplicates, and
  crash-replay (ADR-009) all converge on `DO NOTHING` at the DB layer.
- **Positive:** The composite index `(gpu_id, timestamp DESC)` aligns with the
  API's primary query pattern and has been EXPLAIN-verified to use an index scan.
- **Negative:** Long/narrow schema is verbose for queries that need multiple
  metrics per GPU per timestamp — a pivot or `crosstab` query is needed. For this
  project's read API (raw telemetry rows) this is not a concern.
- **Negative:** The natural-key uniqueness constraint means two concurrent
  Streamers that produce the same `(gpu_id, metric_name)` pair within the same
  nanosecond wall-clock tick will produce a `DO NOTHING` drop — one reading is
  silently discarded. This is accepted behaviour (see ADR-002).
- **Negative:** RFC3339Nano restamp precision is now a locked constraint: changing
  the Streamer to second granularity would require a schema migration and an
  explicit decision to loosen the dedup semantics.

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| Wide table (metric per column) | Schema churn on every new metric; sparse rows where some metrics are NULL; DDL change required to add new DCGM counters. Incompatible with the goal of metric-agnostic ingestion. |
| Serial / UUID surrogate PK (no natural key) | Every row is unique by construction — breaks idempotent upsert entirely. A separate unique index could be added, but this duplicates the natural key at extra index cost. |
| Add `streamer_id` to the natural key | Per-streamer dedup (not global) — MQ redeliveries from different Collectors of the same Streamer's message would no longer be idempotent. See ADR-002 for fuller analysis. |
| Second-granularity restamp (RFC3339, not RFC3339Nano) | Collapses multiple same-second readings from one Streamer onto a single row via `DO NOTHING`. Produces incorrect behaviour for the looping CSV where many rows share the same sensor timestamp. |
