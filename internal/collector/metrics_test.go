package collector

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestObserveFlush_Exposition verifies flush outcomes route to the right
// series: success feeds the latency/size histograms and batch counter,
// dead-letters feed their counter, and transient errors count separately
// without polluting the success histograms.
func TestObserveFlush_Exposition(t *testing.T) {
	start := time.Now().Add(-10 * time.Millisecond)
	observeFlush(start, 50, 0, nil)          // clean batch
	observeFlush(start, 10, 2, nil)          // batch with 2 dead-lettered rows
	observeFlush(start, 5, 0, errors.New("db down")) // transient failure

	rr := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()

	assert.Contains(t, body, "collector_batches_total 2", "two successful flushes")
	assert.Contains(t, body, "collector_db_errors_total 1", "one transient failure")
	assert.Contains(t, body, "collector_rows_dead_lettered_total 2")
	assert.Contains(t, body, "collector_batch_persist_seconds_count 2",
		"latency observed only for successful flushes")
	assert.Contains(t, body, "collector_batch_rows_count 2")
	assert.Contains(t, body, "# HELP collector_batch_persist_seconds")
}
