-- gpu_metrics_dlq: dead-letter table for rows that deterministically fail to
-- persist into gpu_metrics. A row lands here when the Collector's
-- bisect-on-failure isolates it as poison (SQLSTATE class 22 data exception or
-- 23 integrity violation) — transient DB errors never dead-letter.
--
-- The full TelemetryMessage is preserved as JSONB (protojson) so poison rows
-- can be inspected via SQL after the schema/constraint issue is fixed. No API
-- surface exists for this table yet — inspect/replay endpoints and retention
-- are future work (see docs/FUTURE.md, "Known operational gaps").
CREATE TABLE IF NOT EXISTS gpu_metrics_dlq (
    id                BIGSERIAL   PRIMARY KEY,
    broker_id         BIGINT      NOT NULL, -- stable broker message id
    delivery_attempts INT         NOT NULL, -- broker delivery count at dead-letter time
    payload           JSONB       NOT NULL, -- full TelemetryMessage (protojson)
    error             TEXT        NOT NULL, -- SQLSTATE + message of the failed insert
    dead_lettered_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Operators page through recent poison first.
CREATE INDEX IF NOT EXISTS idx_gpu_metrics_dlq_at
    ON gpu_metrics_dlq (dead_lettered_at DESC);
