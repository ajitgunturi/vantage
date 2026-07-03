---
phase: quick-260703-ilo-backfill-missing-adrs
plan: 01
type: execute
wave: 1
depends_on: []
autonomous: true
requirements: [DOC-ADR-BACKFILL]
files_modified:
  - docs/adr/ADR-003-single-module-directory-ownership.md
  - docs/adr/ADR-004-ring-buffer-store-interface.md
  - docs/adr/ADR-005-raw-protoc-over-buf.md
  - docs/adr/ADR-006-long-narrow-schema-natural-key.md
  - docs/adr/ADR-007-pgx-batch-on-conflict-over-copyfrom.md
  - docs/adr/ADR-008-chart-enforced-single-replica-post-install-hook.md
  - docs/adr/ADR-009-opt-in-wal-extension.md
  - docs/adr/ADR-010-telemetrypage-pagination-envelope.md
  - docs/adr/README.md
  - README.md

must_haves:
  truths:
    - "Eight new ADR files (ADR-003..ADR-010) exist in docs/adr/, each matching the ADR-001/002 Nygard format (Status/Date/Phase header + Context / Decision / Consequences / Alternatives Considered)."
    - "docs/adr/README.md is an index table with exactly 10 rows (ADR-001..ADR-010), columns: number | title | status | date | phase."
    - "The main README.md 'Design records' section links to docs/adr/README.md alongside the existing per-ADR links."
    - "Cross-links between related ADRs (002<->006, 001/006<->007, 001/004<->009) resolve to real filenames."
  artifacts:
    - docs/adr/ADR-003-single-module-directory-ownership.md
    - docs/adr/ADR-004-ring-buffer-store-interface.md
    - docs/adr/ADR-005-raw-protoc-over-buf.md
    - docs/adr/ADR-006-long-narrow-schema-natural-key.md
    - docs/adr/ADR-007-pgx-batch-on-conflict-over-copyfrom.md
    - docs/adr/ADR-008-chart-enforced-single-replica-post-install-hook.md
    - docs/adr/ADR-009-opt-in-wal-extension.md
    - docs/adr/ADR-010-telemetrypage-pagination-envelope.md
    - docs/adr/README.md
  key_links:
    - "docs/adr/README.md index rows point at the exact ADR filenames listed in files_modified."
    - "README.md Design records section -> docs/adr/README.md (relative link resolves from repo root)."
---

<objective>
Backfill the eight missing Architecture Decision Records (ADR-003 through ADR-010) that document
decisions already made and shipped across Phases 1-6 but never written up, then add an index and
wire it into the main README.

The two existing records (ADR-001 bidi at-least-once, ADR-002 natural-key collision) establish the
house style. The decision log is scattered across `.planning/STATE.md` (## Decisions + Key
Decisions), `.planning/PROJECT.md` (Key Decisions table), and `.planning/ROADMAP.md` (phase goals +
evolution footnotes). This task consolidates the eight highest-value decisions into standalone ADRs
so a reader/grader can find the "why" without spelunking the planning directory.

Purpose: reference-quality decision trail; every non-obvious deviation from the brief has a citable
record.
Output: 8 new ADR markdown files + docs/adr/README.md index + one README.md link edit.

This is a **docs-only** task — no Go source, proto, chart, or Makefile changes. No threat model is
required (no code, no new attack surface).
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/STATE.md
@docs/adr/ADR-001-bidi-at-least-once-delivery.md
@docs/adr/ADR-002-natural-key-microsecond-collision.md
</context>

<format_contract>
Every backfilled ADR MUST reproduce the ADR-001/002 Nygard structure:

1. `# ADR-00N: <Title>` H1.
2. A header block of bold key/value lines:
   - `**Status:**` — as specified per ADR below (Accepted, or Accepted with a deviation note).
   - `**Date:**` — the decision date given per ADR below (the date the decision was made, NOT today).
   - `**Phase:**` — the phase the decision was made in, given per ADR.
   - Because these are written after the fact, add one more header line:
     `**Backfilled:** 2026-07-03` (documents that the record — not the decision — is retroactive).
   - Include `**Supersedes / Related:**` or inline cross-link lines where the anchor calls for it.
3. `## Context` — the situation and forces (mine the cited `.planning` sources for specifics).
4. `## Decision` — what was decided, in the present/active voice ("Adopt…", "Accept…").
5. `## Consequences` — Positive / Negative-risk bullets (follow ADR-001's two-subhead shape or
   ADR-002's flat bullet shape; either is acceptable).
6. `## Alternatives Considered` — a table (`| Alternative | Trade-off |`) or bullet list naming the
   rejected options and WHY they lost. Every ADR below lists its alternatives — use them.

Each ADR MUST cite its `.planning` source(s) inline (e.g. "plan 01-02", "STATE.md ## Decisions",
"PROJECT.md Key Decisions", "06-REVIEW.md finding F-06") so the trail is auditable.
Keep length in the ADR-001/002 range (~40-100 lines); prose over exhaustive dumps.
</format_contract>

<tasks>

<task type="auto">
  <name>Task 1: Author the eight backfilled ADRs (ADR-003..ADR-010)</name>
  <files>docs/adr/ADR-003-single-module-directory-ownership.md, docs/adr/ADR-004-ring-buffer-store-interface.md, docs/adr/ADR-005-raw-protoc-over-buf.md, docs/adr/ADR-006-long-narrow-schema-natural-key.md, docs/adr/ADR-007-pgx-batch-on-conflict-over-copyfrom.md, docs/adr/ADR-008-chart-enforced-single-replica-post-install-hook.md, docs/adr/ADR-009-opt-in-wal-extension.md, docs/adr/ADR-010-telemetrypage-pagination-envelope.md</files>
  <read_first>
    - docs/adr/ADR-001-bidi-at-least-once-delivery.md — house style (header block, Context/Decision/Consequences, Compliance-style source citations, deviation framing).
    - docs/adr/ADR-002-natural-key-microsecond-collision.md — house style (flat Consequences bullets + Alternatives Considered table).
    - .planning/STATE.md — `## Decisions` list (lines ~124-163) + `### Key Decisions` for the raw per-plan rationale each ADR must reflect.
    - .planning/PROJECT.md — `## Key Decisions` table (module, MQ, WAL, Phase-5 hook, replicas:1) + `## Constraints`.
    - .planning/ROADMAP.md — phase goals + Evolution footnotes (Phase 6 insertion, WAL renumber 6->7, API-05 pagination moved out of Out-of-Scope).
    - For ADR-007 detail on the C-1 ack-on-persist fix, cross-reference the quick task `.planning/quick/260702-ku8-fix-all-the-gaps-identified-as-part-of-t/` (referenced from STATE Quick Tasks table).
    - For ADR-010, `.planning/phases/06-production-hardening-assignment-alignment/06-REVIEW.md` (finding F-06 / API-05) and plan/summary 06-02.
  </read_first>
  <action>
Write all eight ADR files following <format_contract>. Per-ADR content anchors (mine the cited
sources for concrete specifics — do not invent facts beyond what the sources support):

- **ADR-003 `single-module-directory-ownership`** — Status: Accepted. Date: 2026-06-27. Phase: 1.
  Decision: one `go.mod` at repo root (`github.com/ajitg/vantage`); services are independent by
  *directory + Dockerfile*, not by separate modules; `pkg/` is the ONLY cross-service surface; no
  Go workspace, no `replace` directives, so `go build ./...` / `go test ./...` work directly.
  Sources: PROJECT.md Key Decisions, CLAUDE.md module-discipline, ROADMAP Phase 1.
  Alternatives: multi-module + `go.work` (workspace complexity, replace churn); module-per-service
  (independent versioning nobody needs; breaks single-repo `./...` builds).

- **ADR-004 `ring-buffer-store-interface`** — Status: Accepted. Date: 2026-06-27. Phase: 1.
  Decision: bounded ring buffer behind a `Store` interface (`internal/queue`); `sync.Mutex` chosen
  over `sync.RWMutex` because every path (incl. `TryDequeue`) mutates head+count — all paths are
  writes; drop-oldest on `Enqueue` when full, drop-newest (tail eviction) on `Requeue` when full
  (prioritizes in-flight redelivery); `Enqueue` returns `bool` not `error` (in-memory backend is
  unconditional; bool signals drop without forcing callers to handle never-occurring errors);
  `Inspect()` returns `StoreStats` by value (snapshot copy prevents callers racing on internal
  state). The interface seam is deliberately shaped so a WAL backend (ADR-009) can slot in later.
  Sources: plans 01-02 and 01.1-02, STATE.md ## Decisions (sync.Mutex, Enqueue bool, Inspect by
  value, drop-newest Requeue). Cross-link ADR-009.
  Alternatives: RWMutex (no read-only paths to optimize); return error from Enqueue (dead error
  branch); unbounded queue (no backpressure, OOM risk).

- **ADR-005 `raw-protoc-over-buf`** — Status: Accepted (deviation from the researched stack). Date:
  2026-06-27. Phase: 1. Context: the stack research (.claude/CLAUDE.md) recommends buf CLI v1.71.0
  as the modern toolchain. Decision: use raw `protoc` instead — a single `api/proto/mq.proto`,
  `paths=source_relative`, with the generated `pkg/pb` committed to the repo so builds and CI need
  no protoc/buf install. Rationale: one proto file gains nothing from buf's lint/BSR/breaking-change
  machinery; committed stubs keep the build hermetic. Sources: STATE.md Open Decisions (Phase 1
  RESOLVED: proto tooling = raw protoc), ROADMAP Phase 1.
  Alternatives: buf CLI + remote BSR plugins (extra tool + network dep for a single contract);
  generate stubs at build time / not committing pkg/pb (forces every build+CI to install protoc).

- **ADR-006 `long-narrow-schema-natural-key`** — Status: Accepted. Date: 2026-06-29. Phase: 2.
  Decision: long/narrow schema — one row per `(gpu_id, metric_name, timestamp)` rather than wide
  metric-per-column; a `uq_gpu_metrics_natural_key` unique constraint on that triple enables
  idempotent `INSERT ... ON CONFLICT` upsert (absorbs MQ at-least-once redelivery); GPU **UUID**
  (from `msg.GetUuid()`), not the DCGM ordinal, is the `gpu_id` identity (D-04); the RFC3339Nano
  Streamer restamp is locked because second-granularity restamps collapse same-second readings onto
  the natural key. Composite index `(gpu_id, timestamp DESC)` is the read path. Sources: plan 02-01,
  STATE.md ## Decisions (uq_gpu_metrics_natural_key, RFC3339Nano, GpuID from uuid). Cross-link
  ADR-002 (the microsecond-collision consequence of this key) and ADR-007 (the upsert that uses it).
  Alternatives: wide table (metric-per-column) (schema churn per new metric; sparse rows); serial/
  UUID surrogate PK (breaks idempotent upsert — every row unique by construction); adding
  `streamer_id` to the key (per-streamer dedup, not global — see ADR-002).

- **ADR-007 `pgx-batch-on-conflict-over-copyfrom`** — Status: Accepted. Date: 2026-06-29. Phase: 3.
  Context: the stack research recommends `pgxpool.CopyFrom` as the fastest bulk path, but CopyFrom
  cannot express `ON CONFLICT`. Decision: use `pgx.Batch` / `SendBatch` with
  `INSERT ... ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING`. This makes end-to-end
  exactly-once = broker at-least-once (ADR-001) × idempotent upsert (ADR-006). Document the pgx v5
  single-implicit-transaction batch semantics that make the C-1 fix correct: the whole batch runs in
  one implicit transaction, so the first `Exec` error rolls back the entire batch → no acks are sent
  → the MQ redelivers (ack-on-persist, not ack-on-receive). Sources: plan 03-03, STATE.md ##
  Decisions (ON CONFLICT DO NOTHING), quick task 260702-ku8 (C-1 ack-on-persist data-loss fix).
  Cross-link ADR-001 and ADR-006.
  Alternatives: CopyFrom (fastest, but no ON CONFLICT — cannot be idempotent); per-message
  transactions (kills throughput; COPY/batch is transactional already); in-memory dedup in the
  Collector (stateful, defeats the point of a DB-enforced natural key).

- **ADR-008 `chart-enforced-single-replica-post-install-hook`** — Status: Accepted (owner-approved
  deviation from locked D-09). Date: 2026-07-02. Phase: 5. Decision (two linked chart choices):
  (a) MQ Deployment hardcodes `replicas: 1` + `strategy: Recreate` directly in the sub-chart
  template (NOT values-overridable) so the in-memory single-replica broker invariant (ADR-001)
  cannot be violated by a values override or rolling update (split-brain). (b) The DB migration Job
  moved from `pre-install` to `post-install,post-upgrade`: the pre-install hook's wait-for-postgres
  init container deadlocked against the same release's Postgres (Helm runs pre-install hooks before
  installing the chart's own manifests). Post-install preserves the intent (dedicated Job per
  release op, Helm blocks until done; services tolerate the brief pre-migration window). Bounded
  loud failure: `activeDeadlineSeconds: 300` on the Job + `helm install --timeout 6m` (WR-04) turn
  silent hangs into named bounded errors. Sources: plan 05-05, PROJECT.md Key Decisions (both rows),
  STATE.md Key Decisions (Phase 5). Cross-link ADR-001.
  Alternatives: replicas as a values knob (a bad override splits the broker's in-memory state);
  keep migration pre-install (deadlocks on same-release Postgres); no deadline/timeout (silent hang
  on cold image pulls).

- **ADR-009 `opt-in-wal-extension`** — Status: Accepted (implementation pending — Phase 7). Date:
  2026-06-27 (decided at roadmap creation; renumbered Phase 6 -> Phase 7 on 2026-07-03). Phase:
  roadmap-level / Phase 7. Decision: add an OPT-IN write-ahead-log persistence backend behind the
  same `Store` interface (ADR-004) — batched group-commit fsync + replay-on-restart, at-least-once —
  as a deliberate, documented extension of the brief's in-memory-only baseline. In-memory stays the
  DEFAULT and byte-for-byte unchanged; WAL is selected by a config flag. Single-replica / no-cluster
  constraints unchanged (WAL persists to local disk, never a cluster). Note the Phase 01.1 overlap:
  delivery-level at-least-once now lives in the broker (ADR-001), so the WAL narrows to crash
  durability only. Sources: PROJECT.md Key Decisions + Active requirement, STATE.md Key Decisions +
  Roadmap Evolution (renumber), ROADMAP Phase 7. Cross-link ADR-001 and ADR-004.
  Alternatives: mandatory disk persistence (violates the brief's in-memory-only baseline as the
  default); no durability at all (a broker crash loses un-consumed messages); external broker
  (forbidden by spec).

- **ADR-010 `telemetrypage-pagination-envelope`** — Status: Accepted. Date: 2026-07-03. Phase: 6
  (requirement API-05, audit finding F-06). Decision: push `limit`/`offset` down into the
  composite-index SQL path (OFFSET as a pgx placeholder only — no string concat, T-06-03);
  `has_next` computed via a `limit+1` sentinel row (avoids a separate COUNT query); a `TelemetryPage`
  JSON envelope (data + pagination metadata) REPLACES the earlier `X-Truncated` / `X-Row-Limit`
  response headers — a breaking response-shape change. This reverses the documented
  "API pagination out of scope" entry (now API-05). Sources: 06-REVIEW.md finding F-06 / API-05,
  plan 06-02, STATE.md ## Decisions (limit+1 sentinel, OFFSET placeholder), ROADMAP Evolution
  footnote (API pagination moved out of Out-of-Scope). Cross-link ADR-006 (composite index this
  paginates over).
  Alternatives: keep truncation headers (non-standard; clients miss them; no real paging); COUNT(*)
  for total (extra query per request; the sentinel is cheaper); cursor/keyset pagination (heavier;
  limit/offset is sufficient for a fixture-scale read API).

Write each file with the Write tool (net-new files). Do NOT run swag/proto/build — docs only.
  </action>
  <verify>
    <automated>test -f docs/adr/ADR-003-single-module-directory-ownership.md && test -f docs/adr/ADR-004-ring-buffer-store-interface.md && test -f docs/adr/ADR-005-raw-protoc-over-buf.md && test -f docs/adr/ADR-006-long-narrow-schema-natural-key.md && test -f docs/adr/ADR-007-pgx-batch-on-conflict-over-copyfrom.md && test -f docs/adr/ADR-008-chart-enforced-single-replica-post-install-hook.md && test -f docs/adr/ADR-009-opt-in-wal-extension.md && test -f docs/adr/ADR-010-telemetrypage-pagination-envelope.md && for f in docs/adr/ADR-00[3-9]*.md docs/adr/ADR-010*.md; do grep -q '^## Context' "$f" && grep -q '^## Decision' "$f" && grep -q '^## Consequences' "$f" && grep -q '^## Alternatives Considered' "$f" && grep -q '^\*\*Status:\*\*' "$f" && grep -q '^\*\*Backfilled:\*\* 2026-07-03' "$f" || { echo "FORMAT FAIL: $f"; exit 1; }; done && echo ALL_ADRS_OK</automated>
  </verify>
  <acceptance_criteria>
    - All 8 ADR files exist at the exact paths in <files>.
    - Each of the 8 files contains all four required section headers (## Context, ## Decision,
      ## Consequences, ## Alternatives Considered) plus a header block with **Status:**, **Date:**,
      **Phase:**, and **Backfilled:** 2026-07-03.
    - Each ADR cites at least one `.planning` source inline.
    - Cross-links use real filenames: ADR-006 references ADR-002 and ADR-007; ADR-007 references
      ADR-001 and ADR-006; ADR-009 references ADR-001 and ADR-004; ADR-008 references ADR-001.
    - No Go/proto/chart/Makefile files touched.
  </acceptance_criteria>
  <done>Eight backfilled ADRs authored, format-verified, each source-cited with resolving cross-links.</done>
</task>

<task type="auto">
  <name>Task 2: ADR index (docs/adr/README.md) + link from main README</name>
  <files>docs/adr/README.md, README.md</files>
  <read_first>
    - README.md lines ~700-704 — the `## Design records` section (currently two bullet links to
      ADR-001 and ADR-002); this is where the index link is added.
    - The 8 ADR files authored in Task 1 (for titles + status + date + phase to fill the index rows).
    - docs/adr/ADR-001 and ADR-002 headers (for their index rows: statuses/dates/phases — ADR-001
      Accepted / 2026-06-28 / 01.1; ADR-002 Accepted / 2026-07-02 / cross-phase).
  </read_first>
  <action>
Create `docs/adr/README.md` as the ADR index: a short intro line ("Architecture Decision Records for
vantage — Nygard format.") followed by a markdown table with columns
`| # | Title | Status | Date | Phase |` and exactly 10 data rows, ADR-001 through ADR-010, in
order. Each `#` cell links to the ADR file by relative filename (e.g.
`[ADR-003](ADR-003-single-module-directory-ownership.md)`). Fill Status/Date/Phase from each ADR's
header block (ADR-009's status is "Accepted (pending — Phase 7)"; ADR-005/008 note their deviation
status). Add a trailing note that ADR-003..010 were backfilled 2026-07-03.

Then edit README.md `## Design records`: keep the existing ADR-001/ADR-002 bullets, and add a lead
line linking the full index — e.g. a bullet or sentence:
`See the full [ADR index](docs/adr/README.md) for all ten decision records.` Place it at the top of
the section so the index is the entry point. Do NOT remove the existing per-ADR bullets.

Use Edit for README.md (scoped replacement of the Design records section — do NOT Write the whole
file). Use Write for the net-new docs/adr/README.md.

Do NOT commit — the executor/harness commits this quick task with
`docs(adr): backfill ADR-003..010 + index` (no Co-Authored-By trailer).
  </action>
  <verify>
    <automated>test -f docs/adr/README.md && rows=$(grep -c '^| \[ADR-0' docs/adr/README.md) && [ "$rows" -eq 10 ] && grep -q 'docs/adr/README.md' README.md && for n in 001 002 003 004 005 006 007 008 009 010; do grep -q "ADR-$n" docs/adr/README.md || { echo "MISSING ROW ADR-$n"; exit 1; }; done && echo INDEX_OK</automated>
  </verify>
  <acceptance_criteria>
    - docs/adr/README.md exists with a table whose 10 data rows cover ADR-001..ADR-010, each `#`
      cell linking the correct relative filename.
    - Index columns are number | title | status | date | phase.
    - README.md `## Design records` links to docs/adr/README.md and still lists the ADR-001/002
      bullets.
    - No commit made by the plan tasks (harness commits).
  </acceptance_criteria>
  <done>Ten-row ADR index exists and is reachable from the main README; ADR trail is discoverable.</done>
</task>

</tasks>

<artifacts_this_phase_produces>
Nine new files (8 ADRs + 1 index) plus one edited README:

- docs/adr/ADR-003-single-module-directory-ownership.md
- docs/adr/ADR-004-ring-buffer-store-interface.md
- docs/adr/ADR-005-raw-protoc-over-buf.md
- docs/adr/ADR-006-long-narrow-schema-natural-key.md
- docs/adr/ADR-007-pgx-batch-on-conflict-over-copyfrom.md
- docs/adr/ADR-008-chart-enforced-single-replica-post-install-hook.md
- docs/adr/ADR-009-opt-in-wal-extension.md
- docs/adr/ADR-010-telemetrypage-pagination-envelope.md
- docs/adr/README.md (new index)
- README.md (edited: Design records section gains the index link)
</artifacts_this_phase_produces>

<threat_model>
Not applicable — this is a documentation-only task. No Go source, proto contract, Helm chart,
Makefile, or CI changes; no new runtime code paths, inputs, or trust boundaries are introduced.
STRIDE analysis is intentionally omitted.
</threat_model>

<verification>
- Task 1 automated check: all 8 ADR files exist and each carries the four required section headers +
  Status/Backfilled header lines.
- Task 2 automated check: docs/adr/README.md has 10 ADR rows and README.md links the index.
- Manual spot-check (optional): open ADR-006 and confirm its cross-links to ADR-002/ADR-007 resolve.
</verification>

<success_criteria>
- 8 backfilled ADRs (ADR-003..010) in docs/adr/, Nygard-format, each source-cited, cross-links
  resolving to real filenames.
- docs/adr/README.md index with 10 rows (ADR-001..010).
- README.md Design records section links the index.
- Zero non-docs files modified. Conventional Commit `docs(adr): backfill ADR-003..010 + index`
  (no Co-Authored-By trailer) made by the harness, not the plan tasks.
</success_criteria>

<output>
Create `.planning/quick/260703-ilo-backfill-missing-adrs/SUMMARY.md` when done.
</output>
