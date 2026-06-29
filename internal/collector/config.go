// Package collector implements the Collector microservice: it holds a long-lived
// bidi gRPC Consume stream to the MQ, auto-reconnects on drop, batches received
// TelemetryMessages, and upserts them to PostgreSQL via pgxpool.SendBatch with
// ON CONFLICT DO NOTHING for idempotent at-least-once delivery (COLL-01..05).
//
// Architecture note: service logic lives here in internal/collector so that
// cmd/collector/main.go is a thin wiring layer and the exported Consume seam
// is importable by plan 03-04 E2E tests.
package collector

import (
	"os"
	"strconv"
)

// Config holds runtime configuration for the Collector microservice.
// Construct via FromEnv or set fields directly for tests.
type Config struct {
	// MQAddr is the gRPC address of the MQ server (host:port).
	// Set from COLLECTOR_MQ_ADDR; default ":50051".
	MQAddr string

	// BatchSize is the maximum number of messages to accumulate before a
	// size-triggered flush (persist + ack).
	// Set from COLLECTOR_BATCH_SIZE; default 50.
	BatchSize int

	// FlushMS is the ticker interval in milliseconds for time-triggered flushes.
	// A non-empty batch is flushed when either BatchSize or FlushMS is reached,
	// whichever fires first.
	// Set from COLLECTOR_FLUSH_MS; default 500.
	FlushMS int

	// Credit is the initial in-flight window sent to the MQ broker on the first
	// ConsumeClientMsg. Must be >= BatchSize to avoid stalling the stream.
	// Default is 100 (2 × default BatchSize).
	// Set from COLLECTOR_CREDIT.
	Credit int
}

// FromEnv builds a Config from environment variables. Invalid or missing
// integer values fall back to the defaults silently (matches the pattern in
// internal/config/config.go). DSN absence is NOT validated here — that is
// the responsibility of pkg/db.FromEnv.
func FromEnv() Config {
	cfg := Config{
		MQAddr:    ":50051",
		BatchSize: 50,
		FlushMS:   500,
		Credit:    100, // 2 × default BatchSize — must be >= BatchSize
	}
	if v := os.Getenv("COLLECTOR_MQ_ADDR"); v != "" {
		cfg.MQAddr = v
	}
	if v := os.Getenv("COLLECTOR_BATCH_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BatchSize = n
		}
	}
	if v := os.Getenv("COLLECTOR_FLUSH_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FlushMS = n
		}
	}
	if v := os.Getenv("COLLECTOR_CREDIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Credit = n
		}
	}
	return cfg
}
