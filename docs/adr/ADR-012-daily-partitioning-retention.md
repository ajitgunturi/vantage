# ADR-012: Daily Range Partitioning of `gpu_metrics` with O(1) Partition-Drop Retention

**Status:** Accepted
**Date:** 2026-07-24
**Extends:** ADR-006 (which noted partitioning as the retention path), ADR-002 (natural key unchanged)

## Context

`gpu_metrics` had no retention story: a telemetry table growing without bound
eventually fails operationally (index depth, planner statistics staleness,
vacuum pressure). The naive fix — a periodic `DELETE WHERE timestamp < cutoff`
— is the worst option at exactly the volumes where retention matters: it
rewrites gigabytes into dead tuples, bloats every index, and turns vacuum into
a permanent background tax.

## Decision

Convert `gpu_metrics` to **declarative RANGE partitioning by UTC day**
(migration 000003, copy + swap in one transaction), managed by two SQL
functions and a nightly CronJob:

- `gpu_metrics_ensure_partitions(days_ahead)` pre-creates the daily window so
  the write path never waits on DDL.
- `gpu_metrics_drop_old_partitions(retention_days)` drops whole partitions
  past the window — metadata-only, O(1) per day, zero vacuum debt.
- A `DEFAULT` partition catches rows outside the pre-created window
  (backfills, clock skew): inserts never fail on a missing partition. Live
  rows are restamped at publish, so they land in the current day.
- The umbrella chart's retention CronJob (`values.retention`: 14 days,
  nightly 00:17 UTC) runs drop + ensure via the in-cluster Bitnami psql image.

The natural-key unique index gains nothing and loses nothing: it already
contains the partition key (`timestamp`), which Postgres requires on
partitioned unique indexes — the idempotent `ON CONFLICT` upsert (ADR-007) is
untouched. The spec-mandated composite index `(gpu_id, timestamp DESC)` is
declared on the parent and materializes per partition.

## Consequences

**Positive**
- Retention is a metadata operation: dropping a day of telemetry costs the
  same at 1M rows as at 1B. Index depth and planner stats stay bounded by the
  retention window.
- Time-range queries get partition pruning on top of the composite index.

**Negative / accepted**
- Query plans now Append per-partition subplans; empty partitions may
  legitimately choose Seq Scan (cheapest for zero rows). EXPLAIN-based tests
  and smoke checks assert on the data-bearing partitions' index children
  (`*_gpu_id_timestamp_idx`) rather than one global index name.
- Unique-violation errors report the partition's child index name; tests
  assert SQLSTATE 23505 instead.
- Rows in the DEFAULT partition are not covered by partition-drop retention
  (it is a safety net, normally empty); a stray-backfill cleanup remains a
  manual operation.

## Compliance

- Migration 000003 up/down; partition routing, ensure-idempotency, retention
  drop/survival and the default-partition safety net proven against real
  Postgres; storage smoke asserts partitioned relkind + live functions.
