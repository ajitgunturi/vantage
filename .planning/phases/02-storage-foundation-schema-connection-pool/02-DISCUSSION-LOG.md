# Phase 2: Storage Foundation — Schema + Connection Pool - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-27
**Phase:** 2-storage-foundation-schema-connection-pool
**Areas discussed:** Schema shape, GPU identity + natural key, Migration mechanism, Index-usage proof

---

## Schema shape (DB-01)

| Option | Description | Selected |
|--------|-------------|----------|
| Long/narrow | One row per reading: `gpu_metrics(identity, timestamp, metric_name, value, dims)`. Maps proto 1:1; trivial per-message upsert. | ✓ |
| Wide/pivoted | One column per DCGM metric. Literal "numerical columns", but requires pivoting ~10 messages into one row; fights reactive insert + upsert. | |

**User's choice:** Long/narrow (recommended)
**Notes:** The proto `TelemetryMessage` already carries exactly one `metric_name`+`value` per message, and the CSV is DCGM long/EAV format. Long schema is the natural, almost-forced fit for the streaming + idempotent-upsert model. "Numerical columns" satisfied by `value DOUBLE PRECISION`.

---

## GPU identity + natural key (DB-02, DB-04)

| Option | Description | Selected |
|--------|-------------|----------|
| uuid | Identity = GPU uuid (globally unique across hosts). Ordinal/hostname as attributes. Natural key = (uuid, metric_name, timestamp). | ✓ |
| ordinal gpu_id ('0') | Literal spec wording; simplest; but ambiguous across hosts — weak identity/key. | |
| hostname + ordinal | Composite identity without uuid; complicates API path + index. | |

**User's choice:** uuid (recommended)
**Notes:** Reconciliation captured as D-04 — the identity column stays **named `gpu_id`** (honoring the spec-mandated index `(gpu_id, timestamp DESC)` and `/gpus/{id}` route verbatim) but **stores the uuid value**. Natural-key unique constraint = `(gpu_id, metric_name, timestamp)` drives Phase-3 `ON CONFLICT` idempotency.

---

## Migration mechanism (DB-01)

| Option | Description | Selected |
|--------|-------------|----------|
| golang-migrate + embedded SQL | Versioned up/down `.sql` via `go:embed`, applied programmatically. Standard, testable, clean deploy. One new dep. | ✓ |
| Idempotent schema.sql at startup | Single embedded `schema.sql` run by pkg/db via pgx. Zero deps, simplest; no version history/rollback. | |
| goose | Comparable alternative migration tool. | |

**User's choice:** golang-migrate + embedded SQL (recommended)
**Notes:** Justified by the project's "production-grade reference implementation" goal; gives a one-shot migrate init-job in Phase 5.

---

## Index-usage proof (DB-02)

| Option | Description | Selected |
|--------|-------------|----------|
| Seed at scale, assert Index Scan | testcontainers Postgres, seed ~100k rows across many gpu_ids/timestamps, EXPLAIN range query, assert `Index Scan`. Proves planner *chooses* it. | ✓ |
| Small seed + disable seqscan | Few rows + `SET enable_seqscan=off`. Proves usability, not planner choice. | |

**User's choice:** Seed at scale, assert Index Scan (recommended)
**Notes:** Mirrors the Phase-1 lesson — size the test workload so the gate proves the real property (planner picks the index), not an artifact. `ANALYZE` before `EXPLAIN`.

---

## Claude's Discretion

- DSN/config plumbing (env var name, `MaxConns` default + override, `pgxpool.ParseConfig` tuning).
- `pkg/db` surface shape (constructor signature, where the migrate runner lives, health-check helper).
- Exact non-identity column set/types from the proto/CSV (`device`, `model_name`, `hostname`, `container`, `pod`, `namespace`, `labels_raw`), nullability.

## Deferred Ideas

- Collector batch insert / `ON CONFLICT` upsert against the constraint → Phase 3 (COPY has no upsert; likely batched `INSERT ... ON CONFLICT`).
- Streamer restamp precision (`RFC3339Nano`) — load-bearing for the natural key; validate in Phase 3.
- API read queries / `/gpus/{id}` resolution against uuid identity → Phase 4.
- WAL replay driving idempotency end-to-end → Phase 6.
- Retention/partitioning of the time-series table → out of v1 scope.
