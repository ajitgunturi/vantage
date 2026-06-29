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

// Telemetry returns the metric rows for the given gpu_id ordered by timestamp
// DESC, capped at limit rows. start and end are optional inclusive RFC3339
// time bounds (nil = unbounded in that direction; OQ-3 partial bounds).
//
// Design — two-query approach (RESEARCH Pattern 5 / A1):
//   - No bounds: simpler query without any IS NULL predicate; index on
//     (gpu_id, timestamp DESC) is used without the OR branch overhead.
//   - At least one bound: nullable-bound predicate so the planner can still
//     use the composite index for both full and partial windows (API-03).
//
// Returns a non-nil empty slice when no rows match (encodes as [] not null).
//
// Security: all parameters are pgx-bound ($1..$4); no string concatenation;
// DSN never embedded in errors (T-04-03, T-04-04, T-04-06 / ASVS V5, V8).
// Telemetry returns the metric rows for the given gpu_id ordered by timestamp
// DESC, capped at limit rows. start and end are optional inclusive RFC3339
// time bounds (nil = unbounded in that direction; OQ-3 partial bounds).
//
// Design — two-query approach (RESEARCH Pattern 5 / A1):
//   - No bounds: simpler query without any IS NULL predicate; index on
//     (gpu_id, timestamp DESC) is used without the OR branch overhead.
//   - At least one bound: nullable-bound predicate so the planner can still
//     use the composite index for both full and partial windows (API-03).
//
// Returns a non-nil empty slice when no rows match (encodes as [] not null).
//
// Security: all parameters are pgx-bound ($1..$4); no string concatenation;
// DSN never embedded in errors (T-04-03, T-04-04, T-04-06 / ASVS V5, V8).
func Telemetry(
	ctx context.Context,
	pool *pgxpool.Pool,
	id string,
	start, end *time.Time,
	limit int,
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
		rows, err = pool.Query(ctx,
			`SELECT `+cols+`
			 FROM gpu_metrics
			 WHERE gpu_id = $1
			 ORDER BY timestamp DESC
			 LIMIT $2`,
			id, limit,
		)
	} else {
		// Windowed path — nullable-bound predicate keeps partial bounds working
		// (OQ-3): when start is nil, $2::timestamptz IS NULL → TRUE → no lower
		// bound; same for end/$3. The planner still uses idx_gpu_metrics_gpu_id_ts
		// because gpu_id is the leading column and ORDER BY timestamp DESC matches
		// the index direction (API-03 / DB-02).
		rows, err = pool.Query(ctx,
			`SELECT `+cols+`
			 FROM gpu_metrics
			 WHERE gpu_id = $1
			   AND ($2::timestamptz IS NULL OR timestamp >= $2)
			   AND ($3::timestamptz IS NULL OR timestamp <= $3)
			 ORDER BY timestamp DESC
			 LIMIT $4`,
			id, start, end, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("db: Telemetry: query: %w", err)
	}
	defer rows.Close()

	result := make([]models.GpuMetric, 0)
	for rows.Next() {
		var m models.GpuMetric
		if scanErr := rows.Scan(
			&m.GpuID, &m.Timestamp, &m.MetricName, &m.Value,
			&m.Device, &m.ModelName, &m.Hostname,
			&m.Container, &m.Pod, &m.Namespace, &m.LabelsRaw,
		); scanErr != nil {
			return nil, fmt.Errorf("db: Telemetry: scan: %w", scanErr)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: Telemetry: rows: %w", err)
	}
	return result, nil
}
