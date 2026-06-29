//go:build integration

// Package db_test holds black-box integration tests for pkg/db.
// All tests require Docker (testcontainers-go spins up postgres:17-alpine).
// Run with: go test -race -tags=integration ./pkg/db/... -count=1
package db_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ajitg/vantage/pkg/db"
)

var (
	testPool *pgxpool.Pool
	testCtr  *postgres.PostgresContainer
)

// TestMain starts a single postgres:17-alpine container for the whole package,
// runs migrations to establish the schema, takes a Snapshot, then opens a
// shared pool. Each individual test restores the DB to that snapshot via
// t.Cleanup so tests are isolated without paying the cost of a new container.
//
// Pitfall-5 guard: username MUST be "postgres" and the database name MUST NOT
// be "postgres" for Snapshot/Restore to work correctly (testcontainers-go #2474).
// Pitfall-6 guard: pool startup ping uses a 5-second context timeout.
func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("vantage_test"),
		postgres.WithUsername("postgres"), // must be "postgres" for Snapshot/Restore (Pitfall 5)
		postgres.WithPassword("secret"),
		postgres.BasicWaitStrategies(),
		postgres.WithSQLDriver("pgx"), // required for Snapshot/Restore
	)
	if err != nil {
		log.Fatalf("start postgres: %v", err)
	}
	defer testcontainers.TerminateContainer(ctr) //nolint:errcheck
	testCtr = ctr

	dsn := ctr.MustConnectionString(ctx, "sslmode=disable")

	// Apply migrations on the fresh container before snapshotting.
	if err := db.Migrate(ctx, dsn); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// Capture snapshot AFTER migrations; each test restores to this clean state.
	if err := ctr.Snapshot(ctx); err != nil {
		log.Fatalf("snapshot: %v", err)
	}

	// Open the shared pool with a 5s timeout on the startup Ping (Pitfall 6).
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	pool, err := db.New(pingCtx, db.Config{DSN: dsn, MaxConns: 5})
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	testPool = pool
	defer pool.Close()

	os.Exit(m.Run())
}

// seedRows bulk-inserts n rows into gpu_metrics across >=50 distinct gpu_id values
// and the 10 known DCGM metric_name values. Timestamps are spread over a 24-hour
// window ending now, ensuring sub-second-distinct rows for the natural-key constraint.
// All SQL is parameterized ($1, $2, ...) — no string-built queries.
func seedRows(ctx context.Context, pool *pgxpool.Pool, n int) error {
	const gpuCount = 50
	metricNames := []string{
		"DCGM_FI_DEV_GPU_UTIL",
		"DCGM_FI_DEV_MEM_COPY_UTIL",
		"DCGM_FI_DEV_ENC_UTIL",
		"DCGM_FI_DEV_DEC_UTIL",
		"DCGM_FI_DEV_FB_FREE",
		"DCGM_FI_DEV_FB_USED",
		"DCGM_FI_DEV_SM_CLOCK",
		"DCGM_FI_DEV_MEM_CLOCK",
		"DCGM_FI_DEV_POWER_USAGE",
		"DCGM_FI_DEV_GPU_TEMP",
	}

	// Spread n rows evenly over 24 hours. Each step is 864ms for n=100k,
	// well above TIMESTAMPTZ microsecond precision — all timestamps are distinct.
	base := time.Now().UTC().Add(-24 * time.Hour)
	step := (24 * time.Hour) / time.Duration(n)

	const insertSQL = `
		INSERT INTO gpu_metrics (gpu_id, timestamp, metric_name, value)
		VALUES ($1, $2, $3, $4)`

	for i := 0; i < n; i++ {
		// Distribute rows so each (gpu_id, metric_name) pair gets n/(gpuCount*metrics) unique timestamps.
		// gpuIdx cycles slowly (changes every len(metricNames) rows) so each GPU
		// accumulates a spread of timestamps across all metric_name values.
		gpuIdx := (i / len(metricNames)) % gpuCount
		gpuID := fmt.Sprintf("GPU-%04d-0000-0000-0000-000000000000", gpuIdx)
		metricName := metricNames[i%len(metricNames)]
		ts := base.Add(time.Duration(i) * step)
		value := float64(i%100) + 0.5

		if _, err := pool.Exec(ctx, insertSQL, gpuID, ts, metricName, value); err != nil {
			return fmt.Errorf("seed row %d: %w", i, err)
		}
	}
	return nil
}

// restoreDB restores the container to its post-migration snapshot and then
// flushes any terminated connections from the pool. This flush is intentional:
// Restore() calls pg_terminate_backend() on all connections, which marks them
// as dead. Pinging immediately after evicts those dead connections so the next
// test starts with a fresh one. The Ping error is discarded — it is expected.
func restoreDB(ctx context.Context, t *testing.T) {
	t.Helper()
	if err := testCtr.Restore(ctx); err != nil {
		t.Errorf("restore: %v", err)
	}
	// Force pgxpool to evict terminated connections. The error is expected.
	_ = testPool.Ping(context.Background())
}

// TestMigration verifies DB-01: after db.Migrate runs, the gpu_metrics table
// exists in the public schema with the expected identity and metric columns.
func TestMigration(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	const q = `SELECT column_name FROM information_schema.columns
	           WHERE table_schema = 'public' AND table_name = 'gpu_metrics'
	           ORDER BY ordinal_position`

	rows, err := testPool.Query(ctx, q)
	require.NoError(t, err, "query information_schema.columns for gpu_metrics")
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		require.NoError(t, rows.Scan(&col))
		cols = append(cols, col)
	}
	require.NoError(t, rows.Err())

	require.NotEmpty(t, cols, "gpu_metrics table must exist (migration was applied)")
	require.Contains(t, cols, "gpu_id", "gpu_metrics must have gpu_id column")
	require.Contains(t, cols, "timestamp", "gpu_metrics must have timestamp column")
	require.Contains(t, cols, "metric_name", "gpu_metrics must have metric_name column")
	require.Contains(t, cols, "value", "gpu_metrics must have value column")
}

// TestNew verifies DB-03: db.New returns a non-nil pool whose Ping succeeds.
func TestNew(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, testPool.Ping(pingCtx), "shared pool must be healthy and pingable")
}

// TestUniqueConstraint verifies DB-04: inserting a duplicate (gpu_id, metric_name,
// timestamp) violates uq_gpu_metrics_natural_key and produces an error referencing
// the constraint name.
func TestUniqueConstraint(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	const insertSQL = `INSERT INTO gpu_metrics (gpu_id, timestamp, metric_name, value)
	                   VALUES ($1, $2, $3, $4)`

	gpuID := "GPU-dup-test-0000-0000-0000-000000000000"
	ts := time.Now().UTC()
	metric := "DCGM_FI_DEV_GPU_UTIL"

	// First insert succeeds.
	_, err := testPool.Exec(ctx, insertSQL, gpuID, ts, metric, 42.0)
	require.NoError(t, err, "first insert must succeed")

	// Second insert with identical (gpu_id, metric_name, timestamp) must fail.
	_, err = testPool.Exec(ctx, insertSQL, gpuID, ts, metric, 99.0)
	require.Error(t, err, "duplicate (gpu_id, metric_name, timestamp) must violate unique constraint")
	require.Contains(t, err.Error(), "uq_gpu_metrics_natural_key",
		"error must reference the named unique constraint uq_gpu_metrics_natural_key")
}

// TestCompositeIndexUsed verifies DB-02: after seeding 100k rows and running
// ANALYZE, an EXPLAIN on a selective single-gpu 1-hour range query shows
// "Index Scan" (not "Seq Scan") — proving the planner uses the composite index
// (gpu_id, timestamp DESC) at representative scale without enable_seqscan tricks.
func TestCompositeIndexUsed(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	// Seed 100k rows so planner statistics reflect real data volume.
	require.NoError(t, seedRows(ctx, testPool, 100_000), "seed 100k rows")

	// ANALYZE ensures pg_class.reltuples reflects the seeded count; without it
	// the planner underestimates rows and defaults to Seq Scan.
	_, err := testPool.Exec(ctx, "ANALYZE gpu_metrics")
	require.NoError(t, err, "ANALYZE must succeed after seed")

	// Selective query: one GPU over a 1-hour window ordered timestamp DESC.
	// The composite index (gpu_id, timestamp DESC) covers this pattern exactly.
	const explainQ = `EXPLAIN (FORMAT TEXT)
		SELECT * FROM gpu_metrics
		WHERE gpu_id = $1
		  AND timestamp >= $2
		  AND timestamp < $3
		ORDER BY timestamp DESC`

	// GPU-0000 exists in the seeded data (gpu_idx=0 → "GPU-0000-...").
	targetGPU := "GPU-0000-0000-0000-0000-000000000000"
	end := time.Now().UTC()
	start := end.Add(-1 * time.Hour)

	rows, err := testPool.Query(ctx, explainQ, targetGPU, start, end)
	require.NoError(t, err)
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line + "\n")
	}
	require.NoError(t, rows.Err())

	planStr := plan.String()
	// Use assert (non-fatal) so the full plan is printed on failure.
	assert.Contains(t, planStr, "Index Scan",
		"expected composite index (gpu_id, timestamp DESC) to be used; full plan:\n%s", planStr)
	assert.NotContains(t, planStr, "Seq Scan on gpu_metrics",
		"sequential scan on gpu_metrics must not appear in plan; full plan:\n%s", planStr)
}
