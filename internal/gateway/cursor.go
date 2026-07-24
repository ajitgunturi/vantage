package gateway

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// telemetryCursor names a position in the (timestamp DESC, metric_name DESC)
// total order — the last row of the page already delivered. The natural key
// (gpu_id, metric_name, timestamp) makes the pair unique per GPU, so a cursor
// identifies exactly one row.
//
// Encoded as base64url(JSON). The token is OPAQUE to clients: its layout may
// change; clients must round-trip next_cursor verbatim. Timestamps carry
// RFC3339Nano so the Postgres microsecond value survives the round-trip.
type telemetryCursor struct {
	TS     time.Time `json:"ts"`
	Metric string    `json:"mn"`
}

func encodeCursor(c telemetryCursor) string {
	raw, _ := json.Marshal(c) // cannot fail: fixed struct of marshalable types
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (telemetryCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return telemetryCursor{}, fmt.Errorf("cursor is not valid base64url: %w", err)
	}
	var c telemetryCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return telemetryCursor{}, fmt.Errorf("cursor payload malformed: %w", err)
	}
	if c.TS.IsZero() || c.Metric == "" {
		return telemetryCursor{}, fmt.Errorf("cursor payload incomplete")
	}
	return c, nil
}
