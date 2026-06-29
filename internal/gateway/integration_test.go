//go:build integration

// Package gateway_test provides end-to-end integration tests for the API Gateway.
// Tests require Docker (testcontainers-go spins up postgres:17-alpine) and
// the Rancher Desktop socket.
//
// Run with:
//
//	DOCKER_HOST=unix://$HOME/.rd/docker.sock \
//	TESTCONTAINERS_RYUK_DISABLED=true \
//	go test -race -tags=integration ./internal/gateway/... -count=1
package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ajitg/vantage/internal/gateway"
	"github.com/ajitg/vantage/pkg/db"
	"github.com/ajitg/vantage/pkg/models"
)

// package-level vars shared across all tests via TestMain.
var (
	testPool *pgxpool.Pool
	testCtr  *postgres.PostgresContainer
)

// TestMain starts a single postgres:17-alpine container for the whole package,
// applies migrations, takes a Snapshot, and opens a shared pool.
//
// Pitfall-5 guard: username MUST be "postgres" and the database name MUST NOT
// be "postgres" for Snapshot/Restore to work correctly (testcontainers-go #2474).
func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("vantage_test"),
		postgres.WithUsername("postgres"), // MUST be "postgres" for Snapshot/Restore (Pitfall 5)
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

	if err := db.Migrate(ctx, dsn); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := ctr.Snapshot(ctx); err != nil {
		log.Fatalf("snapshot: %v", err)
	}

	pool, err := db.New(ctx, db.Config{DSN: dsn, MaxConns: 5})
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	testPool = pool
	defer pool.Close()

	os.Exit(m.Run())
}

// restoreDB restores the container to its post-migration snapshot and evicts
// terminated connections from the pool (same pattern as pkg/db/db_test.go).
func restoreDB(ctx context.Context, t *testing.T) {
	t.Helper()
	if err := testCtr.Restore(ctx); err != nil {
		t.Errorf("restore: %v", err)
	}
	// Force pgxpool to evict terminated connections. The error is expected.
	_ = testPool.Ping(context.Background())
}

// seedMetric inserts a single row into gpu_metrics using models.InsertSQL.
// Timestamps are nanosecond-spaced to avoid natural-key conflicts.
func seedMetric(t *testing.T, gpuID string, ts time.Time) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), models.InsertSQL,
		gpuID,
		ts.UTC(),
		"DCGM_FI_DEV_GPU_UTIL",
		float64(50),
		"nvidia0",
		"NVIDIA H100",
		"test-host",
		"",
		"",
		"",
		"",
	)
	require.NoError(t, err, "seedMetric: insert failed for gpu_id=%s", gpuID)
}

// TestListGPUs_TwoGPUs seeds three rows across two distinct gpu_id UUIDs and
// asserts that GET /api/v1/gpus returns 200 with a JSON array of exactly those
// two UUIDs, sorted ascending (API-01).
func TestListGPUs_TwoGPUs(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuA := "GPU-aaaaaaaa-0000-0000-0000-000000000000"
	gpuB := "GPU-bbbbbbbb-0000-0000-0000-000000000000"
	base := time.Now().UTC()

	// Three rows — two for gpuA (different timestamps), one for gpuB.
	seedMetric(t, gpuA, base)
	seedMetric(t, gpuA, base.Add(1*time.Microsecond))
	seedMetric(t, gpuB, base.Add(2*time.Microsecond))

	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(testPool, cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus", nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code, "expected 200")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var ids []string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&ids))
	require.Len(t, ids, 2, "must return exactly 2 distinct gpu_ids")
	assert.Equal(t, gpuA, ids[0], "UUIDs must be sorted ascending")
	assert.Equal(t, gpuB, ids[1], "UUIDs must be sorted ascending")
}

// TestListGPUs_Empty asserts that an empty gpu_metrics table returns
// HTTP 200 with a literal empty JSON array [] (not null, not 404).
func TestListGPUs_Empty(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(testPool, cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gpus", nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code, "empty table must return 200")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	body := w.Body.String()
	// Must be exactly "[]" (not "null") — non-nil empty slice encodes as [].
	assert.JSONEq(t, "[]", body, "empty table body must be []")
	fmt.Printf("empty body: %q\n", body) // diagnostic
}
