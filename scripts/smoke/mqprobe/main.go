// Command mqprobe is a minimal gRPC client used by the Phase 1 smoke check
// (scripts/smoke/phase01-mq.sh). It exercises the MQ data plane end-to-end
// over the gRPC boundary, in pure Go so the manual smoke suite needs no
// grpcurl/protoc install — only the Go toolchain that the repo already requires.
//
// Modes (-mode):
//
//	both     (default) Open a Consume stream, then Produce N — the
//	         consumer-already-attached path. Verifies all N stream back.
//	produce  Produce N messages and exit, leaving them buffered in the MQ.
//	consume  Attach a Consume stream and drain N messages, then exit.
//
// Running -mode produce in one invocation and -mode consume in a later one
// exercises the late-join path: the producer publishes and disconnects, and a
// consumer that attaches afterwards still drains the buffered messages.
//
// Usage:
//
//	go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:55051 -n 20
//	go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:55051 -n 20 -mode produce
//	go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:55051 -n 20 -mode consume
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ajitg/vantage/pkg/pb"
)

func main() {
	addr := flag.String("grpc", "127.0.0.1:50051", "MQ gRPC address")
	n := flag.Int("n", 20, "number of messages to produce and/or consume")
	mode := flag.String("mode", "both", "both | produce | consume")
	timeout := flag.Duration("timeout", 10*time.Second, "overall deadline")
	flag.Parse()

	if err := run(*addr, *n, *mode, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "mqprobe: FAIL: %v\n", err)
		os.Exit(1)
	}
}

func run(addr string, n int, mode string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	client := pb.NewMQServiceClient(conn)

	switch mode {
	case "both":
		return runBoth(ctx, client, n, addr)
	case "produce":
		if err := produce(ctx, client, n); err != nil {
			return err
		}
		fmt.Printf("mqprobe: OK — produced %d (left buffered) via %s\n", n, addr)
		return nil
	case "consume":
		got, err := consume(ctx, client, n)
		if err != nil {
			return err
		}
		fmt.Printf("mqprobe: OK — consumed %d via %s\n", got, addr)
		return nil
	default:
		return fmt.Errorf("unknown -mode %q (want both|produce|consume)", mode)
	}
}

// runBoth opens the Consume stream first — mirrors a Collector connecting before
// the Streamer publishes — then Produces N concurrently, verifying all N stream
// back. This is the consumer-already-attached path.
func runBoth(ctx context.Context, client pb.MQServiceClient, n int, addr string) error {
	stream, err := client.Consume(ctx, &pb.ConsumeRequest{ConsumerId: "smoke"})
	if err != nil {
		return fmt.Errorf("open consume stream: %w", err)
	}

	received := make(chan error, 1)
	go func() {
		_, err := drain(stream, n)
		received <- err
	}()

	if err := produce(ctx, client, n); err != nil {
		return err
	}

	select {
	case err := <-received:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting to consume %d messages: %w", n, ctx.Err())
	}

	fmt.Printf("mqprobe: OK — produced %d, consumed %d via %s\n", n, n, addr)
	return nil
}

// produce publishes n DCGM-shaped telemetry messages over unary Produce calls.
func produce(ctx context.Context, client pb.MQServiceClient, n int) error {
	for i := 0; i < n; i++ {
		_, err := client.Produce(ctx, &pb.ProduceRequest{
			Message: &pb.TelemetryMessage{
				Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
				MetricName: "DCGM_FI_DEV_GPU_UTIL",
				GpuId:      "0",
				Uuid:       fmt.Sprintf("GPU-smoke-%04d", i),
				Value:      float64(i),
			},
		})
		if err != nil {
			return fmt.Errorf("produce %d/%d: %w", i+1, n, err)
		}
	}
	return nil
}

// consume opens a Consume server-stream and drains n messages from it.
func consume(ctx context.Context, client pb.MQServiceClient, n int) (int, error) {
	stream, err := client.Consume(ctx, &pb.ConsumeRequest{ConsumerId: "smoke"})
	if err != nil {
		return 0, fmt.Errorf("open consume stream: %w", err)
	}
	return drain(stream, n)
}

// drain reads exactly n messages off the stream, validating each carries a
// metric name. Returns the number successfully received.
func drain(stream pb.MQService_ConsumeClient, n int) (int, error) {
	got := 0
	for got < n {
		msg, err := stream.Recv()
		if err != nil {
			return got, fmt.Errorf("recv after %d/%d: %w", got, n, err)
		}
		if msg.GetMetricName() == "" {
			return got, fmt.Errorf("received message with empty metric_name after %d/%d", got, n)
		}
		got++
	}
	return got, nil
}
