package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ajitg/vantage/pkg/models"
)

// DistinctGPUIDs returns the de-duplicated, ascending-sorted list of GPU UUID
// strings that have at least one row in gpu_metrics.
//
// An empty table returns a non-nil empty slice so that the JSON encoder
// produces [] rather than null (API-01).
//
// Security: DSN is never embedded in error strings (ASVS V8). Errors carry
// context ("db: DistinctGPUIDs: ...") but not the connection string value.
func DistinctGPUIDs(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx,
		"SELECT DISTINCT gpu_id FROM gpu_metrics ORDER BY gpu_id")
	if err != nil {
		return nil, fmt.Errorf("db: DistinctGPUIDs: query: %w", err)
	}
	defer rows.Close()

	// Allocate a non-nil empty slice so json.Marshal produces [] not null.
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db: DistinctGPUIDs: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: DistinctGPUIDs: rows: %w", err)
	}

	return ids, nil
}

// GPUExists reports whether the given id appears at least once in gpu_metrics.
//
// Used by the telemetry handler to distinguish "unknown GPU" (404) from
// "known GPU with empty window" (200 []) — resolving OQ-2.
//
// Security: id is bound as $1 (never string-concatenated); injection impossible
// (T-04-03 / ASVS V5). DSN never embedded in error strings (ASVS V8).
func GPUExists(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM gpu_metrics WHERE gpu_id = $1)",
		id,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db: GPUExists: %w", err)
	}
	return exists, nil
}

// TelemetryCount returns the total number of metric rows for the given gpu_id
// within the optional inclusive [start, end] window (nil = unbounded), matching
// the filter predicate of Telemetry exactly. It backs the pagination envelope's
// "total" field (API-05 amendment, ADR-010).
//
// The COUNT(*) runs against the composite index (gpu_id, timestamp DESC) —
// an index-only scan for a single GPU at fixture scale.
//
// Security: all parameters are pgx-bound ($1..$3); no string concatenation;
// DSN never embedded in errors (ASVS V5, V8).
func TelemetryCount(
	ctx context.Context,
	pool *pgxpool.Pool,
	id string,
	start, end *time.Time,
) (int64, error) {
	var total int64
	var err error

	if start == nil && end == nil {
		// Simple path — mirrors the unbounded Telemetry query predicate.
		err = pool.QueryRow(ctx,
			`SELECT count(*) FROM gpu_metrics WHERE gpu_id = $1`,
			id,
		).Scan(&total)
	} else {
		// Windowed path — same nullable-bound predicate as Telemetry (OQ-3).
		err = pool.QueryRow(ctx,
			`SELECT count(*) FROM gpu_metrics
			 WHERE gpu_id = $1
			   AND ($2::timestamptz IS NULL OR timestamp >= $2)
			   AND ($3::timestamptz IS NULL OR timestamp <= $3)`,
			id, start, end,
		).Scan(&total)
	}
	if err != nil {
		return 0, fmt.Errorf("db: TelemetryCount: %w", err)
	}
	return total, nil
}

// Telemetry returns the metric rows for the given gpu_id ordered by timestamp
// DESC, capped at limit rows starting from offset. start and end are optional
// inclusive RFC3339 time bounds (nil = unbounded; OQ-3 partial bounds).
//
// Design — two-query approach (RESEARCH Pattern 5 / A1):
//   - No bounds: simpler query without any IS NULL predicate; index on
//     (gpu_id, timestamp DESC) is used without the OR branch overhead.
//   - At least one bound: nullable-bound predicate so the planner can still
//     use the composite index for both full and partial windows (API-03).
//
// offset is the row offset for pagination (API-05). Both limit and offset are
// passed as pgx $N placeholders — never string-concatenated (T-06-03 / ASVS V5).
//
// Returns a non-nil empty slice when no rows match (encodes as [] not null).
//
// Security: all parameters are pgx-bound ($1..$5); no string concatenation;
// DSN never embedded in errors (T-04-03, T-04-04, T-04-06 / ASVS V5, V8).
func Telemetry(
	ctx context.Context,
	pool *pgxpool.Pool,
	id string,
	start, end *time.Time,
	limit, offset int,
) ([]models.GpuMetric, error) {
	// COALESCE converts NULLs in optional text columns to empty strings so that
	// the scan target (*string) never encounters a NULL. In production, the
	// Collector inserts empty strings (not NULLs) via models.InsertSQL, but
	// test helpers and direct SQL may leave columns NULL.
	const cols = `gpu_id, timestamp, metric_name, value,
	              COALESCE(device, ''), COALESCE(model_name, ''), COALESCE(hostname, ''),
	              COALESCE(container, ''), COALESCE(pod, ''), COALESCE(namespace, ''),
	              COALESCE(labels_raw, '')`

	var (
		rows pgx.Rows
		err  error
	)

	if start == nil && end == nil {
		// Simple path — no time filtering; index on (gpu_id, timestamp DESC) is
		// used directly without the OR-IS-NULL overhead.
		// OFFSET $3 is bound as a pgx placeholder — never string-concatenated (T-06-03).
		rows, err = pool.Query(ctx,
			`SELECT `+cols+`
			 FROM gpu_metrics
			 WHERE gpu_id = $1
			 ORDER BY timestamp DESC, metric_name DESC
			 LIMIT $2 OFFSET $3`,
			id, limit, offset,
		)
	} else {
		// Windowed path — nullable-bound predicate keeps partial bounds working
		// (OQ-3): when start is nil, $2::timestamptz IS NULL → TRUE → no lower
		// bound; same for end/$3. The planner still uses idx_gpu_metrics_gpu_id_ts
		// because gpu_id is the leading column and ORDER BY timestamp DESC matches
		// the index direction (API-03 / DB-02).
		// OFFSET $5 is bound as a pgx placeholder — never string-concatenated (T-06-03).
		rows, err = pool.Query(ctx,
			`SELECT `+cols+`
			 FROM gpu_metrics
			 WHERE gpu_id = $1
			   AND ($2::timestamptz IS NULL OR timestamp >= $2)
			   AND ($3::timestamptz IS NULL OR timestamp <= $3)
			 ORDER BY timestamp DESC, metric_name DESC
			 LIMIT $4 OFFSET $5`,
			id, start, end, limit, offset,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("db: Telemetry: query: %w", err)
	}
	defer rows.Close()
	return scanMetrics(rows)
}

// TelemetryAfter is the keyset (cursor) page fetch: rows strictly AFTER the
// cursor position in the (timestamp DESC, metric_name DESC) total order —
// pagination cost is O(page), not O(offset). OFFSET-based paging walks and
// discards every skipped index entry (OFFSET 1e6 touches a million entries
// per page); the keyset predicate seeks directly instead.
//
// The (timestamp, metric_name) pair is unique per gpu_id (natural key
// DB-04), so the order is total and pages are stable under concurrent
// ingest — new rows land ahead of an already-issued cursor, never inside
// earlier pages.
//
// The row-comparison predicate (timestamp, metric_name) < ($c_ts, $c_mn) is
// exactly "later in DESC order than the cursor row". The timestamp bound
// rides idx_gpu_metrics_gpu_id_ts; the metric_name tie-break only linearly
// scans within one identical timestamp (nanosecond restamping makes such
// ties rare and tiny).
//
// Security: all parameters are pgx-bound ($1..$6); no string concatenation;
// DSN never embedded in errors (ASVS V5, V8).
func TelemetryAfter(
	ctx context.Context,
	pool *pgxpool.Pool,
	id string,
	start, end *time.Time,
	cursorTS time.Time,
	cursorMetric string,
	limit int,
) ([]models.GpuMetric, error) {
	const cols = `gpu_id, timestamp, metric_name, value,
	              COALESCE(device, ''), COALESCE(model_name, ''), COALESCE(hostname, ''),
	              COALESCE(container, ''), COALESCE(pod, ''), COALESCE(namespace, ''),
	              COALESCE(labels_raw, '')`

	rows, err := pool.Query(ctx,
		`SELECT `+cols+`
		 FROM gpu_metrics
		 WHERE gpu_id = $1
		   AND ($2::timestamptz IS NULL OR timestamp >= $2)
		   AND ($3::timestamptz IS NULL OR timestamp <= $3)
		   AND (timestamp, metric_name) < ($4, $5)
		 ORDER BY timestamp DESC, metric_name DESC
		 LIMIT $6`,
		id, start, end, cursorTS, cursorMetric, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("db: TelemetryAfter: query: %w", err)
	}
	defer rows.Close()
	return scanMetrics(rows)
}

// scanMetrics drains rows into a non-nil slice (encodes as [] not null).
func scanMetrics(rows pgx.Rows) ([]models.GpuMetric, error) {
	result := make([]models.GpuMetric, 0)
	for rows.Next() {
		var m models.GpuMetric
		if scanErr := rows.Scan(
			&m.GpuID, &m.Timestamp, &m.MetricName, &m.Value,
			&m.Device, &m.ModelName, &m.Hostname,
			&m.Container, &m.Pod, &m.Namespace, &m.LabelsRaw,
		); scanErr != nil {
			return nil, fmt.Errorf("db: telemetry scan: %w", scanErr)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: telemetry rows: %w", err)
	}
	return result, nil
}
