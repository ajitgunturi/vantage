// Package logger provides a shared structured logging helper for all vantage
// microservices. It wraps log/slog with a JSON handler and a per-service
// "service" attribute so log lines from different binaries are distinguishable.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New creates a JSON slog.Logger writing to os.Stderr with the given service
// name attached as a "service" attribute on every log record.
//
// Log level is read from the LOG_LEVEL environment variable
// (DEBUG/INFO/WARN/ERROR, case-insensitive). Any unknown or unset value
// defaults to slog.LevelInfo.
//
// Typical usage in cmd/*/main.go (first two statements of main):
//
//	l := logger.New("mq")
//	slog.SetDefault(l)
func New(service string) *slog.Logger {
	var level slog.Level
	switch strings.ToUpper(os.Getenv("LOG_LEVEL")) {
	case "DEBUG":
		level = slog.LevelDebug
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return slog.New(h).With(slog.String("service", service))
}
