---
phase: quick-260703-jja
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - .planning/ROADMAP.md
  - .planning/STATE.md
  - docs/FUTURE.md
  - README.md
  - docs/mq.readme.md
  - docs/streamer.readme.md
  - docs/collector.readme.md
  - docs/gateway.readme.md
  - docs/development.readme.md
autonomous: true
requirements: []
must_haves:
  truths:
    - "ROADMAP marks Phase 7 (WAL) as DEFERRED / optional post-v1; the frozen Phase 7 design record is retained."
    - "STATE.md reflects the deferral: next action = open PR for adr-backfill → main; WAL deferred."
    - "docs/FUTURE.md documents two designed-but-deferred extensions: opt-in WAL and multi-schema message support."
    - "README is slimmed to critical info + a Deploy & Try It section with real, verified commands; the long narrative is MOVED (not deleted) into per-service/topic docs."
    - "README carries a submission checklist mapping graded items to their location and links every docs/*.readme.md, FUTURE.md, the ADR index, and AI_USAGE/AI_PROMPTS."
  artifacts:
    - docs/FUTURE.md
    - docs/mq.readme.md
    - docs/streamer.readme.md
    - docs/collector.readme.md
    - docs/gateway.readme.md
    - docs/development.readme.md
  key_links:
    - "docs/FUTURE.md → docs/adr/ADR-009-opt-in-wal-extension.md and the frozen Phase 7 ROADMAP entry"
    - "slim README → docs/{mq,streamer,collector,gateway,development}.readme.md + docs/FUTURE.md + docs/adr/README.md"
---

<objective>
Submission wrap-up (docs only). Defer Phase 7 (WAL) to a documented future enhancement, add a second
designed-but-deferred extension (multi-schema messages), aggressively slim the 716-line README to
critical info plus a concrete Deploy & Try It walkthrough, move the long per-phase/per-service
narrative into per-service docs, and add a submission checklist.

Purpose: Ready the `adr-backfill` branch for a v1 submission PR; give a grader a short, accurate
front door instead of a 716-line phase log.
Output: Updated ROADMAP.md + STATE.md; new docs/FUTURE.md; slim README.md; five moved-content docs
under docs/ (mq/streamer/collector/gateway/development .readme.md).

DESIGN CALL (avoid duplication): the slim README itself serves as the user guide / quickstart — there
is NO separate docs/USER_GUIDE.md. Depth lives in the per-service docs, linked from the README.

CONSTRAINTS (non-negotiable):
- Docs-only. NO Go / proto / chart / Makefile changes. Never invent a `make` target, service name,
  or port — verify each against the Makefile / deployments/ charts / cmd/ defaults (facts provided in
  the task actions below). Content is MOVED, not invented; preserve technical facts, trim redundancy.
- Stay on branch `adr-backfill`. Do NOT create or switch branches.
- Do NOT touch `docs/adr/ADR-001*` or `docs/adr/ADR-002*`. Do NOT rename any existing file.
- Commit with Conventional Commits (`docs(...)`). NO `Co-Authored-By` trailer.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
</execution_context>

<context>
@.planning/ROADMAP.md
@.planning/STATE.md
@README.md
@Makefile
@docs/adr/README.md
@docs/adr/ADR-009-opt-in-wal-extension.md
@docs/adr/ADR-006-long-narrow-schema-natural-key.md
@scripts/smoke/phase05-kind.sh
@deployments/values.yaml
</context>

<tasks>

<task type="auto">
  <name>Task 1: Defer Phase 7 (WAL) in ROADMAP + STATE, and create docs/FUTURE.md</name>
  <files>.planning/ROADMAP.md, .planning/STATE.md, docs/FUTURE.md</files>
  <action>
PART A — Edit `.planning/ROADMAP.md` (scoped edits only — do NOT rewrite the whole file):
  1. Phases list (line ~16): mark the Phase 7 bullet deferred, e.g.
     `- [~] **Phase 7: MQ Durability — Opt-in WAL Persistence (DEFERRED — optional post-v1 enhancement)** - Crash-durable broker mode behind the Store interface; at-least-once via replay`.
  2. `### Phase 7:` heading: append ` (DEFERRED — optional, post-v1)` and add a one-line note under it
     stating WAL is NOT part of the v1 submission, that the design below is retained as the frozen
     design record, and pointing to `docs/FUTURE.md` + `docs/adr/ADR-009`. KEEP all existing Phase 7
     goal / success-criteria text intact (frozen design record).
  3. Progress table Phase 7 row: Status → `Deferred (optional post-v1)`.
  4. Roadmap-evolution footnotes (bottom): add
     `*Updated 2026-07-03: Phase 7 (WAL durability) DEFERRED as an optional post-v1 enhancement (owner directive); design frozen, tracked in docs/FUTURE.md + ADR-009. v1 submission = Phases 1–6 on branch adr-backfill.*`

PART B — Edit `.planning/STATE.md` (scoped edits; single source of truth — do not duplicate prose):
  1. `## Current Position` Status/Next: Phase 6 complete; WAL deferred; next action = open PR for
     `adr-backfill` → main (squash-merge per workflow).
  2. `### Active TODOs`: mark the "Plan Phase 6" item done (Phase 6 complete); rewrite the Phase 7 item
     to state WAL is DEFERRED (optional post-v1; tracked in docs/FUTURE.md + ADR-009), not pending.
  3. `## Session Continuity` → **Next action**: "Open PR for branch `adr-backfill` → main
     (squash-merge); WAL deferred to post-v1 (docs/FUTURE.md)."
  Do NOT edit the frozen `## Decisions` history or `## Performance Metrics` tables.

PART C — Create `docs/FUTURE.md` ("Future Enhancements"), ~1–2 pages, precise plain-language prose
matching the recently-edited ADR tone (read ADR-009 + ADR-006 in context first). Both extensions are
document-only — state clearly they are NOT implemented in v1. Intro paragraph: this file tracks
intentionally-deferred, already-designed work so graders can see the extension path without it being
in v1 scope. Then:
  (a) Opt-in WAL persistence backend — short paragraph: a durable `Store` selected by config flag;
      batched group-commit fsync + replay-on-restart giving at-least-once; the in-memory ring buffer
      stays the byte-for-byte default; safe because the Phase 2 natural-key unique constraint + the
      idempotent Collector already absorb redelivery duplicates. Link
      `docs/adr/ADR-009-opt-in-wal-extension.md` and the frozen Phase 7 ROADMAP entry. Link, don't restate.
  (b) Multi-schema message support in the same broker — design sketch only, sub-sections:
      - Envelope / proto evolution options (name tradeoffs, pick no winner): `oneof` payload vs. opaque
        `bytes` payload + `schema_id`/`content_type` fields vs. topic-per-schema.
      - Broker-side: the ring buffer / `Store` is already payload-agnostic (moves bytes) so opaque
        payloads need no core change; contrast per-topic queues vs. single queue + type tag.
      - Consumer-side dispatch: Collector routes by schema tag; unknown-schema → dead-letter vs.
        skip-and-log.
      - DB impact: the long/narrow schema (ADR-006) already absorbs new *metric names* with no DDL, but
        new message *kinds* would need their own tables + natural-key/idempotency keys; link ADR-006.
      - OpenAPI / gateway impact: new read shapes → new gateway endpoints + regenerated swag annotations.
      Close with a one-line status: designed, deferred, not scheduled — revisit post-v1.
  </action>
  <verify>
    <automated>grep -qi "DEFERRED" .planning/ROADMAP.md && grep -qi "adr-backfill" .planning/STATE.md && test -f docs/FUTURE.md && grep -qi "ADR-009" docs/FUTURE.md && grep -qi "multi-schema" docs/FUTURE.md && grep -qi "ADR-006" docs/FUTURE.md && echo OK</automated>
  </verify>
  <done>ROADMAP Phase 7 marked DEFERRED (phases list + heading + progress table) with design text preserved and a dated footnote; STATE Next action = open the adr-backfill PR with WAL listed as deferred; docs/FUTURE.md documents both the opt-in WAL extension (linking ADR-009 + frozen roadmap entry) and multi-schema message support (linking ADR-006), explicitly document-only.</done>
</task>

<task type="auto">
  <name>Task 2: Move README narrative into docs/*.readme.md (content preserved)</name>
  <files>docs/mq.readme.md, docs/streamer.readme.md, docs/collector.readme.md, docs/gateway.readme.md, docs/development.readme.md</files>
  <action>
MOVE (not delete) the long content out of README.md into five topic docs under docs/. Preserve
technical facts verbatim where they matter (env-var tables, endpoint semantics, smoke-script step
lists, delivery-semantics prose); trim only redundancy. Source everything from the current README
sections in context. Split as follows:

  - `docs/mq.readme.md` ← README "Run the MQ", config env table (MQ_GRPC_ADDR, MQ_HTTP_ADDR,
    MQ_BUFFER_SIZE, MQ_CONSUME_CREDIT), "Delivery semantics — broker-side at-least-once",
    "Inspect the queue" + counter meanings, "Produce & consume a message" (mqprobe modes),
    and the MQ portion of the smoke-01 description.
  - `docs/streamer.readme.md` ← README Phase 3 Streamer parts: run instructions, Streamer env table
    (STREAMER_MQ_ADDR, STREAMER_CSV_PATH, STREAMER_LOOP_DELAY_MS), CSV prerequisite, restamp/D-04 note,
    streamer health port (:9000 / STREAMER_HEALTH_ADDR).
  - `docs/collector.readme.md` ← README Phase 3 Collector parts: run instructions, Collector env table
    (VANTAGE_DB_DSN, COLLECTOR_MQ_ADDR, COLLECTOR_BATCH_SIZE, COLLECTOR_FLUSH_MS, COLLECTOR_CREDIT),
    exactly-once / ON CONFLICT explanation (+ ADR-002 caveat link), collector health port
    (:9001 / COLLECTOR_HEALTH_ADDR), and inspect-rows psql snippets.
  - `docs/gateway.readme.md` ← README Phase 4 + the Phase 6 pagination content: run instructions,
    Gateway env table (GATEWAY_ADDR, VANTAGE_DB_DSN, VANTAGE_DB_MAX_CONNS, VANTAGE_GATEWAY_MAX_ROWS),
    the API endpoints + error codes, Swagger UI + `make swagger`, and the pagination envelope
    (limit/offset, has_next, response JSON shape).
  - `docs/development.readme.md` ← everything not service-specific: repository layout, Phase 2 storage
    foundation (dev stack `make dev-up`/`dev-down`, `cmd/migrate`, VANTAGE_DB_DSN, GPU identity D-04,
    smoke-02 steps), the full smoke suite details (`make smoke`, `make smoke-0N`, per-phase step
    lists), Testing section (`make test`/`coverage`/`lint`, race tests), Phase 5 DevOps deep content
    (independent deploys, soak, test-harness, kind caveats), Phase 6 health/probes/resources/HPA/slog
    detail tables, and CI. This is the "how it's built and verified" reference.

Each doc starts with a one-line title + a "← back to the [README](../README.md)" link. Do NOT invent
any command, env var, or port — every fact must come from the current README/Makefile in context.
Preserve existing links to ADR-001 / ADR-002 (do not alter those ADR files). Fix the one stale fact
while moving it: README currently says the WAL backend is "planned for Phase 6" — write it in
docs/mq.readme.md as "Phase 7 (deferred post-v1; see docs/FUTURE.md)".
  </action>
  <verify>
    <automated>for f in mq streamer collector gateway development; do test -f "docs/$f.readme.md" || { echo "MISSING docs/$f.readme.md"; exit 1; }; done && grep -qi "MQ_CONSUME_CREDIT" docs/mq.readme.md && grep -qi "COLLECTOR_BATCH_SIZE" docs/collector.readme.md && grep -qi "pagination" docs/gateway.readme.md && grep -qi "dev-up" docs/development.readme.md && echo OK</automated>
  </verify>
  <done>Five docs/*.readme.md exist carrying the moved MQ / streamer / collector / gateway / development-and-testing content with technical facts preserved (env tables, endpoints, smoke steps); each links back to README; the stale "Phase 6" WAL reference is corrected to deferred Phase 7 in docs/mq.readme.md. No Go/proto/chart/Makefile files touched.</done>
</task>

<task type="auto">
  <name>Task 3: Rewrite README.md slim — critical info + Deploy & Try It + submission checklist + links</name>
  <files>README.md</files>
  <action>
Replace README.md with a slim front door. Keep ONLY critical info; everything else now lives in the
docs/*.readme.md from Task 2 (link them). Sections, in order:

  1. Title + one-paragraph intro + the CSV→…→client architecture arrow diagram (keep from current README).
  2. Services table (compact — the existing 5-row table: service, entrypoint, role, status).
  3. Prerequisites (compact: Go 1.26+, make, curl, Docker + docker compose; later protoc, kind + Helm
     via `make tools`).
  4. "## Make commands" — a table of the Makefile targets and their purpose. Verify EVERY target
     against the Makefile in context (available: help, tools, proto, build, test, coverage, e2e,
     swagger, lint, tidy, clean, smoke, smoke-%, docker, docker-%, kind-up, helm-install, kind-down,
     dev-up, dev-down, kind-load, deploy, dependency-update, soak, test-harness). Do NOT invent targets.
  5. "## Deploy & Try It" — the concrete grader walkthrough. Verify service/release names + ports
     against deployments/ + scripts/smoke/phase05-kind.sh (facts: Helm release `vantage`; services
     `vantage-mq`, `vantage-gateway`, `vantage-streamer`, `vantage-collector`; gateway container port
     8080; smoke port-forwards gateway to local 8081, MQ HTTP to local 8082). Steps:
       a. `make kind-up` then `make deploy` (docker → kind-load → helm-install), then `make smoke-05`.
       b. `kubectl port-forward svc/vantage-gateway 8081:8080` — then concrete curl calls against
          `http://localhost:8081`:
            - `GET /api/v1/gpus`
            - `GET /api/v1/gpus/{id}/telemetry`
            - the `?start_time=&end_time=` time-window variant (RFC3339)
            - the `?limit=&offset=` pagination variant (mention the has_next envelope)
       c. Swagger UI URL: `http://localhost:8081/swagger/`.
       d. MQ inspect via `kubectl port-forward svc/vantage-mq 8082:8080` then
          `curl -s localhost:8082/api/v1/queue/inspect`.
       e. `kubectl scale deployment/vantage-streamer --replicas=3` (and collector) to show horizontal
          scaling; one line on WHY MQ stays single-replica (in-memory broker, ADR-001).
       f. `make kind-down` to tear down.
     (Optionally add a 2-line "Run locally without k8s" pointer to docs/development.readme.md rather
     than duplicating the local run steps.)
  6. "## Submission Checklist" — compact table mapping what a grader checks → where it lives:
     build/test/≥90% coverage/lint gates → `Makefile` (`make build test coverage lint`);
     race tests → `make test` (`go test -race`);
     kind E2E + soak → `make deploy`, `make smoke-05`, `make soak`, `make test-harness`;
     auto-generated OpenAPI → `make swagger`, `/swagger/`;
     ADR index → `docs/adr/README.md`;
     AI usage/prompts → `docs/AI_USAGE.md`, `docs/AI_PROMPTS.md`;
     future enhancements → `docs/FUTURE.md`;
     CI → `.github/workflows/ci.yml`.
  7. "## Documentation" — link every moved doc: docs/mq.readme.md, docs/streamer.readme.md,
     docs/collector.readme.md, docs/gateway.readme.md, docs/development.readme.md, plus docs/FUTURE.md,
     docs/adr/README.md (ADR index), docs/AI_USAGE.md, docs/AI_PROMPTS.md, and instructions.md /
     CLAUDE.md for the spec + conventions.
  8. Keep a one-line closing note: built phase-by-phase with GSD; this is the final v1 consolidation.

Preserve the existing ADR-001 / ADR-002 links where referenced. Do not rename README.md. Keep it
skimmable — headings + fenced blocks; target well under ~200 lines.
  </action>
  <verify>
    <automated>test -f README.md && L=$(wc -l < README.md) && echo "README lines: $L" && grep -qi "Deploy & Try It" README.md && grep -qi "Submission Checklist" README.md && grep -qi "port-forward svc/vantage-gateway" README.md && grep -qi "docs/FUTURE.md" README.md && grep -qi "docs/mq.readme.md" README.md && [ "$L" -lt 300 ] && echo OK</automated>
  </verify>
  <done>README is slimmed (well under 300 lines) to intro + diagram + services table + prerequisites + make-commands table + a verified Deploy & Try It curl walkthrough + submission checklist + a Documentation section linking all five docs/*.readme.md, FUTURE.md, the ADR index, and AI docs. Every command/service/port is real (verified against Makefile + charts + smoke script). ADR-001/ADR-002 untouched.</done>
</task>

</tasks>

<verification>
- `git status` shows only docs/planning files changed: `.planning/ROADMAP.md`, `.planning/STATE.md`,
  `docs/FUTURE.md`, `README.md`, and `docs/{mq,streamer,collector,gateway,development}.readme.md`.
  No `.go`, `.proto`, chart, or `Makefile` diffs.
- Every `make` target, service name, and port in README's Make-commands / Deploy & Try It sections
  exists in the Makefile / deployments/ charts / scripts/smoke/phase05-kind.sh.
- Still on branch `adr-backfill` (no branch created/switched). ADR-001/ADR-002 untouched; nothing renamed.
- No content silently lost: the long README narrative is present across the five docs/*.readme.md.
</verification>

<success_criteria>
- Phase 7 (WAL) is DEFERRED in ROADMAP + STATE, design frozen, not deleted.
- docs/FUTURE.md documents WAL + multi-schema as designed-but-deferred, document-only.
- README is a slim, accurate front door with a working Deploy & Try It walkthrough and a submission
  checklist; the deep narrative is moved into per-service/topic docs and linked.
- No separate USER_GUIDE.md (slim README is the guide) — no duplicated content.
- Commit is `docs(...)`-typed with no Co-Authored-By trailer.
</success_criteria>

<output>
Commit all changed files with a `docs(...)` Conventional Commit (no Co-Authored-By trailer) on the
current `adr-backfill` branch. Report the commit hash.
</output>
