# ADR-003: Single Go Module with Directory-Based Service Ownership

**Status:** Accepted
**Date:** 2026-06-27
**Phase:** 1 — MQ Foundation
**Backfilled:** 2026-07-03

## Context

The assignment brief (`instructions.md`) describes four independent microservices
(`cmd/mq`, `cmd/streamer`, `cmd/collector`, `cmd/gateway`) sharing a common
`pkg/` library and a single `api/proto/` contract. Two common Go multi-service
layouts were considered at project start:

1. **Single module** — one `go.mod` at repo root; services are independent by
   _directory + Dockerfile_, not by module boundary. `go build ./...` and
   `go test ./...` operate across the whole tree from the root.
2. **Multi-module** — a separate `go.mod` per service (or per `cmd/` directory),
   optionally stitched together by a `go.work` workspace and `replace` directives
   during local development.

The brief enforces that services share only `pkg/` and communicate only through
`pkg/pb` (generated protobuf). It does not mandate any particular Go module
topology. The choice was made at Phase 1 setup (plan 01-01) and recorded in
`CLAUDE.md` as the Module Discipline rule.

Sources: PROJECT.md Key Decisions (row 1), CLAUDE.md §Module discipline,
ROADMAP Phase 1 goals (plan 01-01).

## Decision

Adopt a **single Go module** rooted at `github.com/ajitg/vantage` with one
`go.mod` at the repository root. Services are independent by
**directory boundary + Dockerfile**, not by separate modules. The shared surface
is `pkg/` only; a service in `cmd/<svc>` may import `pkg/*` and its own internal
helpers but never another service's code.

Consequences of this layout:

- `go build ./...` and `go test ./...` work from the root without any workspace
  or replace directives.
- No `go.work` file, no `replace` entries — every dependency is resolved from a
  single `go.sum`.
- Service isolation is enforced by convention and code review, not by the Go
  module system. The CI `make build` + `make test` targets surface cross-service
  import violations as compile errors (a service importing another service's
  package fails because there is no legitimate import path for it).

## Consequences

- **Positive:** A single `go mod tidy` keeps the whole repo consistent; no
  per-service lock file drift.
- **Positive:** `go test -race ./...` from the root covers all services and
  shared packages in one pass — the Makefile `test` target relies on this.
- **Positive:** No `replace` churn when a shared `pkg/` type changes — all
  consumers rebuild against the updated source automatically.
- **Negative:** All services share a single dependency graph. A dependency bump
  for one service affects the compile of every other service; version conflicts
  across services must be resolved at the module level rather than per-service.
- **Negative:** Service isolation is a naming convention, not a hard module
  boundary. A future contributor can accidentally import across service boundaries
  and the compiler will not catch it unless a lint rule or CI check flags it.

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| Multi-module (`go.mod` per service) | True module-level isolation; independent versioning and dependency graphs. But: requires a `go.work` workspace for local development, `replace` directives for cross-module `pkg/` imports, and separate `go mod tidy` per service. Adds toolchain complexity that provides no practical benefit for a single-repo project with a single team. |
| Module-per-service, no `go.work` | Produces hermetic (fully self-contained — each service builds independently with no shared build graph or external workspace file) per-service builds. But: cross-service `pkg/` changes require publishing an intermediate module version or using `replace` permanently — incompatible with `go build ./...` at the root, which is the Makefile's primary build command. |
