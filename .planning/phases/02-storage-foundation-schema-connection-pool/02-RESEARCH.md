# Phase 02: Storage Foundation — Schema + Connection Pool - Research

**Researched:** 2026-06-28
**Domain:** PostgreSQL time-series schema, pgxpool, golang-migrate, testcontainers-go
**Confidence:** MEDIUM (Go package versions verified via proxy.golang.org; API patterns via pkg.go.dev and official docs)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Schema shape (DB-01)**
- D-01: Long/narrow table — `gpu_metrics(gpu_id, timestamp, metric_name, value, + descriptive dims)`. One row per metric reading; maps proto `TelemetryMessage` 1:1.
- D-02: `value DOUBLE PRECISION` satisfies "numeric columns". `metric_name TEXT` names the metric. 10 DCGM metrics are data, not columns.

**GPU identity + natural key (DB-02, DB-04)**
- D-03: Canonical GPU identity = GPU `uuid` (globally unique across fleet). Ordinal, `device`, `model_name`, `hostname` are descriptive attributes.
- D-04: Identity column is named `gpu_id` (matches spec, index expression, and `/api/v1/gpus/{id}` route), but stores the uuid value. Document this in migration DDL comment.
- D-05: Natural-key unique constraint = `(gpu_id, metric_name, timestamp)`. Targets `ON CONFLICT (gpu_id, metric_name, timestamp)` in Phase 3 COLL-05.

**Migration mechanism (DB-01)**
- D-06: `golang-migrate` with `//go:embed`-ed versioned SQL files. Up/down `.sql` files embedded in binary and applied programmatically.

**Index-usage proof (DB-02)**
- D-07: Use testcontainers-go (`postgres:17-alpine`), seed ~100k rows, run `EXPLAIN` on `(gpu_id, timestamp DESC)` range query, assert the plan contains `Index Scan` (not `enable_seqscan` workaround). Proves planner picks index, not just that it's usable.

**Cross-cutting: living README + manual smoke suite (DOC-01, QA-06, OPS-06)**
- D-08: Living README quickstart grown per phase.
- D-09: `scripts/smoke/phase02-*.sh` + `make smoke-02`; `docker-compose.yml` + `make dev-up`/`make dev-down`. Phase-2 smoke: `make dev-up` → run migration → psql confirms table + index → EXPLAIN range query shows Index Scan.

### Claude's Discretion
- DSN/config plumbing: env var name (`VANTAGE_DB_DSN` / `DATABASE_URL`), `MaxConns` default and override, `pgxpool.ParseConfig` tuning.
- `pkg/db` surface shape: constructor signature, whether migrate runner lives in `pkg/db` or a small helper path, health-check helper.
- Non-identity column set/types (`device`, `model_name`, `hostname`, `container`, `pod`, `namespace`, `labels_raw`) and nullable logic.

### Deferred Ideas (OUT OF SCOPE)
- Collector batch insert / `ON CONFLICT` upsert logic → Phase 3.
- Streamer restamp precision (RFC3339Nano) → Phase 3.
- API read queries / `/gpus/{id}` resolution → Phase 4.
- WAL replay driving idempotent path end-to-end → Phase 6.
- Retention/partitioning of the time-series table → out of v1 scope.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| DB-01 | Relational time-series schema (`gpu_id`, `timestamp TIMESTAMPTZ`, numeric metric columns) created by migration | Schema DDL section; golang-migrate iofs pattern; DCGM CSV column mapping |
| DB-02 | Composite index `(gpu_id, timestamp DESC)` defined and verified used via `EXPLAIN` | EXPLAIN ANALYZE patterns; seqscan pitfall; seed scale guidance |
| DB-03 | `pgxpool` connection-pool initialization in shared `pkg/db`, reused by Collector and Gateway | pgxpool.ParseConfig + NewWithConfig API; env var pattern |
| DB-04 | Natural-key unique constraint enables idempotent inserts | Unique constraint `(gpu_id, metric_name, timestamp)`; ON CONFLICT tension with CopyFrom flagged |
</phase_requirements>

---

## Summary

Phase 2 delivers the PostgreSQL storage foundation: a versioned migration that creates a long/narrow
time-series table, a shared `pgxpool` initializer in `pkg/db`, and a test proving the composite index
`(gpu_id, timestamp DESC)` is used at representative scale. Nothing reads or writes the table at runtime
this phase — the planner produces the seam Collector and Gateway plug into.

The pinned stack (jackc/pgx/v5 v5.10.0, testcontainers-go v0.43.0, golang-migrate v4.19.1) is
all verified on proxy.golang.org. The key landmine is the CopyFrom-vs-ON-CONFLICT tension deferred
to Phase 3: Phase 2 defines the unique constraint `(gpu_id, metric_name, timestamp)` that makes
upserts possible, but CopyFrom cannot express ON CONFLICT — Phase 3 must choose batched `INSERT ... ON
CONFLICT` instead of CopyFrom for the idempotent insert path. Phase 2 also surfaces the timestamp
precision risk: if Streamer restamps at second granularity (RFC3339), duplicate readings within the
same second on the same GPU/metric would share a natural key and silently collapse — Phase 3 must
restamp at nanosecond precision (RFC3339Nano).

The cross-cutting convention established this phase (D-08/D-09): `docker-compose.yml` dev stack +
`make dev-up`/`make dev-down`, smoke scripts at `scripts/smoke/phase02-*.sh`, and incremental
README growth. The Phase-1 smoke backfill (MQ) already landed; Phase 2 adds the Postgres smoke.

**Primary recommendation:** Use `pgxpool.ParseConfig` + `pgxpool.NewWithConfig` (not bare
`pgxpool.New`) so `MaxConns` and `HealthCheckPeriod` are tunable without DSN manipulation. Run
migrations programmatically via `golang-migrate` with `iofs` source driver over `//go:embed`-ed SQL.
Prove the index via a real 100k-row seed test (no `enable_seqscan` shortcut).

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Time-series DDL (table + index + constraint) | Database | — | Schema lives in Postgres; migration creates it once |
| Connection pool (`pkg/db`) | Shared Library | — | Single initializer imported by Collector and Gateway; not a service |
| Migration runner | Shared Library (`pkg/db` or `cmd/migrate` helper) | — | Applied programmatically at service startup or as a one-shot init job (Phase 5) |
| EXPLAIN index proof | Test tier (testcontainers-go) | — | No runtime component; a test that asserts planner behavior at scale |
| docker-compose dev stack | DevOps / scripts | — | Local dependency provider for manual smoke; independent of Phase-5 kind/Helm |
| Smoke suite (phase02) | scripts/smoke | Makefile | Human-run verification harness |

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| github.com/jackc/pgx/v5 | v5.10.0 | pgxpool + query execution | De-facto Go Postgres driver; v5 redesigned pgxpool for correct concurrency; CopyFrom bulk path in Phase 3 |
| github.com/golang-migrate/migrate/v4 | v4.19.1 | Versioned schema migrations | go:embed source driver, advisory-locked, rollback via down files; production-grade history |
| github.com/testcontainers/testcontainers-go | v0.43.0 | Run real Postgres container in tests | First-class Snapshot/Restore for cheap test isolation; active maintenance |
| github.com/testcontainers/testcontainers-go/modules/postgres | v0.43.0 | Postgres-specific helpers | `BasicWaitStrategies()`, `Snapshot()`, `Restore()`, `MustConnectionString()` |
| github.com/stretchr/testify | v1.11.1 | Test assertions | Already in go.mod; `require` for fatal, `assert` for non-fatal |

### Supporting (migration-specific imports)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/golang-migrate/migrate/v4/source/iofs | v4.19.1 | iofs source driver | Required to embed SQL files via `//go:embed` |
| github.com/golang-migrate/migrate/v4/database/pgx/v5 | v4.19.1 | pgx/v5 database driver for migrate | Required to use `pgx5://` connection URL with golang-migrate |

**Installation (additions to go.mod):**
```bash
go get github.com/jackc/pgx/v5@v5.10.0
go get github.com/golang-migrate/migrate/v4@v4.19.1
go get github.com/testcontainers/testcontainers-go@v0.43.0
go get github.com/testcontainers/testcontainers-go/modules/postgres@v0.43.0
```

**Version verification (proxy.golang.org):** [VERIFIED: proxy.golang.org]
- `github.com/jackc/pgx/v5` → v5.10.0, published 2026-06-03
- `github.com/golang-migrate/migrate/v4` → v4.19.1, published 2025-11-29
- `github.com/testcontainers/testcontainers-go` → v0.43.0, published 2026-06-19
- `github.com/testcontainers/testcontainers-go/modules/postgres` → v0.43.0, published 2026-06-19

---

## Package Legitimacy Audit

> The package legitimacy seam (gsd-tools) only supports npm/pypi/crates ecosystems. Go packages
> are verified via proxy.golang.org (the canonical Go module proxy), which requires a valid VCS tag
> at the official source repository. All packages below are long-established Go ecosystem standards.

| Package | Registry | Age | Weekly DLs | Source Repo | Verdict | Disposition |
|---------|----------|-----|------------|-------------|---------|-------------|
| jackc/pgx/v5 | pkg.go.dev | ~8 yrs | very high | github.com/jackc/pgx | OK | Approved |
| golang-migrate/migrate/v4 | pkg.go.dev | ~7 yrs | very high | github.com/golang-migrate/migrate | OK | Approved |
| testcontainers/testcontainers-go | pkg.go.dev | ~5 yrs | high | github.com/testcontainers/testcontainers-go | OK | Approved |
| testcontainers-go/modules/postgres | pkg.go.dev | ~2 yrs | high | github.com/testcontainers/testcontainers-go | OK | Approved |

**Packages removed due to SLOP verdict:** none
**Packages flagged as suspicious (SUS):** none

All packages confirmed on proxy.golang.org with valid VCS origins. [VERIFIED: proxy.golang.org]

---

## Architecture Patterns

### System Architecture Diagram

```
[pkg/db/migrations/*.sql]
    │ //go:embed
    ▼
[pkg/db.Migrate(ctx, dsn)] ──── golang-migrate/iofs ──── pgx5://DSN
    │                                  │
    │ (run once at startup             │ advisory lock prevents
    │  or one-shot init job)           │ concurrent migration
    ▼
[PostgreSQL: gpu_metrics table]
    │ composite index (gpu_id, timestamp DESC)
    │ unique constraint (gpu_id, metric_name, timestamp)
    │
[pkg/db.New(ctx, cfg)] ──── pgxpool.ParseConfig + NewWithConfig
    │                              │
    │                         MaxConns, HealthCheckPeriod, DSN
    │
    ├──── [cmd/collector] (Phase 3: CopyFrom / ON CONFLICT inserts)
    └──── [cmd/gateway]   (Phase 4: SELECT queries)

[pkg/db/integration_test.go] ──── testcontainers-go (postgres:17-alpine)
    │ TestMain: start → migrate → Snapshot
    │ each test: Restore(ctx) in t.Cleanup
    │ seed 100k rows → ANALYZE → EXPLAIN
    └──── assert "Index Scan" in plan
```

### Recommended Project Structure
```
pkg/
└── db/
    ├── db.go              # New(ctx, cfg) → *pgxpool.Pool, Migrate(ctx, dsn) → error
    ├── config.go          # Config struct (DSN, MaxConns, etc.), FromEnv() helper
    ├── migrations/
    │   ├── 000001_init_schema.up.sql
    │   └── 000001_init_schema.down.sql
    └── db_test.go         # TestMain + integration tests (testcontainers-go)
```

Notes:
- Migrate runner lives in `pkg/db` alongside the pool so both use the same DSN path.
- `pkg/db/migrations/` is embedded at compile time — no runtime file access needed.
- The `db_test.go` file is covered by the ≥90% gate since it exercises real logic in `db.go`.

### Pattern 1: pgxpool Construction (ParseConfig approach)

**What:** Use `pgxpool.ParseConfig` to get a `*pgxpool.Config`, tune fields, then call
`pgxpool.NewWithConfig`. This is safer than embedding overrides in the DSN string.

**When to use:** Always — gives explicit control over `MaxConns` and `HealthCheckPeriod`.

```go
// Source: pkg.go.dev/github.com/jackc/pgx/v5/pgxpool [VERIFIED: proxy.golang.org]
func New(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
    poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
    if err != nil {
        return nil, fmt.Errorf("db: parse config: %w", err)
    }
    if cfg.MaxConns > 0 {
        poolCfg.MaxConns = cfg.MaxConns
    }
    poolCfg.HealthCheckPeriod = 30 * time.Second

    pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
    if err != nil {
        return nil, fmt.Errorf("db: create pool: %w", err)
    }
    // pgxpool.New does NOT pre-connect — ping to verify reachability
    if err := pool.Ping(ctx); err != nil {
        pool.Close()
        return nil, fmt.Errorf("db: ping: %w", err)
    }
    return pool, nil
}
```

Key point: `pgxpool.NewWithConfig` returns immediately without opening connections. The `Ping`
call above is the earliest failure signal. [CITED: pkg.go.dev/github.com/jackc/pgx/v5/pgxpool]

### Pattern 2: golang-migrate with iofs + go:embed

**What:** Embed `.sql` files in the binary, apply them programmatically at startup.

**When to use:** Every service that needs to ensure the schema is current before accepting traffic.

```go
// Source: pkg.go.dev/github.com/golang-migrate/migrate/v4/source/iofs [CITED: pkg.go.dev]
// pkg/db/db.go

import (
    "embed"
    "errors"

    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5:// scheme
    "github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Migrate(ctx context.Context, dsn string) error {
    src, err := iofs.New(migrationsFS, "migrations")
    if err != nil {
        return fmt.Errorf("db: iofs source: %w", err)
    }

    // Convert postgres:// DSN to pgx5:// scheme for golang-migrate
    pgx5DSN := strings.Replace(dsn, "postgres://", "pgx5://", 1)

    m, err := migrate.NewWithSourceInstance("iofs", src, pgx5DSN)
    if err != nil {
        return fmt.Errorf("db: migrate init: %w", err)
    }
    defer m.Close()

    if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
        return fmt.Errorf("db: migrate up: %w", err)
    }
    return nil
}
```

Critical note: `errors.Is(err, migrate.ErrNoChange)` must be treated as success — it means
migrations are already at the latest version. Surfacing it as an error breaks idempotent startup.

### Pattern 3: testcontainers-go TestMain + Snapshot/Restore

**What:** Single Postgres container for the whole test package; snapshot after migrations;
restore before each test for cheap isolation.

```go
// Source: golang.testcontainers.org/modules/postgres/ [CITED: golang.testcontainers.org]
// pkg/db/db_test.go

var (
    testPool *pgxpool.Pool
    testCtr  *postgres.PostgresContainer
)

func TestMain(m *testing.M) {
    ctx := context.Background()

    ctr, err := postgres.Run(ctx,
        "postgres:17-alpine",
        postgres.WithDatabase("vantage_test"),
        postgres.WithUsername("vantage"),
        postgres.WithPassword("secret"),
        postgres.BasicWaitStrategies(),
        postgres.WithSQLDriver("pgx"), // required for Snapshot/Restore
    )
    if err != nil {
        log.Fatalf("start postgres: %v", err)
    }
    defer testcontainers.TerminateContainer(ctr)
    testCtr = ctr

    dsn := ctr.MustConnectionString(ctx, "sslmode=disable")

    // Run migrations on the fresh container
    if err := Migrate(ctx, dsn); err != nil {
        log.Fatalf("migrate: %v", err)
    }

    // Capture snapshot AFTER migrations; each test restores to this state
    if err := ctr.Snapshot(ctx); err != nil {
        log.Fatalf("snapshot: %v", err)
    }

    // Open shared pool for tests
    pool, err := New(ctx, Config{DSN: dsn, MaxConns: 5})
    if err != nil {
        log.Fatalf("pool: %v", err)
    }
    testPool = pool
    defer pool.Close()

    os.Exit(m.Run())
}

func TestWithDB(t *testing.T) {
    ctx := context.Background()
    t.Cleanup(func() {
        if err := testCtr.Restore(ctx); err != nil {
            t.Fatalf("restore: %v", err)
        }
    })
    // test runs on a clean DB at the migrated snapshot state
}
```

### Pattern 4: EXPLAIN ANALYZE index-scan assertion at 100k rows

**What:** Insert representative seed data, run `ANALYZE`, then assert the plan contains `Index Scan`.

**Critical details:**
- PostgreSQL's planner uses statistics from `pg_class.reltuples`, populated by `ANALYZE`. Without
  `ANALYZE` after a bulk seed, the planner may underestimate row count and choose seq scan.
- At small scales (< ~1000 rows, single disk page), the planner nearly always picks seq scan
  regardless — proof requires realistic row counts. 100k rows across many `gpu_id`s and
  `metric_name`s is sufficient for index preference to emerge.
- The query must be **selective** (filter on a specific `gpu_id` + time window) — not a full-table
  scan. A query returning most rows will correctly prefer seq scan.
- `SET enable_seqscan = off` is a planner hint that discourages seq scans but does NOT forbid them
  (plan output shows `Disabled: true` if forced). D-07 correctly rejects this approach — it proves
  usability, not planner preference.

```go
// pkg/db/db_test.go — index-scan assertion [ASSUMED: test pattern; approach is standard]
func TestCompositeIndexUsed(t *testing.T) {
    ctx := context.Background()
    t.Cleanup(func() { _ = testCtr.Restore(ctx) })

    // Seed 100k rows across multiple GPUs and metric types, spread over 24h
    if err := seedRows(ctx, testPool, 100_000); err != nil {
        t.Fatalf("seed: %v", err)
    }

    // ANALYZE so planner statistics reflect the seeded data
    if _, err := testPool.Exec(ctx, "ANALYZE gpu_metrics"); err != nil {
        t.Fatalf("analyze: %v", err)
    }

    // Query a specific gpu_id over a 1-hour window (selective)
    const explainQ = `EXPLAIN (FORMAT TEXT)
        SELECT * FROM gpu_metrics
        WHERE gpu_id = $1
          AND timestamp >= $2
          AND timestamp < $3
        ORDER BY timestamp DESC`

    targetGPU := "GPU-5fd4f087-86f3-7a43-b711-4771313afc50"
    end := time.Now().UTC()
    start := end.Add(-1 * time.Hour)

    rows, err := testPool.Query(ctx, explainQ, targetGPU, start, end)
    require.NoError(t, err)
    defer rows.Close()

    var plan strings.Builder
    for rows.Next() {
        var line string
        require.NoError(t, rows.Scan(&line))
        plan.WriteString(line + "\n")
    }

    planStr := plan.String()
    assert.Contains(t, planStr, "Index Scan",
        "expected composite index to be used; plan:\n%s", planStr)
}
```

### Schema DDL (the migration)

**File:** `pkg/db/migrations/000001_init_schema.up.sql`

```sql
-- gpu_metrics: long/narrow time-series table.
-- One row per DCGM metric reading, mapping TelemetryMessage 1:1.
--
-- Identity note (D-04): gpu_id stores the GPU UUID (e.g. "GPU-5fd4f087-..."),
-- NOT the ordinal ("0"/"1"). Named gpu_id to match the spec's composite index
-- expression and /api/v1/gpus/{id} route. See CONTEXT.md D-03/D-04.

CREATE TABLE IF NOT EXISTS gpu_metrics (
    -- Identity (D-03 / D-04)
    gpu_id      TEXT        NOT NULL,  -- GPU UUID; named gpu_id per spec (D-04)
    timestamp   TIMESTAMPTZ NOT NULL,  -- microsecond precision; Streamer must use RFC3339Nano (Phase 3)

    -- Metric payload (D-01 / D-02)
    metric_name TEXT        NOT NULL,  -- e.g. "DCGM_FI_DEV_GPU_UTIL"
    value       DOUBLE PRECISION NOT NULL,

    -- Descriptive attributes (not identity; from CSV / proto fields)
    device      TEXT,                  -- e.g. "nvidia0"
    model_name  TEXT,                  -- e.g. "NVIDIA H100 80GB HBM3"
    hostname    TEXT,
    container   TEXT,
    pod         TEXT,
    namespace   TEXT,
    labels_raw  TEXT                   -- raw Prometheus label string
);

-- Composite index (DB-02): planner uses this for gpu_id + time-range queries.
-- DESC on timestamp: matches ORDER BY timestamp DESC in typical telemetry reads.
CREATE INDEX IF NOT EXISTS idx_gpu_metrics_gpu_id_ts
    ON gpu_metrics (gpu_id, timestamp DESC);

-- Natural-key unique constraint (DB-04): enables idempotent upsert (COLL-05, Phase 3).
-- Uniqueness on (gpu_id, metric_name, timestamp) means redelivering the same logical
-- reading hits the same key and updates rather than inserting a duplicate.
-- WARNING: This requires sub-second timestamp precision from the Streamer (RFC3339Nano).
-- If Streamer restamps at second granularity (RFC3339), multiple readings per second
-- on the same GPU/metric collapse to the same key — Phase 3 must enforce RFC3339Nano.
CREATE UNIQUE INDEX IF NOT EXISTS uq_gpu_metrics_natural_key
    ON gpu_metrics (gpu_id, metric_name, timestamp);
```

**File:** `pkg/db/migrations/000001_init_schema.down.sql`

```sql
DROP TABLE IF EXISTS gpu_metrics;
```

### Anti-Patterns to Avoid

- **Bare `pgxpool.New` without Ping:** The pool returns immediately with no connections open. If the
  DSN is wrong, the error surfaces only on the first query — potentially mid-request. Always ping.
- **`migrate.ErrNoChange` treated as error:** This is success — schema already at latest version.
  Fail-fast on this error will break every restart after the first migration run.
- **`db.name = "postgres"` with Snapshot/Restore:** testcontainers Snapshot drops and recreates the
  main database using the system `postgres` database as staging. If the test database IS named
  `postgres`, the restore fails. Always use a non-default name (e.g., `vantage_test`).
- **Seeding too few rows for index test:** < ~1000 rows on a single disk page — planner ignores
  the index. Seed 100k rows spread across multiple `gpu_id`s and time ranges.
- **No `ANALYZE` before `EXPLAIN`:** Without ANALYZE, `pg_class.reltuples` is 0 for a freshly
  seeded table; planner uses defaults and almost certainly chooses seq scan.
- **`enable_seqscan = off` to "prove" index:** Discourages, does not forbid. The plan node gets
  `Disabled: true`. Proves the index is valid, not that the planner prefers it at real scale.
- **Multi-statement DDL without `x-multi-statement=true`:** golang-migrate wraps each migration
  file in a transaction by default. A single file with multiple DDL statements runs fine in a
  transaction. The exception: `CREATE INDEX CONCURRENTLY` cannot run inside a transaction — use a
  separate migration file or omit CONCURRENTLY for the initial schema creation.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Schema versioning | Custom `CREATE TABLE IF NOT EXISTS` in `main()` | `golang-migrate` | No history, no rollback, no advisory lock, deploy races |
| Migration source | Read SQL files from disk at runtime | `iofs` + `//go:embed` | Files must exist on final distroless image; embed removes runtime path dependency |
| Test DB lifecycle | `t.TempDir()` + local Postgres | `testcontainers-go` | Real Postgres 17; snapshot/restore is 10x cheaper than container-per-test |
| Per-test DB reset | DROP/CREATE schema in each test | `ctr.Snapshot()` + `ctr.Restore()` | Snapshot is a Postgres template copy — order-of-magnitude faster |
| Connection pool | `sync.Mutex` + `[]*pgx.Conn` slice | `pgxpool` | Health checks, acquire timeout, MaxConnLifetime, concurrency-safe — solved problems |
| DSN construction | String concatenation | `pgxpool.ParseConfig` | URL-encodes special chars, validates DSN before pool creation, exposes Config struct for tuning |

**Key insight:** The Postgres stack has solved all of these problems in well-maintained, well-tested
libraries. Hand-rolling any of them introduces subtle concurrency, lifecycle, or portability bugs.

---

## Common Pitfalls

### Pitfall 1: Seq Scan in index-proof test (D-07)
**What goes wrong:** EXPLAIN returns `Seq Scan` even though the composite index exists.
**Why it happens:** Either the table has too few rows (planner cost model prefers seq scan on
single-page tables) or `ANALYZE` hasn't run (planner sees 0 estimated rows and falls back to seq scan).
**How to avoid:** Seed ≥100k rows, call `ANALYZE gpu_metrics` before `EXPLAIN`, and query a
**selective** filter (single `gpu_id` over a short time window, not a full table scan).
**Warning signs:** `EXPLAIN` output contains `Seq Scan on gpu_metrics` or plan cost is suspiciously low.

### Pitfall 2: Timestamp precision collapse (cross-phase, D-05 + Phase 3)
**What goes wrong:** The natural-key constraint `(gpu_id, metric_name, timestamp)` silently deduplicates
readings that are genuinely distinct (e.g., two GPU_UTIL readings 50ms apart, same GPU).
**Why it happens:** If Streamer restamps with `time.Now().UTC().Format(time.RFC3339)` (second
granularity), all readings within the same second share the same `timestamp` value and the
unique constraint treats them as the same logical event.
**How to avoid:** Phase 3 MUST restamp with `time.RFC3339Nano`. The `TIMESTAMPTZ` column holds
microsecond precision; the restamp must match. Surface this as a locked constraint in Phase 3 planning.
**Warning signs:** Phase 3 Collector test shows fewer rows inserted than expected under high throughput.

### Pitfall 3: golang-migrate URL scheme mismatch
**What goes wrong:** `migrate.NewWithSourceInstance("iofs", src, dsn)` returns an error `no driver
for postgres` or similar, even with the pgx/v5 driver import.
**Why it happens:** golang-migrate for pgx/v5 expects the `pgx5://` URL scheme. Standard
`postgres://` or `postgresql://` DSNs are handled by the `postgres` driver (which requires `lib/pq`,
not pgx/v5). The pgx/v5 driver is registered under `pgx5`.
**How to avoid:** Replace `postgres://` with `pgx5://` before passing to golang-migrate. Or use a
separate DSN for migration calls:
  `pgx5DSN := strings.Replace(dsn, "postgres://", "pgx5://", 1)`
**Warning signs:** Error message mentions "no driver found", "unknown driver", or database does not open.

### Pitfall 4: CopyFrom cannot express ON CONFLICT (Phase 3 landmine)
**What goes wrong:** Phase 3 tries to use `pgxpool.CopyFrom` for bulk inserts with idempotency, but
`COPY` protocol has no `ON CONFLICT` clause — all unique constraint violations abort the entire COPY.
**Why it happens:** The COPY protocol is a raw stream insert with no per-row conflict handling. The
unique constraint established in Phase 2 (DB-04) is correct, but the insertion mechanism matters.
**How to avoid:** Phase 3 must use `INSERT ... ON CONFLICT (gpu_id, metric_name, timestamp) DO UPDATE`
or `DO NOTHING` in a batched form (e.g., via `pgx.Batch`/`SendBatch`). CopyFrom remains valid for
scenarios without a uniqueness requirement. Flag this as a locked Phase 3 decision.
**Warning signs:** Phase 3 test with seeded duplicates fails with "duplicate key value violates unique constraint" mid-COPY.

### Pitfall 5: testcontainers Snapshot with username != "postgres"
**What goes wrong:** `ctr.Snapshot(ctx)` silently fails or `ctr.Restore(ctx)` panics.
**Why it happens:** The Snapshot feature uses `pg_dump`/`CREATE DATABASE ... TEMPLATE` and connects
as the container user. A known bug (testcontainers-go issue #2474): if the container username is not
`postgres`, snapshot creation may fail silently. WithSQLDriver("pgx") is also required.
**How to avoid:** Use `postgres.WithUsername("postgres")` in `postgres.Run(...)`, and always pass
`postgres.WithSQLDriver("pgx")`. Use a non-default database name (not `postgres`).
**Warning signs:** Restore appears to succeed but test data from the previous test persists.

### Pitfall 6: Ping context deadline on pool creation
**What goes wrong:** `pool.Ping(ctx)` hangs indefinitely or returns context deadline exceeded.
**Why it happens:** The context passed to `pgxpool.NewWithConfig` and `Ping` is used for the initial
connection attempt. If a background context is passed with no deadline and the DB is unreachable,
`Ping` blocks until the OS TCP timeout (~2min).
**How to avoid:** Use a context with a reasonable deadline for startup ping:
  `pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second); defer cancel(); pool.Ping(pingCtx)`
**Warning signs:** Service startup hangs for minutes before failing; no error is logged promptly.

---

## Code Examples

### Complete `pkg/db` public surface

```go
// Source: derived from pkg.go.dev/github.com/jackc/pgx/v5/pgxpool [CITED: pkg.go.dev]
// pkg/db/config.go

package db

import (
    "fmt"
    "os"
    "strconv"
)

// Config holds all pool configuration. FromEnv reads standard env vars.
type Config struct {
    DSN      string // postgres:// or pgx5:// DSN
    MaxConns int32  // 0 = use pgxpool default (max(4, NumCPU))
}

// FromEnv builds Config from environment variables.
// VANTAGE_DB_DSN (required) — postgres://user:pass@host:5432/dbname?sslmode=disable
// VANTAGE_DB_MAX_CONNS (optional, default 0 = library default)
func FromEnv() (Config, error) {
    dsn := os.Getenv("VANTAGE_DB_DSN")
    if dsn == "" {
        return Config{}, fmt.Errorf("db: VANTAGE_DB_DSN is required")
    }
    cfg := Config{DSN: dsn}
    if s := os.Getenv("VANTAGE_DB_MAX_CONNS"); s != "" {
        n, err := strconv.ParseInt(s, 10, 32)
        if err != nil {
            return Config{}, fmt.Errorf("db: VANTAGE_DB_MAX_CONNS: %w", err)
        }
        cfg.MaxConns = int32(n)
    }
    return cfg, nil
}
```

### docker-compose.yml for dev stack

```yaml
# Source: docker.com/compose reference [ASSUMED]
# docker-compose.yml (repo root)
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: vantage
      POSTGRES_USER: vantage
      POSTGRES_PASSWORD: vantage
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U vantage -d vantage"]
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  pgdata:
```

Makefile targets to add:
```makefile
dev-up: ## Start local dev dependencies (Postgres)
	docker compose up -d --wait

dev-down: ## Stop local dev dependencies
	docker compose down
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `pgx v4` (github.com/jackc/pgx/v4) | `pgx v5` (jackc/pgx/v5) | 2022 | v5 redesigned pgxpool; `pgx.CopyFromRows` signature changed; mixing v4/v5 causes type conflicts |
| `lib/pq` + `database/sql` | `pgx v5` direct + `pgxpool` | ongoing | pgx bypasses database/sql abstraction; native pgx types (pgtype), better error messages, faster |
| `golang-migrate` PostgreSQL driver | `golang-migrate` pgx/v5 driver (`database/pgx/v5`) | v4.15+ | pgx/v5 requires separate driver import; URL scheme is `pgx5://` not `postgres://` |
| `testcontainers-go` per-test container | `testcontainers-go` Snapshot/Restore | v0.28+ | Snapshot creates Postgres DB template; Restore drops/recreates from template — ~10x faster than new container |
| Sequential index | `(gpu_id, timestamp DESC)` composite | N/A | Composite covers the primary query pattern; DESC matches natural read order |

**Deprecated/outdated:**
- `lib/pq`: unmaintained; pgx/v5 is the community standard for new Go Postgres code.
- `pgx v4`: Still widely used but v5 has been stable since 2022; mixing v4 and v5 in one module causes type conflicts.
- `golang-migrate` v3: Use v4 (import path includes `/v4`).

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | docker-compose.yml uses `postgres:17-alpine` image tag | Code Examples | Minor: different image name; fix by checking Docker Hub |
| A2 | `strings.Replace(dsn, "postgres://", "pgx5://", 1)` correctly converts DSN for golang-migrate pgx5 driver | Pattern 2 | Medium: migrate fails with "no driver"; workaround is to pass `pgx5://` DSN directly |
| A3 | 100k rows at representative distribution is sufficient for index preference to emerge | Pattern 4 | Medium: if planner still picks seq scan, increase to 500k or adjust seqscan cost parameters |
| A4 | testcontainers-go Snapshot bug (#2474) is fixed in v0.43.0 when WithSQLDriver("pgx") is used | Pitfall 5 | High: Snapshot silently fails; mitigation is to always run a post-restore row count assertion |
| A5 | `pkg/db.Migrate()` will be called by both Collector and Gateway at startup | Architecture | Medium: if only one service runs migrations, a race on first deploy is possible — consider a separate init job in Phase 5 |

---

## Open Questions (RESOLVED)

> Resolved during Phase 2 planning (plans 02-01, 02-02): Q1 — `Migrate()` lives in `pkg/db` AND a
> thin `cmd/migrate` one-shot entrypoint reuses it; Q2 — env var is `VANTAGE_DB_DSN`; Q3 — the
> `ON CONFLICT` vs `pgx.Batch` choice is a Phase-3 decision, flagged forward (not decided here).

1. **Should `Migrate()` live in `pkg/db` or a `cmd/migrate` binary?**
   - What we know: CONTEXT.md says "migrate runner lives in `pkg/db` or a small `cmd/...` path" (discretion).
   - What's unclear: A shared function in `pkg/db` means Collector AND Gateway both run migrations at startup — creates a deploy-time advisory-lock race if both start simultaneously. A separate one-shot job avoids this.
   - Recommendation: Put `Migrate()` in `pkg/db` (simple, no extra binary) and document that services run migrations sequentially — advisory lock in golang-migrate handles the concurrent-start case. Phase 5 can wrap it in an init-job if desired.

2. **`VANTAGE_DB_DSN` vs `DATABASE_URL`?**
   - What we know: CONTEXT.md leaves this to Claude's discretion.
   - What's unclear: `DATABASE_URL` is a Heroku/12-factor convention that many tools (Railway, Render) recognize automatically. `VANTAGE_DB_DSN` is more explicit and scoped.
   - Recommendation: `VANTAGE_DB_DSN` — this is a Kubernetes deployment (Phase 5), not a PaaS; namespaced env vars avoid collision with any infra tooling that might inject `DATABASE_URL`.

3. **`pgx.Batch` vs `INSERT ... ON CONFLICT` for Phase 3 idempotent inserts (Phase 3 decision, surface now)**
   - What we know: Phase 2 establishes the unique constraint `(gpu_id, metric_name, timestamp)`. Phase 3 must use `ON CONFLICT`. CopyFrom cannot do upserts.
   - What's unclear: Whether Phase 3 uses `pgx.Batch/SendBatch` (batched per-row statements) or a multi-value `INSERT ... VALUES (...), (...) ON CONFLICT` approach.
   - Recommendation: Surface in Phase 3 CONTEXT.md. Either approach is correct; `SendBatch` has cleaner per-row error reporting.

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go | All compilation | ✓ | 1.26 | — |
| Docker | testcontainers-go (tests) | [check at test time] | — | Skip integration tests; unit tests only |
| psql CLI | Phase-2 smoke script | [check at smoke time] | — | Replace with `go run` query tool |
| docker compose | `make dev-up` | [check at smoke time] | — | Run Postgres directly: `docker run -d postgres:17-alpine` |

**Missing dependencies with no fallback:** Docker is required for testcontainers-go integration tests.
If Docker is unavailable, the ≥90% coverage gate cannot be met (integration tests cover `pkg/db`
logic). The plan should include a build tag (`//go:build integration`) to skip container tests in
environments without Docker.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | `go test` (standard library) |
| Config file | none — via `go test ./...` and `make test` |
| Quick run command | `go test -race ./pkg/db/...` |
| Full suite command | `make test` (= `go test -race -count=1 ./...`) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| DB-01 | Migration creates `gpu_metrics` table with correct columns | Integration | `go test -race ./pkg/db/... -run TestMigration` | ❌ Wave 0 |
| DB-02 | `EXPLAIN` on `(gpu_id, ts DESC)` range query shows `Index Scan` at 100k rows | Integration | `go test -race ./pkg/db/... -run TestCompositeIndexUsed` | ❌ Wave 0 |
| DB-03 | `New(ctx, cfg)` returns healthy pool that can `Ping` | Integration | `go test -race ./pkg/db/... -run TestNew` | ❌ Wave 0 |
| DB-04 | Inserting a duplicate `(gpu_id, metric_name, timestamp)` triggers constraint | Integration | `go test -race ./pkg/db/... -run TestUniqueConstraint` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test -race ./pkg/db/... -run TestNew` (quick smoke, no seed)
- **Per wave merge:** `make test && make coverage`
- **Phase gate:** Full suite green before `/gsd-verify-work`

### Wave 0 Gaps
- [ ] `pkg/db/db_test.go` — TestMain + TestMigration + TestNew + TestCompositeIndexUsed + TestUniqueConstraint
- [ ] `pkg/db/db.go` — New(), Migrate(), Config
- [ ] `pkg/db/config.go` — Config struct, FromEnv()
- [ ] `pkg/db/migrations/000001_init_schema.up.sql`
- [ ] `pkg/db/migrations/000001_init_schema.down.sql`
- [ ] Framework install: `go get github.com/jackc/pgx/v5@v5.10.0 github.com/golang-migrate/migrate/v4@v4.19.1 github.com/testcontainers/testcontainers-go@v0.43.0 github.com/testcontainers/testcontainers-go/modules/postgres@v0.43.0`

---

## Security Domain

> `security_enforcement: true` (from .planning/config.json). ASVS Level 1 active.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | This phase: no user-facing API, no auth |
| V3 Session Management | no | Pool connections are not user sessions |
| V4 Access Control | no | No API endpoints this phase |
| V5 Input Validation | yes (low risk) | DSN env var validated in `FromEnv()`; SQL is parameterized (not string-concatenated) |
| V6 Cryptography | no | No secrets stored; DSN is env-injected |
| V8 Data Protection | partial | DSN contains DB credentials — must not be logged |

### Known Threat Patterns for Go + PostgreSQL

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| SQL injection via query string | Tampering | pgx uses parameterized queries (`$1`, `$2`); never concatenate values into SQL |
| DSN credential logging | Info Disclosure | Redact DSN in error messages; log only host/port, not password |
| Overly permissive `MaxConns` | DoS | Default `max(4, NumCPU)` is safe; cap via `VANTAGE_DB_MAX_CONNS` env var |
| Migration race on concurrent startup | Denial of Service | golang-migrate uses PostgreSQL advisory lock — only one runner proceeds; others wait |

---

## Sources

### Primary (MEDIUM confidence — verified via proxy.golang.org and pkg.go.dev)
- [proxy.golang.org — jackc/pgx/v5 v5.10.0](https://proxy.golang.org/github.com/jackc/pgx/v5/@latest) — version and publish date verified
- [proxy.golang.org — golang-migrate/migrate/v4 v4.19.1](https://proxy.golang.org/github.com/golang-migrate/migrate/v4/@latest) — version and publish date verified
- [proxy.golang.org — testcontainers-go v0.43.0](https://proxy.golang.org/github.com/testcontainers/testcontainers-go/@latest) — version and publish date verified
- [pkg.go.dev — pgxpool API](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool) — New, ParseConfig, Config fields, Ping, Close
- [pkg.go.dev — iofs source driver](https://pkg.go.dev/github.com/golang-migrate/migrate/v4/source/iofs) — New function, embed.FS usage pattern
- [pkg.go.dev — golang-migrate/database/pgx/v5](https://pkg.go.dev/github.com/golang-migrate/migrate/v4/database/pgx/v5) — pgx5:// URL scheme, WithInstance signature
- [pkg.go.dev — testcontainers-go/modules/postgres](https://pkg.go.dev/github.com/testcontainers/testcontainers-go/modules/postgres) — Run, BasicWaitStrategies, Snapshot, Restore, MustConnectionString
- [golang.testcontainers.org — Postgres module docs](https://golang.testcontainers.org/modules/postgres/) — TestMain + Snapshot/Restore pattern

### Secondary (LOW confidence — web content, cross-checked)
- [PostgreSQL docs — Using EXPLAIN](https://www.postgresql.org/docs/current/using-explain.html) — cost model, enable_seqscan behavior, ANALYZE statistics
- [Cybertec — Index scan vs seq scan](https://www.cybertec-postgresql.com/en/postgresql-indexing-index-scan-vs-bitmap-scan-vs-sequential-scan-basics/) — scan type selection thresholds
- [testcontainers-go issue #2474](https://github.com/testcontainers/testcontainers-go/issues/2474) — Snapshot bug with non-default username (flags Pitfall 5)

---

## Metadata

**Confidence breakdown:**
- Standard stack (versions): HIGH — all confirmed on proxy.golang.org with VCS origins
- pgxpool API: MEDIUM — confirmed via pkg.go.dev
- golang-migrate iofs pattern: MEDIUM — confirmed via pkg.go.dev
- testcontainers Snapshot/Restore: MEDIUM — confirmed via official docs; Pitfall 5 risk LOW/MEDIUM (known bug, version-specific)
- EXPLAIN / index-scan behavior: MEDIUM — confirmed via PostgreSQL official docs
- Schema DDL: HIGH — derived directly from CONTEXT.md locked decisions + DCGM CSV inspection
- Test pattern (assertion code): LOW/ASSUMED — standard Go testing idiom; specific behavior depends on runtime

**Research date:** 2026-06-28
**Valid until:** 2026-07-28 (packages are stable; EXPLAIN behavior is Postgres-version-stable at 17)
