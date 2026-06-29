# Phase 2: Storage Foundation — Schema + Connection Pool - Context

**Gathered:** 2026-06-27
**Status:** Ready for planning
**Source:** discuss-phase (4 forks resolved with user) + project research

<domain>
## Phase Boundary

This phase delivers the **PostgreSQL storage foundation** — schema + shared connection pool —
and nothing that reads or writes it at runtime. In scope:
- A **migration** that creates the time-series telemetry table with the mandated composite index
  `(gpu_id, timestamp DESC)` and a natural-key unique constraint for idempotent inserts.
- `pkg/db` — a shared `pgxpool` initializer that **both** the Collector (Phase 3) and the API
  Gateway (Phase 4) import and reuse.
- A test that **proves the composite index is used** via `EXPLAIN` at representative scale.

**Out of scope this phase:** the Collector and its batch-insert/upsert logic (Phase 3), the
Streamer (Phase 3), the API Gateway read queries (Phase 4), Dockerfiles/Helm (Phase 5), WAL
durability (Phase 6). This phase builds the seam everyone else plugs into; it does not wire any
service to it. Requirements: **DB-01, DB-02, DB-03, DB-04**.

**Mode:** MVP (vertical slice) — per ROADMAP. The planner organizes the slice as
migration → pool → index-proof, the thinnest end-to-end "storage works" vertical.

**Cross-cutting deliverables established this phase (new project convention, see D-08/D-09):**
Phase 2 is the first phase to need Postgres/Docker, so it *establishes* the manual-test harness and
the living README that every subsequent phase extends:
- `docker-compose.yml` dev stack + `make dev-up`/`make dev-down` (Postgres now; services added later).
- `scripts/smoke/` + `make smoke-NN` (per phase) and `make smoke` (all phases so far).
- `README.md` quickstart, grown incrementally per phase.
This includes a thin **backfill of Phase 1 (MQ)** — a `smoke/phase01` MQ check + its README section —
so the suite and quickstart are coherent from the start, not retrofitted later.

</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Schema shape (DB-01)
- **D-01 — Long/narrow table.** One row per metric reading:
  `gpu_metrics(gpu_id, timestamp, metric_name, value, + descriptive dims)`. This maps the proto
  `TelemetryMessage` **1:1** (each message already carries exactly one `metric_name` + one `value`),
  and makes the per-message idempotent upsert (COLL-05, Phase 3) trivial. **Rejected** the wide/pivoted
  "one column per metric" shape — it would force buffering/pivoting ~10 streamed messages into a single
  row, fighting the reactive per-message insert model and complicating upsert.
- **D-02 — "Numerical columns" satisfied by `value DOUBLE PRECISION`.** The brief's wording
  (`gpu_id`, `timestamp`, "numerical columns") is met by a numeric `value` column in the long schema;
  `metric_name` (TEXT) names which metric the value is. The 10 known DCGM metrics
  (`DCGM_FI_DEV_GPU_UTIL`, `_FB_USED`, `_GPU_TEMP`, `_POWER_USAGE`, `_SM_CLOCK`, `_MEM_CLOCK`,
  `_MEM_COPY_UTIL`, `_FB_FREE`, `_ENC_UTIL`, `_DEC_UTIL`) are data, not columns.

### GPU identity + natural key (DB-02, DB-04)
- **D-03 — Canonical GPU identity = the GPU `uuid`** (e.g. `GPU-5fd4f087-...`), which is globally
  unique across hosts — production-correct for a fleet. The ordinal (`"0"`), `device` (`nvidia0`),
  `model_name`, and `hostname` are stored as **descriptive attributes**, not identity.
- **D-04 — Reconcile with the spec's literal `gpu_id` index/API.** The brief hard-mandates the
  composite index `(gpu_id, timestamp DESC)` and the route `/api/v1/gpus/{id}`. Resolution: **keep the
  identity column named `gpu_id`** (so the mandated index expression and `{id}` path are honored
  verbatim) but **store the uuid value in it**. An id is an id; the column name matches the spec, the
  value is the globally-unique uuid. Document this clearly in the migration so it isn't surprising.
- **D-05 — Natural-key unique constraint = `(gpu_id, metric_name, timestamp)`.** This is what makes
  at-least-once WAL redelivery (Phase 6) idempotent: redelivering the same logical reading hits the
  same key and upserts instead of duplicating. The Collector's `ON CONFLICT (gpu_id, metric_name,
  timestamp)` (Phase 3, COLL-05) targets exactly this constraint.

### Migration mechanism (DB-01)
- **D-06 — `golang-migrate` with `go:embed`-ed versioned SQL.** Up/down `.sql` files embedded in the
  binary and applied programmatically. De-facto standard, testable, and gives a clean deploy story
  (a one-shot migrate init-job/container in Phase 5). Accepts one new dependency — justified by the
  project's "production-grade reference implementation" goal. **Rejected** the zero-dep idempotent
  `schema.sql`-at-startup (no version history/rollback) and goose (no preference for it over migrate).

### Index-usage proof (DB-02)
- **D-07 — Prove the planner *chooses* the index at scale.** Use `testcontainers-go`
  (`postgres:17-alpine`), seed **~100k rows** spread across many `gpu_id`s and timestamps so the
  planner genuinely prefers the index, run `EXPLAIN` on a `(gpu_id, timestamp DESC)` range query, and
  **assert the plan contains an `Index Scan`** on the composite index. **Rejected** the small-seed +
  `SET enable_seqscan=off` approach — it proves the index is *usable*, not that the planner *picks* it,
  which is the actual DB-02 claim. (Mirrors the Phase-1 lesson: size test workloads so the gate proves
  the real property, not an artifact.)

### Cross-cutting: living README + manual smoke suite (DOC-01, QA-06 — project convention)
- **D-08 — Living README quickstart, grown per phase.** `README.md` is built **incrementally** as
  phases complete (not deferred to Phase 5). Each phase's plan MUST include a task to add/extend the
  README quickstart for the component(s) that phase delivers, so a reader can clone → run → see it work.
  README ownership normally sits with DevOps/QA (Phase 5), but the incremental cadence means every
  phase touches it; that is intended and authorized.
- **D-09 — Runnable manual smoke suite, Makefile-driven + docker-compose.** A manual, runnable test
  suite the user can execute to verify each phase's deliverables:
  - `scripts/smoke/phaseNN-*.sh` — runnable shell smoke checks (psql/curl/grpcurl), one set per phase.
  - `make smoke-NN` runs one phase's smoke; `make smoke` runs all phases shipped so far.
  - `docker-compose.yml` + `make dev-up`/`make dev-down` provide local dependencies (Postgres from this
    phase on) **without** needing the Phase-5 kind/Helm stack — manual testing works for Phases 2–4.
  - This is distinct from the automated integration tests (QA-03) and the coverage gate (QA-04): smoke
    scripts are a *human-run, watch-it-work* demo/verification harness, kept simple and dependency-light.
  - **Phase-2 smoke specifically:** `make dev-up` → run migration → `psql` shows the table + composite
    index exist → an `EXPLAIN` on a range query shows `Index Scan` (the DB-02 claim, runnable by hand).
  - **Backfill Phase 1 (MQ)** this phase: a small smoke that starts MQ, produces/consumes a message,
    and `curl`s `/api/v1/queue/inspect`, plus its README section.

### Claude's Discretion
- DSN/config plumbing for the pool: env var name (e.g. `VANTAGE_DB_DSN` / `DATABASE_URL`), `MaxConns`
  default and how it's overridden, `pgxpool.ParseConfig` tuning — choose idiomatic defaults, document
  briefly.
- `pkg/db` surface shape: constructor signature (e.g. `New(ctx, cfg) (*pgxpool.Pool, error)`), whether
  the migrate runner lives in `pkg/db` or a small `cmd/.../migrate` path, health-check helper — keep
  it minimal and reusable by both Collector and Gateway.
- Exact non-identity column set/types (`device`, `model_name`, `hostname`, `container`, `pod`,
  `namespace`, `labels_raw`) and whether any are nullable — follow the proto/CSV, keep numeric `value`
  as `DOUBLE PRECISION`.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Authoritative spec & conventions
- `instructions.md` §3 (PostgreSQL Database Layer & Schema) — mandates relational time-series schema
  (`gpu_id`, `timestamp TIMESTAMPTZ`, numerical columns) and the composite index `(gpu_id, timestamp DESC)`.
- `CLAUDE.md` — repo conventions, layout, hard constraints (pgx/v5 `pgxpool`, composite index in DDL,
  directory ownership: `pkg/db` is Storage/Pipeline Engineer's surface).
- `.planning/PROJECT.md` — project context + Key Decisions.
- `.planning/REQUIREMENTS.md` — DB-01..DB-04 (this phase), plus downstream COLL-05 / QA-03 / DUR-02
  that consume this schema.

### Contracts produced upstream (Phase 1)
- `api/proto/mq.proto` — `TelemetryMessage` field set the schema must persist (long format, one
  `metric_name`+`value` per message; `gpu_id` ordinal + `uuid` both present on the wire).
- `pkg/pb/` — generated structs the Collector (Phase 3) maps into the DB model.

### Research (phase-relevant)
- `.planning/research/STACK.md` — pgx/v5 (`v5.10.0`), testcontainers-go (`v0.43.0`), Postgres image.
- `.planning/research/ARCHITECTURE.md` — service boundaries; `pkg/db` as the only shared DB surface.
- `.planning/research/PITFALLS.md` — pgxpool concurrency, COPY vs batch, EXPLAIN/index pitfalls.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pkg/pb/mq.pb.go` — `TelemetryMessage` Go struct; the Phase-3 Collector maps it to the DB model, so
  the schema column set should align with these fields (names/types) to keep mapping mechanical.

### Established Patterns
- Single Go module `github.com/ajitg/vantage`; shared code lives only in `pkg/` (here: `pkg/db`,
  and likely `pkg/models` for the telemetry row struct). Services never import each other.
- Phase-1 convention: real logic in importable packages so the ≥90% coverage gate measures logic, not
  `main` wiring. `pkg/db` is logic → it is on the coverage gate.

### Integration Points
- `pkg/db` pool is imported by **both** Collector (Phase 3 writer) and Gateway (Phase 4 reader) — design
  the constructor + config to serve both roles without service-specific assumptions.

</code_context>

<specifics>
## Specific Ideas

- **Identity column carries the uuid but is named `gpu_id`** (D-04) — add a SQL comment on the column
  stating this so future readers/the Gateway aren't surprised that `gpu_id` looks like a uuid.
- **Timestamp precision is load-bearing for the natural key (cross-phase flag for Phase 3).** The
  proto comment specifies the Streamer restamps with `time.RFC3339`, which is **second-granularity**.
  Many readings per (gpu_id, metric_name) per second would then collapse to the same `(gpu_id,
  metric_name, timestamp)` key and upsert over each other — losing distinct readings by accident, not
  by redelivery. **Assumption to validate in Phase 3:** restamp at sub-second precision
  (`time.RFC3339Nano`) so the natural key separates genuinely-distinct readings; the `timestamp`
  column is `TIMESTAMPTZ` (microsecond) and can hold it. Phase 2 defines the key; Phase 3 must honor
  the precision. Surface this in the plan — do not silently assume second-precision is fine.
- Seed-at-scale index test (~100k rows) should spread timestamps over a realistic window and across
  multiple `gpu_id`s/`metric_name`s, then `ANALYZE` before `EXPLAIN` so stats are populated.

</specifics>

<deferred>
## Deferred Ideas

- **Collector batch insert / `ON CONFLICT` upsert against this constraint** → Phase 3 (COLL-03/04/05).
  `pgx.CopyFrom` vs `INSERT ... ON CONFLICT` trade-off is a Phase-3 decision; COPY has no upsert, so the
  idempotent path likely uses batched `INSERT ... ON CONFLICT` — note for Phase 3, not decided here.
- **Streamer restamp precision (RFC3339Nano)** → Phase 3 (see specifics flag above).
- **API read queries / `/gpus/{id}` resolution against the uuid identity** → Phase 4.
- **WAL replay driving the idempotent path end-to-end** → Phase 6 (DUR-02, QA-05).
- **Retention/partitioning of the time-series table** → out of v1 scope (not in the brief).

</deferred>

---

*Phase: 02-storage-foundation-schema-connection-pool*
*Context gathered: 2026-06-27 via discuss-phase*
