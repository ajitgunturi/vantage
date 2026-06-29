// cmd/streamer is the entrypoint for the Streamer microservice.
// It reads DCGM GPU metrics from a CSV file in an infinite loop, restamps each
// row at RFC3339Nano precision, and publishes each record to the MQ via the gRPC
// Produce client.
//
// Configuration is driven by environment variables (see internal/streamer.FromEnv):
//
//	STREAMER_MQ_ADDR       MQ gRPC address (default :50051)
//	STREAMER_CSV_PATH      Path to DCGM metrics CSV (required)
//	STREAMER_LOOP_DELAY_MS Inter-row sleep in ms (default 1)
package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/ajitg/vantage/internal/streamer"
)

func main() {
	cfg := streamer.FromEnv()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log.Printf("streamer: MQ addr %s, CSV %s, loop delay %dms",
		cfg.MQAddr, cfg.CSVPath, cfg.LoopDelayMS)

	g, gctx := errgroup.WithContext(ctx)

	// Stream goroutine — calls Run which loops forever until ctx is cancelled.
	g.Go(func() error {
		return streamer.Run(gctx, cfg)
	})

	// Shutdown coordination — waits for the context to be done.
	// streamer.Run detects ctx.Err() internally and exits cleanly; no teardown needed.
	g.Go(func() error {
		<-gctx.Done()
		return nil
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
