# ADR-0005: GPU UUID as the canonical identity

- Status: Proposed
- Date: 2026-06-24

## Context
The API uses `{id}` in `GET /api/v1/gpus/{id}/telemetry`. In the dataset, `gpu_id` is only the local
index 0–7 and repeats across all 31 hosts; the globally unique, stable identifier is the DCGM
**`uuid`** (e.g. `GPU-5fd4f087-...`). 247 distinct UUIDs were measured.

## Decision (proposed)
Treat **`uuid` as the canonical GPU identity** and the value of `{id}` in the API and the primary key
in Postgres. Expose `hostname` + `gpu_id` as attributes for human readability, and optionally accept
a `hostname:gpu_id` composite as an alternate lookup later.

## Driving Prompt
No direct user prompt yet — flagged as open question in `PROJECT.md` §5. Defaulting to UUID pending
explicit confirmation; recorded so the choice is visible to the panel.

## Consequences
- (+) Globally unique, stable across host reboots; clean PK and partition key for the MQ.
- (−) UUIDs are long/opaque in URLs; mitigated by the `/gpus` list returning friendly attributes.
- ⚠️ **Needs user confirmation** before it moves to Accepted and the schema is frozen.

## Alternatives considered
- **`gpu_id` (0–7)** — rejected: not globally unique.
- **`hostname:gpu_id` composite** — unique and readable, but a compound key complicates joins and
  partition routing; keep as a secondary lookup at most.
