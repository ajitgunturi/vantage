// Package config provides env-first configuration for the MQ service.
// All settings have sensible defaults and can be overridden via environment variables.
package config

import (
	"os"
	"strconv"

	"github.com/ajitg/vantage/internal/queue"
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
	// ConsumeCredit is the initial in-flight message window granted to each
	// consumer when a bidi Consume stream opens (MQ-10 / D-07, D-08).
	// The engine applies this default when a client's first credit message
	// carries a value <= 0. Env: MQ_CONSUME_CREDIT (default 20; non-positive
	// or non-numeric values are silently ignored and the default is kept).
	ConsumeCredit int
	// OverflowPolicy selects Enqueue behavior at a full capacity budget:
	// "drop-oldest" (default — telemetry freshness-first: the oldest reading is
	// the least valuable under overload; evictions are counted), "reject"
	// (lossless backpressure via ResourceExhausted), or "block" (bounded
	// producer wait). Env: MQ_OVERFLOW_POLICY (invalid values are ignored).
	OverflowPolicy string
	// BlockTimeoutMS bounds the "block" policy's producer wait in milliseconds.
	// Env: MQ_BLOCK_TIMEOUT_MS (default 1000; non-positive values ignored).
	BlockTimeoutMS int
	// MaxDeliveries dead-letters a message after this many delivery attempts
	//. Env: MQ_MAX_DELIVERIES (default 5).
	MaxDeliveries int
	// DLQCapacity bounds the dead-letter lane.
	// Env: MQ_DLQ_CAPACITY (default 1000).
	DLQCapacity int
	// RetryBackoffBaseMS / RetryBackoffMaxMS shape the exponential redelivery
	// visibility backoff. Env: MQ_RETRY_BACKOFF_BASE_MS (default 500),
	// MQ_RETRY_BACKOFF_MAX_MS (default 30000).
	RetryBackoffBaseMS int
	RetryBackoffMaxMS  int
	// LeaseTTLMS is the per-lease deadline before the sweeper reclaims an
	// unacked delivery. Env: MQ_LEASE_TTL_MS (default 30000).
	LeaseTTLMS int
	// DrainTimeoutMS bounds the SIGTERM preStop drain: how long the broker
	// keeps serving consumers (while refusing producers) to clear the backlog
	// before exiting. Must fit inside the pod's
	// terminationGracePeriodSeconds. Env: MQ_DRAIN_TIMEOUT_MS (default 20000).
	DrainTimeoutMS int
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
		GRPCAddr:       ":50051",
		HTTPAddr:       ":8080",
		BufferSize:     10000,
		ConsumeCredit:  20,
		OverflowPolicy: string(queue.PolicyDropOldest),
		BlockTimeoutMS: 1000,

		MaxDeliveries:      5,
		DLQCapacity:        1000,
		RetryBackoffBaseMS: 500,
		RetryBackoffMaxMS:  30000,
		LeaseTTLMS:         30000,
		DrainTimeoutMS:     20000,
	}

	intEnv := func(name string, dst *int) {
		if v := os.Getenv(name); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				*dst = n
			}
		}
	}
	intEnv("MQ_MAX_DELIVERIES", &cfg.MaxDeliveries)
	intEnv("MQ_DLQ_CAPACITY", &cfg.DLQCapacity)
	intEnv("MQ_RETRY_BACKOFF_BASE_MS", &cfg.RetryBackoffBaseMS)
	intEnv("MQ_RETRY_BACKOFF_MAX_MS", &cfg.RetryBackoffMaxMS)
	intEnv("MQ_LEASE_TTL_MS", &cfg.LeaseTTLMS)
	intEnv("MQ_DRAIN_TIMEOUT_MS", &cfg.DrainTimeoutMS)

	if v := os.Getenv("MQ_BUFFER_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BufferSize = n
		}
	}
	if v := os.Getenv("MQ_CONSUME_CREDIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ConsumeCredit = n
		}
	}
	if v := os.Getenv("MQ_OVERFLOW_POLICY"); v != "" {
		if p, err := queue.ParseOverflowPolicy(v); err == nil {
			cfg.OverflowPolicy = string(p)
		}
	}
	if v := os.Getenv("MQ_BLOCK_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BlockTimeoutMS = n
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
