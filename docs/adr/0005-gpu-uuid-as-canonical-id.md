# ADR-0005: GPU UUID as the canonical identity

- Status: Accepted
- Date: 2026-06-24 (proposed); 2026-06-24 (accepted)

## Context
The API uses `{id}` in `GET /api/v1/gpus/{id}/telemetry`. In the dataset, `gpu_id` is only the local
index 0–7 and repeats across all 31 hosts; the globally unique, stable identifier is the DCGM
**`uuid`** (e.g. `GPU-5fd4f087-...`). 247 distinct UUIDs were measured.

## Decision
Treat **`uuid` as the canonical GPU identity**: the value of `{id}` in the API, the conflict/primary
key in Postgres (`(uuid, metric_name, ts)`), and the MQ partition routing key (`hash(uuid) % N`).
Expose `hostname` + `gpu_id` as attributes for human readability, and optionally accept a
`hostname:gpu_id` composite as an alternate lookup later.

## Driving Prompt
Confirmed by the user on 2026-06-24 during GSD project initialization, as the GATE 0 decision ahead of
Phases 2/5/6. Backed by project research (`.planning/research/SUMMARY.md`, `PITFALLS.md` P0): 247
distinct, globally-unique UUIDs measured in the dataset; `gpu_id` is host-local only.

## Consequences
- (+) Globally unique, stable across host reboots; clean PK and partition key for the MQ.
- (−) UUIDs are long/opaque in URLs; mitigated by the `/gpus` list returning friendly attributes.
- Schema may now be frozen: `telemetry` PK `(uuid, metric_name, ts)`, partition key = `uuid`,
  API `{id}` = `uuid`. Unblocks Phase 2 (partition key), Phase 5 (collector schema), Phase 6 (`{id}`).

## Alternatives considered
- **`gpu_id` (0–7)** — rejected: not globally unique.
- **`hostname:gpu_id` composite** — unique and readable, but a compound key complicates joins and
  partition routing; keep as a secondary lookup at most.
