package gateway_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/gateway"
)

// TestFromEnv exercises all branches of gateway.FromEnv, including defaults,
// custom values, and both invalid-value error paths.
func TestFromEnv(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("GATEWAY_ADDR", "")
		t.Setenv("VANTAGE_GATEWAY_MAX_ROWS", "")

		cfg, err := gateway.FromEnv()
		require.NoError(t, err)
		assert.Equal(t, ":8080", cfg.Addr)
		assert.Equal(t, 1000, cfg.MaxRows)
	})

	t.Run("custom_addr", func(t *testing.T) {
		t.Setenv("GATEWAY_ADDR", ":9090")
		t.Setenv("VANTAGE_GATEWAY_MAX_ROWS", "")

		cfg, err := gateway.FromEnv()
		require.NoError(t, err)
		assert.Equal(t, ":9090", cfg.Addr)
	})

	t.Run("custom_max_rows", func(t *testing.T) {
		t.Setenv("GATEWAY_ADDR", "")
		t.Setenv("VANTAGE_GATEWAY_MAX_ROWS", "500")

		cfg, err := gateway.FromEnv()
		require.NoError(t, err)
		assert.Equal(t, 500, cfg.MaxRows)
	})

	t.Run("invalid_max_rows_not_integer", func(t *testing.T) {
		t.Setenv("VANTAGE_GATEWAY_MAX_ROWS", "abc")

		_, err := gateway.FromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VANTAGE_GATEWAY_MAX_ROWS")
	})

	t.Run("invalid_max_rows_non_positive", func(t *testing.T) {
		t.Setenv("VANTAGE_GATEWAY_MAX_ROWS", "0")

		_, err := gateway.FromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VANTAGE_GATEWAY_MAX_ROWS")
	})

	t.Run("invalid_max_rows_negative", func(t *testing.T) {
		t.Setenv("VANTAGE_GATEWAY_MAX_ROWS", "-5")

		_, err := gateway.FromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VANTAGE_GATEWAY_MAX_ROWS")
	})
}
