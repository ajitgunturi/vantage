# Streamer — CSV Telemetry Producer

← back to the [README](../README.md)

The Streamer (`cmd/streamer/`) loops a DCGM metrics CSV forever, restamps each record with the
current UTC time, and publishes to the MQ via the generated gRPC `Produce` client. Up to 10
instances run concurrently (soak-proven in Phase 5).

## Prerequisites

- A DCGM metrics CSV file in the repo root (`dcgm_metrics_*.csv`). Copy or symlink one before
  running the Streamer or `make smoke-03`. The CSV is gitignored — never committed.
- A running MQ (`./bin/mq`, gRPC on `:50051`).

## CSV in Kubernetes (kind)

The streamer image bakes a CSV into `/data/dcgm_metrics.csv` **only** via
`make deploy CSV=/path/to/your.csv` (`build/streamer.Dockerfile`, `DEPLOY_CSV` build arg) —
there is no fallback; deploy prompts for the path on a TTY and fails loudly when scripted.
Plain `make docker` builds a CSV-less image: dev/test flows mount the single test fixture
`testdata/fixture.csv` at runtime (see `docker-compose.full.yml`). The image is built
locally and loaded into kind — never pushed to a registry. A ConfigMap cannot carry the real
file (1.1MB > the 1MiB ConfigMap limit); set `streamer.useFixtureConfigMap=true` in Helm
values to force the tiny deterministic 3-GPU fixture instead of the image-baked data.

## Run the Streamer

```sh
export STREAMER_CSV_PATH=./dcgm_metrics_<date>.csv
export STREAMER_MQ_ADDR=:50051
./bin/streamer            # loops the CSV forever; Ctrl-C to stop
# or: STREAMER_CSV_PATH=... go run ./cmd/streamer
```

Built with `make build`.

## Environment variables

| Env var | Default | Meaning |
|---|---|---|
| `STREAMER_MQ_ADDR` | `:50051` | gRPC address of the MQ server |
| `STREAMER_CSV_PATH` | — | Path to the DCGM metrics CSV (required) |
| `STREAMER_LOOP_DELAY_MS` | `1` | Inter-row sleep in ms; 0 disables |
| `STREAMER_HEALTH_ADDR` | `:9000` | Health-endpoint listen address (`/healthz`, `/readyz`) |

## Restamping & GPU identity (D-04)

Each record is restamped with the current execution time at **`RFC3339Nano`** (nanosecond)
precision — second-granularity restamps would collapse multiple same-second readings onto the
same natural key in Postgres (see [`ADR-006`](adr/ADR-006-long-narrow-schema-natural-key.md)).

`gpu_id` in the database stores the GPU **UUID** (e.g. `GPU-5fd4f087-bfa9-2f3d-...`), not the
ordinal index (`"0"`, `"1"`, …) from the CSV's `gpu_id` column. `models.FromProto` maps the proto
`uuid` field to `db.gpu_id`. The ordinal is kept in the proto for debugging but is never written
to Postgres.

## Health

The Streamer runs a lightweight health listener on `:9000` (`STREAMER_HEALTH_ADDR`).
Readiness means the MQ stream has been dialed and entered:

```sh
curl -s http://localhost:9000/healthz
curl -s http://localhost:9000/readyz
```

## Batched publish & metrics

Rows publish in batches of `STREAMER_BATCH_SIZE` (default 100) via the MQ's
`ProduceBatch` RPC (ADR-013) — set `1` for the legacy one-RPC-per-row path.
Rows are still restamped individually at CSV *read* time, so batching never
collides timestamps. `GET /metrics` on the health port (ADR-014):
`streamer_rows_produced_total`, `streamer_rows_skipped_total`, and
`streamer_produce_retries_total` (the client-side view of broker
backpressure).
