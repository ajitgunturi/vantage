//go:build integration

// Package streamer_test provides bufconn integration tests for the Streamer.
// These tests prove STREAM-03: the generated gRPC Produce client delivers
// restamped records to a live MQ broker entirely in-process (no Docker, no OS
// TCP port, no testcontainers). A real MQServer backed by a RingStore is wired
// to a real Streamer client via google.golang.org/grpc/test/bufconn.
//
// Run with:
//
//	go test -race -tags=integration ./internal/streamer/... -run TestStreamProduce
package streamer_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/ajitg/vantage/internal/queue"
	"github.com/ajitg/vantage/internal/server"
	"github.com/ajitg/vantage/internal/streamer"
	"github.com/ajitg/vantage/pkg/pb"
)

const bufSize = 1 << 20 // 1 MB in-memory transport buffer

// newBufconnMQ stands up a real MQServer over bufconn and returns a gRPC
// ClientConn connected to it. The server is stopped and the listener closed via
// t.Cleanup when the test ends.
func newBufconnMQ(t *testing.T) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	mqSrv := server.NewMQServer(queue.NewRingStore(5000), 100)
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
	return conn
}

// writeIntegrationCSV writes a small deterministic 3-row fixture CSV into
// t.TempDir() and returns its path together with the expected UUIDs in row order.
func writeIntegrationCSV(t *testing.T) (path string, uuids []string) {
	t.Helper()
	uuids = []string{
		"GPU-integ-001",
		"GPU-integ-002",
		"GPU-integ-003",
	}
	gpuOrdinals := []string{"0", "1", "2"}

	var sb strings.Builder
	sb.WriteString("Timestamp,metric_name,gpu_id,device,uuid,model_name,hostname,container,pod,namespace,value,labels_raw\n")
	for i, uuid := range uuids {
		sb.WriteString(strings.Join([]string{
			"2025-07-18T20:42:34Z",       // col 0: original timestamp (discarded)
			"DCGM_FI_DEV_GPU_UTIL",        // col 1: metric_name
			gpuOrdinals[i],                // col 2: gpu_id ordinal
			"nvidia0",                     // col 3: device
			uuid,                          // col 4: GPU UUID (the real identity)
			"NVIDIA H100 80GB HBM3",       // col 5: model_name
			"hostname1",                   // col 6: hostname
			"",                            // col 7: container
			"",                            // col 8: pod
			"",                            // col 9: namespace
			fmt.Sprintf("%.1f", float64(i+1)*10.0), // col 10: value
			"",                            // col 11: labels_raw
		}, ","))
		sb.WriteByte('\n')
	}

	path = t.TempDir() + "/integ_fixture.csv"
	require.NoError(t, os.WriteFile(path, []byte(sb.String()), 0o600))
	return path, uuids
}

// TestStreamProduce proves STREAM-03 end-to-end in-process:
//
//  1. streamer.Stream(once=true) publishes exactly 3 records to a real bufconn MQ.
//  2. A Consume stream on the same connection verifies: exactly 3 messages received,
//     each with a UUID matching the fixture, and a Timestamp parseable as RFC3339Nano.
//
// No Docker, no testcontainers, no OS ports required.
func TestStreamProduce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := newBufconnMQ(t)
	client := pb.NewMQServiceClient(conn)

	csvPath, expectedUUIDs := writeIntegrationCSV(t)

	// Publish one pass through the fixture CSV to the in-process MQ.
	err := streamer.Stream(ctx, client, csvPath, 0, true)
	require.NoError(t, err, "Stream(once=true) must complete without error")

	// Open a Consume stream on the same connection to verify broker received the msgs.
	stream, err := client.Consume(ctx)
	require.NoError(t, err, "Consume stream must open successfully")

	// Initial credit handshake: grant 10 slots so all 3 messages arrive before acks.
	err = stream.Send(&pb.ConsumeClientMsg{Credit: 10, ConsumerId: "test-consumer"})
	require.NoError(t, err, "initial credit send must succeed")

	// Receive exactly 3 messages, acking each.
	receivedUUIDs := make(map[string]bool, len(expectedUUIDs))
	for i := 0; i < 3; i++ {
		msg, recvErr := stream.Recv()
		require.NoError(t, recvErr, "Recv message %d must succeed", i+1)

		// Timestamp must parse as RFC3339Nano (STREAM-02).
		ts, parseErr := time.Parse(time.RFC3339Nano, msg.GetTimestamp())
		require.NoError(t, parseErr,
			"message %d Timestamp must be RFC3339Nano, got: %q", i+1, msg.GetTimestamp())
		// Must be in UTC.
		require.Equal(t, time.UTC, ts.Location(), "message %d Timestamp must be UTC", i+1)
		// Must be recent — not the original CSV timestamp "2025-07-18T20:42:34Z".
		require.NotEqual(t, "2025-07-18T20:42:34Z", msg.GetTimestamp(),
			"message %d Timestamp must be restamped", i+1)

		receivedUUIDs[msg.GetUuid()] = true

		// Ack the message so the broker removes it from its in-flight lease table.
		ackErr := stream.Send(&pb.ConsumeClientMsg{AckId: msg.GetId()})
		require.NoError(t, ackErr, "ack for message %d must succeed", i+1)
	}

	// All three fixture UUIDs must have been received exactly once.
	for _, uuid := range expectedUUIDs {
		require.True(t, receivedUUIDs[uuid],
			"fixture UUID %q was not received in the Consume stream", uuid)
	}
}
