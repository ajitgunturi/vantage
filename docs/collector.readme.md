# Collector — MQ Consumer + Batch Persister

← back to the [README](../README.md)

The Collector (`cmd/collector/`) holds a long-lived bidirectional `Consume` stream against the MQ
(acking each message after it is persisted), auto-reconnects when the stream drops, and
batch-inserts telemetry into PostgreSQL via `pgxpool`.

## Run the Collector

```sh
# Prerequisites: MQ running (:50051) and Postgres up (make dev-up).
export VANTAGE_DB_DSN=postgres://vantage:vantage@localhost:5432/vantage?sslmode=disable
export COLLECTOR_MQ_ADDR=:50051
./bin/collector           # auto-migrates the schema on startup
# or: go run ./cmd/collector
```

Built with `make build`.

## Environment variables

| Env var | Default | Meaning |
|---|---|---|
| `VANTAGE_DB_DSN` | — | PostgreSQL connection string (required) |
| `COLLECTOR_MQ_ADDR` | `:50051` | gRPC address of the MQ server |
| `COLLECTOR_BATCH_SIZE` | `50` | Rows to accumulate before a size-flush |
| `COLLECTOR_FLUSH_MS` | `500` | Ticker interval in ms for time-flush |
| `COLLECTOR_CREDIT` | `100` | Initial in-flight window (must be ≥ BATCH_SIZE) |
| `COLLECTOR_HEALTH_ADDR` | `:9001` | Health-endpoint listen address (`/healthz`, `/readyz`) |

## Effectively-once persistence (the key property)

Delivery from the MQ is **at-least-once** — redelivery after a consumer disconnect can hand the
same message to a Collector more than once. What the Collector guarantees is **effectively-once
persistence**: the idempotent `ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING` upsert
absorbs redeliveries, so each telemetry reading lands in Postgres once — within the granularity
of that natural key. The E2E test (QA-03) proves this end-to-end under
`test/e2e/pipeline_test.go` (run via `make e2e` — requires Docker/Rancher Desktop).

> **Concurrent Streamers:** Under ≥2 simultaneous Streamer instances, nanosecond-level timestamp
> collisions can cause `ON CONFLICT DO NOTHING` to silently drop a duplicate row. This is accepted
> by design — see [`ADR-002`](adr/ADR-002-natural-key-microsecond-collision.md).

## Poison rows — bisect-on-failure + dead-letter table

pgx v5 `SendBatch` runs a batch in **one implicit transaction**, so a single row that
deterministically fails at exec time (constraint violation, numeric overflow — SQLSTATE class
22/23) would abort the whole batch, leave it unacked, and loop through redelivery forever. The
Collector now isolates such poison instead of wedging (ADR-011):

- On a poison-class batch error, the batch is **bisected**: each half retries in its own
  transaction, recursing to the single failing row in O(log n) round-trips. Re-running halves
  that already landed is harmless — the insert is the idempotent `ON CONFLICT DO NOTHING` upsert.
- The isolated row is preserved in **`gpu_metrics_dlq`** (stable broker id, delivery attempts,
  full protojson payload, SQLSTATE + message), then **acked** — the DLQ insert is the durable
  handling that makes the ack safe. Inspect or replay it with SQL against that table.
- **Transient** errors (connection loss, timeouts, serialization, admin shutdown) never
  dead-letter: the batch stays unacked and redelivers exactly as before.

## Health

The Collector runs a lightweight health listener on `:9001` (`COLLECTOR_HEALTH_ADDR`).
Readiness means the MQ stream is open (first `Recv` succeeded):

```sh
curl -s http://localhost:9001/healthz
curl -s http://localhost:9001/readyz
```

## Inspect rows in PostgreSQL

```sh
# Most-recent 10 readings via compose (no host psql required)
docker compose exec -T postgres psql -U vantage -d vantage \
  -c 'SELECT gpu_id, metric_name, timestamp, value FROM gpu_metrics ORDER BY timestamp DESC LIMIT 10;'

# Row count and GPU diversity
docker compose exec -T postgres psql -U vantage -d vantage \
  -c 'SELECT count(*), count(distinct gpu_id) FROM gpu_metrics;'
```
