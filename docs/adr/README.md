# Architecture Decision Records (ADRs)

An ADR captures a decision about **how the running system is designed** — its structure, guarantees,
and the trade-offs behind them. Each is a choice a future maintainer would want the reasoning for,
with a genuine road-not-taken.

**In scope (architecture):** how the message queue achieves durability, the storage engine, the
module topology, the wire protocol, the data-model identity key.

**Out of scope (engineering process):** branching strategy, CI pipeline, testing/coverage approach,
and pre-commit tooling are *not* ADRs. They live in operational docs — see
[`../BRANCHING.md`](../BRANCHING.md), the root `Makefile`, and `.github/workflows/`.

Each ADR records the **Driving Prompt** — the verbatim user instruction behind it — so the human → AI
decision trail stays auditable. The scaffold-era prompt log is the frozen pre-GSD appendix
[`../PROMPT_HISTORY.md`](../PROMPT_HISTORY.md); from GSD adoption onward the build/AI-usage evidence
is the `.planning/` tree (research, per-phase SPEC/PLAN/VERIFICATION, requirements, roadmap, commits).

## Format
```
# ADR-NNNN: <title>
- Status: Proposed | Accepted | Superseded by ADR-XXXX
- Date: YYYY-MM-DD
## Context        — the forces and constraints
## Decision       — what we chose
## Driving Prompt — verbatim user instruction(s) behind it
## Consequences    — trade-offs, what this makes easy/hard, follow-ups
## Alternatives considered
```

## Index
| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-custom-mq-durable-segment-log.md) | Custom MQ with durable append-only segment log | Accepted |
| [0002](0002-postgresql-for-collector-persistence.md) | PostgreSQL for collector persistence | Accepted |
| [0003](0003-multi-module-monorepo.md) | Multi-module monorepo (5× go.mod + go.work) | Accepted |
| [0004](0004-grpc-streaming-transport.md) | gRPC streaming as the MQ transport | Accepted |
| [0005](0005-gpu-uuid-as-canonical-id.md) | GPU UUID as the canonical identity | Proposed |
