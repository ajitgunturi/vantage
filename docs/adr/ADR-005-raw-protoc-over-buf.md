# ADR-005: Hermetic Builds via Committed Generated Proto Code

**Status:** Accepted (deviation from the researched stack recommendation)
**Scope note:** The architectural decision here is *committing `pkg/pb/` so builds
and CI need no proto toolchain*. The raw-`protoc`-over-`buf` choice is the
mechanism serving that decision, recorded alongside it.
**Date:** 2026-06-27
**Phase:** 1 — MQ Foundation
**Backfilled:** 2026-07-03

## Context

The vantage stack research (`.claude/CLAUDE.md` Technology Stack section)
recommends **buf CLI v1.71.0** as the modern protobuf toolchain. `buf` provides:
lint enforcement, breaking-change detection against `main`, and remote BSR plugin
invocation (`buf generate` with remote plugins eliminates local `protoc` plugin
installs). The Makefile `tools` target was initially written to install `buf`.

The project has exactly **one proto file**: `api/proto/mq.proto`, defining two
RPCs (`Produce` and `Consume`) and a handful of message types. There is no plan
to publish the proto contract to an external registry or to version it
independently of the service.

At Phase 1 setup the trade-off was evaluated and recorded in STATE.md Open
Decisions as "Phase 1 (RESOLVED): proto tooling = raw protoc". The ROADMAP Phase
1 goal list reflects the raw-protoc choice.

Sources: STATE.md ## Open Decisions (Phase 1 RESOLVED), CLAUDE.md Technology
Stack (`buf` recommendation with rationale), ROADMAP Phase 1.

## Decision

Use **raw `protoc`** with `paths=source_relative` to generate `pkg/pb/` from
`api/proto/mq.proto`. The generated Go files (`pkg/pb/*.go`) are **committed to
the repository**, making builds hermetic: `go build ./...` and `go test ./...`
require no `protoc` or `buf` install at build time.

The `make proto` target regenerates `pkg/pb/` when the `.proto` file changes and
requires `protoc` + the Go plugins locally, but this target is only needed when
the proto contract evolves — not on every build or CI run.

This is a deliberate, documented deviation from the stack recommendation, made
because:

1. A single `.proto` file gains nothing from buf's lint rules (no large naming
   inconsistency surface), BSR (no external consumers), or breaking-change
   detection (the contract only needs to be stable within the mono-repo).
2. Committing `pkg/pb/` removes all toolchain dependencies from the CI/CD critical
   path. Builds remain green even if the buf BSR becomes unavailable.
3. The engineering cost of `buf generate` (remote plugin resolution, `buf.gen.yaml`
   versioning) exceeds its value for one file owned by one team.

## Consequences

- **Positive:** `go build ./...` is hermetic — no proto toolchain needed at
  build time; CI has no external dependency on buf BSR or protoc.
- **Positive:** Simpler Makefile `proto` target (one shell command, no
  `buf.gen.yaml` to maintain).
- **Negative:** `pkg/pb/` contains generated code in version control. If
  `mq.proto` is changed without running `make proto`, the committed stubs go
  out of sync silently — there is no CI check that compares the generated output
  against the source proto. This is a regen-drift risk.
- **Negative:** Deviates from the recommended toolchain; a future contributor
  expecting `buf` will not find it, and will need to read this ADR to understand
  why raw `protoc` is used.
- **Neutral:** If the proto contract grows significantly (e.g., multiple files,
  external consumers, published to a registry), migrating to `buf` later is
  straightforward — `buf.gen.yaml` replaces the `protoc` invocation in
  `make proto`, and the generated output remains identical in structure.

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| buf CLI v1.71.0 (recommended) | Full lint, breaking-change detection, remote BSR plugins — valuable for a multi-file, multi-team contract. For a single-file internal contract it adds network dependency (BSR), a non-Go tool install in CI, and a `buf.gen.yaml` to maintain. Overkill at this scale. |
| Generate stubs at build time (not committing `pkg/pb/`) | Ensures the committed source is always the truth; no drift risk. But forces every developer and CI pipeline to install `protoc` + Go plugins before `go build ./...` works, which breaks the hermetic build guarantee. |
| Hand-write gRPC service code (no proto) | Eliminates protoc entirely. Incompatible with the assignment's gRPC requirement and the goal of a typed, contract-first interface between services. |
