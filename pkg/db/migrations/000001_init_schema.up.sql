-- gpu_metrics: long/narrow time-series table.
-- One row per DCGM metric reading, mapping TelemetryMessage 1:1 (D-01).
--
-- Identity note (D-04): gpu_id stores the GPU UUID (e.g. "GPU-5fd4f087-..."),
-- NOT the ordinal ("0"/"1"). Named gpu_id to match the spec's composite index
-- expression and /api/v1/gpus/{id} route. See CONTEXT.md D-03/D-04.

CREATE TABLE IF NOT EXISTS gpu_metrics (
    -- Identity (D-03 / D-04)
    gpu_id      TEXT             NOT NULL, -- GPU UUID; named gpu_id per spec (D-04)
    -- Timestamp precision note: Streamer MUST restamp at RFC3339Nano (nanosecond
    -- precision) in Phase 3. If restamped at second granularity (RFC3339), multiple
    -- readings within the same second on the same GPU/metric share the same natural
    -- key and are treated as duplicates — Phase 3 must enforce RFC3339Nano.
    timestamp   TIMESTAMPTZ      NOT NULL, -- microsecond precision stored by Postgres

    -- Metric payload (D-01 / D-02)
    metric_name TEXT             NOT NULL, -- e.g. "DCGM_FI_DEV_GPU_UTIL"
    value       DOUBLE PRECISION NOT NULL, -- D-02: numeric metric value

    -- Descriptive attributes (not identity; sourced from CSV / proto fields)
    device      TEXT,                      -- e.g. "nvidia0"
    model_name  TEXT,                      -- e.g. "NVIDIA H100 80GB HBM3"
    hostname    TEXT,
    container   TEXT,
    pod         TEXT,
    namespace   TEXT,
    labels_raw  TEXT                       -- raw Prometheus label string
);

-- Composite index (DB-02): planner uses this for gpu_id + time-range queries.
-- DESC on timestamp matches ORDER BY timestamp DESC in typical telemetry reads.
-- No CONCURRENTLY: golang-migrate wraps each file in a transaction; CONCURRENTLY
-- cannot run inside a transaction and would cause the migration to fail.
CREATE INDEX IF NOT EXISTS idx_gpu_metrics_gpu_id_ts
    ON gpu_metrics (gpu_id, timestamp DESC);

-- Natural-key unique constraint (DB-04): enables idempotent upsert (COLL-05, Phase 3).
-- Uniqueness on (gpu_id, metric_name, timestamp) means redelivering the same logical
-- reading hits the same key and can use ON CONFLICT rather than inserting a duplicate.
-- WARNING: Requires sub-second timestamp precision from the Streamer (RFC3339Nano).
-- If Streamer restamps at second granularity, multiple readings per second on the
-- same GPU/metric collapse to the same key — Phase 3 must enforce RFC3339Nano.
CREATE UNIQUE INDEX IF NOT EXISTS uq_gpu_metrics_natural_key
    ON gpu_metrics (gpu_id, metric_name, timestamp);
