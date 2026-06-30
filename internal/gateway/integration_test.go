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
	"net/url"
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

// ── Telemetry endpoint tests (API-02, API-03, OQ-1..4) ──────────────────────

// telemetryPath builds the URL path for GET /api/v1/gpus/{id}/telemetry with
// optional query parameters encoded correctly for httptest.
func telemetryPath(gpuID string, params url.Values) string {
	base := fmt.Sprintf("/api/v1/gpus/%s/telemetry", url.PathEscape(gpuID))
	if len(params) == 0 {
		return base
	}
	return base + "?" + params.Encode()
}

// decodeMetrics decodes a []gateway.GpuMetricResponse from the recorder body.
func decodeMetrics(t *testing.T, w *httptest.ResponseRecorder) []gateway.GpuMetricResponse {
	t.Helper()
	var rows []gateway.GpuMetricResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rows),
		"response body must be a valid JSON array of GpuMetricResponse")
	return rows
}

// TestGetTelemetry_NoFilter seeds 3 rows for a GPU and asserts that
// GET /api/v1/gpus/{id}/telemetry returns 200 with all rows ordered
// by timestamp DESC (API-02).
func TestGetTelemetry_NoFilter(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-aaaaaaaa-0000-0000-0000-000000000011"
	base := time.Now().UTC().Truncate(time.Second) // whole-second for RFC3339 round-trip

	// Seed 3 rows with distinct 1-second-apart timestamps.
	seedMetric(t, gpuID, base)
	seedMetric(t, gpuID, base.Add(1*time.Second))
	seedMetric(t, gpuID, base.Add(2*time.Second))

	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(testPool, cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, telemetryPath(gpuID, nil), nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code, "expected 200")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	rows := decodeMetrics(t, w)
	require.Len(t, rows, 3, "must return exactly 3 rows")

	// Rows must be ordered timestamp DESC — newest first.
	assert.True(t, rows[0].Timestamp.After(rows[1].Timestamp),
		"row[0] must be newer than row[1] (DESC order)")
	assert.True(t, rows[1].Timestamp.After(rows[2].Timestamp),
		"row[1] must be newer than row[2] (DESC order)")

	// All rows must belong to the requested GPU.
	for i, row := range rows {
		assert.Equal(t, gpuID, row.GpuID, "row[%d] must have correct gpu_id", i)
	}
}

// TestGetTelemetry_TimeWindow seeds rows at t1 < t2 < t3 < t4 and asserts that
// ?start_time=t2&end_time=t3 returns exactly the two in-range rows (API-03).
func TestGetTelemetry_TimeWindow(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-bbbbbbbb-0000-0000-0000-000000000022"
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Minute)

	t1 := base
	t2 := base.Add(1 * time.Minute)
	t3 := base.Add(2 * time.Minute)
	t4 := base.Add(3 * time.Minute)

	seedMetric(t, gpuID, t1)
	seedMetric(t, gpuID, t2)
	seedMetric(t, gpuID, t3)
	seedMetric(t, gpuID, t4)

	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(testPool, cfg)

	q := url.Values{}
	q.Set("start_time", t2.Format(time.RFC3339))
	q.Set("end_time", t3.Format(time.RFC3339))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, telemetryPath(gpuID, q), nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code, "expected 200 for windowed query")

	rows := decodeMetrics(t, w)
	require.Len(t, rows, 2, "window [t2,t3] must return exactly 2 rows")
	// First row must be t3 (newest, DESC order).
	assert.True(t, rows[0].Timestamp.Equal(t3) || rows[0].Timestamp.After(t2),
		"first row must be at or after start_time, ordered DESC")
}

// TestGetTelemetry_PartialBounds tests OQ-3: partial time bounds are accepted.
// Sub-case A: only start_time (unbounded upper) — returns rows at or after start.
// Sub-case B: only end_time (unbounded lower) — returns rows at or before end.
func TestGetTelemetry_PartialBounds(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-cccccccc-0000-0000-0000-000000000033"
	base := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Minute)

	t1 := base
	t2 := base.Add(1 * time.Minute)
	t3 := base.Add(2 * time.Minute)

	seedMetric(t, gpuID, t1)
	seedMetric(t, gpuID, t2)
	seedMetric(t, gpuID, t3)

	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(testPool, cfg)

	t.Run("only_start_time", func(t *testing.T) {
		q := url.Values{}
		q.Set("start_time", t2.Format(time.RFC3339)) // unbounded upper
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, telemetryPath(gpuID, q), nil)
		router.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		rows := decodeMetrics(t, w)
		require.Len(t, rows, 2, "start_time=t2, no end → must return t2 and t3")
	})

	t.Run("only_end_time", func(t *testing.T) {
		q := url.Values{}
		q.Set("end_time", t2.Format(time.RFC3339)) // unbounded lower
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, telemetryPath(gpuID, q), nil)
		router.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		rows := decodeMetrics(t, w)
		require.Len(t, rows, 2, "end_time=t2, no start → must return t1 and t2")
	})
}

// TestGetTelemetry_UnknownGPU asserts OQ-2: a gpu_id absent from the table
// returns 404 with an ErrorResponse JSON body.
func TestGetTelemetry_UnknownGPU(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	// Table is empty after restore — no GPU exists.
	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(testPool, cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		telemetryPath("GPU-does-not-exist-00000000-ffff", nil), nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusNotFound, w.Code, "unknown gpu_id must return 404")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var errResp gateway.ErrorResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.NotEmpty(t, errResp.Error)
}

// TestGetTelemetry_KnownGPUEmptyWindow asserts OQ-2: a known gpu_id with no
// rows matching the requested time window returns 200 with an empty array []
// (not 404, not null).
func TestGetTelemetry_KnownGPUEmptyWindow(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-dddddddd-0000-0000-0000-000000000044"
	// Seed one row at a past time.
	pastTime := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	seedMetric(t, gpuID, pastTime)

	// Query a window far in the future — no rows will match.
	futureStart := time.Now().UTC().Add(24 * time.Hour)
	futureEnd := time.Now().UTC().Add(48 * time.Hour)

	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(testPool, cfg)

	q := url.Values{}
	q.Set("start_time", futureStart.Format(time.RFC3339))
	q.Set("end_time", futureEnd.Format(time.RFC3339))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, telemetryPath(gpuID, q), nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"known GPU with empty window must return 200 (not 404)")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	// Capture body before decoding — json.Decoder advances the buffer.
	body := w.Body.String()
	var rows []gateway.GpuMetricResponse
	require.NoError(t, json.Unmarshal([]byte(body), &rows),
		"empty-window body must be valid JSON")
	assert.Len(t, rows, 0, "empty window must return [] (not null)")
	assert.JSONEq(t, "[]", body, "empty-window body must be exactly []")
}

// TestGetTelemetry_BadTime asserts OQ-4: a malformed start_time parameter
// returns 400 with an ErrorResponse body (via the integration-test container).
func TestGetTelemetry_BadTime(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	cfg := gateway.Config{Addr: ":8080", MaxRows: 1000}
	router := gateway.NewRouter(testPool, cfg)

	// Use a raw path so the bad param is not URL-encoded to something valid.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/api/v1/gpus/GPU-test/telemetry?start_time=2024-99-99T00:00:00Z", nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "malformed start_time must return 400")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var errResp gateway.ErrorResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.NotEmpty(t, errResp.Error)
}

// TestGetTelemetry_ResultCap asserts OQ-1: result count is capped at MaxRows
// even when more rows exist. Uses a config with MaxRows: 2 and seeds 3 rows.
func TestGetTelemetry_ResultCap(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	gpuID := "GPU-eeeeeeee-0000-0000-0000-000000000055"
	base := time.Now().UTC().Truncate(time.Second)

	seedMetric(t, gpuID, base)
	seedMetric(t, gpuID, base.Add(1*time.Second))
	seedMetric(t, gpuID, base.Add(2*time.Second))

	// Deliberately small cap to verify enforcement (OQ-1).
	cfg := gateway.Config{Addr: ":8080", MaxRows: 2}
	router := gateway.NewRouter(testPool, cfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, telemetryPath(gpuID, nil), nil)
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	rows := decodeMetrics(t, w)
	assert.Len(t, rows, 2,
		"result must be capped at MaxRows=2 even though 3 rows exist")
}
