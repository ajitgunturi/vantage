# PROJECT: Elastic GPU Telemetry Pipeline with Custom Message Queue

> Context document derived from `GPU Telemetry Pipeline Message Queue.pdf` + analysis of
> `dcgm_metrics_20250718_134233.csv`. This is the single source of truth for scope, data model,
> and design decisions. Implementation has **not** started — awaiting go-ahead.

---

## 1. Assignment Summary (verbatim requirements)

Build an **elastic, scalable, stable telemetry pipeline** for an AI cluster (many hosts, each with
1+ GPUs). Language: **Golang**. Deploy via **Docker + Kubernetes + Helm**.

### Mandated components
| Component | Responsibility | Elasticity requirement |
|---|---|---|
| **Telemetry Streamer** (producer) | Read telemetry from CSV, stream periodically over the custom MQ. Loop the CSV to simulate a continuous stream. **The processing time = the telemetry's timestamp** (we re-stamp, not replay original ts). | Scale streamers up/down dynamically |
| **Telemetry Collector** (consumer) | Consume from MQ, parse, persist. | Scale collectors up/down dynamically |
| **API Gateway** | REST API exposing telemetry; **OpenAPI spec auto-generated**. | — |
| **Messaging Queue** | **Custom-built** MQ (NOT ZeroMQ/RabbitMQ/Kafka). Library or service. Must reason about scale, performance, availability. | Streamer/collector capped at **10 instances** each for the exercise |

### Hard constraints
- Custom MQ only — no off-the-shelf brokers.
- Max **10** streamer and **10** collector instances (scale ceiling for the exercise).
- All components deployable to Kubernetes via **Helm charts**.
- Go, idiomatic, with graceful error handling + memory management.

### API endpoints (required)
1. `GET /api/v1/gpus` — list all GPUs for which telemetry exists.
2. `GET /api/v1/gpus/{id}/telemetry` — all telemetry for one GPU, ordered by time.
3. `GET /api/v1/gpus/{id}/telemetry?start_time=...&end_time=...` — inclusive time-window filter.

### Deliverables (Git repo)
- Source for the full stack.
- **Unit tests required**; system tests = bonus.
- **Dockerfiles** + **Helm charts**.
- **OpenAPI (Swagger)** spec, auto-generated via a **Makefile** target.
- **Makefile** target for tests + **code-coverage** reporting.
- Comprehensive **README**: architecture & design writeup, build/packaging, install workflow,
  sample user workflow, and **how AI assistance was used**.
- **AI-usage doc**: bootstrapping repo/code/tests/build env, with the exact prompts used and notes
  on where prompts fell short and needed manual intervention.

### Success criteria / bonus
Focused scope; clean maintainable systems-level Go; clear logging & error handling; coverage via
Makefile; auto-generated OpenAPI via Makefile; thorough prompt documentation incl. where prompts
fell short; well-documented interfaces.

---

## 2. User-added requirements (beyond the PDF)

1. **Storage = PostgreSQL** (open-source DB is explicitly allowed) for the collector's persistence.
2. **Custom MQ = independent installation** with an **integrated persistence layer** so it
   **survives downtime** (durable, restart-safe — messages not lost on broker crash/restart).
3. **Performance characterization** across producer/consumer ratios, max scale 10 each:
   - producers **>** consumers (backpressure / queue growth regime)
   - producers **<** consumers (consumer starvation / idle regime)
   - producers **=** consumers (balanced regime)

---

## 3. Dataset Analysis — `dcgm_metrics_20250718_134233.csv`

NVIDIA **DCGM exporter** scrape output (Prometheus-style), 2,470 data rows.

### Columns
`timestamp, metric_name, gpu_id, device, uuid, modelName, Hostname, container, pod, namespace, value, labels_raw`

### Cardinality (measured)
- **10 metric names** (each appears 247×): `DCGM_FI_DEV_GPU_UTIL`, `DCGM_FI_DEV_MEM_COPY_UTIL`,
  `DCGM_FI_DEV_ENC_UTIL`, `DCGM_FI_DEV_DEC_UTIL`, `DCGM_FI_DEV_GPU_TEMP`, `DCGM_FI_DEV_POWER_USAGE`,
  `DCGM_FI_DEV_SM_CLOCK`, `DCGM_FI_DEV_MEM_CLOCK`, `DCGM_FI_DEV_FB_FREE`, `DCGM_FI_DEV_FB_USED`.
- **31 hosts** (`mtv5-dgx1-hgpu-001..032`), **8 GPUs/host** (`gpu_id` 0–7).
- **247 distinct UUIDs** = 247 physical GPUs (≈31×8, a few gaps). 10 metrics × 247 ≈ 2470 rows. ✔
- 4 distinct source timestamps (a ~4s snapshot) — irrelevant since we **re-stamp at stream time**.
- `container`, `pod`, `namespace` are **empty** in this file (carry as nullable).

### Data-model implications
- **Canonical GPU identity = `uuid`** (e.g. `GPU-5fd4f087-...`). `gpu_id` (0–7) is only unique
  *within a host*; `Hostname`+`gpu_id` is an alternative composite key. The API's `{id}` path param
  should resolve to **UUID** (globally unique, stable). Decision flagged for confirmation.
- A telemetry datapoint is **(uuid, metric_name, value, ts)** plus GPU dimensions
  (host, gpu_id, device, modelName). Normalize into `gpus` (dimension) + `telemetry` (fact) tables.

---

## 4. Target Architecture (proposed — pre-implementation)

```
 CSV ──► [Streamer ×N] ──produce──► ┌─────────────────────────┐ ──consume──► [Collector ×M] ──► PostgreSQL
 (loop, re-stamp)                   │   CUSTOM MQ BROKER       │              (parse + upsert)        ▲
                                    │  topic/partitions        │                                     │
                                    │  append-only WAL/segments│              [API Gateway] ◄─────────┘
                                    │  offset + ack (at-least- │              REST + auto OpenAPI
                                    │  once), survives restart │
                                    └─────────────────────────┘
```

### 4.1 Custom MQ — design intent
- **Deployable as an independent service** (its own Docker image + Helm chart), with a Go **client
  library** for producers/consumers (gRPC or HTTP/2 framed protocol over TCP).
- **Durability via append-only segment log + WAL** on a PVC (Kafka-lite): writes fsync'd, segmented
  files with an offset index; on restart the broker rebuilds from segments → **survives downtime**.
  (PostgreSQL is reserved for collector data, NOT the MQ, to keep "custom" honest.)
- **Partitions** = unit of consumer parallelism (enables collector scale-out; consumer-group offset
  tracking). Partition count ≥ max consumers (≥10).
- **Delivery = at-least-once** (offset commit/ack after persist); collectors must be **idempotent**
  (upsert on `(uuid, metric_name, ts)`) to tolerate redelivery.
- **Backpressure**: bounded in-memory buffers + flow control so producers>consumers grows the log
  on disk rather than OOM-ing the broker.

### 4.2 Components → deployables
`streamer`, `collector`, `mqbroker`, `apigateway` — each: Go binary + Dockerfile + Helm sub-chart;
umbrella Helm chart wires them + PostgreSQL (subchart or Bitnami dependency) + PVCs.

### 4.3 Performance harness (for the 3 ratios)
Drive `(producers, consumers) ∈ {(10,2),(2,10),(5,5),...}`; measure **throughput (msg/s)**,
**end-to-end latency**, **broker queue depth / consumer lag**, **CPU/mem**. Output a comparison
table + brief analysis for the README. Metrics exposed (Prometheus-style) from each component.

---

## 5. Design Questions — decision ledger

Resolved questions cite their ADR or tooling note and are not re-litigated. The **live open set is
tracked in `STATE.md`** (single source of truth for what's still open); this table is the full record.

| # | Question | Resolution |
|---|---|---|
| 1 | `{id}` semantics: UUID vs `gpu_id` vs `host:gpu_id` | **OPEN** — leaning UUID; ADR-0005 *Proposed*, confirm before freezing schema. |
| 2 | MQ transport: gRPC vs length-prefixed TCP vs HTTP/2 | **Resolved** → ADR-0004 (gRPC streaming). |
| 3 | MQ persistence depth: full segment log vs bounded WAL | **OPEN** — direction set by ADR-0001 (append-only segment log); fidelity/effort TBD at MQ build. |
| 4 | Streaming cadence: per-row interval / batch / loop | **OPEN** — impl detail; decide at streamer build. |
| 5 | Repo layout | **Resolved** → ADR-0003 (multi-module monorepo + `go.work`). |
| 6 | OpenAPI generator: `swaggo/swag` vs `oapi-codegen` | **OPEN** — decide at API-gateway build. |
| 7 | Local dev target: kind / minikube / k3d | **Resolved** → **kind** (tooling; see PROMPT_HISTORY, no ADR). |

Live open set (4): **#1** GPU id · **#3** persistence depth · **#4** cadence · **#6** OpenAPI tool — see `STATE.md`.

---

## 6. Tech Stack
- **Locked by assignment:** Go; PostgreSQL; Docker; Kubernetes; Helm.
- **Locked libs:** `pgx` (Postgres), `slog` (logging), `testify` (tests), Prometheus client (metrics).
- **Still open:** HTTP router (`chi` vs `gin`); OpenAPI generator (`swag` vs `oapi-codegen` — §5 #6).

---

## 7. Status
**Phase: MONOREPO SCAFFOLD.** Project named **vantage** (`github.com/ajitgunturi/vantage`).
Tree + `go.work` + 5 modules + MQ proto/gRPC stubs + `docs/` (ADR 0001–0005 + PROMPT_HISTORY) in
place; `mq` module compiles. Pending: service stubs, Makefile, Helm, Dockerfiles, README, then logic.
See `STATE.md` for the live checklist and `docs/adr/` for decisions.
