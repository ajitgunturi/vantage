# Phase 5: DevOps + Quality Gates — Research

**Researched:** 2026-07-02
**Domain:** Docker multi-stage builds, Helm umbrella charts, kind local Kubernetes, testcontainers-go compose, Makefile gates
**Confidence:** HIGH (codebase verified; stack pinned in CLAUDE.md + go.mod; environment probed)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Helm topology & independence (OPS-02, OPS-03)**
- D-01 — Bitnami PostgreSQL via OCI. Declare `bitnami/postgresql` (chart 18.7.8) as a `Chart.yaml` dependency pulled from `oci://registry-1.docker.io/bitnamicharts`. No in-repo postgres chart.
- D-02 — One umbrella release with per-service flags. Single release (`helm upgrade --install vantage deployments`). Every service sub-chart exposes `enabled` and `image.tag` values. OPS-03 independence demonstrated by `helm upgrade --reuse-values --set <svc>.image.tag=X`.
- D-03 — Self-contained sub-charts. Each service sub-chart owns its full templates — no shared library chart.
- D-04 — MQ single replica + `strategy: Recreate` (locked; non-negotiable).

**Image build & kind workflow (OPS-01, OPS-03)**
- D-05 — `kind load docker-image` moves locally built images into the cluster (new `make kind-load` target). No local registry.
- D-06 — Fixed `:dev` tag + `imagePullPolicy: IfNotPresent`. User choice over `Never`.
- D-07 — `make deploy` composite target = docker → kind-load → helm-install. `kind-up` stays separate.
- D-08 — Multi-stage Dockerfiles: builder `golang:1.26-alpine`, final `gcr.io/distroless/static-debian12`, `CGO_ENABLED=0`.

**Schema migration in-cluster (Phase 2 D-06 forward)**
- D-09 — Helm pre-install/pre-upgrade hook Job runs migrations before service pods roll.
- D-10 — Dedicated migrate image `build/migrate.Dockerfile` → `vantage/migrate:dev` (5th image in `make docker` / `kind-load`). Service images stay single-binary.
- D-11 — Umbrella chart owns the migration Job (schema is a shared concern).

**Smoke & soak scope**
- D-12 — `make smoke-05` proves full E2E through kind: helm install → migration Job complete → all pods Running → streamer publishing → port-forward gateway → `curl /api/v1/gpus` and `/telemetry` return real rows.
- D-13 — Smoke assumes cluster + deploy already exist (`make kind-up deploy` run beforehand); fails fast with a clear message otherwise.
- D-14 — Dedicated `make soak` target, separate from smoke: configurable duration and streamer replica count.

**Live-infrastructure test harness**
- D-15 — `make test-harness` runs a separate e2e suite against live infrastructure. Distinct from `make test`, `make smoke`, and coverage gate.
- D-16 — Compose first, kind-pointable. Default target is docker-compose full stack. Same suite is pointable at a kind/Helm deployment via env-provided base URLs.
- D-17 — Testcontainers-driven Go suite. Go tests (separate package, `e2e` build tag) use `testcontainers-go/modules/compose` to own the stack lifecycle programmatically. Rancher Desktop — Ryuk must stay disabled; compose module pinned to same version as testcontainers-go core (v0.43.0).
- D-18 — Hermetic lifecycle: up → test → down, teardown even on failure; `KEEP=1` escape hatch.
- D-19 — Assertions: pipeline correctness E2E across real container boundaries — row counts grow, gateway endpoints return data consistent with Postgres, MQ inspect counters reconcile.

### Claude's Discretion
- Coverage-gate mechanics for any new Go packages this phase adds (e2e suite must be excluded like `pkg/pb`/`pkg/docs`); whether `cmd/` stays outside the gate as today.
- Resource requests/limits, liveness/readiness probe details, Service types/ports in the charts.
- Helm chart linting (`helm lint`) and any chart-testing tooling.
- Exact soak defaults (duration, replica count) and env override names.
- Port-forward vs NodePort mechanics inside smoke-05.

### Deferred Ideas (OUT OF SCOPE)
- Kill/restart resilience scenarios in the e2e harness — deferred to Phase 6 (WAL crash durability, QA-05).
- Local registry for kind — revisit only if `kind load` becomes a bottleneck.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| OPS-01 | Multi-stage Dockerfile per service (mq, streamer, collector, gateway) | D-08: golang:1.26-alpine builder + distroless final; CGO_ENABLED=0; section "Dockerfile Patterns" |
| OPS-02 | Helm chart with sub-chart per microservice plus PostgreSQL dependency | D-01/D-02/D-03: umbrella chart + 4 self-contained sub-charts + Bitnami OCI dep; section "Helm Structure" |
| OPS-03 | Each microservice builds and deploys independently | D-02: `enabled` + `image.tag` per sub-chart; `helm upgrade --reuse-values --set <svc>.image.tag=X`; section "Independent Deploy" |
| OPS-04 | MQ deploys as single replica with `strategy: Recreate` | D-04: hardcoded in mq sub-chart Deployment; section "MQ Deployment" |
| OPS-05 | Makefile targets for proto, build, test, coverage, swagger | Already present; this phase adds kind-load, deploy, soak, test-harness targets; section "Makefile Targets" |
| QA-01 | Unit tests across all services | Current coverage 90.3%; verify `make test` reaches all internal/ packages; section "Coverage Gate" |
| QA-04 | ≥90% line coverage enforced via Makefile coverage gate | Coverage gate already works; new packages must not dilute it; section "Coverage Gate" |
</phase_requirements>

---

## Summary

Phase 5 completes the operational layer of the vantage pipeline: Docker images, Kubernetes/Helm deployment, and a closed quality loop. The codebase is fully in place — all five binaries (`mq`, `streamer`, `collector`, `gateway`, `migrate`) exist and pass `make test && make coverage`. This phase is almost entirely **new file creation** in `build/` and `deployments/` (neither directory exists yet), plus Makefile extension and a new `test/harness/` Go package.

The critical constraint is the environment: kind v0.32.0 is installed at `~/go/bin/kind` (not on PATH) and requires `DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock` for Rancher Desktop. The Makefile must export these or the `kind-up` target silently fails. Helm 4.0.5 is on PATH and fully supports `apiVersion: v2` charts with OCI dependencies — no extra env vars needed. The `testcontainers-go/modules/compose` package (v0.43.0) needs to be added to go.mod as a direct dependency.

The coverage gate already enforces ≥90% on `./internal/... ./pkg/...` excluding `pkg/pb` and `pkg/docs`. The new `test/harness/` package lives outside that scope and needs an explicit `//go:build e2e` tag to be excluded from `make test` as well.

**Primary recommendation:** Build in this order: (1) Dockerfiles, (2) Makefile additions + `kind-load` plumbing, (3) Helm umbrella + sub-charts + migration hook, (4) smoke-05, (5) soak, (6) test-harness. Keep the migration hook and the Helm dependency update as separate wave items — `helm dependency update` must run before the first `helm-install`.

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| OCI image builds | Build toolchain (Dockerfile + docker CLI) | Makefile orchestration | Go binaries compile statically; distroless final stage minimises CVE surface |
| Cluster lifecycle (kind) | Local machine shell (make targets) | — | kind runs clusters inside Docker containers; no hypervisor |
| Service deployment | Kubernetes / Helm (chart templates) | Makefile (helm-install target) | Helm manages lifecycle, rollback, and per-service `enabled` flags |
| Schema migration | Kubernetes Job (Helm hook) | pkg/db.Migrate embedded (fallback on startup) | Hook guarantees migration before pod roll; services' startup migration is idempotent defence-in-depth |
| E2E smoke validation | Shell script (scripts/smoke/phase05-kind.sh) | kubectl port-forward | Smoke tests human-readable assertions against running cluster |
| Soak test | Shell script (make soak) | kubectl scale | Configurable duration and replica count; asserts sustained throughput |
| Programmatic harness | Go test (test/harness/, e2e build tag) | testcontainers-go/modules/compose | Owns full compose stack lifecycle; pointable at kind cluster via env |
| Coverage gate | Makefile + go test -covermode=atomic | go tool cover | Already enforced at 90.3%; must remain above 90% after this phase |

---

## Standard Stack

All packages listed below are already pinned in `go.mod`. No new runtime dependencies are required. One new test dependency must be added.

### Core (already in go.mod)
| Library | Version | Purpose | Source |
|---------|---------|---------|--------|
| github.com/testcontainers/testcontainers-go | v0.43.0 | Compose stack lifecycle for test-harness | [VERIFIED: go.mod] |
| github.com/testcontainers/testcontainers-go/modules/postgres | v0.43.0 | Already used by existing e2e tests | [VERIFIED: go.mod] |

### New Dependency (must be added to go.mod)
| Library | Version | Purpose | Source |
|---------|---------|---------|--------|
| github.com/testcontainers/testcontainers-go/modules/compose | v0.43.0 | Docker Compose stack lifecycle for D-17 test harness | [CITED: pkg.go.dev/github.com/testcontainers/testcontainers-go/modules/compose] |

**Installation:**
```bash
go get github.com/testcontainers/testcontainers-go/modules/compose@v0.43.0
```

Pin to the same version as `testcontainers-go` core (v0.43.0) — the modules share a release cycle.

### Dev/Build Tooling (already installed, not in go.mod)
| Tool | Version | Location | Status |
|------|---------|---------|--------|
| kind | v0.32.0 | `~/go/bin/kind` | [VERIFIED: environment probe] — NOT on PATH |
| helm | v4.0.5 | `/Users/ajitg/.rd/bin/helm` (Rancher Desktop) | [VERIFIED: environment probe] |
| docker | v29.1.4-rd | `/Users/ajitg/.rd/bin/docker` | [VERIFIED: environment probe] |
| kubectl | v1.35.1 | `/Users/ajitg/.rd/bin/kubectl` | [VERIFIED: environment probe] |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| kind load docker-image | local registry (registry:2) | Local registry requires extra setup; `kind load` is simpler for dev — deferred per D-05 |
| distroless final stage | alpine:3 | Alpine has more CVE surface and includes a shell; distroless enforced per D-08 / CLAUDE.md |
| imagePullPolicy: Never | imagePullPolicy: IfNotPresent | `Never` is safer (fails loudly if image not loaded) but user chose `IfNotPresent` per D-06 |

---

## Package Legitimacy Audit

No new external packages are introduced by this phase in the runtime dependency graph. The one new test dependency is the `testcontainers-go/modules/compose` module — part of the official testcontainers-go project (same GitHub org, same release cadence as `v0.43.0` already in go.mod).

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| testcontainers-go/modules/compose | pkg.go.dev | ~3 yrs | High | github.com/testcontainers/testcontainers-go | OK | Approved — official sub-module of the already-pinned testcontainers-go v0.43.0 |

**Packages removed due to SLOP verdict:** none
**Packages flagged as suspicious SUS:** none

---

## Architecture Patterns

### System Architecture Diagram

```
[ Makefile ]
     |
     |-- make docker ──────────────> [ docker build ]
     |                                     |
     |                               build/mq.Dockerfile        → vantage/mq:dev
     |                               build/streamer.Dockerfile  → vantage/streamer:dev
     |                               build/collector.Dockerfile → vantage/collector:dev
     |                               build/gateway.Dockerfile   → vantage/gateway:dev
     |                               build/migrate.Dockerfile   → vantage/migrate:dev
     |
     |-- make kind-load ───────────> [ kind load docker-image vantage/<svc>:dev ]
     |                                     (x5, into vantage cluster)
     |
     |-- make helm-install ────────> [ helm upgrade --install vantage deployments/ ]
     |                                     |
     |                         deployments/Chart.yaml (umbrella)
     |                         deployments/values.yaml
     |                         deployments/templates/migrate-job.yaml  ← Helm hook (pre-install)
     |                         deployments/charts/mq/        ← Service + Deployment (replicas:1, Recreate)
     |                         deployments/charts/streamer/  ← Deployment
     |                         deployments/charts/collector/ ← Deployment
     |                         deployments/charts/gateway/   ← Deployment + Service (port 8080)
     |                         [bitnami/postgresql OCI dep]  ← StatefulSet
     |
     |-- make smoke-05 ────────────> [ scripts/smoke/phase05-kind.sh ]
     |                                     |
     |                               kubectl get pods --all-running?
     |                               kubectl port-forward svc/vantage-gateway 8081:8080 &
     |                               curl /api/v1/gpus → 200 + rows
     |                               curl /api/v1/gpus/{id}/telemetry → 200 + rows
     |
     |-- make soak ────────────────> [ scripts/soak.sh ]
     |                               kubectl scale deployment/vantage-streamer --replicas=$SOAK_STREAMERS
     |                               poll MQ inspect depth + Postgres row count growth
     |
     └── make test-harness ────────> [ go test -tags=e2e ./test/harness/... ]
                                           |
                                     ComposeStack.Up (full 5-image stack)
                                     HTTP assertions against gateway
                                     MQ inspect assertions
                                     ComposeStack.Down (even on failure)
```

### Recommended Project Structure

```
build/
├── mq.Dockerfile         # builder: golang:1.26-alpine; final: distroless
├── streamer.Dockerfile
├── collector.Dockerfile
├── gateway.Dockerfile
└── migrate.Dockerfile    # 5th image; wraps cmd/migrate binary

deployments/
├── Chart.yaml            # umbrella chart; Bitnami postgres OCI dependency
├── values.yaml           # all sub-chart defaults + postgres config
├── charts/
│   ├── mq/
│   │   ├── Chart.yaml
│   │   └── templates/
│   │       ├── deployment.yaml  # replicas: 1, strategy: Recreate
│   │       └── service.yaml     # ClusterIP ports 50051 (gRPC) + 8080 (HTTP)
│   ├── streamer/
│   │   ├── Chart.yaml
│   │   └── templates/
│   │       └── deployment.yaml  # STREAMER_MQ_ADDR, STREAMER_CSV_PATH
│   ├── collector/
│   │   ├── Chart.yaml
│   │   └── templates/
│   │       └── deployment.yaml  # COLLECTOR_MQ_ADDR, VANTAGE_DB_DSN
│   └── gateway/
│       ├── Chart.yaml
│       └── templates/
│           ├── deployment.yaml  # GATEWAY_ADDR, VANTAGE_DB_DSN
│           └── service.yaml     # ClusterIP port 8080
└── templates/
    └── migrate-job.yaml         # Helm pre-install/pre-upgrade hook Job

scripts/smoke/
└── phase05-kind.sh              # smoke against running kind cluster

test/harness/
└── harness_test.go              # //go:build e2e; testcontainers compose
```

### Pattern 1: Multi-Stage Dockerfile (OPS-01, D-08)

**What:** Two-stage build — full Go toolchain in builder, distroless scratch in final. Service binary is the only artifact.
**When to use:** All five service images. CGO_ENABLED=0 ensures the static binary runs in distroless with no libc dependency.

```dockerfile
# Source: CLAUDE.md stack pattern, .claude/CLAUDE.md "Stack Patterns"
# build/mq.Dockerfile  (pattern shared by all 4 service Dockerfiles)

# ── Stage 1: builder ────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder
WORKDIR /src
# Copy dependency manifests first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree (all services share one module root)
COPY . .

# Build the target service binary. CGO_ENABLED=0 = static binary, no libc.
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/mq ./cmd/mq

# ── Stage 2: final ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS final
COPY --from=builder /out/mq /mq
EXPOSE 50051 8080
ENTRYPOINT ["/mq"]
```

For `migrate.Dockerfile` replace the build target with `./cmd/migrate` and expose no ports.
For `gateway.Dockerfile` add COPY of `pkg/docs/` if the binary embeds swagger via `go:embed`.

> Note: `pkg/docs/docs.go` is imported by `cmd/gateway/main.go` via `_ "github.com/ajitg/vantage/pkg/docs"` — the docs are Go source included in the build, not a separate embed. No additional COPY needed.

### Pattern 2: Helm Umbrella Chart with Self-Contained Sub-charts (OPS-02, D-01–D-03)

**What:** Top-level `deployments/Chart.yaml` declares the Bitnami PostgreSQL OCI dependency. Each service lives in `deployments/charts/<svc>/` and is completely self-contained (no shared templates).
**When to use:** All microservice deployments. `enabled` flag allows deploying subsets.

```yaml
# deployments/Chart.yaml
# Source: CLAUDE.md (chart 18.7.8, OCI from oci://registry-1.docker.io/bitnamicharts)
apiVersion: v2
name: vantage
description: Elastic GPU Telemetry Pipeline — umbrella chart
type: application
version: 0.1.0
appVersion: "dev"

dependencies:
  - name: postgresql
    version: "18.7.8"
    repository: "oci://registry-1.docker.io/bitnamicharts"
    condition: postgresql.enabled
```

After creating this file: `helm dependency update deployments/` generates `Chart.lock` and downloads the Bitnami chart tgz into `deployments/charts/`. This must run before the first `helm-install`.

```yaml
# deployments/values.yaml (top-level defaults)
mq:
  enabled: true
  image:
    repository: vantage/mq
    tag: dev
    pullPolicy: IfNotPresent  # D-06

streamer:
  enabled: true
  image:
    repository: vantage/streamer
    tag: dev
    pullPolicy: IfNotPresent

collector:
  enabled: true
  image:
    repository: vantage/collector
    tag: dev
    pullPolicy: IfNotPresent

gateway:
  enabled: true
  image:
    repository: vantage/gateway
    tag: dev
    pullPolicy: IfNotPresent

postgresql:
  enabled: true
  auth:
    username: vantage
    password: vantage
    database: vantage

migrate:
  image:
    repository: vantage/migrate
    tag: dev
```

### Pattern 3: MQ Deployment — Single Replica + Recreate (OPS-04, D-04)

**What:** The MQ Deployment template hard-codes `replicas: 1` and `strategy.type: Recreate`. These must NOT be templated as values — the MQ is an in-memory broker and multi-replica split-brain is undefined behaviour.

```yaml
# deployments/charts/mq/templates/deployment.yaml (critical section)
# Source: CONTEXT.md D-04, ADR-001 (single-replica requirement)
spec:
  replicas: 1                       # LOCKED: in-memory broker; no rolling split-brain
  strategy:
    type: Recreate                  # LOCKED: new pod only starts after old terminates
  selector:
    matchLabels:
      app.kubernetes.io/name: mq
  template:
    spec:
      containers:
        - name: mq
          image: "{{ .Values.mq.image.repository }}:{{ .Values.mq.image.tag }}"
          imagePullPolicy: {{ .Values.mq.image.pullPolicy }}
          ports:
            - containerPort: 50051   # gRPC (MQ_GRPC_ADDR)
            - containerPort: 8080    # HTTP inspect (MQ_HTTP_ADDR)
          env:
            - name: MQ_GRPC_ADDR
              value: ":50051"
            - name: MQ_HTTP_ADDR
              value: ":8080"
            - name: MQ_BUFFER_SIZE
              value: "10000"
            - name: MQ_CONSUME_CREDIT
              value: "20"
```

### Pattern 4: Migration Hook Job (D-09, D-10, D-11)

**What:** A Kubernetes Job with `helm.sh/hook: pre-install,pre-upgrade` annotation runs `vantage/migrate:dev` to completion before any service pods roll.

```yaml
# deployments/templates/migrate-job.yaml
# Source: Helm docs on hooks; CONTEXT.md D-09/D-11
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ .Release.Name }}-migrate
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "-5"
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
spec:
  template:
    spec:
      restartPolicy: Never          # Job fails loudly; don't restart on migration error
      containers:
        - name: migrate
          image: "{{ .Values.migrate.image.repository }}:{{ .Values.migrate.image.tag }}"
          imagePullPolicy: IfNotPresent
          env:
            - name: VANTAGE_DB_DSN
              value: "postgres://{{ .Values.postgresql.auth.username }}:{{ .Values.postgresql.auth.password }}@{{ .Release.Name }}-postgresql:5432/{{ .Values.postgresql.auth.database }}?sslmode=disable"
```

> The Bitnami PostgreSQL Service name is `{{ .Release.Name }}-postgresql` by default. Verify with `helm template vantage deployments | grep "kind: Service"` after `helm dependency update`.

### Pattern 5: Makefile — kind PATH and Docker Socket

**What:** kind v0.32.0 is installed at `~/go/bin/kind` (not on PATH). The Makefile must export the binary path and DOCKER_HOST for Rancher Desktop.

```makefile
# Prepend ~/go/bin so kind is found regardless of shell PATH
export PATH := $(HOME)/go/bin:$(PATH)
# Rancher Desktop docker socket (required for kind create cluster on macOS)
export DOCKER_HOST := unix:///Users/ajitg/.rd/docker.sock
# testcontainers/ryuk safety (inherited from project convention)
export TESTCONTAINERS_RYUK_DISABLED := true
```

Add these three exports near the top of the Makefile (after the SERVICES/COVERAGE_THRESHOLD vars).

### Pattern 6: kind-load Target (D-05, D-07)

```makefile
# DOCKER_IMAGES includes migrate as a 5th image (D-10).
DOCKER_IMAGES := $(SERVICES) migrate

kind-load: ## Load all service images into the kind cluster
	@for img in $(DOCKER_IMAGES); do \
		echo "== kind load $$img =="; \
		kind load docker-image vantage/$$img:dev --name vantage; \
	done

deploy: docker kind-load helm-install ## Full deploy cycle: build images → load → helm install
```

> `make docker` uses the existing pattern rule `docker-%` which builds `vantage/$*:dev` from `build/$*.Dockerfile`. Adding `migrate` to `DOCKER_IMAGES` (not to `SERVICES`) keeps the `make build` binary gate unchanged (build only builds service binaries, not the migrate binary which has its own Dockerfile stage).

### Pattern 7: testcontainers-go Compose Module (D-17)

```go
// test/harness/harness_test.go
// Source: pkg.go.dev/github.com/testcontainers/testcontainers-go/modules/compose
//go:build e2e

package harness_test

import (
    "context"
    "testing"

    "github.com/testcontainers/testcontainers-go/modules/compose"
    "github.com/testcontainers/testcontainers-go/wait"
)

func TestHarness(t *testing.T) {
    ctx := context.Background()

    stack, err := compose.NewDockerComposeWith(
        compose.WithStackFiles("../../docker-compose.full.yml"),
        compose.StackIdentifier("vantage-harness"),
    )
    if err != nil {
        t.Fatalf("compose.NewDockerComposeWith: %v", err)
    }

    t.Cleanup(func() {
        if err := stack.Down(ctx,
            compose.RemoveOrphans(true),
            compose.RemoveVolumes(true),
        ); err != nil {
            t.Logf("stack.Down: %v", err)
        }
    })

    err = stack.
        WaitForService("gateway", wait.ForListeningPort("8080/tcp")).
        Up(ctx, compose.Wait(true))
    if err != nil {
        t.Fatalf("stack.Up: %v", err)
    }

    // Assertion: row counts grow, gateway returns data
    // ...
}
```

### Pattern 8: Service-to-Service DNS in kind (Kubernetes ClusterIP)

Within the kind cluster, services discover each other via Kubernetes DNS. The umbrella release name is `vantage`, so:

| Service | DNS name within cluster |
|---------|------------------------|
| MQ gRPC | `vantage-mq:50051` |
| MQ HTTP inspect | `vantage-mq:8080` |
| Postgres | `vantage-postgresql:5432` |
| Gateway HTTP | `vantage-gateway:8080` |

Env vars in Helm templates:
- Streamer: `STREAMER_MQ_ADDR=vantage-mq:50051`
- Collector: `COLLECTOR_MQ_ADDR=vantage-mq:50051`, `VANTAGE_DB_DSN=postgres://vantage:vantage@vantage-postgresql:5432/vantage?sslmode=disable`
- Gateway: `GATEWAY_ADDR=:8080`, `VANTAGE_DB_DSN=postgres://vantage:vantage@vantage-postgresql:5432/vantage?sslmode=disable`
- Migrate job: same DSN as collector/gateway

> Streamer requires a DCGM CSV file at runtime. In kind, this must be mounted via a ConfigMap or HostPath volume. For smoke/soak, a short fixture CSV mounted as a ConfigMap is practical. The streamer deployment needs a `STREAMER_CSV_PATH` env var pointing to the mounted path.

### Anti-Patterns to Avoid

- **Putting replicas or strategy in values.yaml for MQ:** The MQ deployment must never allow `replicas: 2`. Hard-code these in the MQ sub-chart Deployment template. Only `image.tag` and `enabled` belong in values.
- **Running `helm-install` before `helm dependency update`:** The Bitnami PostgreSQL chart must be pulled into `deployments/charts/` via `helm dependency update deployments/` before the first install. Add `dependency-update` as a prerequisite of `helm-install`.
- **Including `test/harness` in `make test`:** The harness uses Docker Compose and must not run in unit test context. Enforce with `//go:build e2e` tag and a separate `make test-harness` target.
- **Referencing `cmd/migrate` output in service Dockerfiles:** Each Dockerfile builds exactly one binary. The migrate Dockerfile builds only `./cmd/migrate`.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Helm PostgreSQL in-cluster | Write your own PostgreSQL StatefulSet | Bitnami chart v18.7.8 via OCI | Bitnami handles PVC, health checks, secrets, and service naming correctly |
| Docker compose stack lifecycle in tests | Manually `docker compose up/down` via exec.Command | `testcontainers-go/modules/compose` v0.43.0 | compose module handles port mapping, wait strategies, teardown on failure, and Ryuk-disabled mode |
| Service-level migration bootstrap | Run migrations in each service pod via init-container | Helm pre-install hook Job wrapping existing `cmd/migrate` binary | Hook runs once per release operation; `cmd/migrate` is already idempotent |
| Kubernetes health probes | Custom HTTP server on a side port | Liveness/readiness probes on the existing HTTP ports each service already exposes | MQ: `GET /api/v1/queue/inspect` on :8080; Gateway: `GET /api/v1/gpus` on :8080 |

**Key insight:** The `cmd/migrate` binary was already designed for the k8s init-job use case (documented in the file header). Do not re-implement migration logic — wrap the existing binary in a 5th Dockerfile.

---

## Common Pitfalls

### Pitfall 1: kind Not on PATH
**What goes wrong:** `make kind-up` returns `kind: command not found`. kind is installed at `~/go/bin/kind` by `go install sigs.k8s.io/kind@latest`, which is not on the shell PATH that make inherits.
**Why it happens:** Go installs binaries to `$GOPATH/bin` (defaults to `~/go/bin`); the Makefile inherits the shell PATH which may not include this directory.
**How to avoid:** Add `export PATH := $(HOME)/go/bin:$(PATH)` at the top of the Makefile. The `tools` target already installs kind there; this ensures all make targets find it.
**Warning signs:** `kind: command not found` or `kind: No such file or directory` from any make target.

### Pitfall 2: DOCKER_HOST Not Set for kind on Rancher Desktop
**What goes wrong:** `kind create cluster --name vantage` fails with "Cannot connect to the Docker daemon at unix:///var/run/docker.sock" because Rancher Desktop uses `unix:///Users/ajitg/.rd/docker.sock`.
**Why it happens:** kind defaults to the Docker socket at `/var/run/docker.sock`. Rancher Desktop's socket is at a user-specific path. The project already exports `DOCKER_HOST` for testcontainers but not for make targets.
**How to avoid:** Add `export DOCKER_HOST := unix:///Users/ajitg/.rd/docker.sock` to the Makefile (near the kind-up target or globally). Also required: `TESTCONTAINERS_RYUK_DISABLED=true` for the test-harness target.
**Warning signs:** Connection refused to `/var/run/docker.sock`; `kind create cluster` hangs at "Preparing nodes".

### Pitfall 3: helm dependency update Required Before First Install
**What goes wrong:** `helm upgrade --install vantage deployments` fails with "found in Chart.yaml, but missing in charts/ directory: postgresql".
**Why it happens:** The Bitnami OCI dependency is declared in Chart.yaml but must be fetched into `deployments/charts/postgresql-18.7.8.tgz` by running `helm dependency update deployments/`.
**How to avoid:** Add `helm dependency update deployments/` as a prerequisite step in the `helm-install` Makefile target (or document it as a one-time setup step in the README).
**Warning signs:** Helm install fails with "missing in charts/ directory".

### Pitfall 4: Bitnami Service Name in DSN
**What goes wrong:** The migrate Job or service pods cannot connect to Postgres; DSN hostnames like `postgres:5432` fail.
**Why it happens:** Bitnami PostgreSQL chart creates a Service named `<release-name>-postgresql`. With `helm upgrade --install vantage deployments`, the service is `vantage-postgresql`. Any hardcoded `postgres` or `localhost` DSN fails.
**How to avoid:** Build the DSN in Helm templates using `{{ .Release.Name }}-postgresql`. After `helm dependency update`, verify the service name with `helm template vantage deployments | grep "kind: Service" -A3`.
**Warning signs:** Services crash with "db: ping: connection refused" or "db: VANTAGE_DB_DSN is required" if the env var template is incorrect.

### Pitfall 5: Streamer Needs CSV File in kind
**What goes wrong:** Streamer pod crashes with "STREAMER_CSV_PATH: no such file or directory" because the DCGM CSV is gitignored and not available in the container image.
**Why it happens:** The DCGM CSV is excluded from the repo (gitignored). The streamer image cannot include it. In kind, there is no host path accessible by default.
**How to avoid:** Create a minimal fixture CSV as a Kubernetes ConfigMap in the Helm chart (or as a separate kubectl apply step in the deployment workflow). Mount the ConfigMap as a volume at `/data/dcgm_metrics.csv` and set `STREAMER_CSV_PATH=/data/dcgm_metrics.csv` in the deployment env. For smoke/soak, a 10-row fixture CSV is sufficient.
**Warning signs:** Streamer pod in `CrashLoopBackOff` with logs showing CSV path error.

### Pitfall 6: Coverage Gate Dilution
**What goes wrong:** `make coverage` drops below 90% after adding the test-harness package.
**Why it happens:** If the harness package is included in the gate's package list (`./internal/... ./pkg/...`) or if `make test` pulls in e2e-tagged files, coverage drops.
**How to avoid:** (1) Place harness in `test/harness/` — outside `./internal/...` and `./pkg/...`, so the coverage gate command naturally excludes it. (2) Always use `//go:build e2e` in harness files so `go test ./...` skips them without the tag. (3) `make test-harness` uses `-tags=e2e` and is separate from `make coverage`.
**Warning signs:** `make coverage` shows new packages like `test/harness` in coverage output; percentage drops.

### Pitfall 7: Helm 4 Behavior Differences
**What goes wrong:** Resource waiting behavior changed in Helm 4 — resources that fail now cause hooks to exit immediately rather than timing out.
**Why it happens:** Helm 4.0.5 is installed. The migration hook Job uses `restartPolicy: Never`; if the database isn't ready, the Job fails immediately, and Helm 4 exits the hook rather than retrying for the timeout period.
**How to avoid:** The migration Job should have an init-container or a brief wait loop that polls the Postgres service before running migrations. Alternatively, accept that the migration hook may fail on first install if Postgres is still starting, and rely on Helm's auto-retry or a manual `helm upgrade` re-run. Add an `initContainers` stanza to the migration Job that waits for the postgres Service to accept connections.
**Warning signs:** `helm upgrade --install` fails at migration hook with "Job failed" even though Postgres eventually starts.

### Pitfall 8: Gateway Port Conflict in smoke-05
**What goes wrong:** `kubectl port-forward svc/vantage-gateway 8080:8080` conflicts with the MQ HTTP port (also :8080) if both are forwarded simultaneously to the same local port.
**Why it happens:** MQ exposes HTTP on :8080 (inspect endpoint) and gateway also exposes HTTP on :8080. They are different services in the cluster, but port-forwarding both to local :8080 causes a bind conflict.
**How to avoid:** In smoke-05, port-forward the gateway to a different local port: `kubectl port-forward svc/vantage-gateway 8081:8080`. Use `http://localhost:8081` for gateway assertions and `http://localhost:8082` for MQ inspect if needed.
**Warning signs:** `bind: address already in use` when the smoke script starts the second port-forward.

---

## Code Examples

### Complete Env Variable Map — Helm Template Reference

All env vars come from existing config packages (verified by reading source):

```
MQ service:
  MQ_GRPC_ADDR       default :50051    (internal/config/config.go)
  MQ_HTTP_ADDR       default :8080     (internal/config/config.go)
  MQ_BUFFER_SIZE     default 10000     (internal/config/config.go)
  MQ_CONSUME_CREDIT  default 20        (internal/config/config.go)

Streamer service:
  STREAMER_MQ_ADDR       default :50051          (internal/streamer/config.go)
  STREAMER_CSV_PATH      REQUIRED (no default)   (internal/streamer/config.go)
  STREAMER_LOOP_DELAY_MS default 1               (internal/streamer/config.go)

Collector service:
  COLLECTOR_MQ_ADDR    default :50051  (internal/collector/config.go)
  COLLECTOR_BATCH_SIZE default 50      (internal/collector/config.go)
  COLLECTOR_FLUSH_MS   default 500     (internal/collector/config.go)
  COLLECTOR_CREDIT     default 100     (internal/collector/config.go)
  VANTAGE_DB_DSN       REQUIRED        (pkg/db/config.go)
  VANTAGE_DB_MAX_CONNS optional        (pkg/db/config.go)

Gateway service:
  GATEWAY_ADDR              default :8080  (internal/gateway/config.go)
  VANTAGE_GATEWAY_MAX_ROWS  default 1000  (internal/gateway/config.go)
  VANTAGE_DB_DSN            REQUIRED      (pkg/db/config.go)
  VANTAGE_DB_MAX_CONNS      optional      (pkg/db/config.go)

Migrate job:
  VANTAGE_DB_DSN  REQUIRED  (pkg/db/config.go)
```

### Makefile Additions Summary

```makefile
# Near top of Makefile — export for all targets
export PATH := $(HOME)/go/bin:$(PATH)
export DOCKER_HOST := unix:///Users/ajitg/.rd/docker.sock
export TESTCONTAINERS_RYUK_DISABLED := true

DOCKER_IMAGES := $(SERVICES) migrate   # 5 images: mq streamer collector gateway migrate

# Updated .PHONY
.PHONY: ... kind-load deploy soak test-harness dependency-update

dependency-update: ## Pull Helm chart dependencies (run once before first helm-install)
	helm dependency update deployments/

kind-load: ## Load all service images into the vantage kind cluster
	@for img in $(DOCKER_IMAGES); do \
		echo "== kind load $$img =="; \
		kind load docker-image vantage/$$img:dev --name vantage; \
	done

deploy: docker kind-load helm-install ## Build images, load into kind, install via Helm

helm-install: ## Install/upgrade the umbrella chart into kind
	helm upgrade --install vantage deployments -f deployments/values.yaml

soak: ## Run sustained pipeline soak (SOAK_DURATION=60, SOAK_STREAMERS=3)
	@bash scripts/soak.sh

test-harness: ## Run live-infrastructure E2E harness (requires Docker)
	DOCKER_HOST=unix://$$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true \
	  go test -race -tags=e2e -count=1 -v -timeout 120s ./test/harness/...
```

### docker-compose.full.yml Structure for test-harness

The harness extends the existing `docker-compose.yml` (postgres only) to include all five services:

```yaml
# docker-compose.full.yml — full stack for make test-harness
# Extends docker-compose.yml; postgres service defined there
services:
  postgres:          # inherited from docker-compose.yml
    image: postgres:17-alpine
    # ...same as existing docker-compose.yml...

  mq:
    image: vantage/mq:dev
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      MQ_GRPC_ADDR: ":50051"
      MQ_HTTP_ADDR: ":8080"
    ports:
      - "50051:50051"
      - "8080:8080"    # inspect

  streamer:
    image: vantage/streamer:dev
    depends_on:
      - mq
    environment:
      STREAMER_MQ_ADDR: "mq:50051"
      STREAMER_CSV_PATH: "/data/dcgm_metrics.csv"
      STREAMER_LOOP_DELAY_MS: "5"
    volumes:
      - ./testdata/fixture.csv:/data/dcgm_metrics.csv:ro

  collector:
    image: vantage/collector:dev
    depends_on:
      - mq
      - postgres
    environment:
      COLLECTOR_MQ_ADDR: "mq:50051"
      VANTAGE_DB_DSN: "postgres://vantage:vantage@postgres:5432/vantage?sslmode=disable"

  gateway:
    image: vantage/gateway:dev
    depends_on:
      - postgres
    environment:
      GATEWAY_ADDR: ":8080"
      VANTAGE_DB_DSN: "postgres://vantage:vantage@postgres:5432/vantage?sslmode=disable"
    ports:
      - "8090:8080"    # mapped to 8090 to avoid conflict with mq's 8080

  migrate:
    image: vantage/migrate:dev
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      VANTAGE_DB_DSN: "postgres://vantage:vantage@postgres:5432/vantage?sslmode=disable"
    restart: "no"
```

The `migrate` service runs once and exits (exit 0 = success). The compose `depends_on` on postgres with `condition: service_healthy` ensures the DB is up before migrations run.

---

## Runtime State Inventory

Not applicable — this is a greenfield phase (new `build/` and `deployments/` directories). No existing runtime state to rename or migrate.

---

## Makefile Targets (OPS-05)

All five required targets exist; verify they reach all new code:

| Target | Status | Notes |
|--------|--------|-------|
| `make proto` | Exists | Compiles `api/proto/*.proto` → `pkg/pb/` |
| `make build` | Exists | Builds `mq streamer collector gateway` binaries |
| `make test` | Exists | `go test -race -covermode=atomic ./...` — excludes `e2e`-tagged files |
| `make coverage` | Exists | Enforces ≥90% on `./internal/... ./pkg/...` minus pb/docs |
| `make swagger` | Exists | `swag init -g cmd/gateway/main.go -o pkg/docs` |

New targets this phase adds: `dependency-update`, `kind-load`, `deploy`, `soak`, `test-harness`.

---

## Coverage Gate

The gate currently runs at **90.3%** (STATE.md, post-quick-task-260702-ku8). Any new Go code in `internal/` or `pkg/` must maintain this threshold. New code added this phase:

| New package | In coverage gate scope? | Reason |
|------------|------------------------|--------|
| `test/harness/` | No | Outside `./internal/...` and `./pkg/...`; `//go:build e2e` tag also excludes from `make test` |

No new `internal/` or `pkg/` packages are expected from this phase — the work is primarily Dockerfiles, Helm YAML, shell scripts, and a single Go test file in `test/harness/`.

---

## Validation Architecture

`workflow.nyquist_validation` is enabled (not set to false in config.json).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) + testify v1.11.1 |
| Config file | none — Makefile drives test invocations |
| Quick run command | `make test` |
| Full suite command | `DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true make coverage` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| OPS-01 | Multi-stage Dockerfiles build and produce correct images | Build verification | `make docker && docker images vantage/*:dev` | ❌ Wave 0: smoke-05 script |
| OPS-02 | Helm umbrella chart installs cleanly, all pods Running | Integration (kind) | `make kind-up deploy && kubectl get pods -A` | ❌ Wave 0: kind deploy |
| OPS-03 | Independent deploy: `helm upgrade --reuse-values --set mq.image.tag=X` rolls only MQ | Integration (kind) | `helm upgrade --reuse-values --set mq.image.tag=dev vantage deployments` | ❌ Wave 0: smoke-05 |
| OPS-04 | MQ Deployment has replicas:1, strategy:Recreate | Unit (helm lint) | `helm lint deployments/ && helm template deployments/ | grep -A2 'Recreate'` | ❌ Wave 0: Helm templates |
| OPS-05 | All five Makefile targets execute without error | Smoke | `make proto build test coverage swagger` | ✅ Existing targets |
| QA-01 | Unit tests exist across all services | Unit | `make test` | ✅ Existing tests pass |
| QA-04 | ≥90% line coverage gate passes | Unit + integration | `make coverage` (currently 90.3%) | ✅ Gate already enforced |

### Wave 0 Gaps

- [ ] `scripts/smoke/phase05-kind.sh` — proves OPS-01/02/03 through kind cluster
- [ ] `deployments/` directory and all chart files — prerequisites for any Helm test
- [ ] `build/*.Dockerfile` (×5) — prerequisites for `make docker`
- [ ] `docker-compose.full.yml` + `testdata/fixture.csv` — prerequisites for `make test-harness`
- [ ] `test/harness/harness_test.go` + `go get testcontainers-go/modules/compose@v0.43.0` — harness test infrastructure

### Sampling Rate

- Per task commit: `make build && make test`
- Per wave merge: `make build && make test && make coverage && make lint`
- Phase gate: Full suite green + `make smoke-05` passing before `/gsd-verify-work`

---

## Security Domain

`security_enforcement` is enabled (not set to false in config.json). ASVS Level 1 applies.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | No auth in scope (REQUIREMENTS.md Out of Scope) |
| V3 Session Management | No | Stateless HTTP API; no session tokens |
| V4 Access Control | No | No RBAC in scope for v1 |
| V5 Input Validation | Yes | DSN injection: env vars only, never logged (ASVS V8 invariant already in code) |
| V6 Cryptography | Partial | DSN contains credentials; transmitted in-cluster only (no TLS in kind, acceptable for dev) |
| V7 Error Handling | Yes | DSN never in error strings — enforced in pkg/db, cmd/migrate, cmd/gateway, cmd/collector |
| V8 Data Protection | Yes | DB credentials in Kubernetes Secrets (not in Helm values.yaml in plaintext) |

### Known Threat Patterns

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| DSN in Helm values.yaml | Information Disclosure | Use Kubernetes Secret for database password; reference via `valueFrom.secretKeyRef` in Deployment env |
| Docker image without security scanning | Tampering | Distroless final stage minimises attack surface; `docker scout` or `trivy` scan in CI (out of phase scope) |
| Migration job running with full DB privileges | Elevation of Privilege | Acceptable for dev/local; production would use a migration-scoped DB user — out of scope for v1 |

> **Credentials in values.yaml:** The current pattern puts `password: vantage` directly in values.yaml. This is acceptable for a local dev/kind cluster (LOCAL DEV ONLY — same precedent as docker-compose.yml). The planner should note this as a dev-only configuration.

---

## Environment Availability

| Dependency | Required By | Available | Version | Notes |
|------------|------------|-----------|---------|-------|
| Docker / Rancher Desktop | All image builds, kind, test-harness | ✓ | 29.1.4-rd | Socket at `unix:///Users/ajitg/.rd/docker.sock`; must set DOCKER_HOST |
| kind | `make kind-up`, `make kind-load` | ✓ | v0.32.0 | At `~/go/bin/kind` — NOT on shell PATH; Makefile must export PATH |
| helm | `make helm-install`, `make dependency-update` | ✓ | v4.0.5 | Via Rancher Desktop; `apiVersion: v2` charts fully supported |
| kubectl | smoke-05 (pod status, port-forward) | ✓ | v1.35.1 | Via Rancher Desktop |
| go | `make test`, `make test-harness` | ✓ | 1.26.2 | Matches go.mod `go 1.26` directive |

**Missing dependencies with no fallback:** None — all required tools are present.

**Kind PATH gap (must address in Makefile):** kind v0.32.0 exists at `~/go/bin/kind` but is not on the shell PATH. The Makefile `kind-up` target will fail without `export PATH := $(HOME)/go/bin:$(PATH)` added near the top.

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Bitnami PostgreSQL chart version 18.7.8 is available from `oci://registry-1.docker.io/bitnamicharts` | Standard Stack | `helm dependency update` fails; must find current chart version and update Chart.yaml |
| A2 | Bitnami Service name follows pattern `{{ .Release.Name }}-postgresql` when installed as OCI dependency | Architecture Patterns (migration DSN) | Services cannot connect to Postgres in-cluster; DSN must be corrected |
| A3 | `testcontainers-go/modules/compose` v0.43.0 has the `NewDockerComposeWith` API shown | Code Examples | Compile error in harness; API call signature must be updated |
| A4 | Streamer DCGM CSV can be served as a ConfigMap or volume mount in kind | Common Pitfalls (Pitfall 5) | Streamer crashes in kind with no CSV; alternative is to bake a fixture CSV into the streamer image (acceptable for dev-only kind) |

**Verification steps for A1:** After `helm dependency update deployments/`, check `deployments/Chart.lock` for the resolved version. If 18.7.8 is unavailable, run `helm search repo bitnami/postgresql --versions` (requires `helm repo add bitnami https://charts.bitnami.com/bitnami`) to find the latest stable version.

---

## Open Questions

1. **Streamer CSV in kind cluster**
   - What we know: The DCGM CSV is gitignored; streamer crashes without `STREAMER_CSV_PATH` pointing to a readable file.
   - What's unclear: Whether the planner should create a fixture CSV as a ConfigMap (most portable) or mount from a host path (simpler but kind-specific).
   - Recommendation: Use a small fixture CSV (10–50 rows) as a ConfigMap defined in `deployments/charts/streamer/templates/configmap.yaml`, mounted at `/data/dcgm_metrics.csv`. This avoids host-path coupling and works identically in CI.

2. **Migration hook race with Postgres start**
   - What we know: Bitnami PostgreSQL may take several seconds to become ready. The Helm pre-install hook runs the migrate Job immediately.
   - What's unclear: Whether the migrate Job will fail if it runs before Postgres is ready on first install, and how Helm 4 handles hook failures.
   - Recommendation: Add an init-container to the migration Job that polls `pg_isready` or uses a `wait-for-it.sh` pattern before the main container starts. This prevents a false-fail on first install.

3. **Harness compose file location**
   - What we know: `docker-compose.yml` at repo root covers only Postgres. The harness needs all 5 images.
   - What's unclear: Whether to create `docker-compose.full.yml` at the root or inside `test/harness/`.
   - Recommendation: Place at root (`docker-compose.full.yml`) so it's runnable manually outside the test suite. The harness imports it by relative path from `test/harness/`.

---

## Sources

### Primary (HIGH confidence)
- `go.mod` — all runtime package versions verified directly [VERIFIED: go.mod]
- `internal/*/config.go` — all environment variable names and defaults verified by reading source [VERIFIED: codebase]
- `pkg/db/db.go`, `pkg/db/migrations/*.sql` — migration implementation and schema verified [VERIFIED: codebase]
- `Makefile` — existing targets and pattern rules verified [VERIFIED: codebase]
- `.planning/phases/05-devops-quality-gates/05-CONTEXT.md` — all D-XX decisions [VERIFIED: context file]
- `.claude/CLAUDE.md`, `CLAUDE.md` — stack pins, hard constraints, directory layout [VERIFIED: project docs]
- Environment probe — kind v0.32.0 at ~/go/bin/kind, helm v4.0.5, docker v29.1.4-rd, kubectl v1.35.1 [VERIFIED: bash probes]

### Secondary (MEDIUM confidence)
- pkg.go.dev/github.com/testcontainers/testcontainers-go/modules/compose — ComposeStack API, constructor functions [CITED: pkg.go.dev]
- helm.sh/docs (via web search) — Helm 4 breaking changes: apiVersion v2 still supported; OCI works without HELM_EXPERIMENTAL_OCI; hook failure exits immediately in v4 [CITED: helm.sh/docs]
- blog.bitnami.com — Bitnami OCI chart distribution, `oci://registry-1.docker.io/bitnamicharts` format [CITED: blog.bitnami.com/2023/04/]

### Tertiary (LOW confidence)
- Assumption A1 (Bitnami chart 18.7.8 availability) — from CLAUDE.md, not independently verified against OCI registry [ASSUMED]
- Assumption A2 (Bitnami Service name pattern) — standard Helm convention, not verified by running the chart [ASSUMED]

---

## Metadata

**Confidence breakdown:**
- Dockerfile patterns: HIGH — golang:1.26-alpine + distroless pattern locked in CLAUDE.md; verified by all prior phases passing `make build`
- Helm structure: MEDIUM — umbrella + OCI dep pattern confirmed by docs; specific chart behavior (service name, dependency update flow) contains two ASSUMED items
- Makefile additions: HIGH — existing Makefile read directly; new targets follow established pattern rules
- test-harness: MEDIUM — compose module API verified from pkg.go.dev; Rancher Desktop + Ryuk pattern verified from existing e2e test conventions

**Research date:** 2026-07-02
**Valid until:** 2026-08-02 (stable ecosystem; testcontainers and Helm APIs are not rapidly changing)
