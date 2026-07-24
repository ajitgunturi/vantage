package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCursorRoundTrip: encode→decode preserves the microsecond timestamp
// (Postgres TIMESTAMPTZ precision) and metric name exactly.
func TestCursorRoundTrip(t *testing.T) {
	in := telemetryCursor{
		TS:     time.Date(2026, 7, 24, 10, 30, 45, 123456000, time.UTC),
		Metric: "DCGM_FI_DEV_GPU_UTIL",
	}
	out, err := decodeCursor(encodeCursor(in))
	require.NoError(t, err)
	assert.True(t, out.TS.Equal(in.TS), "timestamp must survive the round-trip")
	assert.Equal(t, in.Metric, out.Metric)
}

func TestDecodeCursor_Malformed(t *testing.T) {
	for name, tok := range map[string]string{
		"not base64":     "!!!not-base64!!!",
		"not json":       "bm90LWpzb24",                             // "not-json"
		"empty payload":  "e30",                                     // "{}"
		"missing metric": "eyJ0cyI6IjIwMjYtMDctMjRUMDA6MDA6MDBaIn0", // {"ts":"..."} only
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeCursor(tok)
			require.Error(t, err)
		})
	}
}
