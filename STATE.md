# STATE

**Project:** vantage (`github.com/ajitgunturi/vantage`) — elastic GPU telemetry pipeline.
**Current phase:** Monorepo scaffold (skeleton in place; service logic pending).
**Last action:** Renamed module paths to vantage, regenerated gRPC stubs, verified `mq` builds.

## Micro-task
- [x] Read `GPU Telemetry Pipeline Message Queue.pdf` + profile dataset
- [x] Capture full context → `PROJECT.md`
- [x] Decide structure: multi-module (5× go.mod) + go.work, gRPC, kind, buf
- [x] Create dir tree, go.work, .gitignore, per-module go.mod
- [x] Define MQ proto contract + generate gRPC stubs (`mq/gen/mqv1`)
- [x] Set up docs/: ADR 0001–0008 + PROMPT_HISTORY
- [x] Name project = vantage / org ajitgunturi; rename module paths; `mq` builds
- [x] GitHub repo created (public) + 5 long-lived feat/* branches + protection rulesets (ADR-0006)
- [x] CI workflow live; first main push went red → fixing via PR on feat/k8s-infra
- [x] Makefile (tools, hooks, proto, build, test, cover, cover-check, cover-logic, lint, kind, helm)
- [x] Coverage gates: 90% line (native) + 100% branch (gobco), fail-open until code (ADR-0007)
- [x] Pre-commit hook (gofmt + golangci-lint) + CI lint job (ADR-0007)
- [ ] **IN FLIGHT:** PR feat/k8s-infra → main (fix CI red + TDD/quality infra); merge once ci-success green
- [ ] Stub service mains (streamer / collector / apigateway / mqbroker) — compiling skeletons
- [ ] k8s-infra Helm umbrella chart + kind config
- [ ] Dockerfiles per service
- [ ] README skeleton
- [ ] Implement MQ durable segment log (ADR-0001) + client lib — TDD
- [ ] Implement collector→Postgres, streamer→CSV loop, API gateway endpoints — TDD
- [ ] Perf harness (producer/consumer ratios)

## Workflow reminder (simplified — ADR-0008)
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

## Open questions
- ADR-0005 (GPU id = UUID) still **Proposed** — confirm before freezing DB schema.
- PROJECT.md §5: MQ persistence depth, streaming cadence, OpenAPI tool (swag vs oapi-codegen).

## Commits
- `main`: bootstrap scaffold + CI/TDD infra (PR #1 merged). Cleanup PR in flight.
