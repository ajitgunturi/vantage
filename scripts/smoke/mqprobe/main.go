// Command mqprobe is a minimal gRPC client used by the Phase 1 smoke check
// (scripts/smoke/phase01-mq.sh). It exercises the MQ data plane end-to-end
// over the gRPC boundary, in pure Go so the manual smoke suite needs no
// grpcurl/protoc install — only the Go toolchain that the repo already requires.
//
// Modes (-mode):
//
//	both     (default) Open a bidi Consume stream, then Produce N — the
//	         consumer-already-attached path. Verifies all N stream back and acks each.
//	produce  Produce N messages and exit, leaving them buffered in the MQ.
//	consume  Attach a bidi Consume stream, receive N messages and ack each by
//	         broker id, then exit.
//
// As of Phase 01.1 Consume is a bidirectional at-least-once stream (MQ-09, MQ-10):
// the client first sends a ConsumeClientMsg carrying its credit window, then acks
// every received message by its broker-assigned id. -mode consume no longer peeks-
// and-discards; it acks what it reads, so the broker removes those messages from
// custody.
//
// Running -mode produce in one invocation and -mode consume in a later one
// exercises the late-join path: the producer publishes and disconnects, and a
// consumer that attaches afterwards still drains (and acks) the buffered messages.
//
// Usage:
//
//	go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:55051 -n 20
//	go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:55051 -n 20 -mode produce
//	go run ./scripts/smoke/mqprobe -grpc 127.0.0.1:55051 -n 20 -mode consume -credit 20
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
	credit := flag.Int("credit", 20, "initial flow-control credit (bidi consume window)")
	timeout := flag.Duration("timeout", 10*time.Second, "overall deadline")
	flag.Parse()

	if err := run(*addr, *n, *credit, *mode, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "mqprobe: FAIL: %v\n", err)
		os.Exit(1)
	}
}

func run(addr string, n, credit int, mode string, timeout time.Duration) error {
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
		return runBoth(ctx, client, n, credit, addr)
	case "produce":
		if err := produce(ctx, client, n); err != nil {
			return err
		}
		fmt.Printf("mqprobe: OK — produced %d (left buffered) via %s\n", n, addr)
		return nil
	case "consume":
		got, err := consume(ctx, client, n, credit)
		if err != nil {
			return err
		}
		fmt.Printf("mqprobe: OK — consumed %d via %s\n", got, addr)
		return nil
	default:
		return fmt.Errorf("unknown -mode %q (want both|produce|consume)", mode)
	}
}

// runBoth opens the bidi Consume stream first — mirrors a Collector connecting
// before the Streamer publishes — sends its initial credit, then Produces N
// concurrently, verifying all N stream back and acking each. Consumer-attached path.
func runBoth(ctx context.Context, client pb.MQServiceClient, n, credit int, addr string) error {
	stream, err := openConsume(ctx, client, credit)
	if err != nil {
		return err
	}

	received := make(chan error, 1)
	go func() {
		_, err := recvAndAck(stream, n)
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

	_ = stream.CloseSend()
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

// consume opens a bidi Consume stream, grants initial credit, then receives and
// acks n messages by broker id.
func consume(ctx context.Context, client pb.MQServiceClient, n, credit int) (int, error) {
	stream, err := openConsume(ctx, client, credit)
	if err != nil {
		return 0, err
	}
	got, err := recvAndAck(stream, n)
	if err != nil {
		return got, err
	}
	_ = stream.CloseSend()
	return got, nil
}

// openConsume opens the bidirectional Consume stream and sends the initial credit
// message (the flow-control handshake the broker requires before any message flows).
func openConsume(ctx context.Context, client pb.MQServiceClient, credit int) (pb.MQService_ConsumeClient, error) {
	stream, err := client.Consume(ctx)
	if err != nil {
		return nil, fmt.Errorf("open consume stream: %w", err)
	}
	if err := stream.Send(&pb.ConsumeClientMsg{Credit: int32(credit), ConsumerId: "smoke"}); err != nil {
		return nil, fmt.Errorf("send initial credit: %w", err)
	}
	return stream, nil
}

// recvAndAck receives exactly n messages, validating each carries a metric name,
// and acks each by its broker-assigned id so the broker removes it from custody
// (at-least-once). Returns the number successfully received and acked.
func recvAndAck(stream pb.MQService_ConsumeClient, n int) (int, error) {
	got := 0
	for got < n {
		msg, err := stream.Recv()
		if err != nil {
			return got, fmt.Errorf("recv after %d/%d: %w", got, n, err)
		}
		if msg.GetMetricName() == "" {
			return got, fmt.Errorf("received message with empty metric_name after %d/%d", got, n)
		}
		if err := stream.Send(&pb.ConsumeClientMsg{AckId: msg.GetId()}); err != nil {
			return got, fmt.Errorf("ack id %d after %d/%d: %w", msg.GetId(), got, n, err)
		}
		got++
	}
	return got, nil
}
