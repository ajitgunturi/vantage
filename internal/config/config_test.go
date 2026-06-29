package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/config"
)

func TestFromEnv_Defaults(t *testing.T) {
	// Ensure env vars are clear.
	t.Setenv("MQ_GRPC_ADDR", "")
	t.Setenv("MQ_HTTP_ADDR", "")
	t.Setenv("MQ_BUFFER_SIZE", "")
	t.Setenv("MQ_CONSUME_CREDIT", "")

	cfg := config.FromEnv()

	require.Equal(t, ":50051", cfg.GRPCAddr)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, 10000, cfg.BufferSize)
	require.Equal(t, 1024, cfg.WorkChCap)
	require.Equal(t, 20, cfg.ConsumeCredit, "ConsumeCredit default must be 20")
}

func TestFromEnv_Overrides(t *testing.T) {
	t.Setenv("MQ_GRPC_ADDR", ":9090")
	t.Setenv("MQ_HTTP_ADDR", ":9091")
	t.Setenv("MQ_BUFFER_SIZE", "5000")

	cfg := config.FromEnv()

	require.Equal(t, ":9090", cfg.GRPCAddr)
	require.Equal(t, ":9091", cfg.HTTPAddr)
	require.Equal(t, 5000, cfg.BufferSize)
	require.Equal(t, 500, cfg.WorkChCap) // max(5000/10, 128) = 500
}

func TestFromEnv_InvalidBufferSize_Ignored(t *testing.T) {
	t.Setenv("MQ_BUFFER_SIZE", "not-a-number")

	cfg := config.FromEnv()

	require.Equal(t, 10000, cfg.BufferSize, "invalid MQ_BUFFER_SIZE should fall back to default")
}

func TestFromEnv_ZeroBufferSize_Ignored(t *testing.T) {
	t.Setenv("MQ_BUFFER_SIZE", "0")

	cfg := config.FromEnv()

	require.Equal(t, 10000, cfg.BufferSize, "non-positive MQ_BUFFER_SIZE should fall back to default")
}

func TestFromEnv_SmallBufferSize_WorkChCap_Floor(t *testing.T) {
	// 100/10 = 10, which is < 128, so WorkChCap should be clamped to 128.
	t.Setenv("MQ_BUFFER_SIZE", "100")

	cfg := config.FromEnv()

	require.Equal(t, 100, cfg.BufferSize)
	require.Equal(t, 128, cfg.WorkChCap, "WorkChCap must be at least 128")
}

func TestFromEnv_ConsumeCredit_Override(t *testing.T) {
	t.Setenv("MQ_CONSUME_CREDIT", "50")

	cfg := config.FromEnv()

	require.Equal(t, 50, cfg.ConsumeCredit, "valid MQ_CONSUME_CREDIT must override default")
}

func TestFromEnv_ConsumeCredit_Invalid_Ignored(t *testing.T) {
	t.Setenv("MQ_CONSUME_CREDIT", "not-a-number")

	cfg := config.FromEnv()

	require.Equal(t, 20, cfg.ConsumeCredit, "non-numeric MQ_CONSUME_CREDIT must keep default 20")
}

func TestFromEnv_ConsumeCredit_NonPositive_Ignored(t *testing.T) {
	for _, v := range []string{"0", "-1", "-100"} {
		t.Setenv("MQ_CONSUME_CREDIT", v)
		cfg := config.FromEnv()
		require.Equal(t, 20, cfg.ConsumeCredit,
			"non-positive MQ_CONSUME_CREDIT=%q must keep default 20", v)
	}
}
