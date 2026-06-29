# Phase 02: Storage Foundation — Schema + Connection Pool - Pattern Map

**Mapped:** 2026-06-28
**Files analyzed:** 9 new/modified files
**Analogs found:** 6 / 9 (3 net-new patterns with no codebase analog)

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `pkg/db/config.go` | config | request-response | `internal/config/config.go` | exact |
| `pkg/db/db.go` | service/utility | request-response | `internal/server/server.go` (constructor idiom) | role-match |
| `pkg/db/db_test.go` | test | CRUD + event-driven | `internal/queue/ring_store_test.go` | role-match (test style); TestMain is net-new |
| `pkg/db/migrations/000001_init_schema.up.sql` | migration | — | none | no analog |
| `pkg/db/migrations/000001_init_schema.down.sql` | migration | — | none | no analog |
| `docker-compose.yml` | config/infra | — | none | no analog |
| `scripts/smoke/phase02-*.sh` | utility/script | request-response | `scripts/smoke/phase01-mq.sh` | exact |
| `Makefile` (extend) | config | — | existing `Makefile` targets | exact |
| `README.md` (extend) | docs | — | existing `README.md` | exact |

---

## Pattern Assignments

### `pkg/db/config.go` (config, request-response)

**Analog:** `internal/config/config.go`

**Imports pattern** (lines 1-8):
```go
package config

import (
    "os"
    "strconv"
)
```

**Core Config struct + FromEnv pattern** (lines 12-70 of `internal/config/config.go`):
```go
// Package config provides env-first configuration for the MQ service.
// All settings have sensible defaults and can be overridden via environment variables.
package config

// Config holds the runtime configuration for the MQ service. All fields are
// populated by FromEnv with defaults; no field should be zero after construction.
type Config struct {
    // GRPCAddr is the TCP address for the gRPC listener (MQ-01, MQ-02).
    GRPCAddr string
    // HTTPAddr is the TCP address for the HTTP control-plane listener (MQ-06).
    HTTPAddr string
    // BufferSize is the capacity of the in-memory ring buffer (MQ-04, MQ-05).
    BufferSize int
}

// FromEnv constructs a Config from environment variables, applying defaults
// for any unset or invalid values. It has no side effects beyond os.Getenv calls.
//
// Environment variables:
//   - MQ_GRPC_ADDR  (default :50051)
//   - MQ_HTTP_ADDR  (default :8080)
//   - MQ_BUFFER_SIZE (default 10000; must be a positive integer; invalid values are ignored)
func FromEnv() Config {
    cfg := Config{
        GRPCAddr:   ":50051",
        HTTPAddr:   ":8080",
        BufferSize: 10000,
    }
    if v := os.Getenv("MQ_BUFFER_SIZE"); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n > 0 {
            cfg.BufferSize = n
        }
    }
    if v := os.Getenv("MQ_GRPC_ADDR"); v != "" {
        cfg.GRPCAddr = v
    }
    return cfg
}
```

**Adaptation for `pkg/db/config.go`:**
- Package name: `db` (not `config` — lives inside the `db` package)
- `FromEnv() (Config, error)` — note the error return (unlike MQ config, the DSN is **required**; missing DSN is a hard error, not a defaultable value)
- `MaxConns int32` — parse with `strconv.ParseInt(s, 10, 32)` (pgxpool uses int32)
- Env vars: `VANTAGE_DB_DSN` (required), `VANTAGE_DB_MAX_CONNS` (optional, default 0 = library default)
- Do NOT log the DSN in error messages (contains credentials); log only context (e.g., `"db: VANTAGE_DB_DSN is required"`)

**Exact error pattern from MQ config (invalid ignored silently) vs db/config (required = hard error):**
```go
// MQ pattern — invalid silently ignored, default kept:
if v := os.Getenv("MQ_BUFFER_SIZE"); v != "" {
    if n, err := strconv.Atoi(v); err == nil && n > 0 {
        cfg.BufferSize = n
    }
}

// db/config pattern — required field returns error:
dsn := os.Getenv("VANTAGE_DB_DSN")
if dsn == "" {
    return Config{}, fmt.Errorf("db: VANTAGE_DB_DSN is required")
}
```

---

### `pkg/db/db.go` (service/utility, request-response)

**Analog:** `internal/server/server.go` (constructor + package doc idiom), `internal/queue/store.go` (interface doc style)

**Package doc pattern** (lines 1-22 of `internal/server/server.go`):
```go
// Package server implements the MQ gRPC service (Produce, Consume) with
// broker-side at-least-once delivery (MQ-09) and client-driven credit flow
// control (MQ-10), per ADR-001.
//
// ...longer rationale...
package server
```
Apply same doc-comment density in `pkg/db`: document the seam (Collector + Gateway both import this), document that `Migrate` must be called before `New` (or concurrently — advisory lock handles it), document the DSN credential redaction rule.

**Exported symbol doc pattern** (lines 44-55 of `internal/server/server.go`):
```go
// ServerStats is a point-in-time snapshot of MQServer state. All fields are
// safe to read without a lock because they are derived from atomic loads.
type ServerStats struct {
    Produced  int64 // messages accepted by Produce
    ...
}
```
Mirror this: `Config` struct fields get inline `//` comments explaining the env var and default.

**Constructor idiom** — `internal/http/inspect.go` (lines 42-60) shows the `New...` function returning a usable handle, not a struct:
```go
func InspectHandler(srv *server.MQServer) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // ...
    }
}
```
For `pkg/db`, the constructor is:
```go
func New(ctx context.Context, cfg Config) (*pgxpool.Pool, error)
```
— returns `*pgxpool.Pool` directly (not a wrapper struct), because Collector and Gateway need the raw pool to call `CopyFrom`, `Query`, etc.

**Error wrapping pattern** — consistent throughout Phase 1; every error is wrapped with context:
```go
// From internal/http pattern — errors are wrapped with package prefix:
return nil, fmt.Errorf("db: parse config: %w", err)
return nil, fmt.Errorf("db: create pool: %w", err)
return nil, fmt.Errorf("db: ping: %w", err)
```

**Compile-time interface assertion** (line 13 of `internal/queue/ring_store.go`):
```go
var _ Store = (*RingStore)(nil)
```
No interface to implement in `pkg/db`, but the Ping-on-startup pattern is the functional equivalent: a compile-time/startup assertion that the pool actually works.

**go:embed + blank import pattern** (net-new; no analog in codebase — copy from RESEARCH.md Pattern 2):
```go
import (
    "embed"
    "errors"

    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5:// scheme
    "github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS
```
The blank import `_` registration pattern mirrors the existing use of gRPC blank imports in `cmd/mq/main.go` (load a driver by side-effect).

---

### `pkg/db/db_test.go` (test, CRUD)

**Analog (test style):** `internal/config/config_test.go` and `internal/queue/ring_store_test.go`

**Test package naming** (line 1 of `internal/config/config_test.go`):
```go
package config_test   // external black-box test package
```
Use `package db_test` for `pkg/db/db_test.go` — consistent with Phase 1 external test package convention.

**Testify import + assertion style** (lines 3-9 of `internal/config/config_test.go`):
```go
import (
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/ajitg/vantage/internal/config"
)
```
- `require` (fatal) for setup and structural assertions; `assert` (non-fatal) for plan-string contents in the EXPLAIN test.
- Both `require` and `assert` may be needed in `db_test.go`; import both.

**Table-driven test pattern** (lines 28-53 of `ring_store_test.go`):
```go
func TestRingStore_Enqueue(t *testing.T) {
    s := NewRingStore(3)
    s.Enqueue(msg("a"))
    stats := s.Inspect()
    require.Equal(t, 2, stats.Depth, "depth after two enqueues")
}
```
Unit-style tests (one clear assertion per function) with descriptive names. Apply same style for `TestMigration`, `TestNew`, `TestUniqueConstraint`.

**TestMain + Snapshot/Restore pattern** — NET-NEW; no analog in codebase. Copy from RESEARCH.md Pattern 3:
```go
var (
    testPool *pgxpool.Pool
    testCtr  *postgres.PostgresContainer
)

func TestMain(m *testing.M) {
    ctx := context.Background()
    ctr, err := postgres.Run(ctx,
        "postgres:17-alpine",
        postgres.WithDatabase("vantage_test"),
        postgres.WithUsername("postgres"),   // must be "postgres" for Snapshot/Restore (Pitfall 5)
        postgres.WithPassword("secret"),
        postgres.BasicWaitStrategies(),
        postgres.WithSQLDriver("pgx"),       // required for Snapshot/Restore
    )
    if err != nil {
        log.Fatalf("start postgres: %v", err)
    }
    defer testcontainers.TerminateContainer(ctr)
    testCtr = ctr

    dsn := ctr.MustConnectionString(ctx, "sslmode=disable")
    if err := Migrate(ctx, dsn); err != nil {
        log.Fatalf("migrate: %v", err)
    }
    if err := ctr.Snapshot(ctx); err != nil {
        log.Fatalf("snapshot: %v", err)
    }
    pool, err := New(ctx, Config{DSN: dsn, MaxConns: 5})
    if err != nil {
        log.Fatalf("pool: %v", err)
    }
    testPool = pool
    defer pool.Close()
    os.Exit(m.Run())
}
```

**t.Cleanup restore pattern** — used instead of defer in test functions (mirrors Phase 1 `t.Setenv` cleanup style):
```go
func TestWithDB(t *testing.T) {
    ctx := context.Background()
    t.Cleanup(func() {
        if err := testCtr.Restore(ctx); err != nil {
            t.Fatalf("restore: %v", err)
        }
    })
    // ... test body
}
```

**EXPLAIN assertion pattern** — NET-NEW; no analog. Copy from RESEARCH.md Pattern 4:
```go
rows, err := testPool.Query(ctx, explainQ, targetGPU, start, end)
require.NoError(t, err)
defer rows.Close()
var plan strings.Builder
for rows.Next() {
    var line string
    require.NoError(t, rows.Scan(&line))
    plan.WriteString(line + "\n")
}
assert.Contains(t, plan.String(), "Index Scan",
    "expected composite index to be used; plan:\n%s", plan.String())
```

---

### `pkg/db/migrations/000001_init_schema.up.sql` (migration, —)

**No codebase analog.** NET-NEW pattern. Use RESEARCH.md Schema DDL verbatim (lines 412-454 of RESEARCH.md). Key points from locked decisions:
- `gpu_id TEXT NOT NULL` — stores UUID value, named `gpu_id` per spec (D-04); add SQL comment
- `timestamp TIMESTAMPTZ NOT NULL` — microsecond precision; SQL comment must warn about RFC3339Nano requirement (Phase 3)
- `value DOUBLE PRECISION NOT NULL`
- `metric_name TEXT NOT NULL`
- Descriptive nullable columns: `device`, `model_name`, `hostname`, `container`, `pod`, `namespace`, `labels_raw`
- `CREATE INDEX IF NOT EXISTS idx_gpu_metrics_gpu_id_ts ON gpu_metrics (gpu_id, timestamp DESC)`
- `CREATE UNIQUE INDEX IF NOT EXISTS uq_gpu_metrics_natural_key ON gpu_metrics (gpu_id, metric_name, timestamp)`
- No `CONCURRENTLY` on initial creation (cannot run inside a transaction; golang-migrate wraps in a transaction)

---

### `pkg/db/migrations/000001_init_schema.down.sql` (migration, —)

**No codebase analog.** Single statement:
```sql
DROP TABLE IF EXISTS gpu_metrics;
```
Indexes are dropped automatically when the table is dropped; no explicit `DROP INDEX`.

---

### `scripts/smoke/phase02-*.sh` (script, request-response)

**Analog:** `scripts/smoke/phase01-mq.sh` (exact match — same harness structure)

**Shell harness pattern** (lines 1-35 of `phase01-mq.sh`):
```bash
#!/usr/bin/env bash
# Phase N smoke check — <description>
#
# Requires: go, curl, psql, docker.  Run via: make smoke-02
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if [ -t 1 ]; then GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RST=$'\033[0m'
else GREEN=''; RED=''; BOLD=''; RST=''; fi
pass() { echo "${GREEN}✓${RST} $*"; }
fail() { echo "${RED}✗ $*${RST}"; exit 1; }
```

**Dependency check pattern** (lines 27-28 of `phase01-mq.sh`):
```bash
command -v go   >/dev/null 2>&1 || fail "go not found on PATH"
command -v curl >/dev/null 2>&1 || fail "curl not found on PATH"
```
Phase-2 smoke adds: `command -v psql >/dev/null 2>&1 || fail "psql not found on PATH"`

**Cleanup trap pattern** (lines 32-36 of `phase01-mq.sh`):
```bash
TMP="$(mktemp -d)"
cleanup() {
  # kill any started processes; rm -rf "$TMP"
}
trap cleanup EXIT
```
Phase-2 smoke does not start a service (postgres comes from `make dev-up`), so cleanup only needs `make dev-down` if the script started it.

**Phase-2 smoke flow** (no existing analog — construct from phase01 harness structure):
1. `make dev-up` (if not already running) → wait for Postgres healthcheck
2. `VANTAGE_DB_DSN=... go run ./cmd/migrate` (or call Migrate directly) → apply schema
3. `psql` → `\d gpu_metrics` → assert table exists + expected columns
4. `psql` → `\di gpu_metrics*` → assert composite index `idx_gpu_metrics_gpu_id_ts` exists
5. `psql` → `EXPLAIN SELECT ... WHERE gpu_id = 'x' AND timestamp BETWEEN ... ORDER BY timestamp DESC` → assert `Index Scan` in output (requires seeded data + ANALYZE first)
6. `pass` + summary line

---

### `Makefile` (extend existing)

**Analog:** Lines 58-66 of existing `Makefile` (`smoke` + `smoke-%` targets)

**Existing smoke target pattern** (lines 58-66):
```makefile
smoke: ## Run every phase's manual smoke check (all phases shipped so far)
    @found=0; for f in scripts/smoke/phase*.sh; do \
        [ -e "$$f" ] || continue; found=1; echo "== $$f =="; bash "$$f" || exit 1; done; \
    [ "$$found" = 1 ] || echo "no smoke scripts yet under scripts/smoke/"

smoke-%: ## Run one phase's manual smoke check, e.g. make smoke-01
    @found=0; for f in scripts/smoke/phase$*-*.sh; do \
        [ -e "$$f" ] || continue; found=1; echo "== $$f =="; bash "$$f" || exit 1; done; \
    [ "$$found" = 1 ] || { echo "no smoke scripts for phase $* (looked for scripts/smoke/phase$*-*.sh)"; exit 1; }
```
`make smoke-02` already works via the wildcard rule — no new Makefile target needed for it.

**New targets to add** (`dev-up`, `dev-down`):
```makefile
dev-up: ## Start local dev dependencies (Postgres via docker compose)
    docker compose up -d --wait

dev-down: ## Stop local dev dependencies
    docker compose down
```
Follow existing pattern: terse `##` comment, match indentation (tab, not spaces).

**Coverage target note** (lines 50-56): The existing `coverage` target only covers `./internal/...`:
```makefile
coverage:
    go test -race -covermode=atomic -coverprofile=coverage.out ./internal/...
```
Phase 2 adds `pkg/db/` — the planner must extend this to `./internal/... ./pkg/...` (or `./...`) so the ≥90% gate covers the new shared library.

---

### `README.md` (extend existing)

**Analog:** Existing `README.md` — living doc, grown per phase. No excerpt needed (planner adds a "Phase 2: Storage Foundation" quickstart section following whatever structure Phase 1 established).

---

## Shared Patterns

### Error wrapping convention
**Source:** Throughout `internal/` — every package-boundary error is wrapped with `"<pkg>: <context>: %w"`.
**Apply to:** `pkg/db/db.go` (`New`, `Migrate` functions).
```go
// Consistent pattern seen in internal/http, internal/server:
return nil, fmt.Errorf("db: parse config: %w", err)
return nil, fmt.Errorf("db: migrate up: %w", err)
return nil, fmt.Errorf("db: ping: %w", err)
```

### Doc-comment density on exported symbols
**Source:** `internal/queue/store.go` (every exported method has a multi-line doc comment); `internal/config/config.go` (env var names documented in `FromEnv` doc comment).
**Apply to:** All exported symbols in `pkg/db/` — `Config`, `FromEnv`, `New`, `Migrate`.

### External (black-box) test package
**Source:** `internal/config/config_test.go` line 1: `package config_test`.
**Apply to:** `pkg/db/db_test.go` — use `package db_test`.

### testify `require` for fatal, `assert` for non-fatal
**Source:** `internal/config/config_test.go` (all `require.Equal`); `internal/queue/ring_store_test.go` (all `require`).
**Apply to:** `pkg/db/db_test.go` — `require.NoError` for setup, `assert.Contains` for the EXPLAIN plan string (non-fatal lets us print the full plan on failure).

### Makefile `##` comment style
**Source:** Lines 15-78 of `Makefile` — all targets use `## <description>` for help text.
**Apply to:** `dev-up`, `dev-down` targets.

---

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `pkg/db/migrations/000001_init_schema.up.sql` | migration | — | No migrations exist in the codebase; golang-migrate + go:embed is net-new |
| `pkg/db/migrations/000001_init_schema.down.sql` | migration | — | Same — net-new pattern |
| `docker-compose.yml` | infra config | — | No docker-compose in codebase; Phase 2 establishes this convention |
| `TestMain` + Snapshot/Restore block in `db_test.go` | test infrastructure | — | testcontainers-go is a new dependency; no TestMain in any Phase 1 test file |

For all four, use RESEARCH.md patterns directly (Patterns 2, 3, Schema DDL section, docker-compose.yml example).

---

## Metadata

**Analog search scope:** `/Users/ajitg/workspace/vantage/internal/`, `scripts/smoke/`, `Makefile`, `go.mod`
**Files scanned:** 10
**Module path confirmed:** `github.com/ajitg/vantage` (`go.mod` line 5)
**Current go.mod dependencies** (relevant additions Phase 2 must make):
- Add: `github.com/jackc/pgx/v5 v5.10.0`
- Add: `github.com/golang-migrate/migrate/v4 v4.19.1`
- Add: `github.com/testcontainers/testcontainers-go v0.43.0`
- Add: `github.com/testcontainers/testcontainers-go/modules/postgres v0.43.0`
- Already present: `github.com/stretchr/testify v1.11.1`
