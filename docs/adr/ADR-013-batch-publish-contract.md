# ADR-013: `ProduceBatch` — Batched Publish with Partial-Accept Contract

**Status:** Accepted
**Date:** 2026-07-24
**Extends:** ADR-011 (overflow policy semantics applied to batches), ADR-001 (data-plane contract)

## Context

The Streamer paid one unary `Produce` round-trip per CSV row, so publish
throughput was bounded by client RPC rate — not broker capacity. Scaling to
10 concurrent Streamers multiplied RPC overhead, not useful work.

## Decision

Add a unary **`ProduceBatch`** RPC (up to 1000 messages) and switch the
Streamer to batched publishing (`STREAMER_BATCH_SIZE`, default 100 — 100×
fewer round-trips; `1` restores the legacy unary path):

- **In-order admission under the same overflow policy as `Produce`.** Under
  the default drop-oldest policy a batch is always fully accepted.
- **Partial-accept contract** (reject/block policies): on the first refusal
  the broker stops and reports `accepted` — the rejected remainder is always
  the batch **suffix**, so the producer retries `messages[accepted:]` after
  backoff without reordering. One refusal event increments `rejected_total`,
  not one per unsent message.
- **Whole-batch validation before any enqueue** (empty, oversized, nil
  entries → `InvalidArgument` with no partial effect); `Unavailable` while
  draining, like `Produce`.
- **Timestamps are unaffected by batching**: rows are restamped individually
  at CSV *read* time (producer-owned, STREAM-02), not at batch flush, so
  batching cannot collide or skew stamps. Partial batches flush at the end of
  every CSV pass.

Client-streaming publish (a `stream ProduceRequest` RPC with periodic
cumulative acks) was considered and rejected: it complicates the
flow-control story for marginal gain over 100-row unary batches at this
scale, and the unary shape keeps the retry contract trivially explicit.

## Consequences

- Publish throughput is bounded by broker admission, not client RPC rate.
- The suffix-retry contract means backpressure composes with batching: the
  Streamer's backoff resets on progress, so a slowly-draining broker receives
  steadily shrinking retries instead of a hot loop.
- Wire-level proof: smoke covers full accept (50-in-1), partial accept at a
  full budget (accepted 10 / rejected 5, zero loss), and the counters.
