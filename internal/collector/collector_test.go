//go:build integration

// Package collector_test provides end-to-end integration tests for the collector
// package. Tests require Docker (testcontainers-go spins up postgres:17-alpine)
// and the Rancher Desktop socket.
//
// Run with:
//
//	DOCKER_HOST=unix://$HOME/.rd/docker.sock \
//	TESTCONTAINERS_RYUK_DISABLED=true \
//	go test -race -tags=integration ./internal/collector/... -count=1
package collector_test

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
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
	"github.com/ajitg/vantage/pkg/db"
	"github.com/ajitg/vantage/pkg/pb"
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

const bufConnSize = 1 << 20 // 1 MiB in-memory transport buffer

// newBufconnMQ stands up a real MQServer over a bufconn listener and returns:
//   - a gRPC ClientConn connected to it
//   - the *server.MQServer (for direct inspection if needed)
//
// Both are cleaned up via t.Cleanup when the test ends.
func newBufconnMQ(t *testing.T) (*grpc.ClientConn, *server.MQServer) {
	t.Helper()

	lis := bufconn.Listen(bufConnSize)
	mqSrv := server.NewMQServer(queue.NewRingStore(5000), 200)
	s := grpc.NewServer()
	pb.RegisterMQServiceServer(s, mqSrv)
	t.Cleanup(func() {
		s.Stop()
		lis.Close()
	})
	go s.Serve(lis) //nolint:errcheck

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn, mqSrv
}

// produce calls Produce n times on the given client, generating distinct
// TelemetryMessage tuples. Timestamps are spaced by 1µs to guarantee distinct
// natural keys (gpu_id, metric_name, timestamp) in gpu_metrics.
func produce(t *testing.T, client pb.MQServiceClient, n int) {
	t.Helper()
	base := time.Now().UTC()
	for i := 0; i < n; i++ {
		msg := &pb.TelemetryMessage{
			Uuid:       fmt.Sprintf("GPU-%04d-0000-0000-0000-000000000000", i%100),
			GpuId:      fmt.Sprintf("%d", i%100),
			MetricName: "DCGM_FI_DEV_GPU_UTIL",
			// RFC3339Nano with 1µs spacing guarantees distinct timestamps per message
			// even for repeated GPU indices (i >= 100), keeping natural keys unique.
			Timestamp: base.Add(time.Duration(i) * time.Microsecond).Format(time.RFC3339Nano),
			Value:     float64(i),
			Device:    "nvidia0",
			ModelName: "NVIDIA H100",
			Hostname:  "test-host",
		}
		_, err := client.Produce(context.Background(), &pb.ProduceRequest{Message: msg})
		require.NoError(t, err, "produce msg %d", i)
	}
}

// rowCount queries the current row count in gpu_metrics.
func rowCount(t *testing.T) int {
	t.Helper()
	var n int
	err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM gpu_metrics").Scan(&n)
	require.NoError(t, err)
	return n
}

// TestConsumeHandshake covers COLL-01: the collector opens the bidi stream,
// sends the initial credit handshake, receives 10 messages, and persists all 10
// to gpu_metrics before the context deadline.
func TestConsumeHandshake(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	conn, _ := newBufconnMQ(t)
	client := pb.NewMQServiceClient(conn)
	produce(t, client, 10)

	cfg := collector.Config{
		BatchSize: 10,
		FlushMS:   200,
		Credit:    20, // >= BatchSize
	}

	consumeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	err := collector.Consume(consumeCtx, client, testPool, cfg)
	// DeadlineExceeded is expected: Consume blocks for more messages after the
	// batch is processed; context times out and Consume returns.
	if err != nil && err != context.DeadlineExceeded {
		t.Logf("Consume returned: %v", err)
	}

	require.Equal(t, 10, rowCount(t), "all 10 messages must land in gpu_metrics (COLL-01)")
}

// TestBatchFlush covers COLL-03: produces 120 messages with BatchSize=50,
// FlushMS=200. Verifies all 120 rows land — size-trigger flush handles batches
// of 50 and the final ticker flush handles the remainder.
func TestBatchFlush(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	conn, _ := newBufconnMQ(t)
	client := pb.NewMQServiceClient(conn)
	produce(t, client, 120)

	cfg := collector.Config{
		BatchSize: 50,
		FlushMS:   200,
		Credit:    100, // >= BatchSize; allows pipelining
	}

	consumeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	err := collector.Consume(consumeCtx, client, testPool, cfg)
	if err != nil && err != context.DeadlineExceeded {
		t.Logf("Consume returned: %v", err)
	}

	require.Equal(t, 120, rowCount(t), "all 120 messages must land in gpu_metrics (COLL-03)")
}

// TestIdempotentUpsert covers COLL-05: two messages with identical natural key
// (uuid→gpu_id, metric_name, timestamp) are produced and consumed. The
// ON CONFLICT DO NOTHING upsert ensures only 1 row lands in gpu_metrics.
func TestIdempotentUpsert(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	conn, _ := newBufconnMQ(t)
	client := pb.NewMQServiceClient(conn)

	// Both messages share the same natural key: same uuid (→ gpu_id), same
	// metric_name, same timestamp. Only Value differs (ignored by ON CONFLICT).
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	msg1 := &pb.TelemetryMessage{
		Uuid:       "GPU-dup-0000-0000-0000-000000000000",
		GpuId:      "0",
		MetricName: "DCGM_FI_DEV_GPU_UTIL",
		Timestamp:  ts,
		Value:      42.0,
		Device:     "nvidia0",
		ModelName:  "NVIDIA H100",
		Hostname:   "test-host",
	}
	msg2 := &pb.TelemetryMessage{
		Uuid:       "GPU-dup-0000-0000-0000-000000000000", // same uuid → same gpu_id in DB
		GpuId:      "0",
		MetricName: "DCGM_FI_DEV_GPU_UTIL", // same metric_name
		Timestamp:  ts,                       // same timestamp → duplicate natural key
		Value:      99.0,                     // different value, but key already conflicts
		Device:     "nvidia0",
		ModelName:  "NVIDIA H100",
		Hostname:   "test-host",
	}

	_, err := client.Produce(ctx, &pb.ProduceRequest{Message: msg1})
	require.NoError(t, err, "produce msg1")
	_, err = client.Produce(ctx, &pb.ProduceRequest{Message: msg2})
	require.NoError(t, err, "produce msg2 (duplicate key)")

	cfg := collector.Config{
		BatchSize: 10,
		FlushMS:   300,
		Credit:    20,
	}

	consumeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err = collector.Consume(consumeCtx, client, testPool, cfg)
	if err != nil && err != context.DeadlineExceeded {
		t.Logf("Consume returned: %v", err)
	}

	require.Equal(t, 1, rowCount(t),
		"duplicate message must not create a second row — ON CONFLICT DO NOTHING (COLL-05)")
}

// TestReconnect covers COLL-02: the collector reconnects after the MQ drops the
// stream. An ephemeral TCP listener is used so the server can be stopped and
// re-bound. The post-restart messages must land after reconnection.
func TestReconnect(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	// Start the first MQ server on an ephemeral port. Capture the address so we
	// can restart on the same address after GracefulStop.
	lis1, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis1.Addr().String()

	mqSrv1 := server.NewMQServer(queue.NewRingStore(5000), 200)
	grpcSrv1 := grpc.NewServer()
	pb.RegisterMQServiceServer(grpcSrv1, mqSrv1)
	go grpcSrv1.Serve(lis1) //nolint:errcheck

	// Produce the initial batch via a dedicated client connection.
	prodConn1, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { prodConn1.Close() })
	produce(t, pb.NewMQServiceClient(prodConn1), 5)

	// Start collector.Run in the background.
	cfg := collector.Config{
		MQAddr:    addr,
		BatchSize: 5,
		FlushMS:   200,
		Credit:    20,
	}
	runCtx, runCancel := context.WithTimeout(ctx, 30*time.Second)
	defer runCancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- collector.Run(runCtx, cfg, testPool)
	}()

	// Wait for the first 5 rows to land — proves the collector connected,
	// consumed, and persisted the initial batch before we kill the server.
	require.Eventually(t, func() bool {
		return rowCount(t) >= 5
	}, 15*time.Second, 200*time.Millisecond,
		"first 5 rows must land before server restart (COLL-02 pre-condition)")

	// GracefulStop the first server, releasing the port. The collector stream ends.
	grpcSrv1.GracefulStop()

	// Brief pause so the OS releases the TCP port before we re-bind.
	time.Sleep(150 * time.Millisecond)

	// Start a fresh MQ server on the same address.
	lis2, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	mqSrv2 := server.NewMQServer(queue.NewRingStore(5000), 200)
	grpcSrv2 := grpc.NewServer()
	pb.RegisterMQServiceServer(grpcSrv2, mqSrv2)
	go grpcSrv2.Serve(lis2) //nolint:errcheck
	t.Cleanup(func() {
		grpcSrv2.GracefulStop()
		lis2.Close()
	})

	// Produce 5 new messages on the second server (distinct timestamps from the
	// first batch — produce uses time.Now() as base, so keys won't collide).
	prodConn2, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { prodConn2.Close() })
	produce(t, pb.NewMQServiceClient(prodConn2), 5)

	// Wait for the collector to reconnect and process the post-restart messages.
	require.Eventually(t, func() bool {
		return rowCount(t) >= 10
	}, 20*time.Second, 200*time.Millisecond,
		"post-reconnect 5 rows must land after collector reconnects (COLL-02)")

	// Cancel context and verify Run exits cleanly.
	runCancel()
	select {
	case runErr := <-runDone:
		if runErr != nil && runErr != context.Canceled && runErr != context.DeadlineExceeded {
			t.Errorf("collector.Run returned unexpected error: %v", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Error("collector.Run did not exit within 10s after context cancel")
	}
}
