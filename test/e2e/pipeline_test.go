//go:build integration

// Package e2e provides an end-to-end integration test for the vantage telemetry
// pipeline (QA-03): CSV → Streamer → MQ (bufconn, in-process) → Collector →
// PostgreSQL (testcontainers postgres:17-alpine).
//
// The core assertion is exactly-once delivery under concurrent collectors:
//
//	count(*) FROM gpu_metrics == count(DISTINCT gpu_id, metric_name, timestamp)
//
// This proves that K=3 concurrent collectors persisting from the same at-least-once
// MQ broker produce zero duplicate rows — the idempotent ON CONFLICT DO NOTHING
// absorbs any redeliveries.
//
// Run with:
//
//	DOCKER_HOST=unix://$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true \
//	  go test -race -tags=integration ./test/e2e/... -run TestEndToEnd -v
package e2e

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/ajitg/vantage/internal/collector"
	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/internal/streamer"
	"github.com/ajitg/vantage/pkg/db"
	"github.com/ajitg/vantage/pkg/pb"
)

// Fixture dimensions: G GPU UUIDs × M metric names = G×M rows total.
// Every (uuid, metric_name) combination is unique in the CSV, which makes the
// exactly-once assertion robust even if two rows are restamped to the same
// nanosecond (ON CONFLICT DO NOTHING keeps one; count(*) == count(distinct) holds).
const (
	fixtureGPUCount    = 10 // G: distinct GPU UUIDs in the fixture
	fixtureMetricCount = 20 // M: distinct metric names per GPU; G×M = 200 rows total
)

// bufconnBuf is the in-memory transport buffer size for the bufconn listener.
const bufconnBuf = 1 << 20 // 1 MB

// Package-level state shared across tests in this package.
var (
	testPool *pgxpool.Pool
	testCtr  *postgres.PostgresContainer
)

// TestMain starts a single postgres:17-alpine container for the whole package,
// runs migrations to establish the gpu_metrics schema, takes a snapshot, then
// opens a shared pool. Each individual test restores the DB to that snapshot so
// tests are isolated without paying the cost of a new container.
//
// Pitfall-5 guard (testcontainers-go #2474): username MUST be "postgres" and the
// database MUST NOT be "postgres" for Snapshot/Restore to work correctly.
// Pitfall-6 guard: pool startup ping uses a 5-second context timeout.
func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("vantage_e2e"),
		postgres.WithUsername("postgres"), // must be "postgres" for Snapshot/Restore (Pitfall 5)
		postgres.WithPassword("secret"),
		postgres.BasicWaitStrategies(),
		postgres.WithSQLDriver("pgx"), // required for Snapshot/Restore
	)
	if err != nil {
		log.Fatalf("e2e: start postgres: %v", err)
	}
	defer testcontainers.TerminateContainer(ctr) //nolint:errcheck
	testCtr = ctr

	dsn := ctr.MustConnectionString(ctx, "sslmode=disable")

	// Apply migrations (creates gpu_metrics table + indexes) before snapshotting.
	if err := db.Migrate(ctx, dsn); err != nil {
		log.Fatalf("e2e: migrate: %v", err)
	}

	// Snapshot AFTER migrations; tests restore to this clean state.
	if err := ctr.Snapshot(ctx); err != nil {
		log.Fatalf("e2e: snapshot: %v", err)
	}

	// Open the shared pool with a 5-second startup ping (Pitfall 6).
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pool, err := db.New(pingCtx, db.Config{DSN: dsn, MaxConns: 10})
	if err != nil {
		log.Fatalf("e2e: pool: %v", err)
	}
	testPool = pool
	defer pool.Close()

	os.Exit(m.Run())
}

// restoreDB resets the container to its post-migration snapshot and pings the
// pool to evict dead connections from prior Restore() calls (Restore calls
// pg_terminate_backend() on all connections).
func restoreDB(ctx context.Context, t *testing.T) {
	t.Helper()
	if err := testCtr.Restore(ctx); err != nil {
		t.Fatalf("e2e: restore: %v", err)
	}
	// Ping evicts pool connections that Restore killed. The error is expected.
	_ = testPool.Ping(context.Background())
}

// startBufconnMQ stands up a real MQServer backed by a RingStore(10000) over a
// bufconn listener. The gRPC server is stopped and the listener closed via
// t.Cleanup when the test ends. Returns the listener so callers can open
// multiple independent ClientConns to the same broker.
func startBufconnMQ(t *testing.T) *bufconn.Listener {
	t.Helper()
	lis := bufconn.Listen(bufconnBuf)
	mqSrv := server.NewMQServer(queue.NewRingStore(10000), 200)
	grpcSrv := grpc.NewServer()
	pb.RegisterMQServiceServer(grpcSrv, mqSrv)
	go grpcSrv.Serve(lis) //nolint:errcheck
	t.Cleanup(func() {
		grpcSrv.Stop()
		lis.Close()
	})
	return lis
}

// newBufconnConn creates a gRPC ClientConn that dials through the bufconn
// listener in-process (no OS TCP port). The connection is closed via t.Cleanup.
func newBufconnConn(t *testing.T, lis *bufconn.Listener) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err, "grpc.NewClient (bufconn) must not error")
	t.Cleanup(func() { conn.Close() })
	return conn
}

// writeFixtureCSV writes a deterministic G×M-row fixture CSV into t.TempDir()
// and returns its path.
//
// Column layout matches the strict 12-column DCGM format (FieldsPerRecord=12
// enforced in streamer.Stream):
//
//	Col 0: Timestamp   — fixed; discarded by streamer (RFC3339Nano restamp)
//	Col 1: metric_name — unique per row (E2E_METRIC_01..E2E_METRIC_20)
//	Col 2: gpu_id      — ordinal "g" (passed through to proto but NOT stored)
//	Col 3: device      — "nvidia{g}"
//	Col 4: uuid        — GPU UUID (stored as db.gpu_id via models.FromProto D-04)
//	Col 5: model_name  — "NVIDIA H100"
//	Col 6: hostname    — "e2e-host"
//	Col 7: container   — "" (empty)
//	Col 8: pod         — "" (empty)
//	Col 9: namespace   — "" (empty)
//	Col 10: value      — unique float per row
//	Col 11: labels_raw — "" (empty)
//
// Every (uuid, metric_name) pair appears exactly once, guaranteeing the
// exactly-once assertion holds regardless of restamp-timestamp collisions.
func writeFixtureCSV(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("Timestamp,metric_name,gpu_id,device,uuid,model_name,hostname,container,pod,namespace,value,labels_raw\n")
	for g := 0; g < fixtureGPUCount; g++ {
		uuid := fmt.Sprintf("GPU-E2E%04d-0000-0000-0000-000000000001", g+1)
		for m := 0; m < fixtureMetricCount; m++ {
			metric := fmt.Sprintf("E2E_METRIC_%02d", m+1)
			value := float64(g*fixtureMetricCount+m+1) * 0.5
			sb.WriteString(fmt.Sprintf(
				"2025-01-01T00:00:00Z,%s,%d,nvidia%d,%s,NVIDIA H100,e2e-host,,,,%.2f,\n",
				metric, g, g, uuid, value,
			))
		}
	}
	path := t.TempDir() + "/e2e_fixture.csv"
	require.NoError(t, os.WriteFile(path, []byte(sb.String()), 0o600),
		"write fixture CSV")
	return path
}

// pollUntilStable polls `SELECT count(*) FROM gpu_metrics` every 100ms until
// the count is unchanged for ≥500ms (5 consecutive polls) or a 20-second
// deadline is exceeded. It calls t.Fatal on timeout.
//
// The stability window (500ms) is 5× the collector FlushMS (100ms) used in the
// E2E test, so any batch in-flight at poll start will flush during the window.
func pollUntilStable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	const (
		interval       = 100 * time.Millisecond
		requiredStable = 5 // 5 × 100ms = 500ms
	)
	deadline := time.Now().Add(20 * time.Second)
	var prev int64 = -1
	stable := 0
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		var count int64
		if err := pool.QueryRow(context.Background(),
			"SELECT count(*) FROM gpu_metrics").Scan(&count); err != nil {
			continue // transient error — retry
		}
		if count > 0 && count == prev {
			stable++
			if stable >= requiredStable {
				return
			}
		} else {
			stable = 0
			prev = count
		}
	}
	t.Fatal("e2e: timed out after 20s waiting for gpu_metrics count to stabilize — pipeline stalled")
}

// TestEndToEnd_ExactlyOnce proves QA-03: the complete pipeline
//
//	CSV → streamer.Stream → MQ (bufconn) → K=3 collector.Consume → PostgreSQL
//
// in-process, with the following assertions:
//
//  1. count(*) > 0: rows landed in the DB.
//
//  2. count(*) == count(DISTINCT gpu_id, metric_name, timestamp): zero duplicate
//     rows under concurrent K=3 collectors consuming from the same at-least-once
//     MQ. ON CONFLICT DO NOTHING absorbs any redeliveries; this assertion
//     catches any duplication that slips through.
//
//  3. count(DISTINCT gpu_id) == fixtureGPUCount: the UUID→gpu_id mapping
//     (models.FromProto D-04) held end-to-end — the db column holds UUIDs,
//     not the ordinal "0"/"1"/"2" values.
func TestEndToEnd_ExactlyOnce(t *testing.T) {
	ctx := context.Background()
	restoreDB(ctx, t)

	// ── 1. In-process MQ over bufconn (no OS port, deterministic teardown) ──────
	lis := startBufconnMQ(t)

	// ── 2. Write fixture CSV: G×M rows, each (uuid, metric_name) unique ──────────
	csvPath := writeFixtureCSV(t)

	// ── 3. Consumer context — separate from test bg-ctx so we control lifecycle ───
	// 60s timeout is generous; pipelines with 200 rows should complete in <5s.
	consCtx, cancelConsumers := context.WithTimeout(ctx, 60*time.Second)
	defer cancelConsumers()

	// ── 4. K=3 concurrent collectors — the concurrent-consumer property under test ─
	//
	// Each collector opens its own ClientConn to the same bufconn MQ broker.
	// In steady state (all acking), the broker delivers each message to exactly
	// one consumer. If a consumer disconnects with unacked leases, the broker
	// requeues them for a survivor — duplicates are absorbed by ON CONFLICT.
	const K = 3
	var wg sync.WaitGroup
	consumerErrs := make([]error, K) // written in goroutines; read after wg.Wait()
	for i := 0; i < K; i++ {
		conn := newBufconnConn(t, lis)
		wg.Add(1)
		go func(idx int, c *grpc.ClientConn) {
			defer wg.Done()
			cfg := collector.Config{
				BatchSize: 50,  // flush on size
				FlushMS:   100, // flush every 100ms; 5× below stability-poll window
				Credit:    100, // in-flight window >= BatchSize
			}
			err := collector.Consume(consCtx, pb.NewMQServiceClient(c), testPool, cfg)
			// context.Canceled and DeadlineExceeded are expected on clean shutdown.
			if err != nil &&
				!errors.Is(err, context.Canceled) &&
				!errors.Is(err, context.DeadlineExceeded) {
				consumerErrs[idx] = err
			}
		}(i, conn)
	}

	// ── 5. Publish all fixture rows via streamer.Stream(once=true) ───────────────
	//
	// once=true: a single pass through the CSV, no loop delay. Blocks until all
	// G×M rows have been sent to the MQ via unary gRPC Produce calls. Collectors
	// may still be processing their batches after Stream returns.
	streamerConn := newBufconnConn(t, lis)
	err := streamer.Stream(consCtx, pb.NewMQServiceClient(streamerConn), csvPath, 0, true)
	require.NoError(t, err, "streamer.Stream(once=true) must complete cleanly")

	// ── 6. Poll until DB count stabilizes (≥500ms unchanged) ────────────────────
	//
	// Ensures all in-flight batches are flushed before we cancel consumers.
	// FlushMS=100ms means any pending batch flushes within 100ms; 500ms of
	// stability (5 polls) is strong evidence the pipeline has drained.
	pollUntilStable(t, testPool)

	// ── 7. Stop consumers and join goroutines ─────────────────────────────────────
	//
	// cancelConsumers triggers the <-ctx.Done() branch in collector.Consume,
	// causing each goroutine to exit. wg.Wait() ensures no goroutine is still
	// running (writing consumerErrs or calling t.Errorf) before assertions run.
	cancelConsumers()
	wg.Wait()

	// Surface any unexpected consumer errors (non-context errors).
	for i, cerr := range consumerErrs {
		require.NoError(t, cerr, "collector %d returned unexpected error", i)
	}

	// ── 8. Exactly-once assertions ────────────────────────────────────────────────

	// Assertion 1: at least one row persisted.
	var totalCount int64
	require.NoError(t,
		testPool.QueryRow(ctx, "SELECT count(*) FROM gpu_metrics").Scan(&totalCount),
		"count(*) FROM gpu_metrics must succeed")
	require.Greater(t, totalCount, int64(0),
		"pipeline must persist at least one row to gpu_metrics")

	// Assertion 2: zero duplicate rows.
	// count(DISTINCT ...) via subquery (PostgreSQL does not support multi-column
	// COUNT DISTINCT directly in the aggregate syntax).
	var distinctCount int64
	require.NoError(t,
		testPool.QueryRow(ctx, `
			SELECT count(*) FROM (
				SELECT DISTINCT gpu_id, metric_name, timestamp FROM gpu_metrics
			) d
		`).Scan(&distinctCount),
		"count(distinct natural key) query must succeed")
	require.Equal(t, totalCount, distinctCount,
		"exactly-once violated: count(*) (%d) != count(distinct natural key) (%d) — "+
			"duplicate rows detected under K=%d concurrent collectors",
		totalCount, distinctCount, K)

	// Assertion 3: UUID→gpu_id mapping propagated end-to-end (D-04 / COLL-04).
	// count(distinct gpu_id) must equal G (fixtureGPUCount), proving the
	// proto.Uuid field (GPU UUID) was stored, not the proto.gpu_id ordinal ("0","1",...).
	var distinctGPUs int64
	require.NoError(t,
		testPool.QueryRow(ctx, "SELECT count(distinct gpu_id) FROM gpu_metrics").Scan(&distinctGPUs),
		"count(distinct gpu_id) query must succeed")
	require.Equal(t, int64(fixtureGPUCount), distinctGPUs,
		"UUID mapping violated: expected %d distinct gpu_id values (GPU UUIDs), got %d — "+
			"check models.FromProto D-04 (must use proto.Uuid, not proto.GpuId)",
		fixtureGPUCount, distinctGPUs)

	t.Logf("QA-03 PASSED: %d rows persisted, %d distinct natural keys, %d distinct gpu_ids — "+
		"exactly-once delivery proven under K=%d concurrent collectors",
		totalCount, distinctCount, distinctGPUs, K)
}
