# ADR-0003: Multi-module monorepo (5× go.mod + go.work)

- Status: Accepted
- Date: 2026-06-24

## Context
Five components — streamer, collector, message queue, api gateway, k8s-infra — live in one repo. The
MQ is both a deployable broker **and** a client library imported by streamer and collector. We need
clean module boundaries, independent Docker builds, and good local-dev ergonomics.

## Decision
**Multi-module monorepo**: each Go component is its own module (`mq`, `streamer`, `collector`,
`apigateway`); `k8s-infra` is config-only (no `go.mod`). Tie them together with:
- `go.work` at the root for local builds/IDE (resolves cross-module imports with no network).
- A `replace github.com/ajitgunturi/vantage/mq => ../mq` directive in each consumer's `go.mod` so
  Docker builds compile standalone without the workspace.
The `mq` module exposes `client/` (producer/consumer lib) + `cmd/mqbroker` (the service).

## Driving Prompt
> We will build a monorepo structure - have 5 modules - 1 module each for - streamer, collector,
> message queue, api gateway and finally the k8s-infra files ...

(Module-layout choice confirmed via question prompt: **"5 separate go.mod (multi-module)"**.)

## Consequences
- (+) True isolation; each service has its own dependency graph and minimal Docker context.
- (+) MQ client lib is versioned/importable like a real published library.
- (−) `go.work` + `replace` must be kept consistent; CI must build each module independently.
- (−) Slightly more boilerplate than a single-module layout.

## Alternatives considered
- **Single go.mod + cmd/ entrypoints** — simplest, but the user explicitly chose multi-module for
  isolation and to model the MQ as a standalone library.
