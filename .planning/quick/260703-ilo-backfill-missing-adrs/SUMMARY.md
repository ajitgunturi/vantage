---
phase: quick-260703-ilo-backfill-missing-adrs
plan: "01"
subsystem: docs/adr
tags: [adr, documentation, architecture, decisions]
dependency_graph:
  requires: []
  provides: [docs/adr/ADR-003..ADR-010, docs/adr/README.md]
  affects: [README.md]
tech_stack:
  added: []
  patterns: [Nygard ADR format, backfill documentation]
key_files:
  created:
    - docs/adr/ADR-003-single-module-directory-ownership.md
    - docs/adr/ADR-004-ring-buffer-store-interface.md
    - docs/adr/ADR-005-raw-protoc-over-buf.md
    - docs/adr/ADR-006-long-narrow-schema-natural-key.md
    - docs/adr/ADR-007-pgx-batch-on-conflict-over-copyfrom.md
    - docs/adr/ADR-008-chart-enforced-single-replica-post-install-hook.md
    - docs/adr/ADR-009-opt-in-wal-extension.md
    - docs/adr/ADR-010-telemetrypage-pagination-envelope.md
    - docs/adr/README.md
  modified:
    - README.md
decisions:
  - "Nygard ADR format with Backfilled: 2026-07-03 header distinguishes retroactive records from live decisions"
  - "Cross-links use real filenames so relative links resolve from the docs/adr/ directory"
  - "ADR index rows preserve the exact Status wording from each ADR header for audit consistency"
metrics:
  duration: "~12 minutes"
  completed: "2026-07-03"
  tasks_completed: 2
  tasks_total: 2
  files_created: 9
  files_modified: 1
status: complete
---

# Phase quick-260703-ilo-backfill-missing-adrs Plan 01: Backfill Missing ADRs Summary

Backfilled eight Architecture Decision Records (ADR-003 through ADR-010) covering
decisions made and shipped across Phases 1-6 that had no written record, then
added a ten-row ADR index at docs/adr/README.md and linked it from the main
README Design records section.

## Tasks Completed

| Task | Name | Commit | Key files |
|------|------|--------|-----------|
| 1 | Author the eight backfilled ADRs (ADR-003..ADR-010) | ca03778 | 8 ADR files in docs/adr/ |
| 2 | ADR index (docs/adr/README.md) + link from main README | 9782893 | docs/adr/README.md, README.md |

## What Was Built

Eight Nygard-format ADRs covering:

- ADR-003: Single Go module with directory-based service ownership
- ADR-004: Bounded ring buffer behind a Store interface (sync.Mutex, bool Enqueue, drop-oldest/newest)
- ADR-005: Raw protoc over buf CLI (deviation from stack recommendation; committed pkg/pb for hermetic builds)
- ADR-006: Long/narrow schema with natural composite key + RFC3339Nano restamp lock
- ADR-007: pgx.Batch ON CONFLICT DO NOTHING over CopyFrom (idempotency + ack-on-persist C-1 fix)
- ADR-008: Chart-enforced replicas:1 + post-install migration hook (deviation from D-09)
- ADR-009: Opt-in WAL persistence behind Store interface (Phase 7, in-memory default unchanged)
- ADR-010: TelemetryPage pagination envelope with limit+1 sentinel and OFFSET placeholder (T-06-03)

Plus docs/adr/README.md index (10 rows, ADR-001..ADR-010) and README.md Design records link.

## Deviations from Plan

None - plan executed exactly as written.

## Self-Check

- All 8 ADR files exist: PASSED
- Format verification (Status, Backfilled, 4 section headers): PASSED (ALL_ADRS_OK)
- ADR index has 10 rows: PASSED (INDEX_OK)
- README.md links docs/adr/README.md: PASSED
- Commits exist: ca03778 (Task 1), 9782893 (Task 2): PASSED
- No Go/proto/chart/Makefile files touched: PASSED

## Self-Check: PASSED
