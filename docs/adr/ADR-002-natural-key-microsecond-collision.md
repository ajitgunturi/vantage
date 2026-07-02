# ADR-002: Natural Key Microsecond Collision Under Concurrent Streamers

**Status:** Accepted
**Date:** 2026-07-02
**Author:** vantage team

---

## Context

The `gpu_metrics` table uses a natural composite primary key:
`(gpu_id, metric_name, timestamp)`.

This key drives the `ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING`
upsert in the Collector, which is the idempotency mechanism for at-least-once
redelivery from the MQ (COLL-05 / ADR-001).

The Streamer restamps every record with `time.Now().UTC()` at
`RFC3339Nano` precision (nanoseconds). With up to 10 concurrent Streamer
instances all reading the same DCGM CSV (STREAM-05), two instances can
produce the same `(gpu_id, metric_name)` pair within the same nanosecond wall
clock tick and receive identical timestamps.

When that happens:
- Both Streamers produce to the MQ successfully (different messages, same payload).
- The Collector inserts the first row; the second hits `ON CONFLICT DO NOTHING`.
- One record is silently dropped — **this is not data loss from a pipeline-
  correctness standpoint** because the two rows carry identical sensor readings.
  The DCGM CSV is a repeating fixture; its semantic content is the metric value
  at a point in time, not a unique event stream.

---

## Decision

Accept the nanosecond-level collision as **by-design behaviour**, not a defect.

Rationale:
1. **Idempotency is a feature, not a bug.** The `ON CONFLICT DO NOTHING` clause
   was added specifically to absorb MQ redeliveries. That same clause absorbs
   concurrent-Streamer collisions at no extra cost.
2. **The fixture is inherently repetitive.** The DCGM CSV contains 2470 rows with
   second-granularity timestamps. RFC3339Nano was chosen (Phase 2 decision) to
   avoid intra-second collisions within a single Streamer pass; it does not
   prevent inter-Streamer collisions at sub-second granularity.
3. **Remediation is possible but out of scope.** Options include adding a
   `streamer_id` column to the natural key, or using a UUID primary key alongside
   the natural key as a unique index. Both require a schema migration and change
   the semantics of "unique metric reading". Neither is required by the spec.

---

## Consequences

- **Positive:** No schema change required; idempotency already in place.
- **Positive:** Concurrent Streamers remain safe to deploy (STREAM-05).
- **Negative:** Under very high concurrency (≥10 Streamers), the effective
  row-insertion rate for any given `(gpu_id, metric_name)` pair saturates once
  all nanosecond slots within a wall-clock second are occupied. This is an
  academic limit for a fixture-replay scenario, not a production sensor stream.
- **Watch:** If the pipeline is ever wired to a real DCGM sensor that emits
  unique events, the natural-key approach should be revisited. Open an ADR
  amendment before making that change.

---

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| Add `streamer_id` to the natural key | Eliminates collision but makes duplicate detection per-streamer, not global — MQ redeliveries from different Collectors would no longer be idempotent |
| UUID primary key | True uniqueness; requires cursor-based pagination at the API layer; larger index footprint |
| Sequence / serial primary key | Simple; breaks idempotent upsert entirely (every row is unique by definition) |
