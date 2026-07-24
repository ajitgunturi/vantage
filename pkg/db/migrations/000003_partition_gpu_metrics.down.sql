-- Revert to the flat (unpartitioned) gpu_metrics table.
CREATE TABLE gpu_metrics_flat (
    gpu_id      TEXT             NOT NULL,
    timestamp   TIMESTAMPTZ      NOT NULL,
    metric_name TEXT             NOT NULL,
    value       DOUBLE PRECISION NOT NULL,
    device      TEXT,
    model_name  TEXT,
    hostname    TEXT,
    container   TEXT,
    pod         TEXT,
    namespace   TEXT,
    labels_raw  TEXT
);

INSERT INTO gpu_metrics_flat SELECT * FROM gpu_metrics;

DROP FUNCTION IF EXISTS gpu_metrics_drop_old_partitions(int);
DROP FUNCTION IF EXISTS gpu_metrics_ensure_partitions(int);
DROP TABLE gpu_metrics; -- cascades to all partitions

ALTER TABLE gpu_metrics_flat RENAME TO gpu_metrics;
CREATE INDEX idx_gpu_metrics_gpu_id_ts
    ON gpu_metrics (gpu_id, timestamp DESC);
CREATE UNIQUE INDEX uq_gpu_metrics_natural_key
    ON gpu_metrics (gpu_id, metric_name, timestamp);
