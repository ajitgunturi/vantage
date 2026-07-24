# ADR-011: Broker delivery hardening — backpressure, retry/DLQ lanes, lease TTL, preStop drain

**Status:** Accepted
**Date:** 2026-07-24
**Extends:** ADR-001 (closes two of its documented guarantee boundaries), ADR-007 (batch persistence), ADR-009 (in-memory default unchanged)

## Context

ADR-001's guarantee-boundaries amendment records where the at-least-once claim
stopped holding: at a full ring, `Enqueue` silently evicted the oldest message
(the producer was never told), and requeuing a disconnected consumer's leases
could evict unrelated fresh messages. Beyond those two, four adjacent weaknesses
shared the same root — the broker had no owned view of delivery state:

1. **Hot-loop redelivery.** Requeue was immediate and front-of-queue, so a
   failing consumer replayed the same messages at full speed, ahead of all
   fresh traffic.
2. **No delivery-attempt tracking.** Broker ids were minted at *dequeue*, so a
   redelivered message got a new id each time — the broker could not recognize
   a message as seen-before, and consumers could not dedup by id.
3. **Poison wedge.** pgx v5 `SendBatch` runs a batch in one implicit
   transaction: one row that deterministically fails aborts the whole batch,
   nothing is acked, and the broker redelivers the identical batch forever.
4. **Stuck-consumer parking.** Leases were reclaimed only on stream close; a
   live-but-wedged consumer (open stream, no acks) parked its window forever.
   Separately, every rollout dropped whatever was in flight (single replica,
   `Recreate`).

## Decision

Restructure the queue engine into a **broker** that owns admission, identity,
lanes, and leases behind the existing `Store` seam (server stays a thin gRPC
adapter; in-memory/from-scratch/single-replica constraints unchanged):

1. **Capacity budget counts in-flight.** Queued (main + retry) plus leased
   messages never exceed `MQ_BUFFER_SIZE`. Corollary: the requeue path always
   has headroom and structurally cannot evict (`dropped_requeue_total` remains
   as a tripwire).
2. **Overflow policy, default `reject`.** At a full budget `Produce` returns
   `ResourceExhausted`; the Streamer's existing retry-with-backoff becomes real
   flow control. `drop-oldest` (previous behavior) and `block` (bounded wait)
   are explicit opt-ins. Refusals are counted (`rejected_total`), and drop
   counters are split by cause.
3. **Stable identity + attempt tracking.** Broker ids are assigned once at
   `Enqueue`; `delivery_attempts` (proto field 14) increments per lease.
4. **Retry lane with visibility backoff.** Redeliveries wait
   `base × 2^(attempts−1)` (capped) before becoming deliverable; visible retry
   work is served before fresh main traffic so aged messages are not starved.
5. **Bounded DLQ + operator endpoints.** A message delivered
   `MQ_MAX_DELIVERIES` times parks in a bounded dead-letter lane —
   `GET /api/v1/queue/dlq`, `POST /api/v1/queue/dlq/replay` (attempts reset,
   id preserved, budget-aware).
6. **Lease TTL sweeper.** Leases unacked past `MQ_LEASE_TTL_MS` are reclaimed
   into the retry lane; the stuck consumer's late acks become no-ops (credit
   revoked per swept lease). Safe because consumers are idempotent (DB upsert).
7. **Collector poison isolation.** On a deterministic row failure (SQLSTATE
   class 22/23 only), the batch bisects to the failing row in O(log n)
   round-trips (safe: idempotent upsert), preserves it in `gpu_metrics_dlq`
   with the reason, and acks it. Transient errors still propagate unacked.
8. **preStop drain.** SIGTERM enters a drain phase: `Produce` refuses
   (readiness flips) while Consume streams keep acking, bounded by
   `MQ_DRAIN_TIMEOUT_MS`; the chart's grace period covers drain + graceful
   stop. Rollout loss is ~zero with healthy consumers.

## Consequences

**Positive**
- The at-least-once guarantee now holds under producer overload and consumer
  churn by construction, not by capacity headroom luck; loss is impossible
  under the default policy, and every non-happy path is a counted signal
  (`rejected_total`, `dead_lettered_total`, `lease_expired_total`).
- One poison message (delivery-level or DB-level) can no longer wedge or
  starve the pipeline; both DLQs have inspect/replay paths.

**Negative / accepted**
- **`Produce` can now fail** with `ResourceExhausted` — a contract change.
  All in-repo producers already retry with backoff; external producers must.
- Redelivery is no longer instantaneous (bounded backoff): latency traded for
  the end of hot-loop amplification.
- Dead-lettered broker messages are still in-memory (lost on restart); the
  durable answer remains the deferred WAL backend (ADR-009). The DB-side
  `gpu_metrics_dlq` *is* durable.
- The broker owns a background sweeper goroutine; `Store` implementations
  carry lease/lane semantics, enlarging the seam a WAL backend must satisfy.

## Compliance

- `docs/mq.readme.md` (config, overload semantics, DLQ, counters),
  `docs/collector.readme.md` (poison isolation), ADR-001 amendment updated.
- Verified by race-detector broker/server tests, collector integration tests
  (real Postgres), and the phase-06 hardening smoke
  (`scripts/smoke/phase06-hardening.sh`): backpressure at a full ring,
  TTL→retry→DLQ→replay round-trip, and SIGTERM drain — over the real wire.
