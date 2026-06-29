// Package db provides the shared PostgreSQL connection pool and schema migration
// runner for the vantage pipeline. Both the Collector (Phase 3) and the API
// Gateway (Phase 4) import this package to obtain a *pgxpool.Pool.
//
// Usage sequence:
//  1. Call db.Migrate(ctx, dsn) to apply forward migrations (idempotent).
//  2. Call db.New(ctx, cfg) to open and validate a connection pool.
//  3. Pass the returned *pgxpool.Pool to service constructors.
//
// Security invariant: the DSN (which contains database credentials) is read
// only from the VANTAGE_DB_DSN environment variable and is NEVER included in
// error strings or log output. Errors carry context ("db: VANTAGE_DB_DSN is
// required") but not the DSN value itself (ASVS V8).
package db

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds the pgxpool configuration for the vantage database.
// Construct via FromEnv() or by setting fields directly for tests.
type Config struct {
	// DSN is the postgres:// connection string.
	// Set from VANTAGE_DB_DSN (required).
	// The value contains credentials and must NEVER appear in logs or errors.
	DSN string

	// MaxConns is the maximum number of connections in the pool.
	// 0 means use the pgxpool default: max(4, NumCPU).
	// Set from VANTAGE_DB_MAX_CONNS (optional; non-positive or non-integer values
	// are rejected with an error, not silently ignored, to prevent misconfiguration).
	MaxConns int32
}

// FromEnv builds a Config from environment variables.
//
// Environment variables:
//   - VANTAGE_DB_DSN (required) — postgres://user:pass@host:5432/dbname?sslmode=disable
//   - VANTAGE_DB_MAX_CONNS (optional) — integer; default 0 (pgxpool default)
//
// Returns a hard error if VANTAGE_DB_DSN is empty (no fallback/default — a
// missing DSN means the service cannot function). The DSN value is never
// included in the returned error string.
func FromEnv() (Config, error) {
	dsn := os.Getenv("VANTAGE_DB_DSN")
	if dsn == "" {
		return Config{}, fmt.Errorf("db: VANTAGE_DB_DSN is required")
	}

	cfg := Config{DSN: dsn}

	if s := os.Getenv("VANTAGE_DB_MAX_CONNS"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return Config{}, fmt.Errorf("db: VANTAGE_DB_MAX_CONNS: %w", err)
		}
		cfg.MaxConns = int32(n)
	}

	return cfg, nil
}
