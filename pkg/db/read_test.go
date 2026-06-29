//go:build integration

// Package db_test — read query integration tests.
// These tests verify DistinctGPUIDs and Telemetry against a real Postgres instance.
// They share the TestMain defined in db_test.go (same build tag, same package).
//
// Run with:
//
//	DOCKER_HOST=unix://$HOME/.rd/docker.sock \
//	TESTCONTAINERS_RYUK_DISABLED=true \
//	go test -race -tags=integration -run 'TestDistinctGPUIDs|TestTelemetry' ./pkg/db/... -count=1
package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/pkg/db"
	"github.com/ajitg/vantage/pkg/models"
)

// seedGPU inserts a single gpu_metrics row for the given gpuID.
// Uses models.InsertSQL to stay aligned with the Collector write path.
func seedGPU(t *testing.T, gpuID string, ts time.Time) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), models.InsertSQL,
		gpuID,
		ts.UTC(),
		"DCGM_FI_DEV_GPU_UTIL",
		float64(42),
		"nvidia0",
		"NVIDIA H100",
		"test-host",
		"", "", "", "",
	)
	require.NoError(t, err, "seedGPU: insert failed for gpu_id=%s", gpuID)
}

// TestDistinctGPUIDs_TwoGPUs seeds three rows (two UUIDs) and asserts
// that DistinctGPUIDs returns de-duplicated, sorted UUIDs.
func TestDistinctGPUIDs_TwoGPUs(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuA := "GPU-aaaaaaaa-0000-0000-0000-000000000001"
	gpuB := "GPU-bbbbbbbb-0000-0000-0000-000000000002"
	base := time.Now().UTC()

	seedGPU(t, gpuA, base)
	seedGPU(t, gpuA, base.Add(time.Microsecond)) // duplicate uuid, different ts
	seedGPU(t, gpuB, base.Add(2*time.Microsecond))

	ids, err := db.DistinctGPUIDs(ctx, testPool)
	require.NoError(t, err)
	require.Len(t, ids, 2, "must return exactly 2 distinct gpu_ids")
	assert.Equal(t, gpuA, ids[0], "must be sorted ascending")
	assert.Equal(t, gpuB, ids[1], "must be sorted ascending")
}

// TestDistinctGPUIDs_Empty asserts that an empty table returns a non-nil
// empty slice (encodes as [] not null).
func TestDistinctGPUIDs_Empty(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	ids, err := db.DistinctGPUIDs(ctx, testPool)
	require.NoError(t, err)
	require.NotNil(t, ids, "empty result must be non-nil (must encode as [] not null)")
	assert.Len(t, ids, 0)
}

// ── Telemetry read tests (API-02, API-03) ────────────────────────────────────

// seedFull inserts a row with all columns populated (non-empty strings for
// nullable text fields). Uses the same column order as models.InsertSQL.
func seedFull(t *testing.T, gpuID string, ts time.Time) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), models.InsertSQL,
		gpuID, ts.UTC(),
		"DCGM_FI_DEV_GPU_UTIL", float64(42.5),
		"nvidia0", "NVIDIA H100", "test-host",
		"", "", "", "",
	)
	require.NoError(t, err, "seedFull: insert failed for gpu_id=%s", gpuID)
}

// TestGPUExists_True asserts that GPUExists returns true for a seeded GPU.
func TestGPUExists_True(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-exists-test-0000-0000-000000000001"
	seedFull(t, gpuID, time.Now().UTC())

	exists, err := db.GPUExists(ctx, testPool, gpuID)
	require.NoError(t, err)
	assert.True(t, exists, "GPUExists must return true for a seeded GPU")
}

// TestGPUExists_False asserts that GPUExists returns false for an unknown GPU.
func TestGPUExists_False(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	exists, err := db.GPUExists(ctx, testPool, "GPU-does-not-exist-ffff")
	require.NoError(t, err)
	assert.False(t, exists, "GPUExists must return false for an unknown GPU")
}

// TestTelemetry_NoFilter seeds 3 rows and asserts that Telemetry returns all
// rows ordered timestamp DESC with a non-nil result (API-02).
func TestTelemetry_NoFilter(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-telemetry-nofilter-000000000001"
	base := time.Now().UTC().Truncate(time.Second)

	seedFull(t, gpuID, base)
	seedFull(t, gpuID, base.Add(1*time.Second))
	seedFull(t, gpuID, base.Add(2*time.Second))

	rows, err := db.Telemetry(ctx, testPool, gpuID, nil, nil, 100)
	require.NoError(t, err)
	require.Len(t, rows, 3, "must return all 3 rows")

	// Rows must be ordered timestamp DESC.
	assert.True(t, rows[0].Timestamp.After(rows[1].Timestamp),
		"row[0] must be newer than row[1]")
	assert.True(t, rows[1].Timestamp.After(rows[2].Timestamp),
		"row[1] must be newer than row[2]")
}

// TestTelemetry_WindowFilter seeds 4 rows and asserts that a start/end window
// returns only the rows within the inclusive bounds (API-03).
func TestTelemetry_WindowFilter(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-telemetry-window-0000000000002"
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Minute)

	t1 := base
	t2 := base.Add(1 * time.Minute)
	t3 := base.Add(2 * time.Minute)
	t4 := base.Add(3 * time.Minute)

	seedFull(t, gpuID, t1)
	seedFull(t, gpuID, t2)
	seedFull(t, gpuID, t3)
	seedFull(t, gpuID, t4)

	rows, err := db.Telemetry(ctx, testPool, gpuID, &t2, &t3, 100)
	require.NoError(t, err)
	require.Len(t, rows, 2, "window [t2,t3] must return exactly 2 rows")
}

// TestTelemetry_EmptyResult asserts that a known GPU with no rows in the
// requested window returns a non-nil empty slice (encodes as [] not null).
func TestTelemetry_EmptyResult(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-telemetry-empty-00000000000003"
	seedFull(t, gpuID, time.Now().UTC().Add(-24*time.Hour))

	futureStart := time.Now().UTC().Add(24 * time.Hour)
	futureEnd := time.Now().UTC().Add(48 * time.Hour)

	rows, err := db.Telemetry(ctx, testPool, gpuID, &futureStart, &futureEnd, 100)
	require.NoError(t, err)
	require.NotNil(t, rows, "empty result must be non-nil (encodes as [] not null)")
	assert.Len(t, rows, 0)
}

// TestTelemetry_Limit asserts OQ-1: result count is capped at the given limit.
func TestTelemetry_Limit(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-telemetry-limit-00000000000004"
	base := time.Now().UTC().Truncate(time.Second)

	for i := range 5 {
		seedFull(t, gpuID, base.Add(time.Duration(i)*time.Second))
	}

	rows, err := db.Telemetry(ctx, testPool, gpuID, nil, nil, 3)
	require.NoError(t, err)
	assert.Len(t, rows, 3, "result must be capped at limit=3")
}

// TestTelemetry_UsesCompositeIndex verifies DB-02 / API-03: the windowed
// telemetry query uses idx_gpu_metrics_gpu_id_ts (composite index on
// gpu_id, timestamp DESC). Seeds 100k rows + ANALYZE so the planner
// selects the index without tricks.
func TestTelemetry_UsesCompositeIndex(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	// 100k rows ensures planner statistics reflect real volume.
	require.NoError(t, seedRows(ctx, testPool, 100_000), "seed 100k rows")

	_, err := testPool.Exec(ctx, "ANALYZE gpu_metrics")
	require.NoError(t, err, "ANALYZE must succeed after seed")

	// EXPLAIN the windowed query that db.Telemetry uses for the bounded case.
	// Using actual time values (non-NULL) so the IS NULL predicates short-circuit
	// to false and the planner sees the full range predicate.
	const explainQ = `EXPLAIN (FORMAT TEXT)
		SELECT gpu_id, timestamp, metric_name, value,
		       device, model_name, hostname, container, pod, namespace, labels_raw
		FROM gpu_metrics
		WHERE gpu_id = $1
		  AND ($2::timestamptz IS NULL OR timestamp >= $2)
		  AND ($3::timestamptz IS NULL OR timestamp <= $3)
		ORDER BY timestamp DESC
		LIMIT $4`

	// GPU-0000 has rows seeded by seedRows (gpuIdx=0).
	targetGPU := "GPU-0000-0000-0000-0000-000000000000"
	endT := time.Now().UTC()
	startT := endT.Add(-1 * time.Hour)

	explainRows, err := testPool.Query(ctx, explainQ, targetGPU, startT, endT, 1000)
	require.NoError(t, err)
	defer explainRows.Close()

	var plan strings.Builder
	for explainRows.Next() {
		var line string
		require.NoError(t, explainRows.Scan(&line))
		plan.WriteString(line + "\n")
	}
	require.NoError(t, explainRows.Err())

	planStr := plan.String()
	assert.Contains(t, planStr, "idx_gpu_metrics_gpu_id_ts",
		"expected composite index idx_gpu_metrics_gpu_id_ts; full plan:\n%s", planStr)
	assert.Contains(t, planStr, "Index Scan",
		"expected Index Scan on composite index; full plan:\n%s", planStr)

	// Also call db.Telemetry to confirm the function exists (RED compile gate).
	_, err = db.Telemetry(ctx, testPool, targetGPU, &startT, &endT, 10)
	require.NoError(t, err, "db.Telemetry must succeed on a seeded GPU")
}
