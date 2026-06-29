// Package models provides the shared domain structs and database-write
// contracts for the vantage GPU telemetry pipeline.  It is the single
// conversion layer between the generated protobuf types in pkg/pb and the
// persistence model, deliberately isolating every other package from the
// generated code.
//
// Critical invariant (COLL-04 / D-04): GpuMetric.GpuID is always sourced
// from the proto Uuid field (GPS UUID string, e.g. "GPU-5fd4f087-..."),
// NEVER from the proto gpu_id field (the ordinal "0", "1", …).  The database
// column is named gpu_id per the spec composite-index expression, but its
// value is the UUID.  This package is the single enforcement point for that
// mapping — do not replicate the conversion elsewhere.
package models

import (
	"fmt"
	"time"

	"github.com/ajitg/vantage/pkg/pb"
)

// GpuMetric is the domain representation of a single DCGM GPU telemetry
// reading.  It maps 1:1 to a row in the gpu_metrics table (D-01).
//
// Field naming follows the database schema (pkg/db/migrations/000001_init_schema.up.sql).
// GpuID stores the GPU UUID (e.g. "GPU-5fd4f087-…"), not the ordinal.
type GpuMetric struct {
	// GpuID is the GPU UUID (sourced from proto.Uuid, NOT proto.gpu_id).
	// Named gpu_id in the DB schema to match the spec composite-index expression.
	GpuID string

	// Timestamp is the restamped reading time in UTC, at RFC3339Nano precision.
	Timestamp time.Time

	MetricName string
	Value      float64
	Device     string
	ModelName  string
	Hostname   string
	Container  string
	Pod        string
	Namespace  string
	LabelsRaw  string
}

// InsertSQL is the idempotent INSERT for a single gpu_metrics row.
//
// Column order (positions $1..$11) matches the natural-key constraint
// (uq_gpu_metrics_natural_key) and the argument order the Collector passes
// to pgxpool.Pool.QueryRow / CopyFromRows:
//
//  $1  gpu_id      — GpuMetric.GpuID      (UUID string)
//  $2  timestamp   — GpuMetric.Timestamp  (time.Time, UTC)
//  $3  metric_name — GpuMetric.MetricName
//  $4  value       — GpuMetric.Value
//  $5  device      — GpuMetric.Device
//  $6  model_name  — GpuMetric.ModelName
//  $7  hostname    — GpuMetric.Hostname
//  $8  container   — GpuMetric.Container
//  $9  pod         — GpuMetric.Pod
//  $10 namespace   — GpuMetric.Namespace
//  $11 labels_raw  — GpuMetric.LabelsRaw
//
// ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING implements
// idempotent upsert (COLL-05 / DB-04): redelivered messages with the same
// natural key are silently discarded rather than causing a duplicate-key error.
const InsertSQL = `INSERT INTO gpu_metrics
    (gpu_id, timestamp, metric_name, value, device, model_name, hostname, container, pod, namespace, labels_raw)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (gpu_id, metric_name, timestamp) DO NOTHING`

// FromProto converts a protobuf TelemetryMessage into a GpuMetric.
//
// UUID mapping (COLL-04 / D-04): GpuMetric.GpuID is set from msg.GetUuid()
// (the GPU UUID, proto field 5).  msg.GetGpuId() (proto field 3, the ordinal
// "0"/"1") is intentionally ignored for the identity column — it is not stored.
//
// Timestamp parsing: the Streamer restamps using time.RFC3339Nano; FromProto
// tries that format first, then falls back to time.RFC3339 for whole-second
// strings.  If neither parses, FromProto returns a descriptive error and the
// zero GpuMetric — callers should skip-and-log the message (T-03-TS).
func FromProto(msg *pb.TelemetryMessage) (GpuMetric, error) {
	var ts time.Time
	var parseErr error

	raw := msg.GetTimestamp()

	// Primary: nanosecond-precision format used by the Streamer.
	ts, parseErr = time.Parse(time.RFC3339Nano, raw)
	if parseErr != nil {
		// Fallback: whole-second RFC3339 (no fractional component).
		ts, parseErr = time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return GpuMetric{}, fmt.Errorf("models: parse timestamp %q: %w", raw, parseErr)
		}
	}

	return GpuMetric{
		// D-04: use Uuid (field 5), NOT GpuId (field 3 — the ordinal).
		GpuID:      msg.GetUuid(),
		Timestamp:  ts.UTC(),
		MetricName: msg.GetMetricName(),
		Value:      msg.GetValue(),
		Device:     msg.GetDevice(),
		ModelName:  msg.GetModelName(),
		Hostname:   msg.GetHostname(),
		Container:  msg.GetContainer(),
		Pod:        msg.GetPod(),
		Namespace:  msg.GetNamespace(),
		LabelsRaw:  msg.GetLabelsRaw(),
	}, nil
}
