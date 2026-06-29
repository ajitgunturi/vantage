// Package db_test holds non-integration (unit) tests for pkg/db.
// These tests exercise FromEnv and the error paths of New and Migrate
// that do not require a running database.
//
// The integration counterpart (db_test.go) is guarded by //go:build integration
// and requires Docker. These tests have no build tag and run under plain
// `go test` / `make test`.
package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/pkg/db"
)

// TestFromEnv exercises all branches of db.FromEnv without a database.
func TestFromEnv(t *testing.T) {
	t.Run("missing DSN returns error", func(t *testing.T) {
		t.Setenv("VANTAGE_DB_DSN", "")
		t.Setenv("VANTAGE_DB_MAX_CONNS", "")

		_, err := db.FromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VANTAGE_DB_DSN is required",
			"error must describe the missing variable, not leak a credential")
	})

	t.Run("DSN only, no MaxConns — defaults to zero", func(t *testing.T) {
		t.Setenv("VANTAGE_DB_DSN", "postgres://user:pass@localhost:5432/vantage")
		t.Setenv("VANTAGE_DB_MAX_CONNS", "")

		cfg, err := db.FromEnv()
		require.NoError(t, err)
		assert.Equal(t, "postgres://user:pass@localhost:5432/vantage", cfg.DSN)
		assert.Equal(t, int32(0), cfg.MaxConns,
			"unset VANTAGE_DB_MAX_CONNS must default to 0 (pgxpool built-in default)")
	})

	t.Run("valid MaxConns is parsed and stored", func(t *testing.T) {
		t.Setenv("VANTAGE_DB_DSN", "postgres://user:pass@localhost:5432/vantage")
		t.Setenv("VANTAGE_DB_MAX_CONNS", "20")

		cfg, err := db.FromEnv()
		require.NoError(t, err)
		assert.Equal(t, int32(20), cfg.MaxConns)
	})

	t.Run("non-integer MaxConns returns error", func(t *testing.T) {
		t.Setenv("VANTAGE_DB_DSN", "postgres://user:pass@localhost:5432/vantage")
		t.Setenv("VANTAGE_DB_MAX_CONNS", "not-a-number")

		_, err := db.FromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VANTAGE_DB_MAX_CONNS",
			"error must identify which variable failed to parse")
	})

	t.Run("MaxConns=0 is accepted (pgxpool built-in default behaviour)", func(t *testing.T) {
		t.Setenv("VANTAGE_DB_DSN", "postgres://user:pass@localhost:5432/vantage")
		t.Setenv("VANTAGE_DB_MAX_CONNS", "0")

		cfg, err := db.FromEnv()
		require.NoError(t, err)
		assert.Equal(t, int32(0), cfg.MaxConns)
	})
}

// TestNew_ParseConfigError verifies that New returns a wrapped "db: parse config:" error
// when the DSN contains an invalid port (99999 > 65535, out of range for uint16).
// pgxpool.ParseConfig validates the port at parse time — no network call is made.
func TestNew_ParseConfigError(t *testing.T) {
	_, err := db.New(context.Background(), db.Config{
		DSN: "postgres://user:pass@localhost:99999/vantage",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: parse config:",
		"error must be wrapped with the expected prefix from New")
}

// TestNew_PingError verifies that New closes the pool and returns a wrapped error when
// the database is unreachable. A short deadline is passed so the test completes in
// milliseconds: the deadline propagates to the internal ping context inside New.
//
// Port 1 on loopback is reserved (requires root to bind); in practice it is either
// closed (instant ECONNREFUSED) or absent. A 200 ms deadline guarantees the test
// never waits the full 5 s internal timeout even if the port were open but unresponsive.
func TestNew_PingError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := db.New(ctx, db.Config{DSN: "postgres://user:pass@127.0.0.1:1/vantage"})
	require.Error(t, err)
	// Both a ping error and a context-deadline error are valid — either means the
	// pool was properly closed and the error was surfaced.
	pingFailed := strings.Contains(err.Error(), "db: ping:")
	ctxExpired := strings.Contains(err.Error(), "context deadline exceeded")
	assert.True(t, pingFailed || ctxExpired,
		"expected a ping or deadline error from New, got: %v", err)
}

// TestMigrate_InvalidDSNError verifies that Migrate returns a wrapped
// "db: migrate init:" error when the DSN is invalid at the config-parse
// level (port 99999 > 65535). The golang-migrate pgx/v5 driver calls
// pgconn.ParseConfig internally, which rejects the DSN before any dial.
func TestMigrate_InvalidDSNError(t *testing.T) {
	err := db.Migrate(context.Background(),
		"postgres://user:pass@localhost:99999/vantage")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db: migrate init:",
		"error must be wrapped with the expected prefix from Migrate")
}
