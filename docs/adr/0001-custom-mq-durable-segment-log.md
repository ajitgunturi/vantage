# ADR-0001: Custom MQ with durable append-only segment log

- Status: Accepted
- Date: 2026-06-24

## Context
The assignment forbids existing brokers (Kafka/RabbitMQ/ZeroMQ) and requires a **custom** message
queue that is deployable as an independent installation and **survives downtime**. We also use
PostgreSQL for collector data. The temptation is to back the queue with Postgres too — but that
would offload the hard part (durability + ordering + offsets) onto an existing system and weaken the
"we built a queue" claim.

## Decision
Build the broker's durability ourselves as a **Kafka-lite append-only segment log** on a persistent
volume:
- Each partition is an ordered sequence of **segment files** (`<baseOffset>.seg`) plus an offset
  index (`<baseOffset>.segidx`).
- Producers append; the broker assigns a **monotonic per-partition offset** and `fsync`s before
  acking.
- Consumer-group offsets are persisted separately so progress survives a restart.
- On startup the broker rebuilds in-memory indexes from the segments → no message loss across
  crash/restart. PostgreSQL is reserved strictly for collector telemetry, never for queue state.

## Driving Prompt
> We must create a custom message queue solution - deployable as an independent installation with a
> persistence layer integrated so that it can survive down times.

## Consequences
- (+) Honest "custom MQ" with real durability semantics; strong interview talking point.
- (+) Append-only + fsync gives at-least-once and clean ordering per partition.
- (−) We own correctness of the log/index/recovery code — needs careful unit tests (crash recovery,
  partial writes, segment roll).
- Follow-up: define segment roll size, fsync batching policy (durability vs throughput knob).

## Alternatives considered
- **Postgres-backed queue** — rejected: defeats "custom", couples queue throughput to DB.
- **Embedded KV (bbolt/Badger) as the log** — viable, but hides the mechanics we want to showcase
  and adds a dependency; revisit only if hand-rolled log proves too costly.
- **In-memory only** — rejected: fails the "survive downtime" requirement outright.
