// cmd/collector is the entrypoint for the Collector microservice.
// It wires DB configuration, migration, connection pool, and the collector
// reconnect loop. All service logic lives in internal/collector; this file is
// a thin composition root with no consume or persist logic.
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

	"github.com/ajitg/vantage/internal/collector"
	"github.com/ajitg/vantage/pkg/db"
	pkglogger "github.com/ajitg/vantage/pkg/logger"
)

func main() {
	l := pkglogger.New("collector")
	slog.SetDefault(l)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := collector.FromEnv()

	dbCfg, err := db.FromEnv()
	if err != nil {
		slog.Error("db config error", "error", err)
		os.Exit(1)
	}
	if err := db.Migrate(ctx, dbCfg.DSN); err != nil {
		slog.Error("migrate error", "error", err)
		os.Exit(1)
	}
	pool, err := db.New(ctx, dbCfg)
	if err != nil {
		slog.Error("db pool error", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	slog.Info("starting",
		"mq_addr", cfg.MQAddr,
		"batch", cfg.BatchSize,
		"flush_ms", cfg.FlushMS,
		"credit", cfg.Credit,
		"health_addr", cfg.HealthAddr,
	)

	runner := collector.NewRunner(cfg, pool)

	g, gctx := errgroup.WithContext(ctx)

	// Primary goroutine: consume loop with reconnect.
	g.Go(func() error {
		return runner.Run(gctx)
	})

	// Health goroutine: liveness + readiness probes on cfg.HealthAddr.
	// /healthz — always 200 (process is alive).
	// /readyz  — 200 once the MQ Consume stream is open; 503 before/during reconnect.
	// Response bodies carry only a status field (T-06-10: no DSN, MQ addr, or config echoed).
	g.Go(func() error {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
		})
		mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
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
			Handler:      mux,
			ReadTimeout:  2 * time.Second,
			WriteTimeout: 2 * time.Second,
		}
		// Shut down the health server when gctx is cancelled (SIGTERM/SIGINT or
		// primary goroutine error). Shutdown is non-blocking here — the goroutine
		// below returns after ListenAndServe exits.
		go func() { <-gctx.Done(); healthSrv.Shutdown(context.Background()) }() //nolint:errcheck
		if err := healthSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	// Shutdown coordinator: unblocks errgroup when ctx is cancelled.
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
