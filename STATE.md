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
- [ ] Stub service mains (streamer / collector / apigateway / mqbroker) — compiling skeletons
- [ ] Makefile (tools, proto, build, test+coverage, openapi, kind, helm)
- [ ] k8s-infra Helm umbrella chart + kind config
- [ ] Dockerfiles per service
- [ ] README skeleton
- [ ] Implement MQ durable segment log (ADR-0001) + client lib
- [ ] Implement collector→Postgres, streamer→CSV loop, API gateway endpoints
- [ ] Unit tests + perf harness (producer/consumer ratios)

## Decisions made (see docs/adr)
- Multi-module monorepo + go.work (ADR-0003); gRPC transport (ADR-0004); kind (ADR-0005); buf (ADR-0006).
- Custom MQ = independent service w/ append-only segment-log durability (ADR-0001); Postgres for data (ADR-0002).
- Canonical GPU id = `uuid` (ADR-0005, Proposed — needs confirm). Tooling (kind/buf) + name (vantage) → PROMPT_HISTORY, not ADRs.

## Open questions
- ADR-0005 (GPU id = UUID) still **Proposed** — confirm before freezing DB schema.
- PROJECT.md §5: MQ persistence depth, streaming cadence, OpenAPI tool (swag vs oapi-codegen).

## Commits
- (none yet — git initialized, ready for first commit once skeleton compiles)
