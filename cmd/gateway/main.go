// @title       Vantage GPU Telemetry API
// @version     1.0
// @description Read API for the Vantage elastic GPU telemetry pipeline.
// @description Exposes sorted GPU IDs and time-series metric rows from PostgreSQL.
// @host        localhost:8080
// @BasePath    /api/v1
// @schemes     http

// cmd/gateway is the entrypoint for the API Gateway microservice.
// It wires the DB connection pool, gateway configuration, and the chi HTTP
// router into a single process managed by an errgroup. SIGTERM/SIGINT triggers
// graceful shutdown: in-flight requests complete, then the server closes.
//
// Security invariant: the DSN (which contains credentials) is read only from
// VANTAGE_DB_DSN and is NEVER logged or included in error strings (ASVS V8).
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

	"github.com/ajitg/vantage/internal/gateway"
	"github.com/ajitg/vantage/pkg/db"
	_ "github.com/ajitg/vantage/pkg/docs" // registers generated OpenAPI spec on init()
	pkglogger "github.com/ajitg/vantage/pkg/logger"
)

func main() {
	l := pkglogger.New("gateway")
	slog.SetDefault(l)

	// Build gateway config from environment (GATEWAY_ADDR, VANTAGE_GATEWAY_MAX_ROWS).
	cfg, err := gateway.FromEnv()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	// Build DB config from environment (VANTAGE_DB_DSN, VANTAGE_DB_MAX_CONNS).
	// DSN is never logged — only the error wrapper context is printed (ASVS V8).
	dbCfg, err := db.FromEnv()
	if err != nil {
		slog.Error("db config error", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown: cancel on SIGTERM or SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Apply forward migrations (idempotent; concurrent callers are safe via pg advisory lock).
	if err := db.Migrate(ctx, dbCfg.DSN); err != nil {
		slog.Error("migrate error", "error", err)
		os.Exit(1)
	}

	// Open the connection pool; close it on function exit.
	pool, err := db.New(ctx, dbCfg)
	if err != nil {
		slog.Error("db pool error", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Build the chi router (registers all API routes + Swagger UI).
	router := gateway.NewRouter(pool, cfg)

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	g, gctx := errgroup.WithContext(ctx)

	// (1) HTTP server goroutine.
	g.Go(func() error {
		slog.Info("listening", "addr", cfg.Addr)
		return srv.ListenAndServe()
	})

	// (2) Shutdown coordination goroutine — waits for signal then drains.
	g.Go(func() error {
		<-gctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	})

	if err := g.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}
