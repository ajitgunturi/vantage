# STATE

**Project:** vantage (`github.com/ajitgunturi/vantage`) — elastic GPU telemetry pipeline.
**Current phase:** Monorepo scaffold (skeleton in place; service logic pending).
**Last action:** Reconciled context-file drift (PROJECT/STATE/ADR) + added project `CLAUDE.md`; trimmed Claude config (PR #3 merged).

## Micro-task
- [x] Read `GPU Telemetry Pipeline Message Queue.pdf` + profile dataset
- [x] Capture full context → `PROJECT.md`
- [x] Decide structure: multi-module (5× go.mod) + go.work, gRPC, kind, buf
- [x] Create dir tree, go.work, .gitignore, per-module go.mod
- [x] Define MQ proto contract + generate gRPC stubs (`mq/gen/mqv1`)
- [x] Set up docs/: ADR 0001–0005 + PROMPT_HISTORY
- [x] Name project = vantage / org ajitgunturi; rename module paths; `mq` builds
- [x] GitHub repo created (public) + `main` protected (branching now simplified — BRANCHING.md)
- [x] CI workflow live; first main push went red → fixed via PR on an ephemeral branch
- [x] Makefile (tools, hooks, proto, build, test, cover, cover-check, cover-logic, lint, kind, helm)
- [x] Coverage gates: 90% line (native) + 100% branch (gobco), fail-open until code (Makefile + CI)
- [x] Pre-commit hook (gofmt + golangci-lint) + CI lint job (.githooks + CI)
- [x] CI red fixed + TDD/quality infra merged to main (PR #1)
- [x] Context-file consistency pass + lean project `CLAUDE.md` + config trim (PR #3)
- [ ] **NEXT:** Stub service mains (streamer / collector / apigateway / mqbroker) — compiling skeletons
- [ ] k8s-infra Helm umbrella chart + kind config
- [ ] Dockerfiles per service
- [ ] README skeleton
- [ ] Implement MQ durable segment log (ADR-0001) + client lib — TDD
- [ ] Implement collector→Postgres, streamer→CSV loop, API gateway endpoints — TDD
- [ ] Perf harness (producer/consumer ratios)

## Workflow reminder (simplified — BRANCHING.md)
- Only `main` is protected (PR + `ci-success` to merge). No long-lived feature branches.
- Work on ephemeral branches (feat/*, fix/*, chore/*) → PR to main → merge when green → delete.
- CI behaviour unchanged: PR→main and merge→main both build/test/lint/cover.
- Run `make hooks` once per clone to install the pre-commit hook.

## Architecture decisions (docs/adr — architecture only)
- ADR-0001 Custom MQ = independent service w/ append-only segment-log durability.
- ADR-0002 PostgreSQL for collector data.
- ADR-0003 Multi-module monorepo + go.work.
- ADR-0004 gRPC streaming transport.
- ADR-0005 Canonical GPU id = `uuid` (**Proposed** — confirm before freezing DB schema).
- Process (branching, CI, TDD/coverage, hooks) lives in BRANCHING.md / Makefile / CI, not ADRs.
- Tooling (kind, buf) + name (vantage) recorded in PROMPT_HISTORY, not ADRs.

## Open questions (canonical live set — PROJECT.md §5 is the full ledger)
1. **GPU `{id}` = UUID** — ADR-0005 *Proposed*; confirm before freezing DB schema.
2. **MQ persistence depth** — segment-log direction set (ADR-0001); fidelity/effort TBD at MQ build.
3. **Streaming cadence** — per-row interval / batch / loop; decide at streamer build.
4. **OpenAPI generator** — `swag` vs `oapi-codegen`; decide at API-gateway build.

## Commits
- `main`: bootstrap scaffold + CI/TDD infra (PR #1) → context/config cleanup (PR #3). Both merged.
