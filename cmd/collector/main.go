// cmd/collector is the entrypoint for the Collector microservice.
// It wires DB configuration, migration, connection pool, and the collector
// reconnect loop. All service logic lives in internal/collector; this file is
// a thin composition root with no consume or persist logic.
package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/ajitg/vantage/internal/collector"
	"github.com/ajitg/vantage/pkg/db"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := collector.FromEnv()

	dbCfg, err := db.FromEnv()
	if err != nil {
		log.Fatalf("collector: db config: %v", err)
	}
	if err := db.Migrate(ctx, dbCfg.DSN); err != nil {
		log.Fatalf("collector: migrate: %v", err)
	}
	pool, err := db.New(ctx, dbCfg)
	if err != nil {
		log.Fatalf("collector: db pool: %v", err)
	}
	defer pool.Close()

	log.Printf("collector: starting — MQ %s, batch %d, flush %dms, credit %d",
		cfg.MQAddr, cfg.BatchSize, cfg.FlushMS, cfg.Credit)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return collector.Run(gctx, cfg, pool)
	})
	g.Go(func() error {
		<-gctx.Done()
		return nil
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
