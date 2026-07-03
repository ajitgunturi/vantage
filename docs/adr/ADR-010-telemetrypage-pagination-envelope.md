# ADR-010: `TelemetryPage` Pagination Envelope with Limit+1 Sentinel

**Status:** Accepted
**Date:** 2026-07-03
**Phase:** 6 — Production Hardening + Assignment Alignment (requirement API-05, audit finding F-06)
**Backfilled:** 2026-07-03
**Related:** [ADR-006](ADR-006-long-narrow-schema-natural-key.md) (composite index this pagination queries over)

## Context

Phase 4 shipped the initial `GET /api/v1/gpus/{id}/telemetry` endpoint with a
configurable `VANTAGE_GATEWAY_MAX_ROWS` ceiling (default 1000) and `X-Truncated`
/ `X-Row-Limit` response headers to signal when the result was capped. API
pagination was documented as **out of scope** in the Phase 4 plan.

Phase 6 was triggered by a post-Phase-5 audit (`06-REVIEW.md`) that surfaced
finding **F-06 / API-05**: the truncation-header approach is non-standard and
provides no real pagination capability — clients cannot determine the total count,
cannot fetch the next page, and are unlikely to inspect HTTP headers for pagination
cues. The ROADMAP Evolution footnote records the move of API pagination from
Out-of-Scope to in-scope for Phase 6 (plan 06-02).

Two specific implementation concerns arose during plan 06-02 design:

1. **`has_next` without a COUNT query:** A separate `SELECT COUNT(*)` to compute
   total rows doubles the DB round-trip per request. A `limit+1` sentinel row
   technique — fetch one more row than requested; if N+1 rows are returned, more
   exist; trim the extra row before responding — computes `has_next` in a single
   query.

2. **SQL injection via OFFSET:** Constructing `OFFSET` as a string in the SQL
   statement (e.g., `fmt.Sprintf("... OFFSET %d", offset)`) is classified as
   T-06-03 in the phase threat register. Passing OFFSET as a pgx positional
   placeholder (`$N`) eliminates this vector.

Sources: 06-REVIEW.md finding F-06 / API-05, plan 06-02, STATE.md ## Decisions
(limit+1 sentinel, OFFSET placeholder T-06-03), ROADMAP Evolution footnote
(pagination moved out of Out-of-Scope).

## Decision

Replace the `X-Truncated` / `X-Row-Limit` response headers with a
**`TelemetryPage` JSON envelope** that wraps the telemetry data array alongside
pagination metadata.

Response shape:

```json
{
  "data": [ /* telemetry rows */ ],
  "pagination": {
    "limit":    50,
    "offset":   0,
    "has_next": true
  }
}
```

Key implementation choices:

1. **`limit` and `offset` as query parameters** — pushed down into the
   composite-index SQL path: `SELECT ... ORDER BY timestamp DESC LIMIT $1 OFFSET $2`.
   Both are pgx positional placeholders — no string concatenation (T-06-03).

2. **`limit+1` sentinel for `has_next`** — the DB query fetches `limit+1` rows.
   If the result has `limit+1` entries, `has_next = true` and the extra row is
   trimmed before serialisation. If the result has `≤ limit` entries, `has_next = false`.
   No separate `COUNT(*)` query.

3. **Breaking response-shape change** — this replaces the earlier `[]TelemetryRow`
   top-level array response with `TelemetryPage`. Clients written against the
   Phase 4 response shape must be updated. This is acceptable because there are
   no external API consumers at this stage; the change is documented in the README
   and in the OpenAPI spec (swag annotations updated).

4. **`ReadyzHandler` response is generic** — unrelated but same-plan fix: the
   readiness probe response contains no DSN text or driver version strings
   (T-06-05).

The `VANTAGE_GATEWAY_MAX_ROWS` ceiling is retained as a server-side hard cap
(default 1000) applied on top of the client-requested `limit`. Clients cannot
request more than `MAX_ROWS` rows per page.

## Consequences

- **Positive:** Standard pagination API — clients receive an explicit `has_next`
  flag and can fetch subsequent pages by incrementing `offset`.
- **Positive:** Single DB query per request (limit+1 sentinel avoids `COUNT(*)`).
- **Positive:** OFFSET as a pgx placeholder eliminates the SQL injection vector
  (T-06-03 closed).
- **Negative:** Breaking change to the response envelope — existing clients see
  a top-level object instead of an array. Documented in the OpenAPI spec and
  README; no external consumers at this stage.
- **Negative:** `OFFSET`-based pagination degrades at large offsets — Postgres
  must scan and discard the first `offset` rows. For the fixture-scale dataset
  (≤ 200k rows per GPU) this is not a practical concern. Keyset/cursor pagination
  would be required at true scale.
- **Neutral:** This decision reverses the "API pagination out of scope" entry from
  Phase 4 planning. The ROADMAP Evolution footnote records this as an explicit
  scope expansion driven by the Phase 6 audit.

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| Keep `X-Truncated` / `X-Row-Limit` headers | Non-standard; clients miss them unless they specifically inspect headers; no real pagination (can only tell if truncated, not how to fetch the next page). Finding F-06 in the audit rated this as a user-experience gap requiring resolution. |
| `COUNT(*)` for total row count | Correct — provides `total_count` to clients. Extra DB round-trip per request; adds latency proportional to table size. The sentinel technique computes `has_next` with zero additional queries and is sufficient for the use case. |
| Keyset / cursor-based pagination | Scales to very large datasets with no `OFFSET` scan penalty. Heavier implementation: requires a stable sort key exposed in the response, opaque cursor encoding, and client bookmarking. `OFFSET` is sufficient for the fixture-scale read API and simpler to implement and test. |
| Separate pagination metadata endpoint | Requires two API calls per "page with metadata" load. Unnecessary complexity when the envelope pattern is simpler and standard. |
