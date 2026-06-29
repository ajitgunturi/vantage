package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// scheme
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// New opens and validates a pgxpool.Pool from the provided Config.
//
// It uses pgxpool.ParseConfig + pgxpool.NewWithConfig (not bare pgxpool.New)
// so MaxConns and HealthCheckPeriod can be tuned independently of the DSN.
// A Ping is issued under a 5-second sub-context to fail fast if the database
// is unreachable (Pitfall 6: bare background contexts block until OS TCP timeout).
//
// On Ping failure the pool is closed and a wrapped error is returned.
// The DSN is never included in any error string.
func New(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	poolCfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}

	// pgxpool.NewWithConfig does NOT open connections — ping to verify reachability.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	return pool, nil
}

// Migrate applies all pending forward migrations from the embedded migrations/
// directory to the database at dsn. It is idempotent: if the schema is already
// at the latest version, migrate.ErrNoChange is treated as success.
//
// golang-migrate holds a PostgreSQL advisory lock for the duration of the run,
// so concurrent calls (e.g. Collector and Gateway starting simultaneously) are
// safe — only one runner applies migrations; others wait and then see ErrNoChange.
//
// The DSN is converted from postgres:// to pgx5:// for the golang-migrate pgx/v5
// driver (Pitfall 3: the pgx/v5 driver is registered under the "pgx5" scheme,
// not "postgres").
//
// Only forward (Up) migrations are applied programmatically. The down.sql files
// are embedded for completeness but are never auto-applied — rolling back requires
// an explicit operator action.
func Migrate(ctx context.Context, dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("db: iofs source: %w", err)
	}

	// Convert postgres:// (pgx pool DSN) to pgx5:// (golang-migrate driver scheme).
	pgx5DSN := strings.Replace(dsn, "postgres://", "pgx5://", 1)

	m, err := migrate.NewWithSourceInstance("iofs", src, pgx5DSN)
	if err != nil {
		return fmt.Errorf("db: migrate init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: migrate up: %w", err)
	}

	return nil
}
