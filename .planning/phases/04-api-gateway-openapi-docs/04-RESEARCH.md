# Phase 04: api-gateway-openapi-docs — Research

**Researched:** 2026-06-29
**Domain:** Go HTTP API Gateway — chi v5, swaggo/swag v1.16, pgxpool read queries
**Confidence:** MEDIUM (stack is locked by CLAUDE.md; annotation patterns and pgx read shapes verified via official pkg.go.dev)

---

## Summary

Phase 4 delivers the API Gateway microservice: a read-only HTTP server that queries `gpu_metrics` in PostgreSQL and exposes three endpoints documented by an auto-generated OpenAPI spec. The tech stack is fully locked (chi v5.3.0, swag v1.16.6, http-swagger v2.0.2, pgxpool v5.10.0) by CLAUDE.md. No alternatives were evaluated.

The gateway is architecturally simple: a thin chi router wraps two query functions that read from `pkg/db`-managed pgxpool. The OpenAPI spec is 100% generated from `swag` annotations on the handlers — no hand-written YAML/JSON. Swagger UI is served at `/swagger/*` via the `http-swagger/v2` package.

The primary implementation risks are (1) a documentation output path discrepancy in the Makefile vs CLAUDE.md text (use `pkg/docs` — the Makefile is authoritative), (2) the coverage gate must be extended to exclude `pkg/docs` as it does `pkg/pb`, and (3) the composite index `idx_gpu_metrics_gpu_id_ts ON gpu_metrics (gpu_id, timestamp DESC)` must be verified used by `EXPLAIN` in the integration test.

Five design decisions are deferred to open questions below; the planner must resolve them before task creation.

**Primary recommendation:** Follow the established `internal/<service>/` pattern: `internal/gateway/` owns handler + config; `cmd/gateway/main.go` is the thin composition root with swag header annotations. The generated docs package lives at `pkg/docs/` (committed, not gitignored).

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| API-01 | `GET /api/v1/gpus` returns unique list of GPU IDs from Postgres | `SELECT DISTINCT gpu_id FROM gpu_metrics ORDER BY gpu_id` via pgxpool.Query; chi route `r.Get("/api/v1/gpus", ...)` |
| API-02 | `GET /api/v1/gpus/{id}/telemetry` returns that GPU's telemetry ordered by time | `SELECT ... FROM gpu_metrics WHERE gpu_id=$1 ORDER BY timestamp DESC`; chi.URLParam(r, "id") |
| API-03 | `GET /api/v1/gpus/{id}/telemetry?start_time=…&end_time=…` filters by time window, MUST use composite index | `WHERE gpu_id=$1 AND timestamp>=$2 AND timestamp<=$3 ORDER BY timestamp DESC` — index `idx_gpu_metrics_gpu_id_ts` satisfies the predicate + ORDER BY; EXPLAIN must confirm Index Scan |
| API-04 | OpenAPI spec fully auto-generated from swag annotations (no hand-written spec) | `swag init -g cmd/gateway/main.go -o pkg/docs`; file-level `@title/@version/@BasePath` + per-handler `@Summary/@Tags/@Produce/@Param/@Success/@Failure/@Router`; Swagger UI at `/swagger/*` via http-swagger |
</phase_requirements>

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| `GET /api/v1/gpus` (list GPU IDs) | API/Backend | — | Read-only query against PostgreSQL; result is a JSON array with no client-state |
| `GET /api/v1/gpus/{id}/telemetry` (time-series rows) | API/Backend | — | Parameterized query against PostgreSQL; heavy I/O path; no frontend state |
| Time-window filtering (start_time/end_time) | API/Backend | — | Parameter parsing + SQL predicate; must use composite index at the DB tier |
| Swagger UI (`/swagger/*`) | API/Backend (embedded static) | — | http-swagger embeds Swagger UI assets in binary via `go:embed`; no separate CDN needed |
| OpenAPI spec generation | Build time | — | `swag init` runs at build time; output committed to `pkg/docs/`; not a runtime concern |

---

## Standard Stack

### Core (add to go.mod for Phase 4)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/go-chi/chi/v5` | v5.3.0 | HTTP router — path variables `{id}`, middleware | Locked by CLAUDE.md; 100% net/http compatible; zero external deps |
| `github.com/swaggo/http-swagger/v2` | v2.0.2 | Serves Swagger UI at `/swagger/*` | Locked by CLAUDE.md; pairs with swag v1.16.6; uses `go:embed` |

### Already in go.mod (reused)

| Library | Version | Purpose | Notes |
|---------|---------|---------|-------|
| `github.com/jackc/pgx/v5` | v5.10.0 | `pgxpool.Query` / `QueryRow` for read queries | `pkg/db` already provides pool; gateway reuses `db.New()` / `db.Migrate()` |
| `github.com/stretchr/testify` | v1.11.1 | Test assertions | Present from Phase 2 |
| `github.com/testcontainers/testcontainers-go` | v0.43.0 | Postgres container in integration tests | Established in Phase 3 |
| `github.com/testcontainers/testcontainers-go/modules/postgres` | v0.43.0 | `postgres.Run()` / `Snapshot()` / `Restore()` | Established in Phase 3 |

### CLI Tool (not in go.mod — install once via `make tools`)

| Tool | Version | Purpose | Install |
|------|---------|---------|---------|
| `swag` CLI | v1.16.6 | `swag init` generates `pkg/docs/` | `go install github.com/swaggo/swag/cmd/swag@v1.16.6` |

**Installation for Phase 4 deps:**
```bash
cd /path/to/vantage
go get github.com/go-chi/chi/v5@v5.3.0
go get github.com/swaggo/http-swagger/v2@v2.0.2
go mod tidy
```

**Makefile swagger target (existing — output is `pkg/docs`, not `docs/`):**
```bash
make swagger   # runs: swag init -g cmd/gateway/main.go -o pkg/docs
```

Note: CLAUDE.md text says `--output docs/` but the **Makefile is authoritative**: `-o pkg/docs`. The planner should treat `pkg/docs` as the canonical output path. The import path in `cmd/gateway/main.go` is `_ "github.com/ajitg/vantage/pkg/docs"`.

---

## Package Legitimacy Audit

The Go ecosystem does not use npm/PyPI/crates; the seam legitimacy check is for those ecosystems. Both packages are locked by CLAUDE.md and were confirmed on `pkg.go.dev`.

| Package | Registry | Published | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----------|-----------|-------------|---------|-------------|
| `github.com/go-chi/chi/v5` | pkg.go.dev | May 22, 2026 (v5.3.0) | High — widely used | github.com/go-chi/chi | OK [CITED: pkg.go.dev] | Approved |
| `github.com/swaggo/http-swagger/v2` | pkg.go.dev | Aug 30, 2023 (v2.0.2) | Moderate | github.com/swaggo/http-swagger | OK [CITED: pkg.go.dev] | Approved |
| `github.com/swaggo/swag` | pkg.go.dev | v1.16.6 stable | High | github.com/swaggo/swag | OK [CITED: pkg.go.dev] | Approved (CLI tool) |

**Packages removed due to SLOP verdict:** none
**Packages flagged as suspicious SUS:** none

---

## Architecture Patterns

### System Architecture Diagram

```
HTTP Client
    │ GET /api/v1/gpus
    │ GET /api/v1/gpus/{id}/telemetry[?start_time=…&end_time=…]
    │ GET /swagger/*
    ▼
cmd/gateway/main.go
    │ signal.NotifyContext → errgroup
    │ db.Migrate() → db.New() → *pgxpool.Pool
    ▼
internal/gateway/server.go (chi.NewRouter)
    ├── GET /api/v1/gpus              → handler.ListGPUs(pool)
    ├── GET /api/v1/gpus/{id}/telemetry → handler.GetTelemetry(pool)
    └── GET /swagger/*                → httpSwagger.Handler(...)
         │
         ▼  (side-effect import)
     pkg/docs/   ← swag-generated; registered on init()
         │
         ▼
    http-swagger embeds Swagger UI assets

handler.ListGPUs:
    pool.Query("SELECT DISTINCT gpu_id FROM gpu_metrics ORDER BY gpu_id")
    → JSON array of strings

handler.GetTelemetry:
    chi.URLParam(r, "id") → gpu_id
    r.URL.Query().Get("start_time") / "end_time" → time.Parse(time.RFC3339, ...)
    pool.Query("SELECT ... FROM gpu_metrics WHERE gpu_id=$1 [AND timestamp>=/$2 AND timestamp<=/$3] ORDER BY timestamp DESC [LIMIT n]")
    → JSON array of GpuMetricResponse

PostgreSQL
    idx_gpu_metrics_gpu_id_ts (gpu_id, timestamp DESC)
    ↑ used by WHERE gpu_id=$1 [AND timestamp range] ORDER BY timestamp DESC
```

### Recommended Project Structure

Following the `internal/<service>/` pattern established in Phases 1–3:

```
cmd/gateway/
└── main.go           # thin composition root: swag header annotations, errgroup, db.New(), chi router

internal/gateway/
├── config.go         # Config struct + FromEnv(); GATEWAY_ADDR default ":8080"
├── server.go         # NewRouter(pool) → chi.Router; mounts all routes + swagger
├── handler.go        # ListGPUs, GetTelemetry — handler functions with swag annotations
├── handler_test.go   # unit tests: httptest.NewRecorder, stub pool (no DB)
└── integration_test.go  # //go:build integration; testcontainers postgres:17-alpine

pkg/docs/             # generated by `make swagger`; COMMITTED (per .gitignore comment)
├── docs.go
├── swagger.json
└── swagger.yaml
```

### Pattern 1: swag Header Annotations (cmd/gateway/main.go)

The file-level annotations MUST appear immediately above the `package main` declaration or the `main()` func. `swag init` scans the file passed via `-g`.

```go
// Package main is the entrypoint for the API Gateway microservice.
//
// @title          Vantage GPU Telemetry API
// @version        1.0
// @description    Read-only REST API for querying GPU telemetry stored in PostgreSQL.
// @BasePath       /api/v1
// @host           localhost:8080
// @schemes        http
package main
```

Source: [CITED: github.com/swaggo/swag README — API General Info annotations]

### Pattern 2: Handler Annotations — GET /api/v1/gpus

```go
// ListGPUs godoc
// @Summary     List GPU IDs
// @Description Returns the unique list of GPU UUIDs that have telemetry data.
// @Tags        gpus
// @Produce     json
// @Success     200  {array}   string
// @Failure     500  {object}  ErrorResponse
// @Router      /gpus [get]
func ListGPUs(pool *pgxpool.Pool) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) { ... }
}
```

Note: `@Router /gpus [get]` — the path is relative to `@BasePath /api/v1`, so the full URL is `/api/v1/gpus`. [CITED: swag README — swag generates full path from BasePath + Router]

### Pattern 3: Handler Annotations — GET /api/v1/gpus/{id}/telemetry

```go
// GetTelemetry godoc
// @Summary     Get GPU telemetry
// @Description Returns telemetry rows for a specific GPU, ordered by timestamp descending.
//              Optionally filtered to a time window via RFC3339 query parameters.
// @Tags        gpus
// @Produce     json
// @Param       id         path   string  true   "GPU UUID (e.g. GPU-5fd4f087-…)"
// @Param       start_time query  string  false  "Start time, inclusive (RFC3339, e.g. 2024-01-01T00:00:00Z)" Format(date-time)
// @Param       end_time   query  string  false  "End time, inclusive (RFC3339)"                              Format(date-time)
// @Success     200  {array}   GpuMetricResponse
// @Failure     400  {object}  ErrorResponse
// @Failure     404  {object}  ErrorResponse
// @Failure     500  {object}  ErrorResponse
// @Router      /gpus/{id}/telemetry [get]
func GetTelemetry(pool *pgxpool.Pool) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) { ... }
}
```

Source: [CITED: pkg.go.dev/github.com/swaggo/swag — @Param format: name, in, type, required, description]

### Pattern 4: chi Routing Setup

```go
// Source: [CITED: pkg.go.dev/github.com/go-chi/chi/v5]
func NewRouter(pool *pgxpool.Pool) http.Handler {
    r := chi.NewRouter()
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)

    r.Route("/api/v1/gpus", func(r chi.Router) {
        r.Get("/", ListGPUs(pool))
        r.Get("/{id}/telemetry", GetTelemetry(pool))
    })

    // Swagger UI — serves index.html at /swagger/ and doc.json at /swagger/doc.json
    r.Get("/swagger/*", httpSwagger.Handler(
        httpSwagger.URL("/swagger/doc.json"),
    ))

    return r
}
```

Key details:
- `chi.URLParam(r, "id")` extracts the `{id}` path variable inside handlers [CITED: chi README]
- `r.URL.Query().Get("start_time")` for query parameters — standard `net/http`
- Handler signature is `func(w http.ResponseWriter, r *http.Request)` — pure `net/http`

### Pattern 5: pgx Read Queries

**DISTINCT gpu_id list:**
```go
// Source: [CITED: pkg.go.dev/github.com/jackc/pgx/v5/pgxpool]
rows, err := pool.Query(ctx,
    "SELECT DISTINCT gpu_id FROM gpu_metrics ORDER BY gpu_id")
if err != nil {
    http.Error(w, `{"error":"db query failed"}`, http.StatusInternalServerError)
    return
}
defer rows.Close()

var ids []string
for rows.Next() {
    var id string
    if err := rows.Scan(&id); err != nil {
        http.Error(w, `{"error":"scan failed"}`, http.StatusInternalServerError)
        return
    }
    ids = append(ids, id)
}
if err := rows.Err(); err != nil {
    http.Error(w, `{"error":"rows error"}`, http.StatusInternalServerError)
    return
}
```

**Time-range telemetry query (composite-index path):**
```go
const telemetrySQL = `
SELECT gpu_id, timestamp, metric_name, value, device, model_name,
       hostname, container, pod, namespace, labels_raw
FROM gpu_metrics
WHERE gpu_id = $1
  AND ($2::timestamptz IS NULL OR timestamp >= $2)
  AND ($3::timestamptz IS NULL OR timestamp <= $3)
ORDER BY timestamp DESC
LIMIT $4`

// Call with: gpuID, startTime (or nil), endTime (or nil), limit
rows, err := pool.Query(ctx, telemetrySQL, gpuID, startOrNil, endOrNil, limit)
```

Alternative — two separate queries (simpler SQL, avoids nullable cast):
- No time params: `WHERE gpu_id = $1 ORDER BY timestamp DESC LIMIT $2`
- With time params: `WHERE gpu_id = $1 AND timestamp >= $2 AND timestamp <= $3 ORDER BY timestamp DESC LIMIT $4`

The two-query approach is preferred for test clarity and EXPLAIN readability. [ASSUMED]

**EXPLAIN expectation for time-range query:**
```sql
EXPLAIN (ANALYZE, FORMAT TEXT)
SELECT ... FROM gpu_metrics
WHERE gpu_id = 'GPU-abc' AND timestamp >= '2024-01-01' AND timestamp <= '2024-12-31'
ORDER BY timestamp DESC LIMIT 100;
```
Expected plan node: `Index Scan using idx_gpu_metrics_gpu_id_ts on gpu_metrics` with
`Index Cond: ((gpu_id = $1) AND (timestamp >= $2) AND (timestamp <= $3))`
— the index `(gpu_id, timestamp DESC)` covers the equality predicate on `gpu_id`, the range on `timestamp`, and the `ORDER BY timestamp DESC` direction without an extra sort step. [ASSUMED: PostgreSQL planner behavior; verify in integration test]

### Pattern 6: Response DTO

Define a gateway-local struct with JSON tags. Do NOT add JSON tags to `pkg/models.GpuMetric` — that struct is a DB write contract, not an HTTP contract.

```go
// GpuMetricResponse is the JSON representation returned by GET /api/v1/gpus/{id}/telemetry.
// Defined here to keep HTTP serialization separate from the domain model in pkg/models.
type GpuMetricResponse struct {
    GpuID      string    `json:"gpu_id"`
    Timestamp  time.Time `json:"timestamp"`      // swag renders as "string, format: date-time"
    MetricName string    `json:"metric_name"`
    Value      float64   `json:"value"`
    Device     string    `json:"device,omitempty"`
    ModelName  string    `json:"model_name,omitempty"`
    Hostname   string    `json:"hostname,omitempty"`
    Container  string    `json:"container,omitempty"`
    Pod        string    `json:"pod,omitempty"`
    Namespace  string    `json:"namespace,omitempty"`
    LabelsRaw  string    `json:"labels_raw,omitempty"`
}

// ErrorResponse is the JSON envelope for 4xx/5xx errors.
type ErrorResponse struct {
    Error string `json:"error"`
}
```

### Pattern 7: http-swagger Wiring

The `cmd/gateway/main.go` must import the generated docs package as a side effect so the `init()` function registers the spec:

```go
import (
    _ "github.com/ajitg/vantage/pkg/docs"               // registers swagger spec on init
    httpSwagger "github.com/swaggo/http-swagger/v2"
)
```

The handler `httpSwagger.URL("/swagger/doc.json")` points to the JSON spec served by http-swagger at that sub-path. The Swagger UI lands at `/swagger/index.html` (also `/swagger/`). [CITED: pkg.go.dev/github.com/swaggo/http-swagger/v2]

### Pattern 8: Composition Root (cmd/gateway/main.go)

Following `cmd/mq/main.go` and `cmd/collector/main.go` exactly:

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    // 1. DB config + migration (idempotent, advisory-locked)
    dbCfg, err := db.FromEnv()
    if err != nil { log.Fatalf("gateway: db config: %v", err) }
    if err := db.Migrate(ctx, dbCfg.DSN); err != nil { log.Fatalf("gateway: migrate: %v", err) }
    pool, err := db.New(ctx, dbCfg)
    if err != nil { log.Fatalf("gateway: db pool: %v", err) }
    defer pool.Close()

    // 2. Chi router
    cfg := gateway.FromEnv()
    router := gateway.NewRouter(pool)
    srv := &http.Server{Addr: cfg.Addr, Handler: router,
        ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second}

    // 3. errgroup shutdown coordination (pattern from cmd/mq)
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error {
        log.Printf("gateway: HTTP on %s", cfg.Addr)
        return srv.ListenAndServe()
    })
    g.Go(func() error {
        <-gctx.Done()
        shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        return srv.Shutdown(shutCtx)
    })

    if err := g.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        log.Fatal(err)
    }
}
```

### Pattern 9: Handler Unit Tests (no DB)

Follow `internal/http/inspect_test.go` pattern — `httptest.NewRecorder` + `httptest.NewRequest`:

```go
func TestListGPUs_Empty(t *testing.T) {
    // Use testcontainers postgres in integration tests; for unit tests
    // use a stub or an in-memory fake that satisfies the query interface.
    // Simplest approach for MVP: test handler with a real (testcontainers) pool
    // in an integration test; unit test validates routing + JSON shape only.
    w := httptest.NewRecorder()
    r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus", nil)
    // ... call handler directly, assert response ...
}
```

For chi route parameter tests, mount the handler on a chi router and use `httptest`:
```go
router := chi.NewRouter()
router.Get("/api/v1/gpus/{id}/telemetry", GetTelemetry(pool))
r := httptest.NewRequest("GET", "/api/v1/gpus/GPU-abc/telemetry", nil)
w := httptest.NewRecorder()
router.ServeHTTP(w, r)
// chi.URLParam only works when routed through chi — not when calling handler directly
```

### Anti-Patterns to Avoid

- **Hand-writing any OpenAPI YAML/JSON:** The spec MUST be 100% generated by `swag init`. Adding any spec file manually breaks API-04.
- **Importing pkg/models.GpuMetric directly for JSON serialization:** It has no JSON tags; adding tags there pollutes the DB write contract. Use a gateway-local DTO.
- **Calling `swag init` from inside the Go binary at startup:** `swag` is a CLI tool run at build time only, not at runtime.
- **Forgetting `_ "github.com/ajitg/vantage/pkg/docs"` import:** Without the side-effect import, the swagger spec is not registered and http-swagger serves no content.
- **Calling handler directly in route-param tests:** `chi.URLParam(r, "id")` returns empty string if the request wasn't routed through chi. Always route via `router.ServeHTTP(w, r)` in tests.
- **Using `docs/` as the swagger output directory:** The Makefile uses `pkg/docs`. A mismatch breaks the import path.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HTTP routing with path vars | Custom `strings.Split` on URL | `chi.URLParam(r, "id")` | chi handles patterns, wildcards, trailing-slash, method dispatch |
| Swagger UI embed | Copy UI assets into `static/` | `http-swagger/v2` + `go:embed` | http-swagger embeds Swagger UI v5 assets as a Go `embed.FS`; no manual asset management |
| OpenAPI spec | Write `swagger.json` by hand | `swag init` annotations | Hand-written specs drift from code; swag guarantees spec-code parity |
| Query param time parsing | Custom string → time logic | `time.Parse(time.RFC3339, ...)` | RFC3339 is the documented format; one parse call + check for `err != nil` is sufficient |
| pgxpool management | Open a new `pgxpool.Pool` in gateway | Reuse `pkg/db.New(ctx, cfg)` | `pkg/db` already handles `MaxConns`, `HealthCheckPeriod`, `Ping` validation, and DSN security |

**Key insight:** The gateway is intentionally thin. Every "infrastructure" problem (routing, docs, DB pooling, UI embedding) has an existing locked library solution. The only custom code is: SQL queries, request parsing, JSON response encoding, and the swag annotations.

---

## Makefile Coverage Gate — Required Fix

The `coverage` target currently excludes only `pkg/pb`:

```makefile
# CURRENT (line 52):
PKGS=$$(go list ./internal/... ./pkg/... | grep -v '/pkg/pb')
```

After Phase 4, `pkg/docs/` is generated code (like `pkg/pb/`) and MUST also be excluded:

```makefile
# REQUIRED UPDATE for Phase 4:
PKGS=$$(go list ./internal/... ./pkg/... | grep -v '/pkg/pb\|/pkg/docs')
```

The planner MUST include a task to update this line before or as part of the coverage gate task.

---

## Open Questions (Design Forks)

The planner must resolve these before creating tasks. Recommended defaults are given.

### OQ-1: Default result cap for telemetry endpoint

**What:** `GET /api/v1/gpus/{id}/telemetry` can return 100k+ rows per GPU. REQUIREMENTS.md says pagination is out of scope. But an unbounded query is operationally dangerous.

**Options:**
- A) Apply `LIMIT 1000` hardcoded (simple, safe, not pagination)
- B) Apply configurable cap via `VANTAGE_GATEWAY_MAX_ROWS` env var (default 1000)
- C) No cap — return all rows (spec-pure but risky)

**Recommended default:** Option B — configurable cap, default 1000. This is not pagination (no cursor/offset), just a safety ceiling. Documents the decision in a code comment.

### OQ-2: Empty result vs 404 for unknown GPU id

**What:** When `GET /api/v1/gpus/{id}/telemetry` receives an `id` that has no rows in `gpu_metrics`, should it return `200 []` or `404`?

**Options:**
- A) `200 []` — REST-idiomatic: the collection exists for any valid id, it just has no members
- B) `404` if the GPU id is not in `SELECT DISTINCT gpu_id FROM gpu_metrics` — resource does not exist

**Recommended default:** Option B — `404` for unknown GPU ids. A GPU that has never reported is a meaningful "not found" in this domain. Requires a pre-check query or checking `len(rows) == 0` after the telemetry query. Note: a GPU with data outside the time window should return `200 []`, not `404`.

### OQ-3: Omitted time params on the telemetry endpoint (API-03 vs API-02 unification)

**What:** API-02 and API-03 are the same route. If `start_time` and `end_time` are both absent, the endpoint degrades to "all telemetry" (API-02 behavior). If only one is provided, what happens?

**Options:**
- A) Partial time params are allowed: omit start → unbounded lower, omit end → unbounded upper
- B) Require both or neither; return `400` if only one is provided

**Recommended default:** Option A — partial params are allowed and practical. The two-query approach (no params → simpler SQL; both params → filtered SQL) with optional single-bound handling is the most flexible for callers.

### OQ-4: Time param validation error response

**What:** If `start_time` or `end_time` is provided but not valid RFC3339, the handler should return a `400`. What JSON shape?

**Recommended default:** `{"error": "invalid start_time: must be RFC3339 (e.g. 2024-01-01T00:00:00Z)"}` — uses the `ErrorResponse` struct already defined above. Simple and consistent.

### OQ-5: `cmd/gateway/main.go` vs `internal/gateway/` for swag `@Router` annotations

**What:** `swag init -g cmd/gateway/main.go` scans the given file and follows imports. Handler annotations in `internal/gateway/handler.go` are picked up if `cmd/gateway/main.go` imports the `gateway` package. This is the standard swag pattern.

**Confirmed approach:** File-level annotations (`@title`, `@BasePath`) in `cmd/gateway/main.go`; handler-level annotations (`@Summary`, `@Router`, etc.) in `internal/gateway/handler.go`. `swag init` follows the import tree automatically. [CITED: swag README — `swag init -g` scans the main file and all transitively imported packages]

---

## Common Pitfalls

### Pitfall 1: Forgetting the docs side-effect import

**What goes wrong:** `internal/gateway/server.go` mounts `httpSwagger.Handler(...)` but `/swagger/` serves a blank page or 404.
**Why it happens:** The `pkg/docs.init()` function registers the embedded spec with the `swaggerFiles` package. Without `import _ "github.com/ajitg/vantage/pkg/docs"` in `cmd/gateway/main.go`, `init()` never runs and the spec is unregistered.
**How to avoid:** Add the blank import to `cmd/gateway/main.go`. The Go compiler rejects unused imports, so this will be caught if omitted — but only if `pkg/docs` exists. Before first `make swagger` run, the import doesn't exist yet, so Wave 0 must include running `make swagger` before `go build`.
**Warning signs:** `swag init` succeeded but Swagger UI shows "Failed to load API definition" or empty `doc.json`.

### Pitfall 2: chi.URLParam returns empty string when handler called directly

**What goes wrong:** `TestGetTelemetry` calls `GetTelemetry(pool)(w, r)` directly (without chi router). `chi.URLParam(r, "id")` returns `""`. Queries return no rows. Test passes for wrong reasons.
**Why it happens:** chi stores URL params in `r.Context()` during route resolution. If the handler is invoked without going through `router.ServeHTTP(w, r)`, the context has no params.
**How to avoid:** In tests, always route through a minimal chi router: `router.Get("/{id}/telemetry", handler); router.ServeHTTP(w, r)`.
**Warning signs:** `chi.URLParam` returns empty string; handler returns 404 for any id.

### Pitfall 3: Composite index not used for DISTINCT gpu_id query

**What goes wrong:** `SELECT DISTINCT gpu_id FROM gpu_metrics` uses a sequential scan (SeqScan) when the table is large.
**Why it happens:** PostgreSQL's DISTINCT can use the index only under certain conditions. A plain `SELECT DISTINCT gpu_id` may use HashAggregate + SeqScan if the planner decides it's cheaper.
**How to avoid:** For the Phase 4 MVP, this is acceptable — the `/gpus` endpoint returns only IDs, not metrics data, and the query is infrequent. If profiling shows slow DISTINCT, use: `SELECT gpu_id FROM gpu_metrics GROUP BY gpu_id ORDER BY gpu_id` — the GROUP BY path more reliably uses an index scan. Document this as a known trade-off.
**Warning signs:** EXPLAIN shows `Seq Scan on gpu_metrics` for the `/gpus` endpoint with a large table.

### Pitfall 4: `make swagger` run order in Wave 0

**What goes wrong:** `go build ./cmd/gateway` fails because `pkg/docs` does not exist yet, but `cmd/gateway/main.go` imports `_ "github.com/ajitg/vantage/pkg/docs"`.
**Why it happens:** `pkg/docs` is a generated package. It must be created by `make swagger` before `go build` can succeed.
**How to avoid:** Wave 0 (bootstrap) task: run `make swagger` once with a stub `cmd/gateway/main.go` containing only the header annotations and an empty `main()`. This generates `pkg/docs/`. Then implement the handlers.
**Warning signs:** `cannot find package "github.com/ajitg/vantage/pkg/docs"`.

### Pitfall 5: Makefile coverage gate fails because pkg/docs is included

**What goes wrong:** `make coverage` fails because `pkg/docs/` contains generated code with 0% coverage, pulling the total below 90%.
**Why it happens:** The coverage command currently excludes only `pkg/pb`: `grep -v '/pkg/pb'`. After Phase 4, `pkg/docs` is generated like `pkg/pb`.
**How to avoid:** Update the Makefile: `grep -v '/pkg/pb\|/pkg/docs'`. This is a required task in Wave 1 or Wave 0.
**Warning signs:** `make coverage` fails with `total coverage: XX% < 90%` after swagger generation.

### Pitfall 6: Time param timezone — must be UTC

**What goes wrong:** Caller sends `start_time=2024-01-01T00:00:00+05:30`. PostgreSQL `TIMESTAMPTZ` stores UTC; comparison works, but the app must parse using `time.Parse(time.RFC3339, ...)` not `time.ParseInLocation`. RFC3339 supports offset notation, and `time.Parse(time.RFC3339, ...)` normalizes to UTC.
**Why it happens:** Using `time.RFC3339Nano` vs `time.RFC3339` — RFC3339 query params are typically whole-second (RFC3339). Try RFC3339 first, then RFC3339Nano as fallback (matches the `models.FromProto` pattern already in the codebase).
**How to avoid:** Use `time.Parse(time.RFC3339, v)` for query params; return clear 400 on parse failure.
**Warning signs:** Time-range queries return empty results despite data existing in the window.

---

## Validation Architecture

`workflow.nyquist_validation` is `true` in `.planning/config.json` — this section is required.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | `testing` (stdlib) + `testify` v1.11.1 |
| Integration tag | `//go:build integration` (established pattern) |
| Quick run (unit) | `go test -race ./internal/gateway/... -count=1` |
| Full suite | `go test -race -count=1 -tags=integration ./internal/gateway/...` |
| Coverage gate | `make coverage` (update Makefile to exclude `pkg/docs`) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| API-01 | `GET /api/v1/gpus` returns `[]string` of known GPU UUIDs | integration | `go test -tags=integration -run TestListGPUs ./internal/gateway/...` | Wave 0 |
| API-02 | `GET /api/v1/gpus/{id}/telemetry` returns rows for known GPU, ordered by `timestamp DESC` | integration | `go test -tags=integration -run TestGetTelemetry_NoFilter ./internal/gateway/...` | Wave 0 |
| API-03 | Time-window filter returns only rows in range; EXPLAIN shows Index Scan on `idx_gpu_metrics_gpu_id_ts` | integration | `go test -tags=integration -run TestGetTelemetry_TimeWindow ./internal/gateway/...` | Wave 0 |
| API-04 | Swagger UI serves at `/swagger/`, `doc.json` is valid JSON with ≥ 3 paths defined | unit + smoke | `go test -run TestSwaggerUI ./internal/gateway/...` + `make smoke-04` | Wave 0 |
| OQ-2 | `404` for unknown GPU id | integration | `go test -tags=integration -run TestGetTelemetry_UnknownGPU ./internal/gateway/...` | Wave 0 |
| OQ-1 | Result cap applied; capped at VANTAGE_GATEWAY_MAX_ROWS | integration | `go test -tags=integration -run TestGetTelemetry_ResultCap ./internal/gateway/...` | Wave 0 |

### Integration Test Pattern (per Phase 3 collector pattern)

```go
//go:build integration

package gateway_test

import (
    "context"
    "log"
    "os"
    "testing"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/modules/postgres"

    "github.com/ajitg/vantage/pkg/db"
)

var testPool *pgxpool.Pool
var testCtr *postgres.PostgresContainer

func TestMain(m *testing.M) {
    ctx := context.Background()
    ctr, err := postgres.Run(ctx, "postgres:17-alpine",
        postgres.WithDatabase("vantage_test"),
        postgres.WithUsername("postgres"),
        postgres.WithPassword("secret"),
        postgres.BasicWaitStrategies(),
        postgres.WithSQLDriver("pgx"),
    )
    if err != nil { log.Fatalf("start postgres: %v", err) }
    defer testcontainers.TerminateContainer(ctr)
    testCtr = ctr

    dsn := ctr.MustConnectionString(ctx, "sslmode=disable")
    if err := db.Migrate(ctx, dsn); err != nil { log.Fatalf("migrate: %v", err) }
    if err := ctr.Snapshot(ctx); err != nil { log.Fatalf("snapshot: %v", err) }

    pool, err := db.New(ctx, db.Config{DSN: dsn, MaxConns: 5})
    if err != nil { log.Fatalf("pool: %v", err) }
    testPool = pool
    defer pool.Close()

    os.Exit(m.Run())
}
```

Run with: `DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=integration ./internal/gateway/... -count=1`

### Sampling Rate

- **Per task commit:** `go test -race ./internal/gateway/... -count=1` (unit only, ~5s)
- **Per wave merge:** `make test` (full suite with race detector)
- **Phase gate:** `make coverage` (≥90% line, with Makefile fix to exclude `pkg/docs`)

### Wave 0 Gaps (files to create before implementation)

- [ ] `internal/gateway/handler_test.go` — unit tests for routing + JSON shape
- [ ] `internal/gateway/integration_test.go` — testcontainers integration tests
- [ ] Run `make swagger` once after stub `cmd/gateway/main.go` is written — creates `pkg/docs/`
- [ ] Update Makefile coverage command: `grep -v '/pkg/pb\|/pkg/docs'`
- [ ] `scripts/smoke/phase04-gateway.sh` — curl-based smoke against `make dev-up` stack

---

## Security Domain

`security_enforcement: true`, `security_asvs_level: 1` in `.planning/config.json`.

### Applicable ASVS Categories (ASVS Level 1)

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | Authentication is explicitly out of scope per REQUIREMENTS.md |
| V3 Session Management | No | Read-only public API; no sessions |
| V4 Access Control | No | No access control in scope |
| V5 Input Validation | Yes | RFC3339 time param parsing with explicit `err != nil` check → `400`; path param `{id}` is passed as a `$1` bind param (SQL injection not possible via `pgxpool.Query` parameterized queries) |
| V6 Cryptography | No | No encryption handled in the gateway |
| V8 Data Protection | Yes (DSN) | `VANTAGE_DB_DSN` must not appear in logs or error strings — enforced by `pkg/db` already; gateway must not log the DSN |

### Known Threat Patterns

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| SQL injection via gpu_id path param | Tampering | pgxpool parameterized queries (`$1`) — fully prevented |
| SQL injection via time params | Tampering | `time.Parse()` validates the value; only the parsed `time.Time` is bound, never the raw string |
| Unbounded DB query (DoS) | Denial of Service | Default `LIMIT` cap (OQ-1 above) prevents single requests from returning millions of rows |
| DSN credential leak in logs | Information Disclosure | `pkg/db.FromEnv()` never includes DSN in error strings; gateway must not call `log.Printf("%v", dbCfg)` |

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | Build all services | Yes | 1.26.2 | — |
| Docker | testcontainers integration tests | Yes (Rancher Desktop) | via `$HOME/.rd/docker.sock` | Skip integration tests |
| PostgreSQL (compose) | Smoke tests | Yes (via `make dev-up`) | postgres:17-alpine | — |
| `swag` CLI | `make swagger` | Not yet installed for v1.16.6 | — | `go install github.com/swaggo/swag/cmd/swag@v1.16.6` |

**Missing dependencies with no fallback:** `swag` CLI must be installed before `make swagger` can run. Wave 0 task: `go install github.com/swaggo/swag/cmd/swag@v1.16.6`.

**Note:** Makefile `tools` target installs `swag@latest`, but CLAUDE.md pins v1.16.6. Use the pinned version for reproducibility.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hand-written `swagger.json` | `swag init` from annotations | ~2019 (swag v1.x stable) | Spec always matches code; no drift |
| net/http `http.ServeMux` (Go 1.22+) for path vars | chi v5 for `{id}` patterns | Ongoing | chi handles regex patterns, subrouters, wildcard; 1.22 ServeMux supports `{id}` but lacks middleware chain |
| Separate static dir for Swagger UI | `go:embed` in http-swagger v2 | 2021 (Go 1.16+) | Zero external assets; binary self-contained |

**Deprecated/outdated:**
- `swag v2.0.0-rc5`: Release candidate (Jan 2026), not stable. CLAUDE.md explicitly says: use v1.16.6. Do not use v2 RC.
- `github.com/golang/protobuf`: Not relevant to the gateway but flagged in CLAUDE.md — do not import.

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Two-query approach (no params vs with params) is preferred over nullable-param SQL | Architecture Patterns — Pattern 5 | Minor: nullable-param SQL also works; two-query is clearer but adds a code branch |
| A2 | PostgreSQL planner will use Index Scan on `idx_gpu_metrics_gpu_id_ts` for the time-range query | Common Pitfalls — Pitfall 3 | Low: if not, `FORCE INDEX` or query hint workaround needed; must verify via EXPLAIN in integration test |
| A3 | `SELECT DISTINCT gpu_id` may use SeqScan on large tables; acceptable for MVP | Common Pitfalls — Pitfall 3 | Low: `GROUP BY gpu_id` alternative is documented; can be changed without breaking API contract |
| A4 | Default result cap of 1000 is appropriate | Open Questions — OQ-1 | Low: exact number is configurable; any reasonable cap is correct for MVP |

---

## Sources

### Primary (MEDIUM confidence — official docs)

- [CITED: pkg.go.dev/github.com/go-chi/chi/v5] — routing API, URLParam, middleware, handler signatures; v5.3.0 verified May 22 2026
- [CITED: pkg.go.dev/github.com/swaggo/swag] — annotation format, @Param structure, {object}/{array} response types; v1.16.6 stable
- [CITED: pkg.go.dev/github.com/swaggo/http-swagger/v2] — Handler/URL option, chi integration example; v2.0.2 verified
- [CITED: pkg.go.dev/github.com/jackc/pgx/v5/pgxpool] — Query/QueryRow/rows.Next/Scan patterns; v5.10.0 in go.mod
- [CITED: github.com/swaggo/swag README] — file-level annotations, -g flag, BasePath relative @Router paths

### Secondary (codebase — HIGH confidence for project-specific claims)

- `/Users/ajitg/workspace/vantage/Makefile` — swagger target `-o pkg/docs` (authoritative output path)
- `/Users/ajitg/workspace/vantage/.gitignore` — `pkg/docs` is committed (not gitignored); see comment on line 25
- `/Users/ajitg/workspace/vantage/pkg/db/migrations/000001_init_schema.up.sql` — exact schema + index definition
- `/Users/ajitg/workspace/vantage/pkg/models/telemetry.go` — GpuMetric struct fields (source for DTO design)
- `/Users/ajitg/workspace/vantage/pkg/db/config.go` — FromEnv, DSN env var naming conventions
- `/Users/ajitg/workspace/vantage/internal/collector/run_test.go` — testcontainers pattern (TestMain, Snapshot/Restore, RYUK_DISABLED)
- `/Users/ajitg/workspace/vantage/internal/http/inspect.go` — handler pattern (http.HandlerFunc, json.NewEncoder)
- `/Users/ajitg/workspace/vantage/cmd/mq/main.go` — composition root pattern (errgroup, signal.NotifyContext, http.Server)

---

## Metadata

**Confidence breakdown:**
- Standard stack: MEDIUM — locked by CLAUDE.md; pkg.go.dev confirmed versions
- Annotation patterns: MEDIUM — verified via official swag README and pkg.go.dev
- chi routing: MEDIUM — verified via pkg.go.dev v5.3.0
- pgx query shapes: MEDIUM — verified via pkg.go.dev pgxpool docs
- EXPLAIN behavior: LOW [ASSUMED] — must be verified in integration test
- Architecture decisions: HIGH — derived from existing codebase patterns (Phases 1-3)

**Research date:** 2026-06-29
**Valid until:** 2026-07-29 (stable libraries; 30-day horizon)
