# Architecture Decision Records (ADRs)

An ADR captures a **load-bearing architectural decision** — one a future maintainer (or the
interview panel) would want the *reasoning* behind, and one with a genuine road-not-taken.

**What earns an ADR:** a choice that is hard to reverse, shapes the system's structure or guarantees,
and has real trade-offs (e.g. how the queue achieves durability, the storage engine, module
topology, the wire protocol, the data-model identity key).

**What does NOT:** reversible tooling and branding picks with no meaningful trade-off — the local
k8s runtime (kind), the codegen tool (buf), the project name. Those are recorded in
[`../PROMPT_HISTORY.md`](../PROMPT_HISTORY.md), which logs **every** prompt verbatim. ADRs are the
curated, decision-level view; PROMPT_HISTORY is the complete raw trail.

Each ADR includes a **Driving Prompt** — the verbatim user instruction behind it — so the human → AI
decision trail stays auditable.

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
| [0006](0006-branching-and-ci-model.md) | Long-lived per-module branches with CI-gated trunk | Accepted |
