# Phase 5: DevOps + Quality Gates — Pattern Map

**Mapped:** 2026-07-02
**Files analyzed:** 15 new/modified files
**Analogs found:** 10 / 15

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `build/mq.Dockerfile` | config (build) | batch | none in repo yet | no-analog |
| `build/streamer.Dockerfile` | config (build) | batch | none in repo yet | no-analog |
| `build/collector.Dockerfile` | config (build) | batch | none in repo yet | no-analog |
| `build/gateway.Dockerfile` | config (build) | batch | none in repo yet | no-analog |
| `build/migrate.Dockerfile` | config (build) | batch | none in repo yet | no-analog |
| `deployments/Chart.yaml` | config (helm) | — | none in repo | no-analog |
| `deployments/values.yaml` | config (helm) | — | `docker-compose.yml` | partial |
| `deployments/templates/migrate-job.yaml` | config (helm hook) | batch | none in repo | no-analog |
| `deployments/charts/mq/…` | config (helm) | — | none in repo | no-analog |
| `deployments/charts/streamer/…` | config (helm) | — | none in repo | no-analog |
| `deployments/charts/collector/…` | config (helm) | — | none in repo | no-analog |
| `deployments/charts/gateway/…` | config (helm) | — | none in repo | no-analog |
| `docker-compose.full.yml` | config (compose) | — | `docker-compose.yml` | role-match |
| `scripts/smoke/phase05-kind.sh` | utility (smoke) | request-response | `scripts/smoke/phase04-gateway.sh` | role-match |
| `test/harness/harness_test.go` | test (e2e) | request-response | `test/e2e/pipeline_test.go` | role-match |
| `Makefile` (modified) | config (build) | — | `Makefile` (current) | exact |

---

## Pattern Assignments

### `build/*.Dockerfile` (×5 — mq, streamer, collector, gateway, migrate)

**Analog:** RESEARCH.md Pattern 1 (no in-repo Dockerfile exists yet — all five are greenfield)

**Two-stage structure:** builder `golang:1.26-alpine`, final `gcr.io/distroless/static-debian12`.
Identical across all five images except the binary name in `go build` and `ENTRYPOINT`.

```dockerfile
# Stage 1: builder
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/<svc> ./cmd/<svc>

# Stage 2: final
FROM gcr.io/distroless/static-debian12 AS final
COPY --from=builder /out/<svc> /<svc>
EXPOSE <port(s)>
ENTRYPOINT ["/<svc>"]
```

**Per-service substitutions:**

| Dockerfile | Binary name | Ports | Notes |
|---|---|---|---|
| `mq.Dockerfile` | `mq` | 50051 8080 | gRPC + HTTP inspect |
| `streamer.Dockerfile` | `streamer` | (none) | client-only; no listen port |
| `collector.Dockerfile` | `collector` | (none) | client-only; no listen port |
| `gateway.Dockerfile` | `gateway` | 8080 | HTTP REST; `pkg/docs` included via Go import (no extra COPY) |
| `migrate.Dockerfile` | `migrate` | (none) | one-shot job; no listen port |

**Existing Makefile pattern rule** (Makefile lines 109–110) drives all builds:
```makefile
docker-%: ## Build a single service image (build/%.Dockerfile)
	docker build -f build/$*.Dockerfile -t vantage/$*:dev .
```
All five Dockerfiles must be named exactly `build/<svc>.Dockerfile` to match this rule. `migrate` is not in `SERVICES` so it must be triggered separately or via an extended `DOCKER_IMAGES` list.

---

### `docker-compose.full.yml` (config/compose, —)

**Analog:** `docker-compose.yml` (repo root)

**Postgres service pattern** (docker-compose.yml lines 13–31) — copy verbatim as the `postgres` service:
```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB:       vantage
      POSTGRES_USER:     vantage
      POSTGRES_PASSWORD: vantage   # LOCAL DEV ONLY
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U vantage -d vantage"]
      interval: 2s
      timeout: 5s
      retries: 15
      start_period: 5s
```

**Additional services** follow the same credential/network pattern.
Port mapping rule to avoid conflicts: MQ HTTP inspect → `8080:8080`; Gateway → `8090:8080` (avoids clash when both are forwarded locally).

**Env var source:** Every var name comes from RESEARCH.md "Complete Env Variable Map":
- `mq`: `MQ_GRPC_ADDR=:50051`, `MQ_HTTP_ADDR=:8080`, `MQ_BUFFER_SIZE=10000`, `MQ_CONSUME_CREDIT=20`
- `streamer`: `STREAMER_MQ_ADDR=mq:50051`, `STREAMER_CSV_PATH=/data/dcgm_metrics.csv`, `STREAMER_LOOP_DELAY_MS=5`
- `collector`: `COLLECTOR_MQ_ADDR=mq:50051`, `VANTAGE_DB_DSN=postgres://vantage:vantage@postgres:5432/vantage?sslmode=disable`
- `gateway`: `GATEWAY_ADDR=:8080`, `VANTAGE_DB_DSN=postgres://vantage:vantage@postgres:5432/vantage?sslmode=disable`
- `migrate`: `VANTAGE_DB_DSN=postgres://vantage:vantage@postgres:5432/vantage?sslmode=disable`

**Streamer CSV volume:** `./testdata/fixture.csv:/data/dcgm_metrics.csv:ro` — a small fixture CSV (10–50 rows, matching DCGM 12-column format) must be created at `testdata/fixture.csv`. Column layout to copy from `test/e2e/pipeline_test.go` lines 187–198 (the `writeFixtureCSV` function).

---

### `scripts/smoke/phase05-kind.sh` (utility/smoke, request-response)

**Analog:** `scripts/smoke/phase04-gateway.sh`

**File header pattern** (phase04-gateway.sh lines 1–20):
```bash
#!/usr/bin/env bash
# Phase 5 smoke check — kind cluster E2E.
#
# Proves full pipeline through kind: all pods Running → streamer publishing →
# gateway serving real rows.
#
# Requires: kubectl, curl. Run via: make smoke-05
# Assumes: make kind-up && make deploy have already been run.
# Idempotent — safe to re-run.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
```

**Color helpers + pass/fail functions** — copy exactly from phase04-gateway.sh lines 29–31:
```bash
if [ -t 1 ]; then GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RST=$'\033[0m'
else GREEN=''; RED=''; BOLD=''; RST=''; fi
pass() { echo "${GREEN}✓${RST} $*"; }
fail() { echo "${RED}✗ $*${RST}"; exit 1; }
```

**Cluster-guard pattern** (new for kind smoke — fail fast if no cluster):
```bash
kubectl get nodes >/dev/null 2>&1 || fail "kind cluster not running — run: make kind-up deploy"
```

**Port-forward pattern** (local 8081 for gateway, 8082 for MQ inspect — avoids local port clash):
```bash
PF_PID=""
cleanup() { [ -n "$PF_PID" ] && kill "$PF_PID" 2>/dev/null || true; }
trap cleanup EXIT

kubectl port-forward svc/vantage-gateway 8081:8080 -n default &
PF_PID=$!
sleep 2  # allow port-forward to bind
```

**curl assertion pattern** — copy from phase04-gateway.sh (same HTTP assertion style):
```bash
status=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/api/v1/gpus)
[ "$status" = "200" ] || fail "GET /api/v1/gpus returned $status (expected 200)"
pass "GET /api/v1/gpus → 200"
```

---

### `test/harness/harness_test.go` (test/e2e, request-response)

**Analog:** `test/e2e/pipeline_test.go`

**Build tag and package declaration** — copy pattern from pipeline_test.go line 1 but change tag:
```go
//go:build e2e

package harness_test
```

**Imports pattern** — adapt from pipeline_test.go lines 23–49 (replace bufconn/grpc imports with compose):
```go
import (
    "context"
    "fmt"
    "net/http"
    "os"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    "github.com/testcontainers/testcontainers-go/modules/compose"
)
```

**Rancher Desktop env pattern** — from project convention (pipeline_test.go docs + Makefile line 8):
```go
// DOCKER_HOST and TESTCONTAINERS_RYUK_DISABLED are expected to be set in the
// environment before running. The make test-harness target exports them.
```

**Stack lifecycle pattern** — testcontainers compose module (RESEARCH.md Pattern 7):
```go
func TestHarness(t *testing.T) {
    ctx := context.Background()

    stack, err := compose.NewDockerComposeWith(
        compose.WithStackFiles("../../docker-compose.full.yml"),
        compose.StackIdentifier("vantage-harness"),
    )
    require.NoError(t, err)

    t.Cleanup(func() {
        if keep := os.Getenv("KEEP"); keep == "1" {
            t.Log("KEEP=1: leaving stack running for debugging")
            return
        }
        _ = stack.Down(ctx, compose.RemoveOrphans(true), compose.RemoveVolumes(true))
    })

    err = stack.
        WaitForService("gateway", wait.ForListeningPort("8080/tcp")).
        Up(ctx, compose.Wait(true))
    require.NoError(t, err)
    // ...assertions...
}
```

**Poll helper pattern** — copy from pipeline_test.go lines 212–239 (`pollUntilStable`) but adapt to HTTP:
Use a simple retry loop with `time.Sleep` polling `GET /api/v1/gpus` until 200 and non-empty body, with a 30s deadline.

**Assertion style** — copy from pipeline_test.go:
```go
require.Equal(t, http.StatusOK, resp.StatusCode, "GET /api/v1/gpus must return 200")
```

---

### `Makefile` (modified — additions only)

**Analog:** `Makefile` (current — exact match, extending existing targets)

**Existing structure to preserve:**
- `SERVICES := mq streamer collector gateway` (line 16) — DO NOT change; add `DOCKER_IMAGES` separately
- `.PHONY` list (line 23–25) — extend it
- `docker-% pattern rule` (lines 109–110) — unchanged; relied on by `make docker`
- `kind-up` / `helm-install` / `kind-down` stubs (lines 112–119) — replace body, keep names

**Exports to add near top** (after line 20, before `.DEFAULT_GOAL`):
```makefile
export PATH := $(HOME)/go/bin:$(PATH)
export DOCKER_HOST := unix:///Users/ajitg/.rd/docker.sock
export TESTCONTAINERS_RYUK_DISABLED := true
```

**New variable** (after `SERVICES` line 16):
```makefile
DOCKER_IMAGES := $(SERVICES) migrate
```

**New targets** (follow the `## comment` pattern of existing targets):
```makefile
dependency-update: ## Pull Helm chart dependencies (run once before first helm-install)
	helm dependency update deployments/

kind-load: ## Load all service images into the vantage kind cluster
	@for img in $(DOCKER_IMAGES); do \
		echo "== kind load $$img =="; \
		kind load docker-image vantage/$$img:dev --name vantage; \
	done

deploy: docker kind-load helm-install ## Full deploy cycle: docker build → kind-load → helm install

soak: ## Run sustained pipeline soak (SOAK_DURATION=60s, SOAK_STREAMERS=3)
	@bash scripts/soak.sh

test-harness: ## Run live-infrastructure E2E harness (requires Docker)
	DOCKER_HOST=unix://$$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true \
	  go test -race -tags=e2e -count=1 -v -timeout 120s ./test/harness/...
```

**kind-up target** — replace stub body with DOCKER_HOST-aware invocation:
```makefile
kind-up: ## Create local kind cluster
	kind create cluster --name vantage
```
(The `export DOCKER_HOST` at the top of the Makefile makes this work without inline env prefix.)

**helm-install target** — add `dependency-update` prerequisite:
```makefile
helm-install: dependency-update ## Install/upgrade the umbrella chart into kind
	helm upgrade --install vantage deployments -f deployments/values.yaml
```

**.PHONY extension** — add to existing list on line 23–25:
```makefile
.PHONY: ... kind-load deploy dependency-update soak test-harness
```

---

## Shared Patterns

### Env Var Names (all Helm templates and docker-compose.full.yml)
**Source:** RESEARCH.md "Complete Env Variable Map" (verified against `internal/*/config.go` and `pkg/db/config.go`)
**Apply to:** All Deployment env sections, migrate-job.yaml, docker-compose.full.yml

The canonical names are:
- `VANTAGE_DB_DSN` — required by collector, gateway, migrate
- `MQ_GRPC_ADDR`, `MQ_HTTP_ADDR`, `MQ_BUFFER_SIZE`, `MQ_CONSUME_CREDIT` — mq service
- `STREAMER_MQ_ADDR`, `STREAMER_CSV_PATH`, `STREAMER_LOOP_DELAY_MS` — streamer service
- `COLLECTOR_MQ_ADDR`, `COLLECTOR_BATCH_SIZE`, `COLLECTOR_FLUSH_MS`, `COLLECTOR_CREDIT` — collector
- `GATEWAY_ADDR`, `VANTAGE_GATEWAY_MAX_ROWS` — gateway service

### Kubernetes DNS Names (all in-cluster references)
**Source:** RESEARCH.md Pattern 8 (release name is `vantage`)
**Apply to:** All Helm Deployment env values that reference other services
```
vantage-mq:50051         # MQ gRPC
vantage-mq:8080          # MQ HTTP inspect
vantage-postgresql:5432  # Postgres (Bitnami naming convention)
vantage-gateway:8080     # Gateway
```

### DOCKER_HOST + Ryuk convention
**Source:** `Makefile` lines 5–14 (comment block), `test/e2e/pipeline_test.go` lines 18–20
**Apply to:** `make test-harness` target, any new testcontainers-based code
```makefile
DOCKER_HOST=unix://$$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true
```

### `//go:build` tag isolation
**Source:** `test/e2e/pipeline_test.go` line 1 (`//go:build integration`)
**Apply to:** `test/harness/harness_test.go` — use `//go:build e2e` (separate tag from `integration` to allow independent invocation)
**Coverage gate impact:** `test/harness/` is outside `./internal/... ./pkg/...` scope — naturally excluded. The `e2e` build tag also excludes it from `make test` (`go test ./...` without tags).

### MQ single-replica lock
**Source:** CONTEXT.md D-04, `docs/adr/ADR-001-bidi-at-least-once-delivery.md`
**Apply to:** `deployments/charts/mq/templates/deployment.yaml`
```yaml
spec:
  replicas: 1        # LOCKED: in-memory broker — never template this value
  strategy:
    type: Recreate   # LOCKED: no rolling update for in-memory broker
```
Do NOT expose `replicas` in `values.yaml` for the MQ sub-chart.

---

## No Analog Found

Files with no close match in the codebase (use RESEARCH.md patterns directly):

| File | Role | Data Flow | Reason |
|---|---|---|---|
| `build/*.Dockerfile` (×5) | config/build | batch | No Dockerfiles exist in repo yet |
| `deployments/Chart.yaml` | config/helm | — | No Helm charts in repo yet |
| `deployments/values.yaml` | config/helm | — | No Helm charts in repo yet; partial analog is docker-compose.yml for credential/service topology |
| `deployments/templates/migrate-job.yaml` | config/helm hook | batch | No Helm hook Jobs in repo |
| `deployments/charts/*/` (×4 sub-charts) | config/helm | — | No Helm charts in repo yet |
| `scripts/soak.sh` | utility/soak | — | No soak script in repo; write from scratch following smoke script conventions |
| `testdata/fixture.csv` | data fixture | — | Must be created; column layout to copy from `test/e2e/pipeline_test.go` lines 187–198 |

---

## Metadata

**Analog search scope:** `scripts/smoke/`, `test/e2e/`, `docker-compose.yml`, `Makefile`
**Files scanned:** 6 files read (Makefile, docker-compose.yml, phase04-gateway.sh, pipeline_test.go, 05-CONTEXT.md, 05-RESEARCH.md)
**Pattern extraction date:** 2026-07-02
