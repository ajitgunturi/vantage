//go:build integration

package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/pkg/models"
)

// TestGpuMetricsIsPartitioned: migration 000003 leaves gpu_metrics as a
// declarative range-partitioned table with a DEFAULT partition and the
// pre-created daily window.
func TestGpuMetricsIsPartitioned(t *testing.T) {
	ctx := context.Background()

	var relkind string
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT relkind FROM pg_class WHERE relname = 'gpu_metrics'").Scan(&relkind))
	require.Equal(t, "p", relkind, "gpu_metrics must be a partitioned parent")

	var partitions int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM pg_inherits WHERE inhparent = 'gpu_metrics'::regclass`).Scan(&partitions))
	// DEFAULT + at least yesterday..today+3 (5 dailies).
	require.GreaterOrEqual(t, partitions, 6, "default + pre-created daily window")
}

// TestPartitionRouting: a live-restamped row lands in TODAY's daily partition;
// a row far outside the pre-created window lands in the DEFAULT partition
// (safety net — no insert failure).
func TestPartitionRouting(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	now := time.Now().UTC()
	_, err := testPool.Exec(ctx, models.InsertSQL,
		"GPU-part-now", now, "DCGM_FI_DEV_GPU_UTIL", 1.0, "", "", "", "", "", "", "")
	require.NoError(t, err)

	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = testPool.Exec(ctx, models.InsertSQL,
		"GPU-part-old", old, "DCGM_FI_DEV_GPU_UTIL", 1.0, "", "", "", "", "", "", "")
	require.NoError(t, err, "out-of-window rows must not fail — DEFAULT partition catches them")

	var nowPart, oldPart string
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT tableoid::regclass::text FROM gpu_metrics WHERE gpu_id = 'GPU-part-now'").Scan(&nowPart))
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT tableoid::regclass::text FROM gpu_metrics WHERE gpu_id = 'GPU-part-old'").Scan(&oldPart))

	assert.Equal(t, "gpu_metrics_p"+now.Format("20060102"), nowPart,
		"live rows route to today's daily partition")
	assert.Equal(t, "gpu_metrics_default", oldPart,
		"stray old rows route to the default partition")
}

// TestEnsurePartitionsIdempotent: a second call creates nothing new.
func TestEnsurePartitionsIdempotent(t *testing.T) {
	ctx := context.Background()
	var created int
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT gpu_metrics_ensure_partitions(3)").Scan(&created))
	assert.Equal(t, 0, created, "window already exists — idempotent re-run")
}

// TestDropOldPartitions: an old daily partition is dropped whole (O(1)); rows
// inside the retention window survive; the guard rejects retention < 1 day.
func TestDropOldPartitions(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	// Fabricate an ancient daily partition with one row in it.
	oldDay := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)
	part := "gpu_metrics_p" + oldDay.Format("20060102")
	_, err := testPool.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s PARTITION OF gpu_metrics FOR VALUES FROM ('2020-06-01') TO ('2020-06-02')", part))
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, models.InsertSQL,
		"GPU-ancient", oldDay.Add(time.Hour), "DCGM_FI_DEV_GPU_UTIL", 1.0, "", "", "", "", "", "", "")
	require.NoError(t, err)

	// A fresh row inside the window must survive retention.
	_, err = testPool.Exec(ctx, models.InsertSQL,
		"GPU-fresh", time.Now().UTC(), "DCGM_FI_DEV_GPU_UTIL", 1.0, "", "", "", "", "", "", "")
	require.NoError(t, err)

	var dropped int
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT gpu_metrics_drop_old_partitions(14)").Scan(&dropped))
	require.Equal(t, 1, dropped, "exactly the ancient partition must drop")

	var exists bool
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT to_regclass($1) IS NOT NULL", part).Scan(&exists))
	assert.False(t, exists, "dropped partition must be gone")

	var fresh int
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT count(*) FROM gpu_metrics WHERE gpu_id = 'GPU-fresh'").Scan(&fresh))
	assert.Equal(t, 1, fresh, "in-window rows must survive retention")

	var ancient int
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT count(*) FROM gpu_metrics WHERE gpu_id = 'GPU-ancient'").Scan(&ancient))
	assert.Equal(t, 0, ancient, "dropped partition's rows are gone with it")

	// Guard: nonsense retention is refused.
	_, err = testPool.Exec(ctx, "SELECT gpu_metrics_drop_old_partitions(0)")
	require.Error(t, err, "retention_days < 1 must be rejected")
}
