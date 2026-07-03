# Phase 6: Production Hardening + Assignment Alignment - Pattern Map

**Mapped:** 2026-07-03
**Files analyzed:** 22 new/modified files
**Analogs found:** 20 / 22

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/http/health.go` | handler | request-response | `internal/http/inspect.go` | exact |
| `internal/http/health_test.go` | test | request-response | `internal/http/inspect.go` (no existing test, use handler_test.go pattern) | role-match |
| `internal/gateway/server.go` | router | request-response | `internal/gateway/server.go` (extend) | exact |
| `internal/gateway/handler.go` | handler | request-response | `internal/gateway/handler.go` (extend) | exact |
| `internal/gateway/handler_test.go` | test | request-response | `internal/gateway/handler_test.go` (extend) | exact |
| `internal/gateway/integration_test.go` | test | CRUD | `internal/gateway/integration_test.go` (extend) | exact |
| `pkg/db/read.go` | service | CRUD | `pkg/db/read.go` (extend — add OFFSET) | exact |
| `pkg/logger/logger.go` | utility | — | no analog (stdlib only) | none |
| `pkg/logger/logger_test.go` | test | — | `internal/gateway/handler_test.go` (pattern) | partial |
| `cmd/mq/main.go` | config/wiring | request-response | `cmd/mq/main.go` (extend errgroup) | exact |
| `cmd/gateway/main.go` | config/wiring | request-response | `cmd/gateway/main.go` (extend) | exact |
| `cmd/streamer/main.go` | config/wiring | event-driven | `cmd/collector/main.go` (same errgroup shape) | exact |
| `cmd/collector/main.go` | config/wiring | event-driven | `cmd/streamer/main.go` (same errgroup shape) | exact |
| `internal/streamer/streamer.go` | service | event-driven | `internal/collector/collector.go` (export ready state) | role-match |
| `internal/collector/collector.go` | service | event-driven | `internal/streamer/streamer.go` (export ready state) | role-match |
| `deployments/charts/mq/templates/deployment.yaml` | config | — | `deployments/charts/gateway/templates/deployment.yaml` | exact |
| `deployments/charts/gateway/templates/deployment.yaml` | config | — | `deployments/charts/mq/templates/deployment.yaml` | exact |
| `deployments/charts/gateway/templates/hpa.yaml` | config | — | no analog in project | none |
| `deployments/charts/streamer/templates/deployment.yaml` | config | — | `deployments/charts/gateway/templates/deployment.yaml` | exact |
| `deployments/charts/collector/templates/deployment.yaml` | config | — | `deployments/charts/gateway/templates/deployment.yaml` | exact |
| `.github/workflows/ci.yml` | config | — | `Makefile` targets (gate source) | partial |
| `docs/AI_PROMPTS.md` | documentation | — | `.planning/phases/*/` artifacts | partial |

---

## Pattern Assignments

### `internal/http/health.go` (new — handler, request-response)

**Analog:** `internal/http/inspect.go`

**Package declaration + imports** (lines 1–11):
```go
package mqhttp

import (
    "net/http"

    "github.com/ajitg/vantage/internal/server"
)
```

**Handler factory pattern** (lines 42–60 of inspect.go):
```go
// InspectHandler returns an http.HandlerFunc that …
func InspectHandler(srv *server.MQServer) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // …
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(resp) //nolint:errcheck
    }
}
```

**New healthz handler — copy structure, simplify body:**
```go
// HealthzHandler returns 200 OK — process is alive (liveness check).
func HealthzHandler() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    }
}
```

**New readyz handler — uses shutdownCh select pattern from `internal/server/server.go` line 351:**
```go
// ReadyzHandler returns 200 when not shutting down; 503 after Shutdown() is called.
// MQServer.IsShuttingDown() must be added: select { case <-s.shutdownCh: return true; default: return false }
func ReadyzHandler(srv *server.MQServer) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if srv.IsShuttingDown() {
            w.Header().Set("Content-Type", "application/json")
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

**MQServer.IsShuttingDown() — add to `internal/server/server.go` near line 351:**
```go
// IsShuttingDown reports whether Shutdown has been called (no lock needed — reads a channel).
func (s *MQServer) IsShuttingDown() bool {
    select {
    case <-s.shutdownCh:
        return true
    default:
        return false
    }
}
```

---

### `cmd/mq/main.go` (extend — wiring)

**Analog:** `cmd/mq/main.go` (extend existing ServeMux registration block)

**Existing mux registration** (lines 53–54):
```go
mux := http.NewServeMux()
mux.HandleFunc("GET /api/v1/queue/inspect", mqhttp.InspectHandler(mqSrv))
```

**Add after existing registration:**
```go
mux.HandleFunc("GET /healthz", mqhttp.HealthzHandler())
mux.HandleFunc("GET /readyz", mqhttp.ReadyzHandler(mqSrv))
```

**slog migration — existing log.Printf calls** (lines 74, 82, 101, 110, 114):
```go
// Before:
log.Printf("mq: gRPC listening on %s", cfg.GRPCAddr)
// After:
slog.Info("gRPC listening", "addr", cfg.GRPCAddr)

// Before:
log.Printf("mq: GracefulStop timeout — forcing Stop()")
// After:
slog.Warn("GracefulStop timeout — forcing Stop()")

// Before:
log.Fatal(err)
// After:
slog.Error("fatal", "error", err); os.Exit(1)
```

**First two lines of main() after slog migration:**
```go
log := pkglogger.New("mq")   // import alias avoids shadowing
slog.SetDefault(log)
```

---

### `internal/gateway/server.go` (extend — router)

**Analog:** `internal/gateway/server.go` (lines 24–42)

**Existing route registration pattern** (lines 29–38):
```go
r.Route("/api/v1/gpus", func(r chi.Router) {
    r.Get("/", ListGPUs(pool))
    r.Get("/{id}/telemetry", GetTelemetry(pool, cfg.MaxRows))
})
r.Get("/swagger/*", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))
```

**Add at root level BEFORE the api/v1 route group (no swag annotations — not user API):**
```go
r.Get("/healthz", HealthzHandler())
r.Get("/readyz", ReadyzHandler(pool))
```

**HealthzHandler and ReadyzHandler live in `internal/gateway/handler.go`** (same package, no import needed). Gateway ReadyzHandler:
```go
func ReadyzHandler(pool *pgxpool.Pool) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
        defer cancel()
        if pool == nil {
            writeError(w, http.StatusServiceUnavailable, "no pool")
            return
        }
        if err := pool.Ping(ctx); err != nil {
            writeError(w, http.StatusServiceUnavailable, "db unavailable")
            return
        }
        writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
    }
}
```

---

### `internal/gateway/handler.go` (extend — pagination)

**Analog:** `internal/gateway/handler.go` lines 70–156

**Query param parsing pattern to copy** (lines 77–94 — RFC3339 time parsing):
```go
if v := r.URL.Query().Get("start_time"); v != "" {
    t, err := time.Parse(time.RFC3339, v)
    if err != nil {
        writeError(w, http.StatusBadRequest,
            "invalid start_time: expected RFC3339 (e.g. 2006-01-02T15:04:05Z)")
        return
    }
    start = &t
}
```

**New query param parsing for limit/offset (same pattern):**
```go
limit := cfg.MaxRows  // or maxRows captured in closure
if v := r.URL.Query().Get("limit"); v != "" {
    n, err := strconv.Atoi(v)
    if err != nil || n < 1 {
        writeError(w, http.StatusBadRequest, "limit must be a positive integer")
        return
    }
    if n > maxRows {
        n = maxRows
    }
    limit = n
}
offset := 0
if v := r.URL.Query().Get("offset"); v != "" {
    n, err := strconv.Atoi(v)
    if err != nil || n < 0 {
        writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
        return
    }
    offset = n
}
```

**New response types (add above GetTelemetry):**
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

**has_next detection (replaces X-Truncated header pattern on lines 132–135):**
```go
// Fetch limit+1 to detect has_next without a COUNT query.
metrics, err := db.Telemetry(dbCtx, pool, id, start, end, limit+1, offset)
// ...
hasNext := len(metrics) > limit
if hasNext {
    metrics = metrics[:limit]
}
// Remove X-Truncated/X-Row-Limit headers — superseded by TelemetryPage.Pagination.
writeJSON(w, http.StatusOK, TelemetryPage{
    Data:       resp,
    Pagination: PaginationMeta{Limit: limit, Offset: offset, HasNext: hasNext},
})
```

**Updated swag annotation block (replace existing @Summary block for GetTelemetry):**
```go
// @Param  limit  query  int  false  "Max rows to return (default: VANTAGE_GATEWAY_MAX_ROWS; ceiling: VANTAGE_GATEWAY_MAX_ROWS)"  minimum(1)
// @Param  offset query  int  false  "Row offset for pagination (default: 0)"  minimum(0)
// @Success 200  {object}  TelemetryPage
// (Remove @Header X-Truncated and @Header X-Row-Limit lines)
```

---

### `pkg/db/read.go` (extend — add OFFSET)

**Analog:** `pkg/db/read.go` lines 79–149

**Function signature change:**
```go
// Before:
func Telemetry(ctx context.Context, pool *pgxpool.Pool, id string, start, end *time.Time, limit int) ([]models.GpuMetric, error)

// After (add offset parameter):
func Telemetry(ctx context.Context, pool *pgxpool.Pool, id string, start, end *time.Time, limit, offset int) ([]models.GpuMetric, error)
```

**Simple path (lines 103–110) — add OFFSET $3:**
```go
rows, err = pool.Query(ctx,
    `SELECT `+cols+`
     FROM gpu_metrics
     WHERE gpu_id = $1
     ORDER BY timestamp DESC
     LIMIT $2 OFFSET $3`,
    id, limit, offset,
)
```

**Windowed path (lines 117–126) — add OFFSET $5:**
```go
rows, err = pool.Query(ctx,
    `SELECT `+cols+`
     FROM gpu_metrics
     WHERE gpu_id = $1
       AND ($2::timestamptz IS NULL OR timestamp >= $2)
       AND ($3::timestamptz IS NULL OR timestamp <= $3)
     ORDER BY timestamp DESC
     LIMIT $4 OFFSET $5`,
    id, start, end, limit, offset,
)
```

**Error string pattern to preserve** (line 129):
```go
return nil, fmt.Errorf("db: Telemetry: query: %w", err)
```

---

### `pkg/logger/logger.go` (new — utility)

**No analog in codebase.** Use RESEARCH.md pattern directly.

**Pattern:**
```go
package logger

import (
    "log/slog"
    "os"
    "strings"
)

// New creates a JSON slog.Logger with a "service" attribute.
// LOG_LEVEL env var controls verbosity (DEBUG/INFO/WARN/ERROR; default INFO).
// Call slog.SetDefault(New("svc")) in main() to bridge stdlib log.* calls.
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

---

### `cmd/streamer/main.go` and `cmd/collector/main.go` (extend — health listener)

**Analog:** `cmd/mq/main.go` (errgroup goroutine pattern, lines 66–114) and `cmd/collector/main.go` (existing two-goroutine errgroup, lines 21–55).

**Existing errgroup pattern in cmd/streamer/main.go** (lines 34–49):
```go
g, gctx := errgroup.WithContext(ctx)

g.Go(func() error {
    return streamer.Run(gctx, cfg)
})
g.Go(func() error {
    <-gctx.Done()
    return nil
})

if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
    log.Fatal(err)
}
```

**Add third goroutine for health listener (copy HTTP server pattern from cmd/mq/main.go lines 55–60):**
```go
// runner must be a struct with IsReady() bool — refactor streamer.Run → runner.Run
runner := streamer.NewRunner(cfg)  // see internal/streamer/streamer.go changes

g.Go(func() error {
    return runner.Run(gctx)
})
g.Go(func() error {
    healthMux := http.NewServeMux()
    healthMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    })
    healthMux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
        if !runner.IsReady() {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusServiceUnavailable)
            w.Write([]byte(`{"status":"not_ready"}`)) //nolint:errcheck
            return
        }
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    })
    healthSrv := &http.Server{
        Addr:         cfg.HealthAddr,  // new field in Config — default ":9000" (streamer) / ":9001" (collector)
        Handler:      healthMux,
        ReadTimeout:  2 * time.Second,
        WriteTimeout: 2 * time.Second,
    }
    go func() { <-gctx.Done(); healthSrv.Shutdown(context.Background()) }() //nolint:errcheck
    if err := healthSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
        return err
    }
    return nil
})
g.Go(func() error {
    <-gctx.Done()
    return nil
})
```

---

### `internal/streamer/streamer.go` and `internal/collector/collector.go` (extend — readiness state)

**Analog for struct pattern:** `internal/server/server.go` (MQServer struct with shutdownCh channel and once.Do guard). The same "state in a struct, exposed via method" pattern.

**Minimal refactor — add Runner struct wrapping existing Run function:**
```go
// Runner holds streamer state including readiness. Replaces package-level Run function.
type Runner struct {
    cfg   Config
    ready atomic.Bool
}

func NewRunner(cfg Config) *Runner { return &Runner{cfg: cfg} }

// IsReady reports whether the first Produce call succeeded.
func (r *Runner) IsReady() bool { return r.ready.Load() }

// Run is the existing streamer.Run logic, moved to method.
// After first successful Produce: r.ready.Store(true)
func (r *Runner) Run(ctx context.Context) error { /* existing logic */ }
```

---

### Helm deployment templates (extend — probes + resources)

**Analog:** `deployments/charts/gateway/templates/deployment.yaml` (lines 1–33) and `deployments/charts/mq/templates/deployment.yaml` (lines 1–45).

**Existing container block pattern** (gateway deployment, lines 22–33):
```yaml
containers:
  - name: gateway
    image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
    imagePullPolicy: {{ .Values.image.pullPolicy }}
    ports:
      - name: http
        containerPort: 8080
    env:
      - name: GATEWAY_ADDR
        value: ":8080"
```

**Add to all four deployment templates after `env:` block:**
```yaml
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
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

**Streamer/collector: add named health port before env block (no existing ports declaration):**
```yaml
          ports:
            - name: health
              containerPort: 9000   # streamer; 9001 for collector
```
Probe uses `port: health` (the name, not number).

**MQ: probe uses existing `port: http` named port** (already declared at mq deployment line 34).

**Gateway: probe uses existing `port: http`; initialDelaySeconds: 60** (extra time for migrate Job).

---

### Per-chart `values.yaml` additions

**No existing values.yaml for gateway** (file did not exist — create). For mq, streamer, collector — same pattern:

```yaml
resources:
  requests:
    cpu: "100m"      # adjust per service (see sizing table below)
    memory: "32Mi"
  limits:
    cpu: "200m"
    memory: "128Mi"

livenessProbe:
  httpGet:
    path: /healthz
    port: http        # "health" for streamer/collector
  initialDelaySeconds: 30   # 60 for gateway
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 6

readinessProbe:
  httpGet:
    path: /readyz
    port: http
  initialDelaySeconds: 30   # 60 for gateway
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 6
```

**Per-service resource sizing (from RESEARCH.md):**

| Service | cpu req | cpu limit | mem req | mem limit |
|---------|---------|-----------|---------|-----------|
| MQ | 100m | 500m | 64Mi | 256Mi |
| Gateway | 100m | 200m | 32Mi | 128Mi |
| Streamer | 50m | 100m | 16Mi | 64Mi |
| Collector | 50m | 100m | 16Mi | 64Mi |

**Gateway only — add autoscaling block:**
```yaml
autoscaling:
  enabled: false
  minReplicas: 1
  maxReplicas: 3
  targetCPUUtilizationPercentage: 80
```

---

### `deployments/charts/gateway/templates/hpa.yaml` (new — config)

**No analog in project.** Use RESEARCH.md pattern directly.

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

---

### `.github/workflows/ci.yml` (new — CI)

**No analog in project.** Gate targets are sourced from `Makefile`.

**Makefile `check-env` dependency:** `.env` must exist with `DOCKER_HOST` set before any `make` target runs. The Makefile auto-creates `.env` with empty `DOCKER_HOST=` if absent, which causes `check-env` to fail. CI workflow must create `.env` before any `make` invocation.

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
      - name: Prepare .env for CI   # MUST be before any make target
        run: printf 'DOCKER_HOST=unix:///var/run/docker.sock\nTESTCONTAINERS_RYUK_DISABLED=true\n' > .env
      - run: make build
      - run: make test
      - run: make coverage
      - run: make lint
```

---

### `internal/gateway/handler_test.go` (extend — health + pagination)

**Analog:** `internal/gateway/handler_test.go` lines 78–119 (existing route registration + bad param tests)

**Pattern for new health tests (copy route-registration pattern):**
```go
func TestHealthz(t *testing.T) {
    router := gateway.NewRouter(nil, gateway.Config{Addr: ":8080", MaxRows: 1000})
    w := httptest.NewRecorder()
    r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
    router.ServeHTTP(w, r)
    assert.Equal(t, http.StatusOK, w.Code)
    assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
}
```

**Pattern for pagination 400 test (copy TestGetTelemetry_BadTime_Unit lines 103–119):**
```go
func TestGetTelemetry_BadLimit_Unit(t *testing.T) {
    router := gateway.NewRouter(nil, gateway.Config{Addr: ":8080", MaxRows: 1000})
    w := httptest.NewRecorder()
    r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus/GPU-test/telemetry?limit=0", nil)
    router.ServeHTTP(w, r)
    require.Equal(t, http.StatusBadRequest, w.Code)
    var errResp gateway.ErrorResponse
    require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
    assert.NotEmpty(t, errResp.Error)
}
```

**Decode helper update:** Tests currently decode `[]GpuMetricResponse`. After pagination, decode `TelemetryPage`:
```go
var page gateway.TelemetryPage
require.NoError(t, json.NewDecoder(w.Body).Decode(&page))
assert.NotNil(t, page.Data)
assert.Equal(t, expectedLimit, page.Pagination.Limit)
```

---

### `internal/gateway/integration_test.go` (extend — pagination integration tests)

**Analog:** `internal/gateway/integration_test.go` — existing `TestGetTelemetry_ResultCap` test. Read the file to find the decode helper before extending.

**Key addition — TestGetTelemetry_Pagination:**
- Seed 5 rows for one GPU.
- Request limit=2 offset=0 → expect 2 rows, has_next=true.
- Request limit=2 offset=4 → expect 1 row, has_next=false.
- Update `TestGetTelemetry_ResultCap` to decode `TelemetryPage` and check `page.Pagination.HasNext`.

---

### `docs/AI_PROMPTS.md` (new — documentation)

**No code analog.** Mine `.planning/phases/*/` artifacts. RESEARCH.md Section "AI Prompt Log Sourcing" contains the complete table structure and failure points to document. No code pattern needed.

---

## Shared Patterns

### Handler Factory Pattern
**Source:** `internal/http/inspect.go` lines 42–60 AND `internal/gateway/handler.go` lines 70–73
**Apply to:** All new health handler functions (healthz, readyz in both mqhttp and gateway packages)
```go
func XxxHandler(deps ...any) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // …
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
    }
}
```

### writeJSON / writeError
**Source:** `internal/gateway/handler.go` lines 40–50
**Apply to:** Gateway health handlers in `internal/gateway/handler.go` (same package — use directly, no import needed)
```go
func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v) //nolint:errcheck
}
func writeError(w http.ResponseWriter, status int, msg string) {
    writeJSON(w, status, ErrorResponse{Error: msg})
}
```

### errgroup Goroutine Wiring
**Source:** `cmd/mq/main.go` lines 66–114
**Apply to:** `cmd/streamer/main.go`, `cmd/collector/main.go` (add health HTTP goroutine as third g.Go block)
```go
g, gctx := errgroup.WithContext(ctx)
g.Go(func() error { /* primary service */ })
g.Go(func() error { /* health HTTP server with gctx.Done shutdown */ })
g.Go(func() error { <-gctx.Done(); return nil })
if err := g.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) { /* handle */ }
```

### slog Migration
**Source:** 27 existing `log.*` calls across `cmd/*` and `internal/*` files
**Apply to:** ALL non-test files that currently import `"log"`
- `log.Printf("svc: msg %s", val)` → `slog.Info("msg", "key", val)`
- `log.Fatalf("svc: %v", err)` → `slog.Error("msg", "error", err); os.Exit(1)`
- First two lines of every `main()` after migration:
```go
l := pkglogger.New("svc-name")
slog.SetDefault(l)
```
- Import alias `pkglogger` avoids shadowing `slog` package in scope.

### Helm Deployment Container Block
**Source:** `deployments/charts/mq/templates/deployment.yaml` lines 27–44 and `deployments/charts/gateway/templates/deployment.yaml` lines 22–33
**Apply to:** All four sub-chart deployment templates for resources + probe blocks
```yaml
resources:
  {{- toYaml .Values.resources | nindent 12 }}
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
```

### Error String Convention
**Source:** `pkg/db/read.go` lines 129, 143, 147
**Apply to:** Any new pkg/db or pkg/logger functions
```go
return nil, fmt.Errorf("pkg/sub: FuncName: context: %w", err)
// DSN never appears in error strings (ASVS V8)
```

---

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `pkg/logger/logger.go` | utility | — | No structured logging helpers exist yet; no external dep (stdlib slog) |
| `deployments/charts/gateway/templates/hpa.yaml` | config | — | No HPA or autoscaling templates exist in the project |

---

## Metadata

**Analog search scope:** `internal/`, `cmd/`, `pkg/`, `deployments/charts/`
**Files scanned:** 17 source files + 4 deployment templates
**Pattern extraction date:** 2026-07-03
