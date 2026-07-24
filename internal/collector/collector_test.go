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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/ajitg/vantage/internal/collector"
	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/pkg/db"
	"github.com/ajitg/vantage/pkg/pb"
)

// ── Fake stream helpers (C-1 test) ──────────────────────────────────────────

// fakeConsumeStream implements pb.MQService_ConsumeClient
// (= grpc.BidiStreamingClient[pb.ConsumeClientMsg, pb.TelemetryMessage])
// for unit/integration tests that need to inspect ack behaviour without a real gRPC server.
//
// Recv() returns pre-loaded messages in order, then blocks on ctx until done.
// Send() records every outgoing ConsumeClientMsg (initial credit + acks).
type fakeConsumeStream struct {
	ctx  context.Context
	msgs []*pb.TelemetryMessage // pre-loaded to deliver via Recv
	pos  int                    // next index in msgs
	mu   sync.Mutex
	sent []*pb.ConsumeClientMsg // all Send calls recorded here
}

func (f *fakeConsumeStream) Send(msg *pb.ConsumeClientMsg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeConsumeStream) Recv() (*pb.TelemetryMessage, error) {
	if f.pos < len(f.msgs) {
		m := f.msgs[f.pos]
		f.pos++
		return m, nil
	}
	// No more pre-loaded messages — block until the context is cancelled
	// (simulates the broker waiting for more messages).
	<-f.ctx.Done()
	return nil, f.ctx.Err()
}

// acksSent returns the number of Send calls that carried an AckId (not credit).
func (f *fakeConsumeStream) acksSent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.sent {
		if m.GetAckId() != 0 {
			n++
		}
	}
	return n
}

// grpc.ClientStream interface — no-ops for test purposes.
func (f *fakeConsumeStream) Header() (metadata.MD, error) { return nil, nil }
func (f *fakeConsumeStream) Trailer() metadata.MD         { return nil }
func (f *fakeConsumeStream) CloseSend() error             { return nil }
func (f *fakeConsumeStream) Context() context.Context     { return f.ctx }
func (f *fakeConsumeStream) SendMsg(_ any) error          { return nil }
func (f *fakeConsumeStream) RecvMsg(_ any) error          { return nil }

// fakeMQClient is a pb.MQServiceClient that returns a fakeConsumeStream.
type fakeMQClient struct {
	stream *fakeConsumeStream
}

func (f *fakeMQClient) Produce(_ context.Context, _ *pb.ProduceRequest, _ ...grpc.CallOption) (*pb.ProduceResponse, error) {
	return nil, fmt.Errorf("produce not supported in fakeMQClient")
}

func (f *fakeMQClient) Consume(_ context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pb.ConsumeClientMsg, pb.TelemetryMessage], error) {
	return f.stream, nil
}

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
	mqSrv := server.NewMQServer(queue.NewBroker(queue.BrokerConfig{Capacity: 5000}), 200)
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
		Timestamp:  ts,                     // same timestamp → duplicate natural key
		Value:      99.0,                   // different value, but key already conflicts
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

// TestBadProtoSkipped verifies that a TelemetryMessage with an unparseable
// timestamp is silently skipped (log-and-continue) and does not abort the batch
// or prevent subsequent valid messages from landing. Covers the persistBatch
// bad-proto path (T-03-03c).
func TestBadProtoSkipped(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	conn, _ := newBufconnMQ(t)
	client := pb.NewMQServiceClient(conn)

	// Produce one bad-proto message (empty timestamp → models.FromProto fails).
	badMsg := &pb.TelemetryMessage{
		Uuid:       "GPU-bad0-0000-0000-0000-000000000000",
		GpuId:      "0",
		MetricName: "DCGM_FI_DEV_GPU_UTIL",
		Timestamp:  "", // invalid — FromProto will return a parse error
		Value:      1.0,
		Device:     "nvidia0",
		ModelName:  "NVIDIA H100",
		Hostname:   "test-host",
	}
	_, err := client.Produce(ctx, &pb.ProduceRequest{Message: badMsg})
	require.NoError(t, err, "produce bad-proto message")

	// Produce one valid message after the bad one.
	goodMsg := &pb.TelemetryMessage{
		Uuid:       "GPU-good-0000-0000-0000-000000000000",
		GpuId:      "0",
		MetricName: "DCGM_FI_DEV_GPU_UTIL",
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Value:      2.0,
		Device:     "nvidia0",
		ModelName:  "NVIDIA H100",
		Hostname:   "test-host",
	}
	_, err = client.Produce(ctx, &pb.ProduceRequest{Message: goodMsg})
	require.NoError(t, err, "produce valid message")

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

	// The bad-proto message must be skipped (not inserted), the valid one must land.
	require.Equal(t, 1, rowCount(t),
		"bad-proto message must be skipped; only the valid message must land (T-03-03c)")
}

// TestPersistBatchError_NoAcks verifies C-1: when the DB insert fails, Consume
// must NOT ack any of the batch messages. The unacked messages stay in the broker's
// lease table and are redelivered on disconnect (at-least-once, ADR-001).
//
// The failure is induced by using a pool that is closed immediately after creation,
// so every SendBatch → Exec call returns an error.
//
// TDD gate: before the C-1 fix (persistBatch always returned nil), flush would
// silently ack every message despite the failed insert → acksSent() returns 5
// and the test fails. After the fix (persistBatch returns the first Exec error),
// flush returns immediately without acking → acksSent() returns 0 → test passes.
func TestPersistBatchError_NoAcks(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	// Open a fresh pool and immediately close it to simulate a DB failure.
	// Any SendBatch → Exec on a closed pool returns a connection-acquisition error.
	dsn := testCtr.MustConnectionString(ctx, "sslmode=disable")
	failPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "create fail pool")
	failPool.Close() // close immediately — SendBatch Exec will fail

	// Pre-load BatchSize=5 valid TelemetryMessages into the fake stream.
	// Messages carry non-zero broker IDs so AckId != 0 when acks are sent.
	// All pass models.FromProto (valid RFC3339Nano timestamp, non-empty UUID).
	const batchSz = 5
	msgs := make([]*pb.TelemetryMessage, batchSz)
	for i := range msgs {
		msgs[i] = &pb.TelemetryMessage{
			Id:         uint64(i + 1), // non-zero broker id — acks carry this id
			Uuid:       fmt.Sprintf("GPU-C1-%04d-0000-0000-0000-000000000000", i),
			GpuId:      fmt.Sprintf("%d", i),
			MetricName: "DCGM_FI_DEV_GPU_UTIL",
			Timestamp:  time.Now().UTC().Add(time.Duration(i) * time.Microsecond).Format(time.RFC3339Nano),
			Value:      float64(i + 1),
			Device:     "nvidia0",
			ModelName:  "NVIDIA H100",
			Hostname:   "test-host",
		}
	}

	// Short context so the test completes promptly in the buggy case too.
	// In the buggy case: flush acks, then Consume blocks waiting for more msgs,
	// ctx expires (3s), Consume returns context.DeadlineExceeded.
	// In the fixed case: persistBatch returns error immediately, Consume returns.
	consumeCtx, consumeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer consumeCancel()

	fStream := &fakeConsumeStream{ctx: consumeCtx, msgs: msgs}
	fakeClient := &fakeMQClient{stream: fStream}

	cfg := collector.Config{
		BatchSize: batchSz, // size-trigger fires when all batchSz msgs arrive
		FlushMS:   5000,    // timer disabled — only size-trigger fires
		Credit:    20,      // comfortably above batchSz
	}

	_ = collector.Consume(consumeCtx, fakeClient, failPool, cfg)

	// Core assertion: zero acks must be sent when persistBatch fails.
	// Acking a failed batch causes the broker to discard messages — silent data loss.
	// In the buggy code: persistBatch returns nil → flush sends 5 acks → acksSent()==5 → FAIL.
	// After fix:         persistBatch returns error → flush exits → acksSent()==0 → PASS.
	require.Zero(t, fStream.acksSent(),
		"C-1: no acks must be sent when persistBatch fails — "+
			"unacked messages will be redelivered by the broker's requeue-on-disconnect")
}

// TestTickerFlushError verifies that Consume returns a flush error when the
// time-triggered flush (ticker) fires and persistBatch fails.
// This covers the ticker-path in consumeStream that TestPersistBatchError_NoAcks
// does not exercise (that test triggers via size flush, not ticker flush).
func TestTickerFlushError(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	// Create a pool and close it immediately so every SendBatch → Exec call fails.
	dsn := testCtr.MustConnectionString(ctx, "sslmode=disable")
	failPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "create fail pool")
	failPool.Close()

	// Pre-load 3 valid messages (< BatchSize=10) so the size-trigger never fires.
	// The ticker fires after FlushMS=50ms, triggering persistBatch on the closed pool.
	const nMsgs = 3
	msgs := make([]*pb.TelemetryMessage, nMsgs)
	for i := range msgs {
		msgs[i] = &pb.TelemetryMessage{
			Id:         uint64(i + 1),
			Uuid:       fmt.Sprintf("GPU-TICK-%04d-0000-0000-0000-000000000000", i),
			GpuId:      fmt.Sprintf("%d", i),
			MetricName: "DCGM_FI_DEV_GPU_UTIL",
			Timestamp:  time.Now().UTC().Add(time.Duration(i) * time.Microsecond).Format(time.RFC3339Nano),
			Value:      float64(i + 1),
			Device:     "nvidia0",
			ModelName:  "NVIDIA H100",
			Hostname:   "test-host",
		}
	}

	consumeCtx, consumeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer consumeCancel()

	fStream := &fakeConsumeStream{ctx: consumeCtx, msgs: msgs}
	fakeClient := &fakeMQClient{stream: fStream}

	cfg := collector.Config{
		BatchSize: 10, // > nMsgs: size-trigger never fires
		FlushMS:   50, // 50ms ticker fires quickly for test speed
		Credit:    20,
	}

	_ = collector.Consume(consumeCtx, fakeClient, failPool, cfg)

	// Zero acks: ticker flush triggered persistBatch which failed (closed pool),
	// so flush returned without acking — same invariant as TestPersistBatchError_NoAcks.
	require.Zero(t, fStream.acksSent(),
		"no acks must be sent when ticker flush fails — unacked messages will be redelivered by broker")
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

	mqSrv1 := server.NewMQServer(queue.NewBroker(queue.BrokerConfig{Capacity: 5000}), 200)
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
	runCtx, runCancel := context.WithTimeout(ctx, 60*time.Second)
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

	// Stop the first server immediately. Stop() is used instead of GracefulStop()
	// because GracefulStop() blocks for ~30s (the gRPC drain interval) while the
	// server-side Consume handler waits for ctx.Done(), eating through the runCtx
	// deadline before the collector ever reconnects. Stop() closes all connections
	// immediately, causing stream.Recv() to return on the client side at once, so
	// the collector reconnects well within the runCtx window. Both Stop() and
	// GracefulStop() cause the stream to drop — both are valid test vectors for COLL-02.
	grpcSrv1.Stop()

	// Brief pause so the OS releases the TCP port before we re-bind.
	time.Sleep(100 * time.Millisecond)

	// Start a fresh MQ server on the same address.
	lis2, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	mqSrv2 := server.NewMQServer(queue.NewBroker(queue.BrokerConfig{Capacity: 5000}), 200)
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

// TestPoisonRowBisectDeadLetter covers poison isolation end-to-end: a batch containing one
// row that deterministically fails at exec time (CHECK violation) must NOT
// wedge the pipeline. The bisect isolates the poison row into gpu_metrics_dlq,
// every good row lands, and ALL messages (including the poison one) are acked
// so the broker never redelivers the batch.
func TestPoisonRowBisectDeadLetter(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	// Fabricate deterministic DB-level poison: any row with value = 4242 fails
	// exec with SQLSTATE 23514. Snapshot restore removes the constraint.
	// Retry: a previous test's snapshot Restore terminates pooled connections
	// (57P01); the first statements may land on stale conns until evicted.
	var err error
	for i := 0; i < 3; i++ {
		_, err = testPool.Exec(ctx,
			"ALTER TABLE gpu_metrics ADD CONSTRAINT poison_test_check CHECK (value <> 4242)")
		if err == nil {
			break
		}
	}
	require.NoError(t, err)

	conn, mqSrv := newBufconnMQ(t)
	client := pb.NewMQServiceClient(conn)

	// 5 messages; index 2 is poison (value=4242).
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		val := float64(i)
		if i == 2 {
			val = 4242
		}
		msg := &pb.TelemetryMessage{
			Uuid:       "GPU-a3a3-0000-0000-0000-000000000000",
			GpuId:      "0",
			MetricName: "DCGM_FI_DEV_GPU_UTIL",
			Timestamp:  base.Add(time.Duration(i) * time.Microsecond).Format(time.RFC3339Nano),
			Value:      val,
		}
		_, err := client.Produce(ctx, &pb.ProduceRequest{Message: msg})
		require.NoError(t, err)
	}

	cfg := collector.Config{BatchSize: 5, FlushMS: 200, Credit: 20}
	consumeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cerr := collector.Consume(consumeCtx, client, testPool, cfg)
	if cerr != nil && cerr != context.DeadlineExceeded {
		t.Logf("Consume returned: %v", cerr)
	}

	// The 4 good rows landed; the poison row did not.
	require.Equal(t, 4, rowCount(t), "good rows must land despite the poison row (bisect isolation)")

	// The poison row is preserved in gpu_metrics_dlq with its failure reason.
	var dlqCount int
	var dlqErr string
	var brokerID int64
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT count(*) FROM gpu_metrics_dlq").Scan(&dlqCount))
	require.Equal(t, 1, dlqCount, "exactly the poison row must be dead-lettered")
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT broker_id, error FROM gpu_metrics_dlq").Scan(&brokerID, &dlqErr))
	require.Contains(t, dlqErr, "23514", "DLQ row must record the SQLSTATE")
	require.Greater(t, brokerID, int64(0), "DLQ row must carry the stable broker id")

	// The payload is recoverable JSON with the poison value.
	var payloadValue float64
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT (payload->>'value')::float8 FROM gpu_metrics_dlq").Scan(&payloadValue))
	require.Equal(t, float64(4242), payloadValue)

	// Every message was acked — the broker holds nothing back (no wedge).
	require.Eventually(t, func() bool {
		st := mqSrv.Stats()
		return st.Consumed == 5 && st.InFlight == 0 && st.Depth+st.RetryDepth == 0
	}, 5*time.Second, 20*time.Millisecond,
		"all 5 messages acked (poison included) — pipeline must not wedge")
}

// TestTransientErrorNeverDeadLetters covers the poison-classifier guard: a transient failure
// (here: cancelled context — not SQLSTATE class 22/23) must propagate so the
// batch stays unacked for redelivery, and put NOTHING in gpu_metrics_dlq.
func TestTransientErrorNeverDeadLetters(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	msgs := []*pb.TelemetryMessage{{
		Uuid:       "GPU-57xx-0000-0000-0000-000000000000",
		GpuId:      "0",
		MetricName: "DCGM_FI_DEV_GPU_UTIL",
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Value:      1,
		Id:         1,
	}}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	n, perr := collector.PersistResilientForTest(cancelled, testPool, msgs)
	require.Error(t, perr, "transient (non-poison) failure must propagate")
	require.Equal(t, 0, n, "nothing may be dead-lettered on a transient failure")

	var dlqCount int
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT count(*) FROM gpu_metrics_dlq").Scan(&dlqCount))
	require.Equal(t, 0, dlqCount, "transient failures must never dead-letter")
}

// TestProducerTimestampPreserved pins the producer-owned timestamp contract:
// the value the Streamer stamps at publish time is what lands in gpu_metrics —
// the Collector parses and persists it verbatim (truncated to Postgres's
// microsecond TIMESTAMPTZ precision, ADR-002) and never substitutes its own
// clock. Consumer-side restamping would corrupt the telemetry timeline.
func TestProducerTimestampPreserved(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() { restoreDB(ctx, t) })

	conn, _ := newBufconnMQ(t)
	client := pb.NewMQServiceClient(conn)

	// A fixed producer timestamp, deliberately far from now() so any
	// consumer-side restamp is unmistakable.
	producerTS := time.Date(2026, 1, 15, 6, 30, 45, 123456000, time.UTC)
	msg := &pb.TelemetryMessage{
		Uuid:       "GPU-tsown-0000-0000-0000-000000000000",
		GpuId:      "0",
		MetricName: "DCGM_FI_DEV_GPU_UTIL",
		Timestamp:  producerTS.Format(time.RFC3339Nano),
		Value:      7,
	}
	_, err := client.Produce(ctx, &pb.ProduceRequest{Message: msg})
	require.NoError(t, err)

	cfg := collector.Config{BatchSize: 1, FlushMS: 100, Credit: 8}
	consumeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = collector.Consume(consumeCtx, client, testPool, cfg) //nolint:errcheck

	var persisted time.Time
	require.NoError(t, testPool.QueryRow(ctx,
		"SELECT timestamp FROM gpu_metrics WHERE gpu_id = $1",
		"GPU-tsown-0000-0000-0000-000000000000").Scan(&persisted))
	require.True(t, persisted.Equal(producerTS),
		"persisted timestamp %s must equal the producer's stamp %s — the consumer must never restamp",
		persisted, producerTS)
}
