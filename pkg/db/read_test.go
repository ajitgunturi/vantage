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

	rows, err := db.Telemetry(ctx, testPool, gpuID, nil, nil, 100, 0)
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

	rows, err := db.Telemetry(ctx, testPool, gpuID, &t2, &t3, 100, 0)
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

	rows, err := db.Telemetry(ctx, testPool, gpuID, &futureStart, &futureEnd, 100, 0)
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

	rows, err := db.Telemetry(ctx, testPool, gpuID, nil, nil, 3, 0)
	require.NoError(t, err)
	assert.Len(t, rows, 3, "result must be capped at limit=3")
}

// ── TelemetryCount tests (pagination.total, ADR-010 amendment) ───────────────

// TestTelemetryCount_NoFilter seeds 3 rows and asserts the unbounded count.
func TestTelemetryCount_NoFilter(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-count-nofilter-00000000000001"
	base := time.Now().UTC().Truncate(time.Second)
	seedFull(t, gpuID, base)
	seedFull(t, gpuID, base.Add(1*time.Second))
	seedFull(t, gpuID, base.Add(2*time.Second))

	total, err := db.TelemetryCount(ctx, testPool, gpuID, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total, "unbounded count must include all 3 rows")
}

// TestTelemetryCount_WindowFilter seeds 4 rows and asserts the windowed count
// matches the same inclusive-bounds predicate as Telemetry.
func TestTelemetryCount_WindowFilter(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-count-window-0000000000000002"
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Minute)
	t1, t2, t3, t4 := base, base.Add(1*time.Minute), base.Add(2*time.Minute), base.Add(3*time.Minute)
	seedFull(t, gpuID, t1)
	seedFull(t, gpuID, t2)
	seedFull(t, gpuID, t3)
	seedFull(t, gpuID, t4)

	total, err := db.TelemetryCount(ctx, testPool, gpuID, &t2, &t3)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "window [t2,t3] must count exactly 2 rows")
}

// TestTelemetryCount_UnknownGPU asserts count 0 for a GPU with no rows.
func TestTelemetryCount_UnknownGPU(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	total, err := db.TelemetryCount(ctx, testPool, "GPU-count-none-ffffffffffffffffff", nil, nil)
	require.NoError(t, err)
	assert.Zero(t, total, "unknown GPU must count 0 rows")
}

// TestTelemetryCount_CancelledContext covers the error-return branch.
func TestTelemetryCount_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := db.TelemetryCount(ctx, testPool, "GPU-some-id", nil, nil)
	require.Error(t, err, "TelemetryCount with cancelled context must error")
	assert.Contains(t, err.Error(), "TelemetryCount",
		"error must carry the function name for diagnosability")
}

// ── Error-path coverage tests ────────────────────────────────────────────────
// The tests below use a pre-cancelled context to trigger the query-error
// branches in DistinctGPUIDs, GPUExists, and Telemetry. pgxpool returns
// context.Canceled immediately on pool.Query / pool.QueryRow when the context
// is already done, which exercises the "return nil, fmt.Errorf(...)" branches
// that are not reachable when queries succeed.

// TestDistinctGPUIDs_CancelledContext verifies that DistinctGPUIDs returns a
// wrapped "db: DistinctGPUIDs: query:" error when the context is cancelled
// before the query executes.
func TestDistinctGPUIDs_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel — pool.Query must fail immediately

	_, err := db.DistinctGPUIDs(ctx, testPool)
	require.Error(t, err, "DistinctGPUIDs with cancelled context must error")
	assert.Contains(t, err.Error(), "DistinctGPUIDs",
		"error must carry the function name for diagnosability")
}

// TestGPUExists_CancelledContext verifies that GPUExists returns a wrapped error
// when the context is cancelled before the query executes.
func TestGPUExists_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := db.GPUExists(ctx, testPool, "GPU-some-id")
	require.Error(t, err, "GPUExists with cancelled context must error")
	assert.Contains(t, err.Error(), "GPUExists",
		"error must carry the function name for diagnosability")
}

// TestTelemetry_CancelledContext verifies that Telemetry returns a wrapped
// "db: Telemetry: query:" error when the context is cancelled before the query.
func TestTelemetry_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := db.Telemetry(ctx, testPool, "GPU-some-id", nil, nil, 10, 0)
	require.Error(t, err, "Telemetry with cancelled context must error")
	assert.Contains(t, err.Error(), "Telemetry",
		"error must carry the function name for diagnosability")
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
	// gpu_metrics is range-partitioned (migration 000003): the parent index
	// idx_gpu_metrics_gpu_id_ts materializes per partition with generated
	// child names like gpu_metrics_p20260724_gpu_id_timestamp_idx — assert on
	// the inherited column suffix, plus partition pruning to a single day.
	assert.Contains(t, planStr, "_gpu_id_timestamp_idx",
		"expected a child of composite index idx_gpu_metrics_gpu_id_ts; full plan:\n%s", planStr)
	assert.Contains(t, planStr, "Index Scan",
		"expected Index Scan on composite index; full plan:\n%s", planStr)
	assert.NotContains(t, planStr, "Seq Scan",
		"sequential scan must not appear; full plan:\n%s", planStr)

	// Also call db.Telemetry to confirm the function exists (RED compile gate).
	_, err = db.Telemetry(ctx, testPool, targetGPU, &startT, &endT, 10, 0)
	require.NoError(t, err, "db.Telemetry must succeed on a seeded GPU")
}

// seedMetric inserts one row with an explicit metric name — for keyset
// tie-break tests where multiple metrics share one timestamp.
func seedMetric(t *testing.T, gpuID, metric string, ts time.Time) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), models.InsertSQL,
		gpuID, ts.UTC(), metric, float64(1),
		"nvidia0", "NVIDIA H100", "test-host", "", "", "", "",
	)
	require.NoError(t, err)
}

// TestTelemetryAfter_KeysetWalk pages through 7 rows (including two metrics
// sharing one timestamp — the tie the metric_name tiebreaker exists for)
// with page size 3, asserting pages are disjoint, ordered, and complete.
func TestTelemetryAfter_KeysetWalk(t *testing.T) {
	t.Cleanup(func() { restoreDB(context.Background(), t) })
	const gpu = "GPU-keyset-0000"
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// 5 distinct timestamps + one timestamp carrying TWO metrics (tie).
	for i := 0; i < 5; i++ {
		seedMetric(t, gpu, "DCGM_FI_DEV_GPU_UTIL", base.Add(time.Duration(i)*time.Second))
	}
	tieTS := base.Add(10 * time.Second)
	seedMetric(t, gpu, "DCGM_FI_DEV_MEM_COPY_UTIL", tieTS)
	seedMetric(t, gpu, "DCGM_FI_DEV_GPU_UTIL", tieTS)

	ctx := context.Background()
	// First page via the offset-free entry point (offset 0).
	page1, err := db.Telemetry(ctx, testPool, gpu, nil, nil, 3, 0)
	require.NoError(t, err)
	require.Len(t, page1, 3)

	var all []models.GpuMetric
	all = append(all, page1...)
	last := page1[len(page1)-1]
	for {
		page, err := db.TelemetryAfter(ctx, testPool, gpu, nil, nil, last.Timestamp, last.MetricName, 3)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		last = page[len(page)-1]
	}

	require.Len(t, all, 7, "keyset walk must visit every row exactly once")
	seen := map[string]bool{}
	for i, m := range all {
		key := m.Timestamp.Format(time.RFC3339Nano) + "/" + m.MetricName
		require.False(t, seen[key], "row %s must not repeat across pages", key)
		seen[key] = true
		if i > 0 {
			prev := all[i-1]
			require.False(t, m.Timestamp.After(prev.Timestamp), "timestamps must be non-increasing")
			if m.Timestamp.Equal(prev.Timestamp) {
				require.Less(t, m.MetricName, prev.MetricName, "ties break by metric_name DESC")
			}
		}
	}
	// The tied timestamp must be the newest and both its metrics adjacent.
	require.True(t, all[0].Timestamp.Equal(tieTS))
	require.True(t, all[1].Timestamp.Equal(tieTS))
	require.Equal(t, "DCGM_FI_DEV_MEM_COPY_UTIL", all[0].MetricName, "DESC tie-break: MEM > GPU alphabetically")
}

// TestTelemetryAfter_WindowFilter: the keyset predicate composes with the
// time-window bounds.
func TestTelemetryAfter_WindowFilter(t *testing.T) {
	t.Cleanup(func() { restoreDB(context.Background(), t) })
	const gpu = "GPU-keyset-win"
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		seedMetric(t, gpu, "DCGM_FI_DEV_GPU_UTIL", base.Add(time.Duration(i)*time.Minute))
	}
	start := base.Add(1 * time.Minute)
	end := base.Add(3 * time.Minute)

	// Cursor at the newest in-window row → remaining in-window rows only.
	page, err := db.TelemetryAfter(context.Background(), testPool, gpu, &start, &end,
		base.Add(3*time.Minute), "DCGM_FI_DEV_GPU_UTIL", 10)
	require.NoError(t, err)
	require.Len(t, page, 2, "only the older in-window rows remain after the cursor")
	assert.True(t, page[0].Timestamp.Equal(base.Add(2*time.Minute)))
	assert.True(t, page[1].Timestamp.Equal(base.Add(1*time.Minute)))
}
