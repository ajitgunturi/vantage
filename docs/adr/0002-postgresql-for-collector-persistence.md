# ADR-0002: PostgreSQL for collector persistence

- Status: Accepted
- Date: 2026-06-24

## Context
Collectors consume telemetry and must persist it for the API gateway to query by GPU and time
window. The assignment allows open-source databases. We need time-ordered range queries
(`start_time`/`end_time`) per GPU and a GPU dimension list.

## Decision
Use **PostgreSQL**. Normalize into a GPU dimension table and a telemetry fact table:
- `gpus(uuid PK, gpu_id, hostname, device, model_name, ...)`
- `telemetry(uuid FK, metric_name, value, ts, ...)` with an index on `(uuid, ts)` for the window
  queries, ordered by time.
- Collectors **upsert** idempotently on a natural key (`uuid, metric_name, ts`) so at-least-once
  redelivery from the MQ does not create duplicates.
- Access via `pgx` (no heavy ORM) for explicit, observable SQL.

## Driving Prompt
> We are allowed to use opensource database such as postgresql to store the data the collector will
> collect and store.

## Consequences
- (+) Mature, ubiquitous, easy to run in kind via Helm subchart; strong range-query support.
- (+) Idempotent upsert turns the MQ's at-least-once into effective exactly-once at rest.
- (−) Write amplification under high producer load; mitigated with batched inserts / COPY.
- Follow-up: decide batch size & whether to partition `telemetry` by time if volume grows.

## Alternatives considered
- **TimescaleDB** — better for time-series but adds an extension; vanilla Postgres is sufficient at
  this scale.
- **SQLite** — rejected: poor concurrent-writer story for scaled collectors.
