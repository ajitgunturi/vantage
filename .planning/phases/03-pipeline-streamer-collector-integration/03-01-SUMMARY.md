---
phase: 03-pipeline-streamer-collector-integration
plan: "01"
subsystem: models
tags: [go, protobuf, grpc, postgresql, telemetry, models]

requires:
  - phase: 02-storage-foundation
    provides: "pkg/pb generated gRPC types; pkg/db schema with gpu_metrics natural-key constraint"

provides:
  - "pkg/models.GpuMetric: canonical domain struct (11 fields) mapping the gpu_metrics table row"
  - "pkg/models.FromProto: single enforcement point for proto.Uuid -> GpuID mapping (COLL-04 / D-04)"
  - "pkg/models.InsertSQL: 11-column idempotent INSERT with ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING"

affects:
  - 03-02-streamer
  - 03-03-collector
  - 04-api-gateway

tech-stack:
  added: []
  patterns:
    - "FromProto conversion pattern: pkg/pb types never escape pkg/models; all other packages depend on the domain struct"
    - "InsertSQL positional-arg ordering matches migration DDL column order exactly ($1=gpu_id .. $11=labels_raw)"

key-files:
  created:
    - pkg/models/telemetry.go
    - pkg/models/telemetry_test.go
  modified: []

key-decisions:
  - "GpuMetric.GpuID sourced exclusively from msg.GetUuid() (proto field 5); msg.GetGpuId() (ordinal) intentionally ignored — single enforcement point for COLL-04 / D-04"
  - "RFC3339Nano primary parse with RFC3339 fallback; unparseable timestamps return error + zero value rather than storing garbage (T-03-TS mitigation)"
  - "InsertSQL column order ($1=gpu_id, $2=timestamp, $3=metric_name … $11=labels_raw) established as the shared contract for the Collector's CopyFromRows argument list"

patterns-established:
  - "Black-box test package (models_test) with testify/require; tests import only public API"
  - "TDD RED-GREEN: test file committed first (compile failure confirms RED), implementation committed after all tests pass"

requirements-completed: [COLL-04]

coverage:
  - id: D1
    description: "FromProto maps proto.Uuid (field 5) to GpuMetric.GpuID, never proto.GpuId (the ordinal) — COLL-04 / D-04 enforced"
    requirement: COLL-04
    verification:
      - kind: unit
        ref: "pkg/models/telemetry_test.go#TestFromProto_UUIDMapping"
        status: pass
    human_judgment: false
  - id: D2
    description: "RFC3339Nano nanosecond timestamp round-trips without loss through FromProto"
    verification:
      - kind: unit
        ref: "pkg/models/telemetry_test.go#TestFromProto_RestampRFC3339Nano"
        status: pass
    human_judgment: false
  - id: D3
    description: "RFC3339 whole-second fallback parse succeeds in FromProto"
    verification:
      - kind: unit
        ref: "pkg/models/telemetry_test.go#TestFromProto_RFC3339Fallback"
        status: pass
    human_judgment: false
  - id: D4
    description: "Unparseable timestamp causes FromProto to return error and zero GpuMetric (T-03-TS)"
    verification:
      - kind: unit
        ref: "pkg/models/telemetry_test.go#TestFromProto_BadTimestamp"
        status: pass
    human_judgment: false
  - id: D5
    description: "All 11 descriptive fields round-trip through FromProto"
    verification:
      - kind: unit
        ref: "pkg/models/telemetry_test.go#TestFromProto_AllFields"
        status: pass
    human_judgment: false
  - id: D6
    description: "InsertSQL has 11 positional params, conflict target (gpu_id, metric_name, timestamp), and DO NOTHING clause"
    verification:
      - kind: unit
        ref: "pkg/models/telemetry_test.go#TestInsertSQL_Shape"
        status: pass
    human_judgment: false

duration: 2min
completed: 2026-06-29
status: complete
---

# Phase 03 Plan 01: pkg/models — GpuMetric Domain Model Summary

**pkg/models package with GpuMetric struct, FromProto UUID-mapping converter, and InsertSQL idempotent-upsert constant — single enforcement point for the proto.Uuid -> gpu_id identity mapping (COLL-04)**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-06-29T10:56:39Z
- **Completed:** 2026-06-29T10:58:35Z
- **Tasks:** 2 (TDD RED + GREEN)
- **Files modified:** 2

## Accomplishments

- `pkg/models.GpuMetric`: 11-field domain struct matching the `gpu_metrics` table DDL 1:1
- `pkg/models.FromProto`: parses `TelemetryMessage` into `GpuMetric`; maps `proto.Uuid` (field 5) to `GpuID` (never the ordinal `proto.GpuId`); RFC3339Nano primary parse with RFC3339 fallback; error on bad timestamp
- `pkg/models.InsertSQL`: idempotent 11-column INSERT with `ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING` — positional args $1..$11 in DDL column order
- 6 unit tests covering all COLL-04 / D-04 invariants; `pkg/models` coverage 100%; overall module coverage 94.3% (gate ≥90%)

## Task Commits

1. **Task 1: RED — failing unit tests** - `3b37f78` (test)
2. **Task 2: GREEN — implement GpuMetric, FromProto, InsertSQL** - `4ec8ded` (feat)

## Files Created/Modified

- `pkg/models/telemetry.go` - GpuMetric struct + FromProto converter + InsertSQL constant
- `pkg/models/telemetry_test.go` - 6 black-box unit tests (package models_test)

## Decisions Made

- `GpuMetric.GpuID` sourced exclusively from `msg.GetUuid()` — `msg.GetGpuId()` (ordinal) is intentionally ignored. This is the single enforcement point; the Collector and Gateway must not duplicate the mapping.
- RFC3339Nano primary with RFC3339 fallback; unparseable timestamps return error + zero value (callers skip-and-log) — aligns with T-03-TS mitigation.
- InsertSQL column order `$1=gpu_id, $2=timestamp, $3=metric_name … $11=labels_raw` established as shared contract for the Collector's batch insert arg list (plan 03-03).

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## Threat Surface Scan

No new network endpoints, auth paths, or file access patterns introduced. `pkg/models` is a pure conversion library (stdlib + pkg/pb). T-03-IDmap and T-03-TS mitigations implemented as planned.

## Next Phase Readiness

- `pkg/models` is a stable leaf library; plan 03-03 (Collector) can import it immediately.
- `InsertSQL` positional-arg order is the shared contract — the Collector's `CopyFromRows` / `QueryRow` call must supply args in the documented order.
- No blockers.

---
*Phase: 03-pipeline-streamer-collector-integration*
*Completed: 2026-06-29*
