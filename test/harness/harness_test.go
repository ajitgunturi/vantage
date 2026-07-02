//go:build e2e

// Package harness_test is the live-infrastructure E2E test harness (D-15..D-19).
//
// It programmatically owns the FULL five-image docker-compose stack
// (docker-compose.full.yml: postgres + migrate + mq + streamer + collector +
// gateway) via the testcontainers-go compose module and asserts pipeline
// correctness across real container boundaries: the streamer's fixture CSV
// flows through the MQ into PostgreSQL and out of the gateway REST API.
//
// Environment: DOCKER_HOST and TESTCONTAINERS_RYUK_DISABLED are expected to be
// set BEFORE running — the `make test-harness` target exports them (Rancher
// Desktop socket; Ryuk disabled per project convention). Do not hardcode the
// socket path here.
//
// Lifecycle (D-18): hermetic up → test → down, teardown even on failure.
// KEEP=1 leaves the stack running for debugging:
//
//	KEEP=1 make test-harness
//
// Kill/restart resilience scenarios are deliberately NOT here (Phase 6, QA-05).
package harness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/compose"
	"github.com/testcontainers/testcontainers-go/wait"
)

// gatewayBase is the host-mapped gateway endpoint (8090 -> container 8080 in
// docker-compose.full.yml; 8090 avoids clashing with mq's host-mapped 8080).
// Point the same suite at a kind/Helm deployment by overriding via env (D-16).
func gatewayBase() string {
	if v := os.Getenv("HARNESS_GATEWAY_BASE"); v != "" {
		return v
	}
	return "http://localhost:8090"
}

// TestHarness proves end-to-end pipeline correctness against the live compose
// stack (D-19): gateway serves GPU ids and telemetry rows that could only have
// arrived via CSV → streamer → MQ → collector → PostgreSQL.
func TestHarness(t *testing.T) {
	ctx := context.Background()

	skipStack := os.Getenv("HARNESS_GATEWAY_BASE") != "" // kind-pointable mode (D-16)

	if !skipStack {
		stack, err := compose.NewDockerComposeWith(
			compose.WithStackFiles("../../docker-compose.full.yml"),
			compose.StackIdentifier("vantage-harness"),
		)
		require.NoError(t, err, "compose.NewDockerComposeWith")

		// Teardown runs even on failure (D-18); KEEP=1 is the debug escape hatch.
		t.Cleanup(func() {
			if os.Getenv("KEEP") == "1" {
				t.Log("KEEP=1: leaving vantage-harness stack running for debugging")
				return
			}
			if derr := stack.Down(ctx,
				compose.RemoveOrphans(true),
				compose.RemoveVolumes(true),
			); derr != nil {
				t.Logf("stack.Down: %v", derr)
			}
		})

		err = stack.
			WaitForService("gateway", wait.ForListeningPort("8080/tcp")).
			Up(ctx, compose.Wait(true))
		require.NoError(t, err, "stack.Up")
	}

	base := gatewayBase()

	// Poll GET /api/v1/gpus until it returns 200 with a non-empty JSON array —
	// proof the streamer published, the collector persisted, and the gateway
	// reads rows (adapted from the pollUntilStable pattern in test/e2e).
	gpus := pollGPUs(t, base, 60*time.Second)
	require.NotEmpty(t, gpus, "gateway must return at least one GPU id")

	// Fetch one GPU's telemetry and assert rows came back.
	gpuID := gpus[0]
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/gpus/%s/telemetry", base, url.PathEscape(gpuID)))
	require.NoError(t, err, "GET telemetry")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET /api/v1/gpus/%s/telemetry must return 200", gpuID)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read telemetry body")
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(body, &rows), "telemetry must be a JSON array")
	require.NotEmpty(t, rows, "telemetry for %s must contain rows", gpuID)

	// Row-count growth (D-19): a second sample a moment later must be >= the
	// first — the streamer loops forever, so the pipeline keeps flowing.
	firstCount := len(rows)
	time.Sleep(3 * time.Second)
	resp2, err := http.Get(fmt.Sprintf("%s/api/v1/gpus/%s/telemetry", base, url.PathEscape(gpuID)))
	require.NoError(t, err)
	defer resp2.Body.Close()
	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)
	var rows2 []map[string]any
	require.NoError(t, json.Unmarshal(body2, &rows2))
	require.GreaterOrEqual(t, len(rows2), firstCount,
		"telemetry row count must not shrink while the pipeline runs")
}

// pollGPUs polls GET {base}/api/v1/gpus every second until it returns 200 with
// a non-empty JSON string array, or the deadline passes (then t.Fatal).
func pollGPUs(t *testing.T, base string, deadline time.Duration) []string {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		time.Sleep(1 * time.Second)
		resp, err := http.Get(base + "/api/v1/gpus")
		if err != nil {
			continue // stack still starting — retry
		}
		body, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		var gpus []string
		if json.Unmarshal(body, &gpus) != nil || len(gpus) == 0 {
			continue // rows not landed yet — retry
		}
		return gpus
	}
	t.Fatal("harness: timed out waiting for GET /api/v1/gpus to return a non-empty array — pipeline stalled")
	return nil
}
