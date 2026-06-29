// Package streamer implements the CSV-to-MQ telemetry streaming service.
// It reads the DCGM CSV in an infinite loop, restamps each row at RFC3339Nano
// precision, and publishes each record to the MQ via the generated gRPC Produce
// client.
package streamer

import (
	"os"
	"strconv"
)

// Config holds the runtime configuration for the Streamer service.
// All fields are populated by FromEnv with defaults; CSVPath must be set by the
// operator and is validated in Run (no default — a missing CSV path is a fatal
// misconfiguration, not a default-able value).
type Config struct {
	// MQAddr is the TCP address of the MQ gRPC listener.
	// Env: STREAMER_MQ_ADDR (default ":50051").
	MQAddr string
	// CSVPath is the path to the DCGM metrics CSV file.
	// Env: STREAMER_CSV_PATH (required; validated in Run, not in FromEnv).
	CSVPath string
	// LoopDelayMS is the inter-row sleep in milliseconds between successive Produce
	// calls, bounding publish rate across up to 10 concurrent instances (T-03-02b).
	// Env: STREAMER_LOOP_DELAY_MS (default 1). Zero disables the delay.
	// Negative values and non-numeric values are treated as the default.
	LoopDelayMS int
}

// FromEnv constructs a Config from environment variables, applying defaults for
// any unset or invalid values. It has no side effects beyond os.Getenv calls.
//
// Environment variables:
//
//	STREAMER_MQ_ADDR       (default ":50051")
//	STREAMER_CSV_PATH      (no default; validated in Run)
//	STREAMER_LOOP_DELAY_MS (default 1; invalid/negative silently keeps default)
func FromEnv() Config {
	cfg := Config{
		MQAddr:      ":50051",
		LoopDelayMS: 1,
	}
	if v := os.Getenv("STREAMER_MQ_ADDR"); v != "" {
		cfg.MQAddr = v
	}
	if v := os.Getenv("STREAMER_CSV_PATH"); v != "" {
		cfg.CSVPath = v
	}
	if v := os.Getenv("STREAMER_LOOP_DELAY_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.LoopDelayMS = n
		}
	}
	return cfg
}
