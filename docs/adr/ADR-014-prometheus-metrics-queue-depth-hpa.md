# ADR-014: Prometheus Metrics on Every Service; Collector Autoscaling on Queue Backlog

**Status:** Accepted
**Date:** 2026-07-24
**Extends:** ADR-011 (whose counters this makes alertable), ADR-008 (single-replica MQ unchanged)

## Context

The broker tracked every operationally-critical counter — drops by cause,
rejections, dead-letters, lease expiries, queue depth — but only as human
JSON on `/api/v1/queue/inspect`. Nothing was scrapeable: no alert could fire
on loss counters, no dashboard could trend backlog, and autoscaling had no
signal beyond CPU. CPU is the wrong signal for a consumer: a Collector
bottlenecked on Postgres round-trips idles its CPU while backlog grows.

## Decision

1. **Every service exposes `GET /metrics`** (Prometheus exposition, own
   registry per service; pods carry `prometheus.io/*` scrape annotations):
   - **MQ**: a custom collector derives every series from the broker's
     `Stats()` snapshot **at scrape time** — zero hot-path instrumentation,
     and the numbers cannot disagree with the inspect endpoint.
   - **Collector**: batch persist latency + batch size histograms, batches,
     transient DB errors, dead-lettered rows.
   - **Streamer**: rows produced/skipped, produce retries (the client-side
     view of broker backpressure).
   - **Gateway**: request-latency histogram labelled by chi route **pattern**
     (bounded cardinality), method, status.
2. **Collector HPA scales on queue backlog, not CPU** (disabled by default;
   requires kube-prometheus-stack + prometheus-adapter, shipped config under
   `deployments/monitoring/`): external metric `mq_queue_backlog` =
   `mq_queue_depth + mq_retry_depth`, target backlog per replica, 120s
   scale-down stabilization against spiky drains. The Gateway keeps its
   CPU-based HPA — request-bound, CPU is the right signal there.

## Consequences

- Loss and pressure are now alertable: `mq_dropped_total{cause}` non-zero,
  `mq_rejected_total` climbing, or `mq_dlq_depth > 0` are one PromQL rule
  away, and the overload counters double as the scale-up trigger the
  freshness-first overflow default (ADR-011) presumes.
- The monitoring stack is opt-in: the charts only annotate pods; Prometheus
  and the adapter are documented installs, not chart dependencies.
- The MQ stays single-replica (ADR-008) — its HPA question does not arise;
  scaling the MQ remains vertical (buffer size) by design.
