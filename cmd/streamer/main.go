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
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/ajitg/vantage/internal/streamer"
	pkglogger "github.com/ajitg/vantage/pkg/logger"
)

func main() {
	l := pkglogger.New("streamer")
	slog.SetDefault(l)

	cfg := streamer.FromEnv()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	slog.Info("starting", "mq_addr", cfg.MQAddr, "csv", cfg.CSVPath, "loop_delay_ms", cfg.LoopDelayMS)

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
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}
