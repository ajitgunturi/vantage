//go:build integration

// Package db_test — read query integration tests.
// These tests verify DistinctGPUIDs against a real Postgres instance.
// They share the TestMain defined in db_test.go (same build tag, same package).
//
// Run with:
//
//	DOCKER_HOST=unix://$HOME/.rd/docker.sock \
//	TESTCONTAINERS_RYUK_DISABLED=true \
//	go test -race -tags=integration -run 'TestDistinctGPUIDs' ./pkg/db/... -count=1
package db_test

import (
	"context"
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
