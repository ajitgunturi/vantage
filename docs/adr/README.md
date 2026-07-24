# Architecture Decision Records

Architecture Decision Records for vantage — Nygard format.

Each record documents a significant architectural or design decision: the context
that forced it, the decision itself, consequences (positive and negative), and the
alternatives that were rejected.

| # | Title | Status | Date | Phase |
|---|-------|--------|------|-------|
| [ADR-001](ADR-001-bidi-at-least-once-delivery.md) | Bidirectional `Consume` with broker-side at-least-once delivery | Accepted (deviation from brief) | 2026-06-28 | 01.1 |
| [ADR-002](ADR-002-natural-key-microsecond-collision.md) | Natural Key Microsecond Collision Under Concurrent Streamers | Accepted | 2026-07-02 | cross-phase |
| [ADR-003](ADR-003-single-module-directory-ownership.md) | Single Go Module with Directory-Based Service Ownership | Accepted | 2026-06-27 | 1 |
| [ADR-004](ADR-004-ring-buffer-store-interface.md) | Bounded Ring Buffer Behind a `Store` Interface | Accepted | 2026-06-27 | 1 |
| [ADR-005](ADR-005-raw-protoc-over-buf.md) | Hermetic Builds via Committed Generated Proto Code | Accepted (deviation from stack recommendation) | 2026-06-27 | 1 |
| [ADR-006](ADR-006-long-narrow-schema-natural-key.md) | Long/Narrow Schema with Natural Composite Key | Accepted | 2026-06-29 | 2 |
| [ADR-007](ADR-007-pgx-batch-on-conflict-over-copyfrom.md) | `pgx.Batch` with `ON CONFLICT DO NOTHING` over `CopyFrom` | Accepted | 2026-06-29 | 3 |
| [ADR-008](ADR-008-chart-enforced-single-replica-post-install-hook.md) | Chart-Enforced Single Replica and Post-Install Migration Hook | Accepted (deviation from plan item D-09) | 2026-07-02 | 5 |
| [ADR-009](ADR-009-opt-in-wal-extension.md) | Opt-In WAL Persistence Backend Behind the `Store` Interface | Accepted (pending — Phase 7) | 2026-06-27 | roadmap / 7 |
| [ADR-010](ADR-010-telemetrypage-pagination-envelope.md) | `TelemetryPage` Pagination Envelope with Limit+1 Sentinel | Accepted | 2026-07-03 | 6 |
| [ADR-011](ADR-011-broker-delivery-hardening.md) | Broker Delivery Hardening — Backpressure, Retry/DLQ Lanes, Lease TTL, preStop Drain | Accepted | 2026-07-24 | post-v1 |
| [ADR-012](ADR-012-daily-partitioning-retention.md) | Daily Range Partitioning of `gpu_metrics` with O(1) Partition-Drop Retention | Accepted | 2026-07-24 | post-v1 |
| [ADR-013](ADR-013-batch-publish-contract.md) | `ProduceBatch` — Batched Publish with Partial-Accept Contract | Accepted | 2026-07-24 | post-v1 |
| [ADR-014](ADR-014-prometheus-metrics-queue-depth-hpa.md) | Prometheus Metrics on Every Service; Collector Autoscaling on Queue Backlog | Accepted | 2026-07-24 | post-v1 |

> ADR-003 through ADR-010 were backfilled on 2026-07-03. The decisions themselves
> were made in the phases listed above; only the written records are retroactive.
