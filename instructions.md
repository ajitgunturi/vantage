# Role & Context
You are an expert Principal Systems Engineer and Cloud-Native Architect specializing in Go (Golang), Kubernetes, PostgreSQL, gRPC, and high-throughput distributed systems. 

You are helping me design and implement an **Elastic GPU Telemetry Pipeline with a Custom Message Queue** from scratch. The system must be production-grade, highly scalable, defensively engineered, and written in clean, idiomatic Go. 

Crucially, the architecture must be structured as **strictly independent microservices**, even if housed within a single repository for this assignment. 

---

## Approved Deviations from the Original Brief

> Owner-approved, documented departures from the brief as originally written.
> Clauses affected below are annotated inline with **[DEVIATION → ADR-NNN]**.
> The original wording is preserved; the deviation note states the current truth.

- **[ADR-001] MQ `Consume` is a *bidirectional* streaming RPC with broker-side
  at-least-once delivery** (per-message ack + client-driven credit + redelivery
  on disconnect), **not** the server-side-streaming / no-ack design originally
  specified. Triggered by a reproduced silent-message-loss defect on consumer
  disconnect. Built in Phase 01.1. In-memory / no-disk / single-replica /
  from-scratch constraints are unchanged. See
  `docs/adr/ADR-001-bidi-at-least-once-delivery.md`.
- **[Pre-existing] Opt-in WAL persistence backend** behind the `Store` interface
  (in-memory remains the default) adds crash durability in Phase 6 — a documented
  extension of the in-memory-only baseline. See `.planning/PROJECT.md` Key Decisions.

---

## High-Level System Architecture
The system consists of four independent microservices and one database running inside a Kubernetes cluster. Each microservice must have its own Dockerfile and Helm chart (or be a distinct sub-chart).

1. **Message Queue (MQ) Microservice:** Built from scratch as a standalone service. **Do NOT use any existing third-party message brokers**. It uses a hybrid networking model: high-performance gRPC for ingestion/consumption, and standard HTTP/REST for admin visibility and debugging.
2. **Streamer Microservice:** A service that reads telemetry data from a local CSV file (`dcgm_metrics_20250718_134233.csv`). It loops through the file indefinitely to simulate a continuous live metrics stream, stamping the current execution time onto each record before publishing via gRPC to the MQ.
3. **Collector Microservice:** Connects to the Custom MQ via a persistent gRPC stream. It consumes data as it arrives, parses the payload, and persists it into a PostgreSQL database. **[DEVIATION → ADR-001]** the stream is now *bidirectional*: the Collector sends credit + per-message acks back to the MQ and acks only after a durable Postgres write (end-to-end at-least-once).
4. **API Gateway Microservice:** An HTTP/REST API querying the PostgreSQL database to expose metrics. The OpenAPI specification must be completely auto-generated via code comments (e.g., using `swag`).
5. **PostgreSQL Database:** The single source of truth for the system, ensuring that horizontally scaled Collectors and API Gateways remain perfectly synchronized.

---

## Detailed Component Requirements

### 1. Message Queue Microservice (Core Engine)
- **Architecture:** Standalone microservice running as a single-replica deployment in Kubernetes.
- **Internal Storage:** Built entirely from scratch using native Go channels or thread-safe internal memory structures (e.g., ring buffers with `sync.RWMutex`). Do not implement disk persistence or complex clustering.
- **Dual-Protocol Transport Layer:**
  - **gRPC Interface (Data Plane):** Provide a unary RPC for Streamers to push data (`Produce`), and a **Bidirectional Streaming RPC** (`Consume`) for Collectors. **[DEVIATION → ADR-001]** originally specified as a *Server-Side Streaming RPC*; changed to bidi so consumers stream credit + per-message acks back to the broker.
  - **HTTP/REST Interface (Control Plane):** Provide a `GET /api/v1/queue/inspect` endpoint returning a JSON summary of the queue status for easy `curl` debugging.
- **Delivery Semantics:** Decoupled, thread-safe queueing. Multiple Collectors pulling from the streaming endpoint receive **unique messages** in steady state. **[DEVIATION → ADR-001]** delivery is now **at-least-once**: a message is removed only when the consumer acks it (by broker-assigned `uint64 id`); unacked in-flight messages are re-queued and redelivered when a consumer disconnects (duplicates possible, absorbed by the idempotent Collector). Client-driven credit bounds the in-flight window (no eager over-pull). Achieved purely in memory — **no disk persistence**.

### 2. Streamer Microservice
- **Streaming Loop:** Continuously parse the assigned CSV file line-by-line. Overwrite or append the *current system timestamp* to the record.
- **Network Client:** Push payloads to the MQ using a generated gRPC client stub.
- **Elasticity:** Support running up to 10 instances concurrently.

### 3. PostgreSQL Database Layer & Schema
- **Design:** Relational schema optimized for time-series telemetry data (`gpu_id`, `timestamp` (TIMESTAMPTZ), and numerical columns).
- **Indexing Strategy:** Create a composite index on `(gpu_id, timestamp DESC)`.

### 4. Collector Microservice
- **Ingestion:** Establish a long-lived bidirectional connection to the MQ gRPC stream. Process incoming metrics reactively and **ack each message** (by broker-assigned id) after it is durably persisted; replenish credit as acks are sent. **[DEVIATION → ADR-001]**
- **Persistence:** Utilize the `jackc/pgx/v5` driver connection pool (`pgxpool`) to handle high-concurrency batch inserts smoothly.

### 5. API Gateway Microservice
Expose an HTTP REST server querying PostgreSQL directly to fulfill these exact endpoints:
- `GET /api/v1/gpus`
- `GET /api/v1/gpus/{id}/telemetry`
- `GET /api/v1/gpus/{id}/telemetry?start_time=...&end_time=...`

---

## Technical Stack & Constraints
- **Language:** Go (Clean, idiomatic).
- **Database:** PostgreSQL (with `pgx/v5`).
- **Protocols:** gRPC (Protobuf v3) + HTTP/1.1 (JSON).
- **Deployment:** Docker containerization with multi-stage builds. Kubernetes orchestration using Helm. Each microservice must have its own deployment definition.
- **OpenAPI Spec:** Must be completely auto-generated from code annotations. 
- **Testing:** Unit tests and integration tests required with a runnable Makefile target verifying code coverage metrics (90% coverage minimum).
- **GSD Framework** Use GSD framework to build the application by breaking it down to relevant phases. 

---

## Expected Code Base Structure (Monorepo for Microservices)
Please draft the foundational repository blueprint. Treat `cmd/` as the entry points for the completely independent services, sharing only the strictly necessary protocol contracts and DB models in `pkg/`:

```text
├── api/
│   └── proto/           # Shared .proto definition files
├── build/               # Dockerfiles for each microservice
│   ├── streamer.Dockerfile
│   ├── collector.Dockerfile
│   ├── mq.Dockerfile
│   └── gateway.Dockerfile
├── cmd/
│   ├── streamer/        # Streamer Microservice main.go
│   ├── collector/       # Collector Microservice main.go
│   ├── mq/              # MQ Microservice main.go
│   └── gateway/         # API Gateway Microservice main.go
├── pkg/                 # Shared libraries (contracts, db connections)
│   ├── pb/              # Generated Go gRPC/Protobuf code
│   ├── db/              # PostgreSQL initialization and queries
│   └── models/          # Shared telemetry data structs
├── deployments/         # Helm charts 
│   ├── charts/          # Sub-charts for streamer, collector, mq, gateway, postgres
│   └── values.yaml      # Global overrides
├── Makefile             # Commands for build, proto gen, test, coverage, and swagger gen
└── README.md
```

---
## Custom Agents

Assume the roles of the following specialized sub-agents to parallelize and validate the work:

Agent 1: The Core Protocol & MQ Engineer
Focus: Define the api/proto/mq.proto schema file. Implement the standalone MQ microservice using Go channels/mutexes behind the gRPC layer and the HTTP inspect endpoint.

Agent 2: The Data & Storage Pipeline Engineer
Focus: Build the PostgreSQL schema with composite indexes. Write pkg/db using pgxpool. Build the Streamer CSV looping mechanism and the Collector reactive stream subscriber that writes to Postgres.

Agent 3: The API Gateway & Docs Engineer
Focus: Implement the API Gateway microservice handling the three GPU filtering routes. Write comprehensive code decorations to auto-generate the OpenAPI spec using swag.

Agent 4: The DevOps & QA Verification Engineer
Focus: Build Multi-stage Dockerfiles in /build, the Helm charts architecture, and the complete Makefile. Ensure all microservices build and deploy independently.

---