# Development, Testing & DevOps Reference

← back to the [README](../README.md)

Reference for building, running locally, testing, and deploying vantage. Per-service run and
configuration details live in the sibling docs — this file covers shared infrastructure, the
smoke suite, testing gates, the Helm/kind deploy workflow, and CI.

---

## Repository layout

```text
api/proto/      # shared gRPC contracts (mq.proto)
cmd/<svc>/      # independent service entrypoints (mq, streamer, collector, gateway)
internal/       # MQ service-private packages (queue, server, http, config)
pkg/            # the ONLY cross-service surface (pb/ generated code; db/, models/)
scripts/smoke/  # runnable manual smoke checks, one set per phase (+ mqprobe gRPC client)
build/          # one multi-stage Dockerfile per service
deployments/    # Helm umbrella chart + per-service sub-charts + values.yaml
Makefile        # build, test, coverage, smoke, proto, swagger, docker, k8s
go.mod          # single module root — one module, four services, no workspaces
```

`make help` lists every target. `make build` skips any service whose `cmd/<svc>/` directory
does not yet exist (used during phased development).

---

## Phase 2 — Storage foundation

### Local Postgres dev stack

`docker-compose.yml` provides a `postgres:17-alpine` instance for local development and smoke
testing. Credentials (`vantage`/`vantage` on `localhost:5432/vantage`) are local dev defaults —
never used in production; production DSNs are always supplied via `VANTAGE_DB_DSN`.

```sh
make dev-up    # start Postgres (waits for healthcheck)
make dev-down  # stop Postgres
```

No `psql` on the host is needed — every SQL assertion in the smoke scripts runs through
`docker compose exec postgres psql`.

### Applying the schema

The shared `pkg/db` library ships the versioned migration embedded in the binary. `cmd/migrate`
is a one-shot runner — it reads `VANTAGE_DB_DSN`, calls `pkg/db.Migrate`, and exits:

```sh
export VANTAGE_DB_DSN=postgres://vantage:vantage@localhost:5432/vantage?sslmode=disable
go run ./cmd/migrate
# migrate: schema up to date
```

`pkg/db.Migrate` is idempotent (`migrate.ErrNoChange` is treated as success), so it is safe to
call on every service startup or restart. The Collector also auto-migrates on startup.

### Storage environment variables

| Env var | Required | Default | Meaning |
|---|---|---|---|
| `VANTAGE_DB_DSN` | yes | — | Full `postgres://` connection string; **never logged** |
| `VANTAGE_DB_MAX_CONNS` | no | 0 (pgxpool default) | Maximum pool connections |

Both env vars are read by `pkg/db.FromEnv()`, shared by Collector and Gateway.

### GPU identity convention (D-04)

`gpu_id` in the database stores the GPU **UUID** (e.g. `GPU-5fd4f087-bfa9-2f3d-...`), not the
ordinal index (`"0"`, `"1"`, …) from the CSV's `gpu_id` column. `models.FromProto` maps the proto
`uuid` field to `db.gpu_id`. The ordinal is kept in the proto for debugging but is never written
to PostgreSQL. The column name `gpu_id` was chosen to match the spec's composite index expression
and the `/api/v1/gpus/{id}` route — the stored value is always the UUID.

### Verify (smoke-02)

`make smoke-02` runs `scripts/smoke/phase02-postgres.sh`, which:

1. Starts the dev stack (`make dev-up`) if Postgres is not already running
2. Applies the schema via `go run ./cmd/migrate`
3. Asserts that `gpu_metrics` exists with the expected columns
4. Asserts that both indexes exist: `idx_gpu_metrics_gpu_id_ts` (composite) and
   `uq_gpu_metrics_natural_key` (unique)
5. Seeds 100,000 rows (`10 GPUs × 10 metrics × 1,000 timestamps`) and runs `ANALYZE`
6. Runs `EXPLAIN` on a selective single-GPU 1-hour range query and asserts `Index Scan`
   (not `Seq Scan`) — proving the planner uses the composite index at representative scale

---

## Smoke suite

A runnable, dependency-light suite that verifies each phase end-to-end. One script per phase
under `scripts/smoke/phaseNN-*.sh`.

```sh
make smoke         # run every phase's smoke check shipped so far
make smoke-01      # MQ only
make smoke-02      # Postgres storage
make smoke-03      # full CSV→MQ→Collector→Postgres pipeline
make smoke-04      # API Gateway endpoints + Swagger spec
make smoke-05      # kind cluster E2E (requires make kind-up && make deploy first)
```

### smoke-01 (MQ)

Builds the MQ on dedicated ports, starts it, then via `mqprobe`:

1. Produces/consumes 20 messages over a real bidi `Consume` stream with credit + per-id acks
2. Runs a **late-join no-loss** scenario — produce 20, consume only 10, then drain the remaining
   10 in a third process — proving zero loss across a producer disconnect
3. Runs a **credit-boundary** scenario — consumer first credit `0` must not deadlock: broker
   substitutes its default window and drains all 20
4. Cross-checks `GET /api/v1/queue/inspect` counters (`delivered_total`, `consumed_total`,
   `redelivered_total`) — the redelivered count goes positive exactly when the partial consumer
   disconnects holding unacked leases, proving redelivery over the wire

### smoke-02 (Postgres)

See [Storage foundation — Verify (smoke-02)](#verify-smoke-02) above.

### smoke-03 (Pipeline)

`scripts/smoke/phase03-pipeline.sh`:

1. Starts the dev Postgres stack (`make dev-up`) if not already running
2. Finds `dcgm_metrics_*.csv` in the repo root (fails cleanly if absent)
3. Builds and starts `bin/mq`, `bin/collector`, `bin/streamer` in the background
4. Waits 5 seconds for telemetry to flow
5. Asserts `count(*) FROM gpu_metrics > 0` (rows landed)
6. Asserts `count(*) == count(DISTINCT gpu_id, metric_name, timestamp)` (zero duplicates)
7. Asserts no ordinal values (`0`, `1`, …) in `gpu_id` (UUID mapping verified, D-04)
8. Prints row count, distinct row count, and distinct GPU count
9. Leaves Postgres running; kills only the three pipeline processes

### smoke-04 (Gateway)

`scripts/smoke/phase04-gateway.sh`:

1. Starts the dev Postgres stack if not already running
2. Builds `bin/gateway` and starts it in the background
3. Waits up to 15s for the gateway to become ready on `:8080`
4. Asserts `GET /api/v1/gpus` → 200 + JSON array
5. Asserts `GET /api/v1/gpus/<first-gpu>/telemetry` → 200 + JSON array
6. Asserts `GET /api/v1/gpus/GPU-does-not-exist/telemetry` → 404
7. Asserts `GET /swagger/doc.json` → 200 + valid JSON spec with ≥ 2 paths
8. Leaves Postgres running; kills only the gateway process

### smoke-05 (kind E2E)

Requires `make kind-up && make deploy` first. `scripts/smoke/phase05-kind.sh`:

1. Verifies kind cluster reachable and Helm release `vantage` installed
2. Verifies migration hook Job completed (or deleted on success per hook policy)
3. Verifies all four Deployments Available (`vantage-mq`, `vantage-streamer`,
   `vantage-collector`, `vantage-gateway`)
4. Port-forwards gateway to local **8081** (`svc/vantage-gateway 8081:8080`)
5. Asserts `GET /api/v1/gpus` returns a non-empty array (pipeline is flowing)
6. Asserts `GET /api/v1/gpus/<id>/telemetry` returns a `TelemetryPage` envelope
7. OPS-03 targeted-upgrade check: `helm upgrade --reuse-values --set mq.image.pullPolicy=Never`
   rolls only the MQ Deployment; streamer/collector/gateway generations are unchanged

---

## Testing

### Automated (unit + integration + coverage)

```sh
make test       # go test -race ./... — unit + integration tests
make coverage   # enforces >= 90% line coverage on internal/ and pkg/ (pkg/pb excluded)
make lint       # golangci-lint (fallback: go vet)
make e2e        # end-to-end pipeline tests (requires Docker)
```

The MQ's correctness under concurrency is proven by race-detector tests in `internal/server` and
`internal/queue` (run at `-count=50`): broker-side at-least-once with no loss on consumer
disconnect (in-flight leases count against the capacity budget, so requeue never evicts;
enqueue overflow is governed by `MQ_OVERFLOW_POLICY` — drop-oldest freshness-first by default,
with lossless `reject` backpressure opt-in — see Overload semantics in `docs/mq.readme.md`),
no over-pull beyond credit
`C`, redelivery of unacked leases to survivors, unique
steady-state delivery, safe ack handling (unknown/double acks are no-ops), and no goroutine leaks.

### E2E and harness

```sh
make e2e           # go test -race -tags=integration ./test/e2e/... (requires Docker)
make test-harness  # go test -race -tags=e2e ./test/harness/... (full five-image compose stack)
KEEP=1 make test-harness  # leave the stack running for debugging
```

The harness (`test/harness/`, `//go:build e2e`) owns a full `docker-compose.full.yml` stack
via testcontainers-go. It waits for the gateway, then asserts rows flowed CSV → streamer →
MQ → collector → Postgres → gateway and that counts keep growing. Point it at a kind deployment
with `HARNESS_GATEWAY_BASE=http://localhost:8081` to skip the compose stack.

---

## Phase 5 — DevOps (Docker + Kubernetes/Helm)

Every service ships as a **multi-stage distroless image** and deploys to a local **kind** cluster
via a **Helm umbrella chart** — four service sub-charts plus a Bitnami PostgreSQL dependency, with
schema migrations run by a `post-install,post-upgrade` hook Job.

### Prerequisites

- **Docker via Rancher Desktop** (or Docker Desktop) — set `DOCKER_HOST` in `.env`
- **kind** at `~/go/bin/kind` (`make tools` installs it; Makefile prepends `~/go/bin` to PATH)
- **Helm v3/v4** on PATH

### Deploy workflow

```sh
make kind-up                        # create the kind cluster
make deploy CSV=/path/to/your.csv   # docker build (5 images) → kind-load → helm-install
make smoke-05                       # prove the deployed pipeline end-to-end
make kind-down                      # delete the cluster
```

`make deploy` requires the DCGM telemetry CSV to bake into the streamer image (`CSV=<path>`;
prompts on a TTY, fails loudly with usage when scripted — see "Bring your own CSV" in the
[README](../README.md)). It builds all five images (`vantage/{mq,streamer,collector,gateway,migrate}:dev`),
loads them into kind, pulls the Bitnami PostgreSQL chart (`dependency-update`, automatic), and
installs the umbrella release `vantage`. The migration hook Job runs after PostgreSQL Service is
available — ensures the schema is applied before service pods roll.

> **Image caveat:** charts use `imagePullPolicy: IfNotPresent` with the fixed `:dev` tag — a
> forgotten `make kind-load` after rebuilding images surfaces as stale code running. Re-run
> `make deploy` after code changes.

### Independent deploys (OPS-03)

Every sub-chart exposes `enabled` and `image.tag`:

```sh
helm upgrade --reuse-values --set mq.image.tag=dev vantage deployments   # rolls ONLY mq
helm upgrade --reuse-values --set streamer.enabled=false vantage deployments
```

The MQ deploys as a **single replica with `strategy: Recreate`** — hardcoded in the sub-chart
template (not a values-overridable setting) because the in-memory broker cannot be replicated
(ADR-001).

### Soak (`make soak`)

```sh
make soak                              # defaults: 60s, 3 streamer replicas
SOAK_DURATION=300 SOAK_STREAMERS=10 make soak   # 10-concurrent-streamer proof
```

Scales the streamer Deployment, asserts rows keep growing, MQ inspect counters reconcile
(`produced_total >= consumed_total`), and queue depth stays bounded below capacity.
Restores 1 replica on exit.

---

## Phase 6 — Production hardening

### Health endpoints and Kubernetes probes

Every service exposes `/healthz` (liveness — process alive) and `/readyz` (readiness — service
ready to handle traffic):

| Service | Health port | Readiness semantics |
|---------|------------|---------------------|
| MQ | `:8080` (existing HTTP ServeMux) | gRPC server not shutting down |
| Gateway | `:8080` (chi router) | DB pool `Ping` succeeds (2s timeout) |
| Streamer | `:9000` (`STREAMER_HEALTH_ADDR`) | MQ stream dialed and entered |
| Collector | `:9001` (`COLLECTOR_HEALTH_ADDR`) | MQ stream open (first `Recv` succeeded) |

The Helm sub-charts wire `livenessProbe` and `readinessProbe` against these endpoints.
Defaults (`initialDelaySeconds: 30`, `failureThreshold: 6`, `periodSeconds: 10`) are tuned for
kind's slower startup; the gateway uses `initialDelaySeconds: 60` to absorb the migrate Job.

### Resource requests and limits

| Service | cpu request / limit | memory request / limit |
|---------|--------------------|-----------------------|
| MQ | 100m / 500m | 64Mi / 256Mi |
| Gateway | 100m / 200m | 32Mi / 128Mi |
| Streamer | 50m / 100m | 16Mi / 64Mi |
| Collector | 50m / 100m | 16Mi / 64Mi |

MQ memory covers the ring buffer (10,000 × ~200 B ≈ 2 MB) plus credit window and gRPC
overhead with 5× headroom. All services set `resources.requests.cpu` — a prerequisite for HPA.

### Gateway HPA (optional)

The gateway sub-chart ships an `autoscaling/v2` HPA, disabled by default:

```sh
helm upgrade vantage deployments --reuse-values \
  --set gateway.autoscaling.enabled=true \
  --set gateway.autoscaling.minReplicas=1 \
  --set gateway.autoscaling.maxReplicas=3 \
  --set gateway.autoscaling.targetCPUUtilizationPercentage=80
```

> **kind caveat:** HPA requires `metrics-server`, which kind does not include by default.
> Without it, `kubectl describe hpa` shows `TARGETS: <unknown>/80%` and no scaling occurs.

### Prometheus metrics endpoints

Every service exposes `GET /metrics` in Prometheus text format (ADR-014): the MQ exports queue
depth, retry/DLQ lane counters, and in-flight gauges; the Gateway a request-latency histogram;
the Streamer and Collector throughput counters. Pods carry `prometheus.io/*` scrape annotations.
See [`deployments/monitoring/README.md`](../deployments/monitoring/README.md) for wiring these
into a Prometheus stack.

### Collector backlog-driven HPA

The Collector sub-chart ships an `autoscaling/v2` HPA
(`deployments/charts/collector/templates/hpa.yaml`, disabled by default) that scales on the
**external metric `mq_queue_backlog`** (`mq_queue_depth + mq_retry_depth`) rather than CPU — a
consumer bottlenecked on Postgres idles its CPU while the broker backlog grows. Requires
kube-prometheus-stack + prometheus-adapter; setup and tuning
(`collector.autoscaling.targetBacklogPerReplica`) in
[`deployments/monitoring/README.md`](../deployments/monitoring/README.md) and
[`ADR-014`](adr/ADR-014-prometheus-metrics-queue-depth-hpa.md).

### Partitioned retention CronJob

`gpu_metrics` is partitioned by UTC day (migration 000003). A nightly CronJob
(`deployments/templates/retention-cronjob.yaml`) drops partitions past the retention window and
pre-creates upcoming ones so the write path never waits on DDL. Configured via the `retention.*`
Helm values (`enabled`, `days`, `precreateDays`, `schedule`). Design:
[`ADR-012`](adr/ADR-012-daily-partitioning-retention.md).

### Structured logging

All four services log through `log/slog` (stdlib, no external dependency) with a JSON handler.
Each log line carries a `service` attribute. DSN and secrets are never logged.

| Env var | Default | Meaning |
|---------|---------|---------|
| `LOG_LEVEL` | `INFO` | Log level: `DEBUG`, `INFO`, `WARN`, `ERROR` |

### CI

GitHub Actions CI runs on every push and pull request (`.github/workflows/ci.yml`):

```sh
make build      # compile all four service binaries
make test       # go test -race
make coverage   # go test -race -tags=integration, >= 90% gate (uses testcontainers)
make lint       # golangci-lint
```

The Makefile is the single source of truth for gate definitions — the workflow only calls make targets.
