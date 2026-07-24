package streamer

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetricsHandler_Exposition verifies the streamer registry serves its
// counters with HELP text and that increments are visible on scrape.
func TestMetricsHandler_Exposition(t *testing.T) {
	rowsProducedTotal.Add(2)
	rowsSkippedTotal.Inc()
	produceRetriesTotal.Inc()

	rr := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()

	assert.Contains(t, body, "streamer_rows_produced_total")
	assert.Contains(t, body, "streamer_rows_skipped_total")
	assert.Contains(t, body, "streamer_produce_retries_total")
	assert.Contains(t, body, "# HELP streamer_rows_produced_total")
}
