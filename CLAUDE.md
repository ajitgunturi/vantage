# vantage — Elastic GPU Telemetry Pipeline

Production-grade, horizontally-scalable telemetry pipeline built from scratch in idiomatic Go.
Four **strictly independent microservices** + PostgreSQL, orchestrated on Kubernetes via Helm.
Built phase-by-phase with the **GSD framework**. This file is the operational source of truth for
autonomous sessions — read it before planning or writing code.

> **Canonical spec: `instructions.md`.** Per-role briefs: `.ai/agents/*.md`. If anything conflicts
> with the spec, the spec wins — and fix the conflicting artifact.

---

## The system

| Service | Entrypoint | Role | Protocols |
|---|---|---|---|
| **MQ** | `cmd/mq/` | Custom message broker, **from scratch** — in-memory only | gRPC (data) + HTTP (control) |
| **Streamer** | `cmd/streamer/` | Loops the DCGM CSV forever, restamps `now`, publishes | gRPC client → MQ `Produce` |
| **Collector** | `cmd/collector/` | Consumes MQ server-stream, batch-inserts to Postgres | gRPC client + `pgxpool` |
| **API Gateway** | `cmd/gateway/` | Read API over Postgres; OpenAPI auto-generated | HTTP/REST + `swag` |
| **PostgreSQL** | (Helm dep) | Single source of truth; time-series schema | — |

Data flow: `CSV → Streamer →(gRPC Produce)→ MQ →(gRPC Consume stream)→ Collector → Postgres → API Gateway → client`

---

## Canonical layout (source of truth: `instructions.md`)

**Single Go module** (`github.com/ajitg/vantage`, one `go.mod` at root). Services are independent by
**directory + Dockerfile**, not by separate modules. The *only* shared code is `pkg/`.

```text
api/
  └─ proto/            # shared .proto contracts (mq.proto)
build/                 # one multi-stage Dockerfile per service
  ├─ mq.Dockerfile  streamer.Dockerfile  collector.Dockerfile  gateway.Dockerfile
cmd/                   # independent service entrypoints (thin main.go each)
  ├─ mq/  streamer/  collector/  gateway/
pkg/                   # shared libraries — the ONLY cross-service surface
  ├─ pb/               # generated gRPC/protobuf Go code (from api/proto)
  ├─ db/               # Postgres init, pgxpool, queries
  └─ models/           # shared telemetry structs
deployments/           # Helm: charts/ (sub-chart per service + postgres) + values.yaml
Makefile               # proto, build, test, coverage, swagger, docker, k8s
README.md
go.mod                 # single module root
dcgm_metrics_*.csv     # input data (gitignored — kept local, not committed)
```

**Module discipline (enforce by convention — this is the independence guarantee):**
- A service in `cmd/<svc>` may import `pkg/*` and its own internal helpers — **never another
  service's code**. Cross-service contracts flow through `pkg/pb` (proto) only.
- One `go.mod`, so `go build ./...` / `go test ./...` work directly — no workspace, no `replace`.

---

## Build / test / verify — drive everything through the Makefile

```
make help        # list targets
make tools       # protoc plugins, swag, golangci-lint, kind
make proto       # compile api/proto/*.proto → pkg/pb
make build       # build all four service binaries into bin/
make test        # go test -race -cover ./...
make coverage    # GATE: enforce >= 90% line coverage
make swagger     # auto-generate OpenAPI from gateway annotations (swag init)
make lint        # golangci-lint (fallback go vet)
make docker      # build all service images from build/*.Dockerfile
make kind-up / helm-install / kind-down   # local k8s lifecycle
```

**"Done" means the gates pass, not just that it compiles.** Before claiming a unit of work complete:
`make build && make test && make coverage && make lint`.

---

## Hard constraints — non-negotiable, enforce in every phase

- **MQ is built from scratch.** Native Go only — channels + `sync.RWMutex`/ring buffers.
  **No third-party brokers** (Kafka/NATS/Rabbit/Redis/etc.). **No clustering.** MQ runs as a
  **single-replica** Deployment. Storage sits behind a `Store` interface: **in-memory is the
  default**; an **opt-in WAL persistence backend** (batched group-commit fsync + replay-on-restart,
  at-least-once) adds crash durability without changing the default — built in Phase 6. (This is a
  deliberate, documented extension of the brief's in-memory-only baseline; see PROJECT.md Key Decisions.)
- MQ delivery is **decoupled and thread-safe**; multiple Collectors must receive **unique** messages
  (no duplication, no leaks). Prove it with `go test -race` concurrency tests before wiring network.
- **OpenAPI is fully auto-generated** from `swag` code annotations — never hand-write the spec.
- DB schema is time-series shaped (`gpu_id`, `timestamp TIMESTAMPTZ`, numeric cols) with a
  **composite index `(gpu_id, timestamp DESC)`** explicitly in the DDL.
- Collector persistence uses **`jackc/pgx/v5` `pgxpool`** with concurrent batch inserts.
- Coverage floor: **≥90% line** across the module.
- Every service: own Dockerfile (multi-stage, distroless/alpine) + own Helm sub-chart; each builds &
  deploys independently.
- Streamer must support up to **10 concurrent instances**; restamp each record with current exec time.

API Gateway endpoints (exact):
`GET /api/v1/gpus` · `GET /api/v1/gpus/{id}/telemetry` · `…/telemetry?start_time=…&end_time=…`
MQ endpoints: gRPC `Produce` (unary), `Consume` (server-stream) · HTTP `GET /api/v1/queue/inspect`.

---

## Agent roles & directory ownership

Sessions act as one of four specialists (briefs in `.ai/agents/`). Stay inside your directories;
treat `api/proto` contracts as the only shared surface and change them deliberately.

| Agent | Owns | Brief |
|---|---|---|
| **MQ Engineer** | `api/proto/`, `cmd/mq/` | `.ai/agents/mq_engineer.md` |
| **Storage/Pipeline Engineer** | `cmd/streamer/`, `cmd/collector/`, `pkg/db/`, `pkg/models/` | `.ai/agents/storage_engineer.md` |
| **Gateway/Docs Engineer** | `cmd/gateway/` (+ swag) | `.ai/agents/gateway_engineer.md` |
| **DevOps/QA Engineer** | `build/`, `deployments/`, `Makefile`, `README.md` | `.ai/agents/devops_qa.md` |

To parallelize independent work, dispatch these as concurrent subagents (e.g. via the Agent tool /
a Workflow), one per directory boundary, then integrate at the proto contract.

---

## How to run a session autonomously

1. **Orient first.** Read this file + `instructions.md` + the relevant `.ai/agents/*.md`. Check GSD
   state in `.planning/` (STATE/ROADMAP/PHASE) before asking "what's next?". This is a GSD project —
   use the `gsd-*` skills (`/gsd-progress`, `/gsd-plan-phase`, `/gsd-execute-phase`,
   `/gsd-verify-phase`). If `.planning/` doesn't exist yet, bootstrap it (`/gsd-new-project`).
2. **TDD-first.** Write race/concurrency and table tests before the implementation, especially for
   MQ internals and the CSV parser. Tests are the spec.
3. **Implement to spec**, respecting directory ownership and the hard constraints above.
4. **Verify and gate.** Run `make build test coverage lint` — fix failures autonomously; read the
   output, don't just re-run.
5. **Commit** with Conventional Commits (`feat(mq): …`, `test(collector): …`). Atomic commits per
   landed unit. (Global rule: no `Co-Authored-By: Claude` trailer.)
6. **Update GSD state** when a commit lands / a gate passes / the micro-task changes. One source of
   truth — don't duplicate status across files.

Keep the console terse: write plans/specs into GSD docs and code into files, print brief confirmations.
