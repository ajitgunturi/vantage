// cmd/collector is the entrypoint for the Collector microservice.
// It wires DB configuration, migration, connection pool, and the collector
// reconnect loop. All service logic lives in internal/collector; this file is
// a thin composition root with no consume or persist logic.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	slog.Info("starting", "mq_addr", cfg.MQAddr, "batch", cfg.BatchSize, "flush_ms", cfg.FlushMS, "credit", cfg.Credit)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return collector.Run(gctx, cfg, pool)
	})
	g.Go(func() error {
		<-gctx.Done()
		return nil
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}
