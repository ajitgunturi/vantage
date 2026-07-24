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
//	STREAMER_HEALTH_ADDR   HTTP health listener address (default :9000)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	slog.Info("starting", "mq_addr", cfg.MQAddr, "csv", cfg.CSVPath, "loop_delay_ms", cfg.LoopDelayMS, "health_addr", cfg.HealthAddr)

	runner := streamer.NewRunner(cfg)

	g, gctx := errgroup.WithContext(ctx)

	// (1) Stream goroutine — runs the CSV→MQ loop until ctx is cancelled.
	g.Go(func() error {
		return runner.Run(gctx)
	})

	// (2) Health HTTP listener — /healthz (liveness) and /readyz (readiness).
	// Readiness reflects streaming state via runner.IsReady() (OBS-01).
	// Bodies return only a status field (T-06-08: no internal state leaked).
	g.Go(func() error {
		healthMux := http.NewServeMux()
		healthMux.Handle("GET /metrics", streamer.MetricsHandler())
		healthMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
		})
		healthMux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
			if !runner.IsReady() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"status":"not_ready"}`)) //nolint:errcheck
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
		})
		healthSrv := &http.Server{
			Addr:         cfg.HealthAddr,
			Handler:      healthMux,
			ReadTimeout:  2 * time.Second,
			WriteTimeout: 2 * time.Second,
		}
		// Shut down the health server when the errgroup context is done.
		go func() { <-gctx.Done(); healthSrv.Shutdown(context.Background()) }() //nolint:errcheck
		slog.Info("streamer health listening", "addr", cfg.HealthAddr)
		if err := healthSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	// (3) Shutdown coordination — waits for context to be done.
	// streamer.Runner.Run detects ctx.Err() internally and exits cleanly.
	g.Go(func() error {
		<-gctx.Done()
		return nil
	})

	if err := g.Wait(); err != nil &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, http.ErrServerClosed) {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}
