# vantage — project conventions

Elastic GPU telemetry pipeline + custom message queue. Go multi-module monorepo.
Module path root: `github.com/ajitgunturi/vantage`. Project context is GSD-managed under `.planning/`:
spec in `.planning/PROJECT.md`, live status in `.planning/STATE.md`, roadmap in `.planning/ROADMAP.md`,
requirements in `.planning/REQUIREMENTS.md`. (Architecture decisions remain in `docs/adr/`.)

## Module map
- `mq/` — custom message queue: `broker/` (durable segment log), `client/` (producer/consumer lib),
  `proto/` + `gen/` (gRPC contract + generated stubs), `cmd/` (broker binary).
- `streamer/` — producer: loops the DCGM CSV, re-stamps timestamps, produces over the MQ.
- `collector/` — consumer: consumes from MQ, parses, upserts into PostgreSQL (idempotent).
- `apigateway/` — REST API + auto-generated OpenAPI.
- `k8s-infra/` — Helm umbrella chart + `kind/` config.
- `docs/adr/` — architecture decisions only (ADR bar: load-bearing forks, not tooling/naming).
- `.planning/` — GSD-managed build record **and AI-usage evidence trail**: research, per-phase
  SPEC/PLAN/VERIFICATION, requirements, roadmap, STATE, and the commit history. This is the source
  of truth for "how AI assistance was used." `docs/PROMPT_HISTORY.md` is the **frozen pre-GSD
  bootstrap appendix** (the scaffold-era prompt log) — not maintained going forward.

Multi-module via `go.work`. Run `go` commands from the module dir, or use Makefile targets.

## Build / test / verify (use the Makefile, don't hand-roll)
- `make build` — build all binaries.
- `make test` — unit tests. `make cover` / `make cover-check` — coverage (gate: 90% line, 100% branch on logic).
- `make lint` — golangci-lint. `make proto` — regenerate gRPC stubs from `mq/proto`.
- `make hooks` — install the pre-commit hook (run once per clone). `make kind` / `make helm` — local k8s.
- After changing Go code, run the relevant module's tests before declaring done; fix failures autonomously.

## Conventions
- Idiomatic Go: `slog` for logging, `testify` for tests, `pgx` for Postgres, Prometheus client for metrics.
- **Spec-first + TDD for every phase** (not only business logic): lock the phase spec (`/gsd-spec-phase N`),
  then write the full failing test suite for the phase scope (starts **red**) and implement only to turn it
  **green**. A phase is done when its suite passes and coverage gates hold (90% line / 100% branch,
  `make cover-check`). No green-by-deletion — weakening a test requires a spec change. See `.planning/PROJECT.md` § Delivery Method.
- Collector writes must be idempotent: upsert on `(uuid, metric_name, ts)`.
- Canonical GPU identity = `uuid` (ADR-0005, **Accepted** 2026-06-24 — PK `(uuid, metric_name, ts)`, partition key, API `{id}`).
- Conventional Commits; no `Co-Authored-By` trailer. Ephemeral `feat/*|fix/*|chore/*` branch → PR to `main` → merge green.
- **Build evidence is GSD-driven**: capture decisions, prompts, and manual interventions as `.planning/`
  artifacts (SPEC/PLAN/VERIFICATION, ADRs, commits) *as you go* — the DOC-02 AI-usage doc is synthesized
  from `.planning/` (+ the frozen `docs/PROMPT_HISTORY.md` appendix), not reconstructed at the end.
