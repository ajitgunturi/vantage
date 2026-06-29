// Package collector_test provides unit tests for the collector configuration.
// These tests have no build tag and run with go test ./... (no integration tag needed).
package collector_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/collector"
)

// TestFromEnvDefaults verifies that FromEnv returns the correct defaults when
// no environment variables are set.
func TestFromEnvDefaults(t *testing.T) {
	// Ensure all collector env vars are unset for this test.
	for _, key := range []string{
		"COLLECTOR_MQ_ADDR",
		"COLLECTOR_BATCH_SIZE",
		"COLLECTOR_FLUSH_MS",
		"COLLECTOR_CREDIT",
	} {
		t.Setenv(key, "")
	}

	cfg := collector.FromEnv()

	require.Equal(t, ":50051", cfg.MQAddr, "default MQAddr must be :50051")
	require.Equal(t, 50, cfg.BatchSize, "default BatchSize must be 50")
	require.Equal(t, 500, cfg.FlushMS, "default FlushMS must be 500")
	require.Equal(t, 100, cfg.Credit, "default Credit must be 100 (2 × BatchSize)")
	require.GreaterOrEqual(t, cfg.Credit, cfg.BatchSize,
		"Credit must always be >= BatchSize to avoid stalling the stream")
}

// TestFromEnvOverrides verifies that each COLLECTOR_* environment variable is
// picked up correctly when set to a valid value.
func TestFromEnvOverrides(t *testing.T) {
	t.Setenv("COLLECTOR_MQ_ADDR", "mq-svc:50052")
	t.Setenv("COLLECTOR_BATCH_SIZE", "25")
	t.Setenv("COLLECTOR_FLUSH_MS", "300")
	t.Setenv("COLLECTOR_CREDIT", "75")

	cfg := collector.FromEnv()

	require.Equal(t, "mq-svc:50052", cfg.MQAddr)
	require.Equal(t, 25, cfg.BatchSize)
	require.Equal(t, 300, cfg.FlushMS)
	require.Equal(t, 75, cfg.Credit)
}

// TestFromEnvInvalidIntegers verifies that invalid integer env var values are
// silently ignored and the defaults are used instead (matches the silent-default
// convention from internal/config/config.go).
func TestFromEnvInvalidIntegers(t *testing.T) {
	t.Setenv("COLLECTOR_BATCH_SIZE", "not-a-number")
	t.Setenv("COLLECTOR_FLUSH_MS", "-10") // negative is not > 0
	t.Setenv("COLLECTOR_CREDIT", "abc")

	cfg := collector.FromEnv()

	require.Equal(t, 50, cfg.BatchSize, "invalid BATCH_SIZE must fall back to default 50")
	require.Equal(t, 500, cfg.FlushMS, "negative FLUSH_MS must fall back to default 500")
	require.Equal(t, 100, cfg.Credit, "invalid CREDIT must fall back to default 100")
}

// TestFromEnvPartialOverride verifies that only the variables that are set get
// overridden; unset variables retain their defaults.
func TestFromEnvPartialOverride(t *testing.T) {
	// Only override MQAddr; leave others at default.
	t.Setenv("COLLECTOR_MQ_ADDR", "custom-mq:9090")
	// Ensure the others are unset.
	os.Unsetenv("COLLECTOR_BATCH_SIZE") //nolint:errcheck
	os.Unsetenv("COLLECTOR_FLUSH_MS")   //nolint:errcheck
	os.Unsetenv("COLLECTOR_CREDIT")     //nolint:errcheck

	cfg := collector.FromEnv()

	require.Equal(t, "custom-mq:9090", cfg.MQAddr)
	require.Equal(t, 50, cfg.BatchSize, "unset BATCH_SIZE must keep default")
	require.Equal(t, 500, cfg.FlushMS, "unset FLUSH_MS must keep default")
	require.Equal(t, 100, cfg.Credit, "unset CREDIT must keep default")
}
