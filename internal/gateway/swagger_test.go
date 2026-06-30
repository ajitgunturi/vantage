package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ajitg/vantage/internal/gateway"
	_ "github.com/ajitg/vantage/pkg/docs" // registers generated OpenAPI spec on init()
)

// TestSwaggerUI verifies that the generated OpenAPI spec is served correctly
// by the gateway router (API-04 runtime proof).
//
// It checks:
//  1. GET /swagger/doc.json → 200 with a valid JSON body whose "paths" object
//     has >= 2 entries (gpus + telemetry endpoints documented).
//  2. GET /swagger/index.html → 200 (Swagger UI is mounted and reachable).
func TestSwaggerUI(t *testing.T) {
	t.Parallel()

	// nil pool is intentional — swagger endpoints don't hit the DB.
	r := gateway.NewRouter(nil, gateway.Config{Addr: ":8080", MaxRows: 1000})

	t.Run("spec_json_served", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/swagger/doc.json", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "GET /swagger/doc.json must return 200")

		var spec map[string]any
		err := json.Unmarshal(w.Body.Bytes(), &spec)
		require.NoError(t, err, "swagger.json must be valid JSON")

		paths, ok := spec["paths"].(map[string]any)
		require.True(t, ok, "swagger.json must contain a 'paths' object")
		assert.GreaterOrEqual(t, len(paths), 2,
			"spec must document >= 2 paths (got %d): %v", len(paths), keys(paths))
	})

	t.Run("swagger_ui_index", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "GET /swagger/index.html must return 200")
	})
}

// keys is a small helper to surface map keys in assertion messages.
func keys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
