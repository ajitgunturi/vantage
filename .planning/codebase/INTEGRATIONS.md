# External Integrations

**Analysis Date:** 2026-06-24

## APIs & External Services

**Custom Message Queue (gRPC):**
- Service: Internal custom-built Kafka-like broker (NOT RabbitMQ/ZeroMQ/Kafka)
  - Transport: gRPC streaming (`google.golang.org/grpc` v1.71.0)
  - Contract: `mq/proto/mqv1/mq.proto`
  - Endpoints:
    - `Produce(stream ProduceRequest) → stream ProduceResponse` — Streaming producer client
    - `Consume(ConsumeRequest) → stream ConsumeResponse` — Server-streaming consumer push (consumer-group offset tracking)
    - `Commit(CommitRequest) → CommitResponse` — Offset acknowledgment (at-least-once delivery)
    - `CreateTopic(TopicSpec) → CreateTopicResponse` — Admin API for topic provisioning
    - `Health(HealthRequest) → HealthResponse` — Liveness probe

**Data Inputs:**
- DCGM CSV Telemetry (NVIDIA DCGM exporter format)
  - Source: CSV file looped by streamer (row-by-row or batched; cadence TBD at implementation)
  - Format: `timestamp, metric_name, gpu_id, device, uuid, modelName, Hostname, container, pod, namespace, value, labels_raw`
  - Cardinality: 10 metric names × 247 GPUs = ~2,470 rows per full loop
  - Metrics: `DCGM_FI_DEV_GPU_UTIL`, `DCGM_FI_DEV_MEM_COPY_UTIL`, `DCGM_FI_DEV_ENC_UTIL`, `DCGM_FI_DEV_DEC_UTIL`, `DCGM_FI_DEV_GPU_TEMP`, `DCGM_FI_DEV_POWER_USAGE`, `DCGM_FI_DEV_SM_CLOCK`, `DCGM_FI_DEV_MEM_CLOCK`, `DCGM_FI_DEV_FB_FREE`, `DCGM_FI_DEV_FB_USED`
  - Re-stamping: Streamer discards original CSV timestamp and re-stamps with current time at produce

## Data Storage

**Primary Data Store:**
- **PostgreSQL** (version 13+, assumed)
  - Client: `github.com/jackc/pgx` (declared in PROJECT.md § 6, not yet in `collector/go.mod`; added via TDD)
  - Connection: Env var `DATABASE_URL` or similar (not yet defined)
  - Use: Collector upserts telemetry (idempotent on `(uuid, metric_name, ts)`)
  - Schema: Two tables (normalized)
    - `gpus` — GPU dimensions (uuid, gpu_id, hostname, device, modelName)
    - `telemetry` — Fact table (uuid, metric_name, value, timestamp)
  - Persistence scope: All telemetry state; custom MQ broker log lives on PVC (separate from DB)

**Message Queue Persistence:**
- **Kubernetes PersistentVolume (PVC)**
  - MQ broker uses append-only segment log + offset index (Kafka-lite design)
  - Path: `/mq-data` (implied; exact path TBD in broker implementation and Helm chart)
  - Durability: Segments fsync'd on write; broker rebuilds state on restart
  - Storage class: TBD (default or SSD for prod; kind uses default)
  - Size: TBD (depends on throughput and retention policy)

**Caching:**
- None declared or in use

## Authentication & Identity

**Auth Provider:**
- None — No authentication system yet
- MQ broker assumes internal-only (Kubernetes DNS service discovery)
- API Gateway assumes internal-only (no public auth token mechanism yet)
- Future: Could be OAuth2 or Kubernetes service account tokens (not in current scope)

**GPU Identity (Decision pending):**
- **Canonical identifier:** UUID (e.g., `GPU-5fd4f087-...`) per ADR-0005 (*Proposed*, not yet confirmed)
- Alternative: `hostname:gpu_id` composite key (secondary; not used in API yet)
- API path: `/api/v1/gpus/{id}` expects UUID resolution (implementation deferred)

## Monitoring & Observability

**Metrics & Exposition:**
- **Prometheus client** (`prometheus/client_golang`, declared in PROJECT.md § 6, not yet in go.mod)
  - Metrics endpoints: Planned for each service (mq, streamer, collector, apigateway)
  - Format: Prometheus text format (implicitly via Prometheus client)
  - Metrics tracked: Throughput (msg/s), latency, broker queue depth, consumer lag, CPU/memory (TBD)
  - Collection: External Prometheus server assumed to scrape `/metrics` on each service port

**Error Tracking:**
- None — Local logging only (via `log/slog` stdlib)

**Logging:**
- **Standard library slog** — Structured logging (Go 1.21+, stdlib)
  - Package: `log/slog` (no dependency; Go stdlib)
  - Implementation deferred to TDD; pattern: contextual fields (uuid, partition, offset, timestamp)

## CI/CD & Deployment

**Hosting Platform:**
- **Kubernetes** (kind locally; any K8s in production)
- **Helm** — Package manager and templating
  - Umbrella chart: `k8s-infra/helm/telemetry/`
  - Sub-charts (pending):
    - `charts/mq-broker/` — Custom message queue broker
    - `charts/streamer/` — Telemetry producer
    - `charts/collector/` — Telemetry consumer + DB upserter
    - `charts/apigateway/` — REST API
    - `charts/postgres/` — PostgreSQL dependency (or Bitnami subchart reference)

**Docker Images:**
- None yet created
- Pending: `Dockerfile` in each of mq/cmd/, streamer/cmd/, collector/cmd/, apigateway/cmd/
- Build: `GOWORK=off go build` inside container (multi-module monorepo isolation)

**CI Pipeline:**
- GitHub Actions workflow (likely in `.github/workflows/`; not yet examined for this context)
- Assumed gates: `make lint`, `make test`, `make cover-check`, `make cover-logic`

## Environment Configuration

**Required Environment Variables (TBD at implementation):**
- `DATABASE_URL` — PostgreSQL connection string (collector only)
- `MQ_BROKER_ADDR` — gRPC broker address (streamer/collector; e.g., `mq-broker:6969`)
- Logging level: TBD (env var not yet defined)
- Metrics port: TBD (each service; default pattern: `:9090` offset per service)

**Configuration Method:**
- Environment variables (Kubernetes ConfigMap and Secret)
- No ConfigMap or Secret manifests yet (Helm chart templates TBD)
- Secrets location: Kubernetes Secret resources (not yet created; will use standard K8s secret practices)

## Webhooks & Callbacks

**Incoming Webhooks:**
- None — All data is pulled (Streamer reads CSV, Collector reads from MQ)

**Outgoing Webhooks:**
- None — No downstream event publishing planned

**Server Callbacks:**
- MQ Consume RPC is server-streaming (broker pushes messages to consumers via gRPC)
- API GET endpoints: RESTful queries, no callbacks

## Data Flow & Message Contracts

**Telemetry Message Format (on MQ):**
```protobuf
message Message {
  bytes key = 1;                    // partition key (GPU uuid)
  bytes value = 2;                  // serialized telemetry row
  int64 timestamp_unix_ns = 3;      // ns-precision arrival time at broker
}
```

**Value Encoding (TBD):**
- Current contract: `bytes value` is opaque
- Expected: JSON or Protocol Buffer encoding of one CSV row
- Parser: Collector implements deserialization (not yet)

**Consumer Group Contract:**
- Topic: Single default topic for GPU telemetry (topic name TBD; implied `gpu-telemetry`)
- Partitions: ≥10 (matches max 10 collectors for scale-out)
- Consumer group: Single group (all collectors in same group, each owns 1+ partition)
- Offset tracking: Per-partition, per-group (broker maintains in segment log)
- Redelivery semantics: At-least-once (Collector must upsert idempotently)

---

*Integration audit: 2026-06-24*
