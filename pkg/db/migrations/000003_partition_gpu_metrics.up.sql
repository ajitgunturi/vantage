-- Convert gpu_metrics to declarative daily RANGE partitions on timestamp.
--
-- Why: telemetry tables grow without bound; row-wise DELETE retention causes
-- table/index bloat and vacuum churn at exactly the volumes where it matters.
-- Dropping a whole daily partition is O(1) metadata work — no dead tuples, no
-- vacuum debt, index depth stays bounded by the retention window.
--
-- Layout:
--   gpu_metrics                : partitioned parent (RANGE on timestamp)
--   gpu_metrics_pYYYYMMDD      : one partition per UTC day (managed by the
--                                functions below + the retention CronJob)
--   gpu_metrics_default        : DEFAULT partition — safety net for rows
--                                outside the pre-created window (backfills,
--                                clock skew). Normally empty: the Streamer
--                                restamps at publish time, so live rows land
--                                in the current day's partition.
--
-- The natural-key unique index includes the partition key (timestamp), as
-- Postgres requires for unique indexes on partitioned tables — the idempotent
-- ON CONFLICT upsert (COLL-05) is unchanged. The composite (gpu_id,
-- timestamp DESC) index (DB-02) is declared on the parent and materializes
-- per partition.

ALTER TABLE gpu_metrics RENAME TO gpu_metrics_flat;
ALTER INDEX idx_gpu_metrics_gpu_id_ts RENAME TO idx_gpu_metrics_gpu_id_ts_flat;
ALTER INDEX uq_gpu_metrics_natural_key RENAME TO uq_gpu_metrics_natural_key_flat;

CREATE TABLE gpu_metrics (
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
) PARTITION BY RANGE (timestamp);

CREATE INDEX idx_gpu_metrics_gpu_id_ts
    ON gpu_metrics (gpu_id, timestamp DESC);

CREATE UNIQUE INDEX uq_gpu_metrics_natural_key
    ON gpu_metrics (gpu_id, metric_name, timestamp);

CREATE TABLE gpu_metrics_default PARTITION OF gpu_metrics DEFAULT;

-- gpu_metrics_ensure_partitions(days_ahead): create daily partitions from
-- yesterday through current_date + days_ahead (UTC). Idempotent — returns the
-- number of partitions actually created. Run by the retention CronJob (and at
-- migration time) so the write path never waits on DDL.
CREATE OR REPLACE FUNCTION gpu_metrics_ensure_partitions(days_ahead int DEFAULT 3)
RETURNS int
LANGUAGE plpgsql
AS $$
DECLARE
    d       date;
    part    text;
    created int := 0;
BEGIN
    FOR d IN SELECT generate_series(current_date - 1, current_date + days_ahead, interval '1 day')::date LOOP
        part := 'gpu_metrics_p' || to_char(d, 'YYYYMMDD');
        IF to_regclass(part) IS NULL THEN
            EXECUTE format(
                'CREATE TABLE %I PARTITION OF gpu_metrics FOR VALUES FROM (%L) TO (%L)',
                part, d, d + 1
            );
            created := created + 1;
        END IF;
    END LOOP;
    RETURN created;
END;
$$;

-- gpu_metrics_drop_old_partitions(retention_days): O(1)-drop every daily
-- partition whose whole day lies before current_date - retention_days.
-- Returns the number of partitions dropped. Guards against nonsensical
-- retention (< 1 day). The DEFAULT partition is never dropped.
CREATE OR REPLACE FUNCTION gpu_metrics_drop_old_partitions(retention_days int)
RETURNS int
LANGUAGE plpgsql
AS $$
DECLARE
    part    record;
    day     date;
    dropped int := 0;
BEGIN
    IF retention_days < 1 THEN
        RAISE EXCEPTION 'retention_days must be >= 1, got %', retention_days;
    END IF;
    FOR part IN
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        WHERE i.inhparent = 'gpu_metrics'::regclass
          AND c.relname ~ '^gpu_metrics_p[0-9]{8}$'
    LOOP
        day := to_date(substring(part.relname from 'p([0-9]{8})$'), 'YYYYMMDD');
        IF day < current_date - retention_days THEN
            EXECUTE format('DROP TABLE %I', part.relname);
            dropped := dropped + 1;
        END IF;
    END LOOP;
    RETURN dropped;
END;
$$;

-- Seed the initial partition window, migrate existing rows, drop the flat table.
SELECT gpu_metrics_ensure_partitions(3);
INSERT INTO gpu_metrics SELECT * FROM gpu_metrics_flat;
DROP TABLE gpu_metrics_flat;
