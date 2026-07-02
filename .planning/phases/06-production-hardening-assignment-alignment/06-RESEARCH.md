# Phase 6: Production Hardening + Assignment Alignment - Research

**Researched:** 2026-07-03
**Domain:** Go health endpoints · Helm probes/resources/HPA · SQL pagination · log/slog · GitHub Actions · AI prompt documentation
**Confidence:** MEDIUM (Go/stdlib areas), LOW (Helm/Kubernetes patterns cross-checked from official docs)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- AI-prompt documentation: `docs/AI_PROMPTS.md` — verbatim prompt log, table format, mine `.planning/` phase artifacts including honest failure notes (C-1, M-2, M-4, Phase-5 deadlock). Link from README + AI_USAGE.md.
- MQ health: `/healthz` + `/readyz` (gRPC serving) on existing `:8080` ServeMux.
- Gateway health: `/healthz` + `/readyz` (DB pool ping) on chi router.
- Streamer/Collector health: new minimal `net/http` listener, port env-configurable.
- Helm probes: values-configurable, all four sub-charts.
- Resource requests/limits: per-service defaults in each sub-chart `values.yaml`.
- HPA: `autoscaling/v2`, CPU-based, `hpa.enabled` values-gate, **gateway only** (required); streamer/collector optional toggle + documented `kubectl scale` workflow.
- MQ chart: `replicas: 1` + `strategy: Recreate` HARDCODED — ADR-001 invariant, never touch.
- Pagination: `limit`/`offset` on `GET /api/v1/gpus/{id}/telemetry`, SQL pushdown, pagination metadata in response, `VANTAGE_GATEWAY_MAX_ROWS` becomes `limit` ceiling, swag updated + spec regenerated.
- CI: `.github/workflows/ci.yml` on push + PR — `make build`, `make test`, `make coverage`, `make lint`. Makefile is the gate source of truth. No Docker/kind jobs (local smoke/soak remain e2e path).
- Structured logging: `log/slog` (stdlib), JSON handler, level via env, per-service `service` attribute.
- README: fix hook description pre-install → post-install,post-upgrade; add health/probe/HPA/pagination sections.

### Claude's Discretion
- Exact pagination response shape (envelope vs headers) — must be swagger-documented.
- Health listener port numbers for streamer/collector.
- slog handler wiring details (shared helper in `pkg/` vs per-service setup) — respect directory-ownership rule.
- CI job topology (single job vs matrix) as long as all four make gates run.

### Deferred Ideas (OUT OF SCOPE)
- Prometheus `/metrics` endpoint + tracing.
- WAL durability / crash recovery (Phase 7).
- Auth/TLS/rate limiting.
- Any change to MQ delivery semantics or the single-replica invariant.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| DOC-02 | Verbatim AI-prompt log `docs/AI_PROMPTS.md`: stage / tool / prompt / outcome / where fell short + manual intervention — covering repo bootstrap, code, unit tests, build env | Section: AI Prompt Log Sourcing |
| DOC-03 | README accuracy — fix migration hook description (pre→post-install); add health/probe/HPA/pagination workflow docs | Section: README Accuracy Fixes |
| OBS-01 | Every service: `/healthz` (liveness) + `/readyz` (readiness); gateway readiness pings DB pool; MQ readiness reflects gRPC server; streamer/collector lightweight `net/http` health listener | Section: Health Endpoint Patterns |
| OBS-02 | All services log through `log/slog` (stdlib): JSON handler, level via env, per-service attribute | Section: Structured Logging Migration |
| OPS-07 | Every Helm sub-chart wires `livenessProbe`/`readinessProbe` (values-configurable) against OBS-01 endpoints | Section: Helm Probe Wiring |
| OPS-08 | Every Helm sub-chart sets resource `requests`/`limits` with sensible per-service defaults | Section: Resource Requests and Limits |
| OPS-09 | Gateway sub-chart: optional `autoscaling/v2` HPA (values-gated, CPU-based); README documents streamer/collector scale workflow; MQ stays single-replica+Recreate | Section: HPA and Scaling Story |
| API-05 | `GET /api/v1/gpus/{id}/telemetry` supports `limit`/`offset` pagination with SQL pushdown and pagination metadata in response; swag updated; spec regenerated | Section: Pagination Pushdown |
| QA-07 | GitHub Actions CI: `make build`, `make test` (-race), `make coverage` (≥90%), `make lint` on push + PR | Section: GitHub Actions CI |
</phase_requirements>

---

## Summary

Phase 6 closes nine audit findings with no architectural changes — MQ delivery semantics, single-replica invariant, and the existing test gates are all preserved. The nine work areas fall into three tiers by implementation complexity:

**High complexity (new code surfaces):** Health endpoints on four services require adding HTTP listeners to streamer and collector (which currently have none), wiring readiness state into their reconnect loops, and registering handlers on the existing MQ ServeMux and gateway chi router. Each new handler needs unit tests to keep the ≥90% coverage gate green.

**Medium complexity (well-scoped additions):** Pagination requires adding `limit`/`offset` to the SQL query in `pkg/db/read.go` (both simple and windowed paths), updating the handler signature, choosing a response envelope shape, and regenerating the OpenAPI spec. Helm probe/resource/HPA additions are mechanical YAML templating in four sub-charts. The GitHub Actions workflow is a single file consuming existing Makefile targets — but requires a `.env` file pre-created in CI to satisfy `check-env`.

**Low complexity (mechanical):** The `log/slog` migration is a sweep of 27 `log.*` calls across 7 non-test files, with a shared `pkg/logger` helper. The README accuracy fix is a two-line correction plus new subsections. The AI_PROMPTS.md is a documentation-only file mined from existing `.planning/` artifacts.

**Primary recommendation:** Work in dependency order — slog first (no interface changes, improves diagnostic clarity for all subsequent work), then health endpoints (new surfaces), then Helm (depends on health ports), then CI (depends on green build), then pagination (handler change requiring test updates), then AI docs and README last (no code dependencies).

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Health liveness (`/healthz`) | Each service binary | — | Process-alive check; must be in-process, not proxied |
| Health readiness (`/readyz`) | Each service binary | DB layer (gateway) | Gateway readiness delegates to `pool.Ping`; others track connection state atomically |
| Helm probe wiring | Deployment config (Helm) | — | Kubernetes consumes the container's HTTP endpoint; configuration lives in chart templates |
| HPA | Helm sub-chart (gateway) | Kubernetes control plane | HPA is a k8s resource; the chart owns the manifest; metrics-server provides CPU data |
| Resource limits | Helm values | — | Resource specs live in chart values, not in application code |
| Pagination | API Gateway (HTTP handler + SQL) | pkg/db (SQL) | Handler parses params and calls db.Telemetry; SQL pushdown happens in pkg/db/read.go |
| Structured logging | Each service binary + pkg/logger | — | slog.SetDefault in main; shared setup helper in pkg/logger (the only shared Go surface) |
| CI | GitHub Actions workflow | Makefile (gate source) | CI invokes make targets; the Makefile defines what passes/fails |
| AI prompt log | docs/ (documentation) | .planning/ (source) | Mining and transcription; no code change |

---

## Standard Stack

### Core (no new dependencies)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `log/slog` | stdlib (Go 1.21+, project on Go 1.26) | Structured logging with JSON output | Stdlib — no new dependency; replaces `log` package; `slog.SetDefault` bridges existing `log.*` calls |
| `net/http` | stdlib | Minimal health listener for streamer/collector | Already used by MQ; zero new dependency; `http.ServeMux` sufficient for two-endpoint health server |
| `autoscaling/v2` | Kubernetes API | HPA CPU-based autoscaling | Stable API since k8s 1.23; `autoscaling/v1` is deprecated |

### Supporting (already in go.mod)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/go-chi/chi/v5` | v5.3.0 | Gateway health routes added to existing chi router | Gateway only; already wired |
| `github.com/jackc/pgx/v5/pgxpool` | v5.10.0 | `pool.Ping(ctx)` for gateway readiness | Single Ping call with short timeout; no schema change |
| `golang.org/x/sync/errgroup` | v0.21.0 | Manage health HTTP server goroutine alongside existing servers | Already used in all cmd/* mains |

**No new Go dependencies required for any item in Phase 6.** [VERIFIED: go.mod codebase grep]

---

## Package Legitimacy Audit

No external packages are added in Phase 6. All implementation uses stdlib (`log/slog`, `net/http`) and packages already present in `go.mod`.

| Package | Status |
|---------|--------|
| `log/slog` | Stdlib — no registry check needed |
| `net/http` | Stdlib — no registry check needed |
| All others | Already in go.mod (phases 1–5 approved) |

**Packages removed due to SLOP verdict:** none
**Packages flagged as suspicious:** none

---

## Architecture Patterns

### System Architecture Diagram (unchanged by Phase 6)

```
CSV → Streamer → [gRPC Produce] → MQ → [gRPC Consume bidi] → Collector → Postgres
                                    ↓                                        ↓
                             HTTP :8080                              HTTP :5001 (new)
                             /api/v1/queue/inspect                   /healthz /readyz
                             /healthz (new)
                             /readyz  (new)

Client → [HTTP REST] → Gateway → Postgres
                          ↓
                    /healthz /readyz (new, on chi router)
                    /api/v1/gpus
                    /api/v1/gpus/{id}/telemetry?limit=&offset=  (extended)
                    /swagger/*

Streamer HTTP :5000 (new) /healthz /readyz
Collector HTTP :5001 (new) /healthz /readyz
```

### Recommended Project Structure Changes

```
.github/
  workflows/
    ci.yml                     # New — GitHub Actions CI gate
cmd/
  streamer/main.go             # Extend errgroup with health HTTP server
  collector/main.go            # Extend errgroup with health HTTP server
  gateway/main.go              # Health routes added via server.go (chi)
  mq/main.go                   # Health routes registered on existing ServeMux
docs/
  AI_PROMPTS.md                # New — verbatim prompt log (DOC-02)
internal/
  gateway/
    handler.go                 # GetTelemetry: add limit/offset params, new response type
    server.go                  # Add /healthz and /readyz routes to chi router
    handler_test.go            # Update for new response envelope
    integration_test.go        # Add pagination tests; update TestGetTelemetry_ResultCap
  http/
    health.go                  # New — shared healthz/readyz handlers for MQ (mqhttp pkg)
    health_test.go             # New — unit tests for health handlers
  streamer/
    streamer.go                # Export readiness state (atomic bool after first Produce)
  collector/
    collector.go               # Export readiness state (atomic bool after stream open)
pkg/
  db/
    read.go                    # Telemetry(): add OFFSET $N param to both SQL paths
  logger/
    logger.go                  # New — shared slog setup helper
    logger_test.go             # New — unit tests
deployments/
  charts/
    mq/
      templates/deployment.yaml  # Add livenessProbe/readinessProbe + resources
      values.yaml               # Add probe timings + resources defaults
    gateway/
      templates/deployment.yaml  # Add livenessProbe/readinessProbe + resources
      templates/hpa.yaml         # New — values-gated HPA
      values.yaml               # Add probe timings + resources + autoscaling
    streamer/
      templates/deployment.yaml  # Add livenessProbe/readinessProbe + resources
      values.yaml               # Add probe timings + resources defaults
    collector/
      templates/deployment.yaml  # Add livenessProbe/readinessProbe + resources
      values.yaml               # Add probe timings + resources defaults
```

---

## Health Endpoint Patterns

### Pattern 1: MQ — Add to Existing ServeMux [VERIFIED: codebase grep]

**Current state:** MQ has `http.ServeMux` on `:8080` with one route `GET /api/v1/queue/inspect`. The mux is constructed in `cmd/mq/main.go` directly.

**What to add:** Register `/healthz` and `/readyz` on the existing `mqhttp` package (or inline in cmd/mq/main.go, but prefer `internal/http/health.go` for test coverage).

```go
// internal/http/health.go (package mqhttp)
// HealthzHandler returns 200 OK — process is alive.
func HealthzHandler() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    }
}

// ReadyzHandler returns 200 if srv is not shut down, 503 if shutdown has started.
// "gRPC server serving" is approximated by the shutdownCh not being closed.
func ReadyzHandler(srv *server.MQServer) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if srv.IsShuttingDown() {
            w.WriteHeader(http.StatusServiceUnavailable)
            w.Write([]byte(`{"status":"shutting_down"}`)) //nolint:errcheck
            return
        }
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    }
}
```

`MQServer` needs a new exported method `IsShuttingDown() bool` that checks `select { case <-s.shutdownCh: return true; default: return false }`.

Registration in `cmd/mq/main.go`:
```go
mux.HandleFunc("GET /healthz", mqhttp.HealthzHandler())
mux.HandleFunc("GET /readyz", mqhttp.ReadyzHandler(mqSrv))
```

**Health endpoints excluded from swagger** — they are on the MQ control plane (not the gateway API), and even the gateway health endpoints are not part of the documented user API.

### Pattern 2: Gateway — Add to Chi Router [VERIFIED: codebase grep]

**Current state:** `internal/gateway/server.go` constructs the chi router with two route groups.

**What to add:** Register at the root level (not under `/api/v1/gpus`):
```go
// internal/gateway/server.go
r.Get("/healthz", HealthzHandler())
r.Get("/readyz", ReadyzHandler(pool))

// ReadyzHandler does a cheap DB pool ping
func ReadyzHandler(pool *pgxpool.Pool) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
        defer cancel()
        if pool == nil {
            http.Error(w, `{"status":"no_pool"}`, http.StatusServiceUnavailable)
            return
        }
        if err := pool.Ping(ctx); err != nil {
            http.Error(w, `{"status":"db_unavailable"}`, http.StatusServiceUnavailable)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    }
}
```

**Ping timeout:** 2 seconds. Not cached — Kubernetes probes are low-frequency (default every 10s). DSN-never-logged rule: the ping error is logged but the pool's DSN is not included in the HTTP response.

### Pattern 3: Streamer — New Health Listener [ASSUMED — no existing HTTP in streamer]

**Current state:** `cmd/streamer/main.go` has a two-goroutine errgroup: one runs `streamer.Run`, one waits for signal. No HTTP listener.

**What to add:** Third goroutine in the errgroup. Export a readiness atomic from `internal/streamer`.

```go
// internal/streamer/streamer.go
// Add exported readiness flag
type Runner struct {
    ready atomic.Bool
}

// IsReady reports whether the first Produce call succeeded.
func (r *Runner) IsReady() bool { return r.ready.Load() }
// Set after first successful Produce:
//   r.ready.Store(true)
```

```go
// cmd/streamer/main.go (addition to errgroup)
runner := streamer.NewRunner(cfg)
// ...
g.Go(func() error {
    return runner.Run(gctx)
})
g.Go(func() error {
    healthMux := http.NewServeMux()
    healthMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    })
    healthMux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
        if !runner.IsReady() {
            w.WriteHeader(http.StatusServiceUnavailable)
            return
        }
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    })
    srv := &http.Server{Addr: cfg.HealthAddr, Handler: healthMux, ReadTimeout: 2*time.Second, WriteTimeout: 2*time.Second}
    go func() { <-gctx.Done(); srv.Shutdown(context.Background()) }()
    if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
        return err
    }
    return nil
})
```

**Port convention:** Streamer health on `:5000` (env `STREAMER_HEALTH_ADDR`, default `:5000`). [ASSUMED — no existing convention]

**Current code shape issue:** `internal/streamer/streamer.go` exports only a package-level `Run(ctx, cfg)` function. Adding a readiness flag requires either (a) converting to a struct with `NewRunner`/`Run` method, or (b) passing a `*atomic.Bool` into the existing `Run`. Option (b) is less invasive; option (a) is cleaner for tests. Research recommends option (a) since it's the established pattern in collector (which has a `Run` function taking a config). [ASSUMED]

### Pattern 4: Collector — New Health Listener [ASSUMED — same as streamer]

**Port convention:** Collector health on `:5001` (env `COLLECTOR_HEALTH_ADDR`, default `:5001`). [ASSUMED]

**Readiness trigger:** After first successful `stream.Recv()` call (MQ stream is connected and delivering messages). The atomic flag is set inside `collector.Run` when the stream is established. Same struct refactor as streamer. [ASSUMED]

---

## Helm Probe Wiring (OPS-07)

### Pattern: Values-Configurable Probes [CITED: kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/]

**In each `values.yaml`:**
```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: http           # named port from containerPort
  initialDelaySeconds: 30
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 6

readinessProbe:
  httpGet:
    path: /readyz
    port: http
  initialDelaySeconds: 30
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 6
```

**In each deployment template:**
```yaml
containers:
  - name: <service>
    ...
    {{- with .Values.livenessProbe }}
    livenessProbe:
      httpGet:
        path: {{ .httpGet.path }}
        port: {{ .httpGet.port }}
      initialDelaySeconds: {{ .initialDelaySeconds }}
      periodSeconds: {{ .periodSeconds }}
      timeoutSeconds: {{ .timeoutSeconds }}
      failureThreshold: {{ .failureThreshold }}
    {{- end }}
    {{- with .Values.readinessProbe }}
    readinessProbe:
      httpGet:
        path: {{ .httpGet.path }}
        port: {{ .httpGet.port }}
      initialDelaySeconds: {{ .initialDelaySeconds }}
      periodSeconds: {{ .periodSeconds }}
      timeoutSeconds: {{ .timeoutSeconds }}
      failureThreshold: {{ .failureThreshold }}
    {{- end }}
```

**Timing rationale for kind:** [ASSUMED]
- `initialDelaySeconds: 30` — kind preloads images but Postgres StatefulSet still needs ~20s to become healthy. Gateway needs additional time for migrate Job to complete (post-install hook, up to 300s in worst case). Consider `initialDelaySeconds: 60` for gateway specifically.
- `failureThreshold: 6` with `periodSeconds: 10` = 60s grace period after initial delay. Total tolerance: 30+60=90s (gateway 60+60=120s).
- Total worst case for helm --timeout 6m: 90s startup + migrate up to 300s + probe convergence 120s = ~510s < 360s. CAUTION: gateway probe convergence must start AFTER migrate completes. Gateway pods may restart once or twice before DB is ready; `failureThreshold: 6` absorbs this without triggering Recreate.

**Port references:** Streamer/collector need a new named port in their deployment templates:
```yaml
ports:
  - name: health
    containerPort: 5000    # streamer
```
The probe `port: health` then references this named port.

**MQ note:** MQ already declares `name: http, containerPort: 8080`. Probe uses `port: http`. No new port needed. [VERIFIED: codebase grep deployments/charts/mq/templates/deployment.yaml]

---

## Resource Requests and Limits (OPS-08)

### Values Pattern [CITED: kubernetes.io docs — ASSUMED sizing]

**Standard Helm pattern in deployment template:**
```yaml
containers:
  - name: <service>
    ...
    resources:
      {{- toYaml .Values.resources | nindent 12 }}
```

**Per-service sizing rationale:** [ASSUMED based on known buffer and workload shapes]

| Service | cpu request | cpu limit | memory request | memory limit | Sizing rationale |
|---------|------------|----------|----------------|-------------|-----------------|
| MQ | 100m | 500m | 64Mi | 256Mi | Ring buffer 10000 × ~200B = 2MB + credit 1000 × ~200B = 200KB + gRPC + overhead ≈ 50MB; 256Mi gives 5× headroom |
| Gateway | 100m | 200m | 32Mi | 128Mi | Stateless read proxy; no in-memory state beyond connection pool |
| Streamer | 50m | 100m | 16Mi | 64Mi | CSV reader + single gRPC client; minimal footprint |
| Collector | 50m | 100m | 16Mi | 64Mi | Batch accumulator; pgxpool + credit window; minimal footprint |

**HPA dependency:** HPA requires `resources.requests.cpu` to be set on the pod. The resource limits in OPS-08 satisfy this prerequisite for the gateway HPA in OPS-09.

---

## HPA and Scaling Story (OPS-09)

### Gateway HPA — New Template [CITED: kubernetes.io/docs/reference/kubernetes-api/autoscaling/horizontal-pod-autoscaler-v2/]

New file `deployments/charts/gateway/templates/hpa.yaml`:
```yaml
{{- if .Values.autoscaling.enabled }}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ .Release.Name }}-gateway
  labels:
    app.kubernetes.io/name: gateway
    app.kubernetes.io/instance: {{ .Release.Name }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ .Release.Name }}-gateway
  minReplicas: {{ .Values.autoscaling.minReplicas }}
  maxReplicas: {{ .Values.autoscaling.maxReplicas }}
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetCPUUtilizationPercentage }}
{{- end }}
```

**Gateway values.yaml additions:**
```yaml
autoscaling:
  enabled: false      # default off — requires metrics-server in cluster
  minReplicas: 1
  maxReplicas: 3
  targetCPUUtilizationPercentage: 80
```

**Metrics-server prerequisite:** `autoscaling/v2` HPA requires `metrics-server` running in the cluster. Kind does NOT include it by default. When `hpa.enabled: true`, the README must note: `kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml --dry-run=client -o yaml | ... (kind TLS patch)`. Since HPA is off by default, this is not a blocking prerequisite. [ASSUMED]

**Streamer/collector scaling story (README section):**
- Manual scale: `kubectl scale deployment/vantage-streamer --replicas=3`
- Helm: `helm upgrade vantage deployments --set streamer.replicaCount=3`
- Values already have `replicas:` field (implied by deployment template spec.replicas); add `replicaCount:` to values.yaml so `--set` works cleanly.
- Correctness already proven to 10 instances in Phase 3 soak tests.

---

## Pagination Pushdown (API-05)

### Pagination Response Shape Recommendation [ASSUMED — Claude's discretion]

**Recommendation: Response envelope** rather than headers.

Rationale:
1. Swagger documents response body types more naturally than response headers.
2. The existing `X-Truncated`/`X-Row-Limit` headers are superseded by pagination metadata — having both would be redundant.
3. An envelope keeps all response metadata in one place for API clients.
4. Breaking change to the response type is unavoidable either way (headers change the HTTP contract too); an envelope is cleaner.

**New response type:**
```go
// TelemetryPage wraps metric rows with pagination metadata.
type TelemetryPage struct {
    Data       []GpuMetricResponse `json:"data"`
    Pagination PaginationMeta      `json:"pagination"`
}

type PaginationMeta struct {
    Limit   int  `json:"limit"`
    Offset  int  `json:"offset"`
    HasNext bool `json:"has_next"`
}
```

**Handler changes:**
- Parse `limit` (default: `cfg.MaxRows`; ceiling: `cfg.MaxRows`) and `offset` (default: 0) from query params.
- Validate: `limit > 0`, `offset >= 0`; invalid values → 400.
- Pass `limit+1` to `db.Telemetry` to detect `has_next`: if len(result) == limit+1, has_next=true, trim last element.
- Remove `X-Truncated` and `X-Row-Limit` headers (superseded).

**SQL changes in `pkg/db/read.go`:**

Current simple path:
```sql
SELECT ... FROM gpu_metrics
WHERE gpu_id = $1
ORDER BY timestamp DESC
LIMIT $2
```

New simple path:
```sql
SELECT ... FROM gpu_metrics
WHERE gpu_id = $1
ORDER BY timestamp DESC
LIMIT $2 OFFSET $3
```

Current windowed path (3 params: id, start, end, limit):
```sql
SELECT ... WHERE gpu_id = $1 AND ... LIMIT $4
```

New windowed path:
```sql
SELECT ... WHERE gpu_id = $1 AND ... LIMIT $4 OFFSET $5
```

**Composite index compatibility:** `ORDER BY timestamp DESC` with `LIMIT $N OFFSET $M` still uses `idx_gpu_metrics_gpu_id_ts (gpu_id, timestamp DESC)` because `gpu_id = $1` satisfies the leading column equality and `timestamp DESC` matches the index order. Large offsets cause index scanning overhead but are acceptable for this scope. [ASSUMED — no explain plan run]

**Swag annotation update for `GetTelemetry`:**
```go
// @Param limit query int false "Max rows to return (default: VANTAGE_GATEWAY_MAX_ROWS; ceiling: VANTAGE_GATEWAY_MAX_ROWS)" minimum(1)
// @Param offset query int false "Row offset for pagination (default: 0)" minimum(0)
// @Success 200 {object} TelemetryPage
```

**X-Truncated/X-Row-Limit:** Remove from handler code and swag annotations. The `TelemetryPage.Pagination.HasNext` field supersedes this.

**Test impact:** [VERIFIED: codebase grep]
- `internal/gateway/handler_test.go`: Unit tests using nil pool — update `TestGetTelemetry_RouteRegistered` to decode `TelemetryPage` instead of `[]GpuMetricResponse`.
- `internal/gateway/integration_test.go`: `TestGetTelemetry_ResultCap` uses `decodeMetrics(t, w)` which decodes `[]GpuMetricResponse`. Must be updated to decode `TelemetryPage` and check `pagination.has_next`.
- Add new integration test: `TestGetTelemetry_Pagination` — seeds 5 rows, requests limit=2 offset=0 (gets 2, has_next=true), then limit=2 offset=4 (gets 1, has_next=false).
- `helper decodeMetrics` → rename to `decodeMetrics` returning `TelemetryPage`, or add new `decodePage` helper.

**Gateway Config changes:** `MaxRows` remains the ceiling for `limit`. No rename needed.

---

## Structured Logging Migration (OBS-02)

### log/slog Pattern [CITED: pkg.go.dev/log/slog]

**No new dependency.** `log/slog` is in stdlib since Go 1.21. This project uses Go 1.26. [VERIFIED: go.mod]

**Shared helper: `pkg/logger/logger.go`**

```go
// pkg/logger/logger.go
package logger

import (
    "log/slog"
    "os"
    "strings"
)

// New creates a JSON slog.Logger with the given service name attribute.
// Log level is read from LOG_LEVEL env var (DEBUG/INFO/WARN/ERROR; default INFO).
// Call slog.SetDefault(logger) in main() to bridge stdlib log.* calls.
func New(service string) *slog.Logger {
    var level slog.Level
    switch strings.ToUpper(os.Getenv("LOG_LEVEL")) {
    case "DEBUG":
        level = slog.LevelDebug
    case "WARN":
        level = slog.LevelWarn
    case "ERROR":
        level = slog.LevelError
    default:
        level = slog.LevelInfo
    }
    h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
    return slog.New(h).With(slog.String("service", service))
}
```

**Usage in each cmd/*/main.go (first two lines of main):**
```go
logger := logger.New("mq")  // or "streamer", "collector", "gateway"
slog.SetDefault(logger)
```

After `slog.SetDefault`, all `log.Printf` calls in the process are forwarded to the slog logger and appear as JSON. But the correct migration replaces each `log.Printf` with the structured equivalent.

**Migration map (27 calls across 7 files):** [VERIFIED: codebase grep]

| File | Count | Pattern |
|------|-------|---------|
| `cmd/mq/main.go` | 5 | `log.Printf("mq: ...")` → `slog.Info/Warn/Error(...)` |
| `cmd/gateway/main.go` | 6 | `log.Printf/Fatalf` → `slog.Info/Error` + `os.Exit(1)` |
| `cmd/collector/main.go` | 5 | `log.Printf/Fatalf` → `slog.Info/Error` + `os.Exit(1)` |
| `cmd/streamer/main.go` | 2 | `log.Printf` → `slog.Info` |
| `cmd/migrate/main.go` | 1 | `log.Printf` → `slog.Info` |
| `internal/streamer/streamer.go` | 4 | `log.Printf` → `slog.Warn/Info` |
| `internal/collector/collector.go` | 4 | `log.Printf` → `slog.Warn/Error` |

**Key mapping:**
- `log.Printf("service: msg %s", val)` → `slog.Info("msg", "field", val)`
- `log.Fatalf("service: %v", err)` → `slog.Error("msg", "error", err); os.Exit(1)`
- `log.Printf("collector: skip bad proto (id=%d): %v", ...)` → `slog.Warn("skip bad proto", "id", msg.GetId(), "error", err)`
- `log.Printf("collector: batch row %d exec: %v", ...)` → `slog.Error("batch exec failed", "row", i, "error", err)`

**DSN-never-logged rule preserved:** The existing code never passes DSN to log calls. This rule is unchanged — slog.Error must not include the DSN in error messages.

**Test impact:**
- `internal/collector/collector_test.go` uses `log.Fatalf` in TestMain — these are integration test setup calls and do NOT need migration (they are test infrastructure, not production logging paths). [VERIFIED: codebase grep]
- `internal/gateway/integration_test.go` uses `log.Fatalf` in TestMain — same. [VERIFIED: codebase grep]
- No test asserts on log output format, so the migration does not break existing tests. [VERIFIED: grep for log output assertion patterns]

**Coverage impact:** New `pkg/logger/logger.go` needs a unit test. A simple `TestNew` that creates a logger, calls `slog.SetDefault`, and emits a log line while checking no panic is sufficient. The function is thin but must be covered to avoid pulling down the ≥90% gate. [ASSUMED — no coverage tool run]

---

## GitHub Actions CI (QA-07)

### Workflow Design [ASSUMED pattern cross-checked with actions/setup-go docs]

**File:** `.github/workflows/ci.yml`

```yaml
name: CI
on:
  push:
    branches: ["**"]
  pull_request:

jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v6
        with:
          go-version: '1.26'
          cache: true
          cache-dependency-path: go.sum

      - name: Install golangci-lint
        run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

      - name: Prepare .env for CI
        run: printf 'DOCKER_HOST=unix:///var/run/docker.sock\nTESTCONTAINERS_RYUK_DISABLED=true\n' > .env

      - name: Build
        run: make build

      - name: Test
        run: make test

      - name: Coverage
        run: make coverage

      - name: Lint
        run: make lint
```

**Critical `.env` requirement:** [VERIFIED: Makefile codebase grep]

The Makefile auto-creates `.env` with empty values if it doesn't exist:
```makefile
ifeq ($(wildcard .env),)
$(shell printf 'DOCKER_HOST=\nTESTCONTAINERS_RYUK_DISABLED=true\n' > .env)
endif
include .env
export DOCKER_HOST TESTCONTAINERS_RYUK_DISABLED
```

If CI doesn't pre-create `.env`, the Makefile creates one with `DOCKER_HOST=` (empty). Then `check-env` fails. The `Prepare .env for CI` step must run BEFORE any `make` invocation.

**Docker availability:** `ubuntu-latest` GitHub Actions runners include Docker pre-installed. `make coverage` runs with `-tags=integration` which starts testcontainers (postgres:17-alpine). The default Docker socket on ubuntu runners is `/var/run/docker.sock`. [ASSUMED — standard GH Actions runner behavior]

**`make test` vs `make coverage` distinction:** [VERIFIED: Makefile codebase grep]
- `make test`: `go test -race -covermode=atomic -coverprofile=coverage.out ./...` — no `-tags=integration` flag, so integration-tagged test files are EXCLUDED. Runs unit tests only. DOCKER_HOST must be set (check-env runs) but Docker is not actually used.
- `make coverage`: `go test -race ... -tags=integration $$PKGS` — runs integration tests. Docker IS used for testcontainers.

**No protoc/swag needed in CI:** Generated files `pkg/pb/*.go` and `pkg/docs/*.go` are committed to the repo. [VERIFIED: codebase ls]. CI does NOT run `make proto` or `make swagger`. If a swagger drift check is desired, add:
```yaml
- name: Swagger drift check
  run: |
    go install github.com/swaggo/swag/cmd/swag@v1.16.4
    swag init -g cmd/gateway/main.go -o /tmp/docs-check
    diff pkg/docs/swagger.json /tmp/docs-check/swagger.json
```
This is optional — the CONTEXT.md mentions it but doesn't require it as a gate.

**No kind cluster:** Smoke tests (`make smoke`, `make soak`, `make deploy`) are NOT part of the CI workflow. They remain local-only. [VERIFIED: CONTEXT.md decision]

**Job topology:** Single job is sufficient. No matrix needed — single Go version (1.26), single OS (ubuntu-latest).

---

## AI Prompt Log Sourcing (DOC-02)

### What the assignment requires [VERIFIED: 06-REVIEW.md]

"Detailed information of all the prompts used and a good description of where a prompt fell short that required manual intervention." Covers four aspects: repo bootstrap, code, unit tests, build env.

### Available source artifacts [VERIFIED: codebase ls .planning/phases/]

| Phase | Artifacts available | What they contain |
|-------|--------------------|--------------------|
| 01 | 01-RESEARCH.md, 01-PLAN-OUTLINE.md, 01-01/02/03-PLAN.md + SUMMARY.md, 01-CONTEXT.md, 01-VERIFICATION.md | MQ core bootstrap, proto design, ring buffer + gRPC wiring |
| 01.1 | 01.1-DISCUSSION-LOG.md, 01.1-CONTEXT.md, 01.1-01..06-PLAN.md + SUMMARY.md | At-least-once redesign, bidi stream, credit window, ack-on-persist C-1 bug |
| 02 | 02-DISCUSSION-LOG.md, 02-CONTEXT.md, 02-01/02-PLAN.md | Schema, pgxpool, migration tooling |
| 03 | 03-RESEARCH.md, 03-01..04-PLAN.md + SUMMARY.md | Streamer, Collector, E2E tests |
| 04 | 04-RESEARCH.md, 04-01..03-PLAN.md | Gateway, chi, swag annotations |
| 05 | 05-DISCUSSION-LOG.md, 05-CONTEXT.md, 05-01..05-PLAN.md, 05-REVIEW.md, 05-SECURITY.md, 05-UAT.md | Docker, Helm, hook deadlock, security review, UAT |
| quick-260702-ku8 | 260702-ku8-PLAN.md, REVIEW-FINDINGS.md, SUMMARY.md | Mid-assignment 4-agent review: C-1, M-2, M-3, M-4 |

### Documented failure points to include (explicitly graded) [VERIFIED: STATE.md + 06-REVIEW.md]

| Stage | Failure | Manual intervention |
|-------|---------|---------------------|
| Code (Phase 01.1) | C-1: Collector ack-on-persist bug — ack sent before DB write, causing message loss on crash | Reproduced via smoke test; debugged by human reviewing sequence; fixed in quick-260702-ku8 |
| Code (Phase 01.1) | M-2: MQ missed wakeup — buffered-1 notify channel woke only one consumer; other consumers starved | Race detector caught it; fixed notify to broadcast by close+reopen |
| Code (Phase 01.1) | M-3: GracefulStop blocking — gRPC GracefulStop() hangs 30s in tests due to drain interval | Fixed with 5s timeout + forced Stop() fallback |
| Code (Phase 04) | M-4: X-Truncated/X-Row-Limit headers set after WriteHeader (HTTP header ordering bug) | Silent in tests; found by code review; fixed by setting headers before WriteHeader |
| Build env (Phase 05) | Helm pre-install hook deadlock against Postgres StatefulSet in same release | Fixed by changing hook annotation to post-install,post-upgrade (deviation from spec accepted by human) |
| Unit tests | Phase 03 smoke false-failed: MVCC race on two-query exactly-once check | Fixed by human observation of MVCC boundary; added fast-path re-test |

### Recommended `docs/AI_PROMPTS.md` structure

Table columns: **Stage** | **Tool/Command** | **Representative Prompt** | **Outcome** | **Where it fell short / Manual intervention needed**

Stages:
1. Repo bootstrap (Phase 01 foundation — proto, go.mod, MQ skeleton)
2. Code gen — MQ core (Phases 01, 01.1)
3. Code gen — pipeline (Phase 03)
4. Code gen — API Gateway (Phase 04)
5. Code gen — DevOps (Phase 05)
6. Unit tests (across all phases — TDD prompts)
7. Build environment (proto toolchain, swag, Helm, Docker)
8. Code review + bug fix (quick-260702-ku8)

Each GSD skill invocation (`/gsd-discuss-phase`, `/gsd-plan-phase`, `/gsd-execute-phase`) is a traceable prompt; the PLAN.md files contain the actual task descriptions that were executed. The DISCUSSION-LOG.md files contain the human-AI design dialogue.

---

## Common Pitfalls

### Pitfall 1: Health Endpoints Excluded from Swagger Spec
**What goes wrong:** Developer registers `/healthz` and `/readyz` on the gateway chi router with `// @Router` swag annotations. These appear in the OpenAPI spec and clutter the documented API.
**Why it happens:** Swag picks up ALL annotated handler functions.
**How to avoid:** Do NOT add swag annotations to health handlers. They are operational, not user-facing API.
**Warning signs:** `pkg/docs/swagger.json` contains a `healthz` or `readyz` path entry after `make swagger`.

### Pitfall 2: Helm Probe Port Mismatch
**What goes wrong:** Probe references port name `http` but the container's port declaration uses a different name or number.
**Why it happens:** Streamer and collector currently declare no ports in their Helm templates. Adding a health listener without adding the corresponding `containerPort` declaration causes the probe to fail with "port not found" or silently connect to nothing.
**How to avoid:** Whenever a new health HTTP server is added to streamer/collector, also add the containerPort declaration with a named port (e.g., `health`). Use the port name (not number) in the probe spec for readability.
**Warning signs:** Pods start but become NotReady immediately; `kubectl describe pod` shows `CreateContainerConfigError` or probe failure on port resolution.

### Pitfall 3: Gateway Readiness Probe Fires Before Migrate Job Completes
**What goes wrong:** Gateway pods become "not ready" on first readiness probe because Postgres schema hasn't been applied yet (migrate Job is still running), causing the gateway to fail `pool.Ping` (connection is fine but schema doesn't exist — though Ping doesn't check schema, only TCP).
**Why it happens:** In practice Ping succeeds as long as Postgres is up; the real risk is gateway erroring on first query. However, `initialDelaySeconds` that is too small causes rapid probe failure which confuses Helm's timeout tracking.
**How to avoid:** `initialDelaySeconds: 60` for gateway (generous to let migrate complete). `failureThreshold: 6` with `periodSeconds: 10` gives additional 60s of grace.
**Warning signs:** `helm install --timeout 6m` fails with "gateway not ready"; `kubectl describe pod` shows repeated readiness failures in the first 2 minutes.

### Pitfall 4: `make test` / `make coverage` Fail in CI Due to Missing `.env`
**What goes wrong:** CI runs `make build` first (no check-env), succeeds. Then `make test` fails: `ERROR: DOCKER_HOST is empty`.
**Why it happens:** The Makefile auto-creates `.env` with empty `DOCKER_HOST=` if `.env` doesn't exist. `include .env` then exports `DOCKER_HOST=""`, failing `check-env`.
**How to avoid:** Add "Prepare .env for CI" step BEFORE the first `make` invocation in the workflow.
**Warning signs:** CI log shows `Created .env template — set DOCKER_HOST` (the Makefile warning) followed by `ERROR: DOCKER_HOST is empty`.

### Pitfall 5: Pagination `has_next` Off-by-One
**What goes wrong:** Query asks for `limit=10`, gets 10 rows, sets `has_next=true`. Caller pages again, gets 0 rows.
**Why it happens:** Fetching exactly `limit` rows doesn't prove there's a next page.
**How to avoid:** Fetch `limit+1` rows. If you get `limit+1`, has_next=true and trim the last element. If you get ≤ limit, has_next=false.
**Warning signs:** Final page of results shows empty `data` array with `has_next: false` only after an extra round-trip.

### Pitfall 6: slog Migration Breaks `log.Fatalf` Semantics
**What goes wrong:** `log.Fatalf` both logs and calls `os.Exit(1)`. After migration to `slog.Error`, only the log happens — the process continues into undefined state.
**Why it happens:** `slog` has no `Fatal` equivalent.
**How to avoid:** Every `log.Fatalf(...)` replacement must be `slog.Error(...); os.Exit(1)`.
**Warning signs:** A previously-fatal error condition now lets the service start in a broken state (DB connect failure, etc.).

### Pitfall 7: HPA Without Resource Requests
**What goes wrong:** HPA is enabled (`hpa.enabled: true`) but CPU utilization is "unknown" — HPA never scales.
**Why it happens:** `autoscaling/v2` HPA requires `resources.requests.cpu` on the pod spec to calculate CPU utilization percentage. Without it, the metric is undefined.
**How to avoid:** OPS-08 (resource limits) must be implemented before or together with OPS-09 (HPA). The resource values in `values.yaml` provide the requests that HPA depends on.
**Warning signs:** `kubectl describe hpa vantage-gateway` shows `TARGETS: <unknown>/80%`.

---

## Code Examples

### JSON handler with service attribute (pkg/logger pattern)
```go
// Source: pkg.go.dev/log/slog (stdlib)
h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
logger := slog.New(h).With(slog.String("service", "gateway"))
slog.SetDefault(logger)
// After SetDefault, slog.Info/Warn/Error use this logger,
// and stdlib log.Printf/log.Printf also forward to it.
```

### Pool.Ping for gateway readiness
```go
// Source: pkg.go.dev/github.com/jackc/pgx/v5/pgxpool (existing in go.mod)
ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
defer cancel()
if err := pool.Ping(ctx); err != nil {
    w.WriteHeader(http.StatusServiceUnavailable)
    return
}
w.WriteHeader(http.StatusOK)
```

### MQServer IsShuttingDown (new method)
```go
// Checks shutdownCh (already exists in server.go) without holding any lock.
func (s *MQServer) IsShuttingDown() bool {
    select {
    case <-s.shutdownCh:
        return true
    default:
        return false
    }
}
```

### OFFSET in db.Telemetry simple path
```go
// pkg/db/read.go — simple path extension
rows, err = pool.Query(ctx,
    `SELECT `+cols+`
     FROM gpu_metrics
     WHERE gpu_id = $1
     ORDER BY timestamp DESC
     LIMIT $2 OFFSET $3`,
    id, limit+1, offset,  // limit+1 for has_next detection
)
```

### has_next detection in handler
```go
hasNext := len(metrics) > limit
if hasNext {
    metrics = metrics[:limit]
}
return TelemetryPage{
    Data: toResponse(metrics),
    Pagination: PaginationMeta{Limit: limit, Offset: offset, HasNext: hasNext},
}
```

### HPA template (gateway chart)
```yaml
# Source: kubernetes.io/docs/reference/kubernetes-api/autoscaling/horizontal-pod-autoscaler-v2/
{{- if .Values.autoscaling.enabled }}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ .Release.Name }}-gateway
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ .Release.Name }}-gateway
  minReplicas: {{ .Values.autoscaling.minReplicas }}
  maxReplicas: {{ .Values.autoscaling.maxReplicas }}
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetCPUUtilizationPercentage }}
{{- end }}
```

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Pool health check | Custom SQL ping (`SELECT 1`) | `pool.Ping(ctx)` | pgxpool.Ping is a proper health check that validates the connection without schema dependency |
| Log level parsing | Custom string→Level switch | Standard switch on `os.Getenv("LOG_LEVEL")` mapped to slog constants | Log levels are fixed by slog: LevelDebug/Info/Warn/Error |
| HPA manifest | Custom scaling controller | `autoscaling/v2` HPA | Kubernetes has built-in HPA; reimplementing is never correct |
| Pagination cursor | UUID/timestamp cursor pagination | LIMIT/OFFSET | Cursor pagination is more scalable but not required by the assignment spec; LIMIT/OFFSET on a composite index is correct and simpler |

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact on this phase |
|--------------|------------------|--------------|---------------------|
| `log` stdlib (unstructured) | `log/slog` JSON handler | Go 1.21 (2023) | F-08: migrate now (standard practice) |
| `autoscaling/v1` HPA (`targetCPUUtilizationPercentage` top-level) | `autoscaling/v2` (metrics array) | k8s 1.23 (2021), stable | Use v2 for the HPA template |
| Hand-written OpenAPI spec | `swag` annotation-driven generation | Phase 4 (project) | Maintain: update annotations, run `make swagger` |
| Pre-install Helm hook | Post-install,post-upgrade hook | Phase 5 (project, WR-04) | README fix: correct the hook description |

**Deprecated/outdated:**
- `autoscaling/v1` HPA: `targetCPUUtilizationPercentage` at spec root is v1 syntax. Phase 6 uses `autoscaling/v2` with `spec.metrics[]`. Never mix.
- `github.com/golang/protobuf`: Deprecated (existing note in CLAUDE.md). Not relevant to Phase 6.

---

## Runtime State Inventory

No rename/refactor is involved in Phase 6. This section is omitted.

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Streamer health port `:5000`, collector health port `:5001` | Health Endpoint Patterns | Port conflict with another service; change env defaults to avoid collision |
| A2 | Streamer readiness = first successful Produce; collector readiness = first stream.Recv | Health Endpoint Patterns | If connection is established but no messages flow, readiness may lag — acceptable for a looping producer |
| A3 | Converting streamer/collector to Runner struct for readiness state exposure | Health Endpoint Patterns | Alternative is passing `*atomic.Bool` pointer; either works, struct is cleaner |
| A4 | MQ resource limit 256Mi is sufficient for buffer 10000 × ~200B | Resource Requests/Limits | If proto messages are larger (~1KB each), limit should be 512Mi; measure in practice |
| A5 | Gateway probe initialDelaySeconds=60 is sufficient for migrate job to complete | Helm Probe Wiring | In a slow network (no kind preload), PostgreSQL pull adds time; increase to 90s if migrate races |
| A6 | metrics-server is required for HPA in kind | HPA | Standard kind does not include metrics-server; HPA silently shows `<unknown>` CPU without it |
| A7 | ubuntu-latest GH Actions runners have Docker at /var/run/docker.sock | GitHub Actions CI | If runner image changes socket path, .env path in CI workflow breaks |
| A8 | Response envelope (TelemetryPage) preferred over headers for pagination metadata | Pagination Pushdown | Headers approach also valid; planner should confirm before implementing |
| A9 | Large OFFSET causes index scan overhead on the composite index | Pagination Pushdown | Deep pages (offset > 10000) will be slow; acceptable for assignment scope |

---

## Open Questions

1. **Streamer/collector health port numbers**
   - What we know: Current services expose no HTTP; ports 5000/5001 are proposed.
   - What's unclear: No existing port allocation document; 5000 is commonly used (Flask default, etc.) and may conflict in non-kind environments.
   - Recommendation: Use 9000/9001 as health ports (less commonly in use for application services). Document in service Config and Helm values.

2. **Swagger drift check in CI**
   - What we know: pkg/docs is committed; make swagger regenerates it.
   - What's unclear: CONTEXT.md mentions it but does not explicitly gate CI on it.
   - Recommendation: Include as an optional step that warns (not fails) on drift, since swag CLI is not cached and adds ~30s.

3. **Gateway HPA metrics-server note**
   - What we know: HPA requires metrics-server; kind does not include it.
   - What's unclear: Whether the grader will enable HPA and attempt to test scaling.
   - Recommendation: README note: "To use HPA, install metrics-server: `kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml`. Kind requires the `--kubelet-insecure-tls` arg patch." Include this note only if `autoscaling.enabled: true` is set in examples.

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | All | ✓ | 1.26.2 | — |
| Helm | OPS-07/08/09 | ✓ | 4.0.5 | — |
| kind | Manual smoke | ✓ | 0.32.0 | — |
| Docker | make coverage (testcontainers) | ✓ (Rancher Desktop) | — | — |
| golangci-lint | make lint | ✓ (in ~/go/bin per Makefile PATH) | — | `go vet` fallback in Makefile |
| kubectl | Manual k8s ops | ✗ | — | Use `kind kubectl` or `helm` for k8s resources |
| metrics-server (k8s) | HPA (OPS-09) | ✗ (not in kind by default) | — | HPA gated `enabled: false`; no fallback needed for default deploy |

**Missing dependencies with no fallback:**
- None that block phase execution.

**Missing dependencies with fallback:**
- `kubectl`: cluster operations possible via `kind kubectl` or `helm`; README note sufficient.
- `metrics-server`: HPA off by default; gates behind `autoscaling.enabled`.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | `testing` (stdlib) + `testify` v1.11.1 |
| Config file | none — driven by Makefile |
| Quick run command | `go test -race ./internal/... ./pkg/...` |
| Full suite command | `make coverage` (includes integration, needs Docker) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| OBS-01 (MQ) | `/healthz` returns 200; `/readyz` returns 200 when not shut down, 503 when shutting down | unit | `go test -race ./internal/http/...` | ❌ Wave 0: `internal/http/health_test.go` |
| OBS-01 (Gateway) | `/healthz` returns 200; `/readyz` returns 503 when pool.Ping fails | unit | `go test -race ./internal/gateway/...` | ❌ Wave 0: add to `handler_test.go` |
| OBS-01 (Streamer/Collector) | `/healthz` 200; `/readyz` reflects atomic ready flag | unit | `go test -race ./internal/streamer/... ./internal/collector/...` | ❌ Wave 0: new test files |
| OBS-02 | Logger creates JSON handler with service attribute; level from env | unit | `go test -race ./pkg/logger/...` | ❌ Wave 0: `pkg/logger/logger_test.go` |
| API-05 | limit/offset params parse correctly; SQL returns OFFSET rows; has_next=true when more rows exist; 400 on invalid params | unit + integration | unit: `go test ./internal/gateway/...`; integration: `make coverage` | ❌ Wave 0: update `handler_test.go` + `integration_test.go` |
| QA-07 | CI workflow file exists and gate targets are invoked | manual | `cat .github/workflows/ci.yml` | ❌ Wave 0: `.github/workflows/ci.yml` |
| OPS-07 | Helm templates render valid probes | manual (helm template) | `helm template deployments/` | existing templates extended |
| OPS-08 | Helm templates render resources block | manual (helm template) | `helm template deployments/` | existing templates extended |
| OPS-09 | HPA template renders when enabled; gateway scales in kind | manual | `helm template --set gateway.autoscaling.enabled=true deployments/` | ❌ Wave 0: `deployments/charts/gateway/templates/hpa.yaml` |
| DOC-02 | AI_PROMPTS.md covers all four aspects; includes failure notes | manual review | `cat docs/AI_PROMPTS.md` | ❌ Wave 0: `docs/AI_PROMPTS.md` |
| DOC-03 | README hook description is post-install,post-upgrade | manual review | `grep 'post-install' README.md` | existing README updated |

### Sampling Rate
- **Per task commit:** `go test -race ./...` (unit only, fast — no Docker)
- **Per wave merge:** `make build && make test`
- **Phase gate:** `make build && make test && make coverage && make lint`

### Wave 0 Gaps
- [ ] `internal/http/health_test.go` — covers OBS-01 MQ health handlers
- [ ] `pkg/logger/logger_test.go` — covers OBS-02 logger setup
- [ ] Health test additions in `internal/gateway/handler_test.go` — covers OBS-01 gateway
- [ ] Health test additions in `internal/streamer/streamer_test.go` — covers OBS-01 streamer
- [ ] Health test additions in `internal/collector/collector_test.go` — covers OBS-01 collector
- [ ] `.github/workflows/ci.yml` — covers QA-07
- [ ] `docs/AI_PROMPTS.md` — covers DOC-02
- [ ] `deployments/charts/gateway/templates/hpa.yaml` — covers OPS-09

---

## Security Domain

`security_enforcement` is not explicitly set false in any config found. Treating as enabled.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Health endpoints are unauthenticated (operational only — per CONTEXT.md, auth is out of scope) |
| V3 Session Management | no | No sessions |
| V4 Access Control | no | Health endpoints intentionally world-readable |
| V5 Input Validation | yes (pagination) | `limit` and `offset` params validated (positive integer, ceiling enforced) before SQL binding |
| V6 Cryptography | no | No new crypto |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Pagination abuse (limit=100000) | DoS | Ceiling at `VANTAGE_GATEWAY_MAX_ROWS` (default 1000); reject > ceiling with 400 |
| SQL injection via limit/offset | Tampering | Use parameterized `$N` binding; NEVER string-concatenate limit or offset into SQL |
| Health endpoint information disclosure | Information Disclosure | Health responses return only `{"status":"ok"}` — no version, config, or DSN info |
| Unbounded health probe connections | DoS | `ReadTimeout: 2s, WriteTimeout: 2s` on health HTTP server; Kubernetes probe frequency is bounded by probe spec |

---

## Sources

### Primary (MEDIUM confidence)
- `go.mod` + codebase grep — verified all existing dependencies, file structure, log call locations
- `internal/gateway/server.go`, `handler.go` — verified chi router structure and existing handler signatures
- `cmd/mq/main.go`, `internal/http/inspect.go` — verified MQ ServeMux structure
- `deployments/charts/*/templates/deployment.yaml` — verified current template shapes (no probes, no resources)
- `deployments/values.yaml` — verified current values structure
- `Makefile` — verified gate targets and .env interaction
- `pkg/db/read.go` — verified two-query SQL patterns for OFFSET addition
- `.planning/phases/*/` artifact listing — verified prompt history sources

### Secondary (LOW confidence)
- [pkg.go.dev/log/slog](https://pkg.go.dev/log/slog) — slog.NewJSONHandler, Logger.With, slog.SetDefault patterns
- [kubernetes.io probes docs](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/) — livenessProbe/readinessProbe YAML fields
- [kubernetes.io HPA v2 reference](https://kubernetes.io/docs/reference/kubernetes-api/autoscaling/horizontal-pod-autoscaler-v2/) — autoscaling/v2 spec structure
- [github.com/marketplace/actions/setup-go-environment](https://github.com/marketplace/actions/setup-go-environment) — actions/setup-go@v6 configuration

### Tertiary (LOW confidence — training knowledge)
- Health endpoint HTTP 200/503 semantics
- Helm `toYaml | nindent` pattern for resources block
- `limit+1` trick for `has_next` detection in offset pagination
- TESTCONTAINERS_RYUK_DISABLED flag for testcontainers in CI

---

## Metadata

**Confidence breakdown:**
- Standard stack: MEDIUM — no new packages; stdlib patterns verified against go.mod
- Architecture: MEDIUM — based on thorough codebase analysis
- Pitfalls: MEDIUM — derived from codebase structure and established Go/k8s patterns
- Kubernetes/Helm patterns: LOW — web-sourced against official docs

**Research date:** 2026-07-03
**Valid until:** 2026-08-03 (Kubernetes API and Helm patterns are stable; slog is stdlib)
