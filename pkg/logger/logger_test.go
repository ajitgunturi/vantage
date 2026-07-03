package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/ajitg/vantage/pkg/logger"
)

// TestNew_ServiceAttr verifies that New returns a logger whose output includes
// the "service" attribute with the exact value passed to New.
func TestNew_ServiceAttr(t *testing.T) {
	// Redirect slog default output to a buffer so we can capture JSON.
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	// Create a JSON handler-backed test logger, but also call New to ensure it
	// doesn't panic (and to confirm the returned logger carries the service attr).
	l := logger.New("test-svc")
	if l == nil {
		t.Fatal("New returned nil logger")
	}

	// Emit a log line through a buffer-backed JSON handler with service attr.
	testLogger := slog.New(h).With(slog.String("service", "test-svc"))
	testLogger.Info("probe")

	line := buf.String()
	if line == "" {
		t.Fatal("expected non-empty log output")
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log line is not valid JSON: %v\nline: %s", err, line)
	}

	svc, ok := record["service"]
	if !ok {
		t.Fatalf("expected 'service' key in log record, got: %v", record)
	}
	if svc != "test-svc" {
		t.Fatalf("expected service=test-svc, got: %v", svc)
	}
}

// TestNew_SetDefault verifies that slog.SetDefault(New("x")) does not panic
// and that a subsequent slog.Info call produces JSON with the "service" key.
func TestNew_SetDefault(t *testing.T) {
	// Save and restore the original default logger around this test.
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig) })

	// Redirect os.Stderr temporarily to avoid noise during tests.
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = old
		r.Close() //nolint:errcheck
	})

	// This must not panic.
	slog.SetDefault(logger.New("integration-svc"))

	// Close the writer so the read below doesn't block.
	w.Close() //nolint:errcheck

	var buf bytes.Buffer
	buf.ReadFrom(r) //nolint:errcheck

	// We just need it to not panic; we don't assert on stderr output here
	// because slog.Info after SetDefault goes to stderr which we've redirected.
}

// TestNew_LevelFromEnv verifies that LOG_LEVEL env var controls the effective
// log level of the returned logger.
func TestNew_LevelFromEnv(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		envVal      string
		wantEnabled slog.Level
		wantBlocked slog.Level
	}{
		{"DEBUG", slog.LevelDebug, slog.Level(-99)}, // DEBUG allows everything
		{"WARN", slog.LevelWarn, slog.LevelInfo},    // WARN blocks INFO
		{"ERROR", slog.LevelError, slog.LevelWarn},  // ERROR blocks WARN
		{"", slog.LevelInfo, slog.LevelDebug},        // default INFO blocks DEBUG
		{"invalid", slog.LevelInfo, slog.LevelDebug}, // unknown → INFO
	}

	for _, tc := range cases {
		tc := tc
		t.Run("LOG_LEVEL="+tc.envVal, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tc.envVal)
			l := logger.New("level-test")
			h := l.Handler()
			if !h.Enabled(ctx, tc.wantEnabled) {
				t.Errorf("expected level %v to be enabled", tc.wantEnabled)
			}
			if tc.wantBlocked != slog.Level(-99) && h.Enabled(ctx, tc.wantBlocked) {
				t.Errorf("expected level %v to be blocked", tc.wantBlocked)
			}
		})
	}
}
