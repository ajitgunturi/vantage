// Package config provides env-first configuration for the MQ service.
// All settings have sensible defaults and can be overridden via environment variables.
package config

import (
	"os"
	"strconv"
)

// Config holds the runtime configuration for the MQ service. All fields are
// populated by FromEnv with defaults; no field should be zero after construction.
type Config struct {
	// GRPCAddr is the TCP address for the gRPC listener (MQ-01, MQ-02).
	GRPCAddr string
	// HTTPAddr is the TCP address for the HTTP control-plane listener (MQ-06).
	HTTPAddr string
	// BufferSize is the capacity of the in-memory ring buffer (MQ-04, MQ-05).
	BufferSize int
	// WorkChCap is the capacity of the work channel between dispatch and Consume goroutines.
	// Derived from BufferSize: max(BufferSize/10, 128).
	WorkChCap int
}

// FromEnv constructs a Config from environment variables, applying defaults
// for any unset or invalid values. It has no side effects beyond os.Getenv calls.
//
// Environment variables:
//   - MQ_GRPC_ADDR  (default :50051)
//   - MQ_HTTP_ADDR  (default :8080)
//   - MQ_BUFFER_SIZE (default 10000; must be a positive integer; invalid values are ignored)
func FromEnv() Config {
	cfg := Config{
		GRPCAddr:   ":50051",
		HTTPAddr:   ":8080",
		BufferSize: 10000,
		WorkChCap:  1024,
	}

	if v := os.Getenv("MQ_BUFFER_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BufferSize = n
			cfg.WorkChCap = max(n/10, 128)
		}
	}
	if v := os.Getenv("MQ_GRPC_ADDR"); v != "" {
		cfg.GRPCAddr = v
	}
	if v := os.Getenv("MQ_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}

	return cfg
}
