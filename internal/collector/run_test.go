// Package collector_test provides unit tests for collector.Run and collector.Consume
// that require no external infrastructure (no Docker, no testcontainers).
// These tests run with the standard go test ./... invocation.
package collector_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ajitg/vantage/internal/collector"
	"github.com/ajitg/vantage/pkg/pb"
)

// TestRunPreCanceledContext verifies that collector.Run returns immediately with
// context.Canceled when the context is already canceled before the first loop
// iteration. The pool may be nil because Run never reaches dialMQ when ctx is
// already done.
func TestRunPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel before calling Run

	cfg := collector.Config{MQAddr: ":50051", BatchSize: 50, FlushMS: 500, Credit: 100}
	err := collector.Run(ctx, cfg, nil /* pool unused — ctx already canceled */)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled,
		"Run with pre-canceled ctx must return context.Canceled immediately")
}

// TestConsumeStreamOpenError verifies that Consume returns an error when the
// underlying gRPC stream cannot be opened (server unreachable). We use a
// connection to a known-down address so client.Consume fails immediately.
func TestConsumeStreamOpenError(t *testing.T) {
	// Connect to an address where nothing is listening. grpc.NewClient is lazy
	// but client.Consume will fail fast (Unavailable) when the stream is opened.
	conn, err := grpc.NewClient("127.0.0.1:1", // port 1 — not a valid listener
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err, "grpc.NewClient is lazy — must not fail here")
	t.Cleanup(func() { conn.Close() })

	client := pb.NewMQServiceClient(conn)
	cfg := collector.Config{BatchSize: 10, FlushMS: 200, Credit: 20}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = collector.Consume(ctx, client, nil /* pool unused — fails before persist */, cfg)
	require.Error(t, err, "Consume must return an error when the stream cannot be opened")
}
