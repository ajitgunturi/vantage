package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
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
