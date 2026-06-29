// Package models_test verifies the shared domain model contract for
// GpuMetric, FromProto, and InsertSQL.
//
// Critical invariant under test (COLL-04 / D-04): FromProto must map
// proto.Uuid (CSV field 5) → GpuMetric.GpuID.  It must never use
// proto.GpuId (field 3, the ordinal "0"/"1").  This package is the
// single enforcement point — the Collector and Gateway rely on it.
package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ajitg/vantage/pkg/models"
	"github.com/ajitg/vantage/pkg/pb"
	"github.com/stretchr/testify/require"
)

const (
	testUUID       = "GPU-5fd4f087-b9df-4e05-82ab-2b01fc8e29f1"
	testOrdinal    = "0"
	testMetricName = "DCGM_FI_DEV_GPU_UTIL"
	testDevice     = "nvidia0"
	testModelName  = "NVIDIA H100 80GB HBM3"
	testHostname   = "node-01"
	testContainer  = "dcgm-exporter"
	testPod        = "dcgm-exporter-pod"
	testNamespace  = "monitoring"
	testLabelsRaw  = `{gpu="0",UUID="GPU-5fd4f087-..."}`
	testValue      = 87.5
)

func validMsg(ts string) *pb.TelemetryMessage {
	return &pb.TelemetryMessage{
		Timestamp:  ts,
		MetricName: testMetricName,
		GpuId:      testOrdinal,  // ordinal — must NOT flow to GpuMetric.GpuID
		Device:     testDevice,
		Uuid:       testUUID, // UUID — this IS what must flow to GpuMetric.GpuID
		ModelName:  testModelName,
		Hostname:   testHostname,
		Container:  testContainer,
		Pod:        testPod,
		Namespace:  testNamespace,
		Value:      testValue,
		LabelsRaw:  testLabelsRaw,
	}
}

// TestFromProto_UUIDMapping asserts COLL-04 / D-04:
// GpuMetric.GpuID must be the UUID (proto.Uuid, field 5), never the ordinal
// (proto.GpuId, field 3).
func TestFromProto_UUIDMapping(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	msg := validMsg(ts)

	m, err := models.FromProto(msg)
	require.NoError(t, err)

	require.Equal(t, testUUID, m.GpuID, "GpuID must equal proto.Uuid (the GPU UUID)")
	require.NotEqual(t, testOrdinal, m.GpuID, "GpuID must NOT be the ordinal (proto.GpuId)")
}

// TestFromProto_RestampRFC3339Nano verifies that a nanosecond-precision
// RFC3339Nano timestamp round-trips without loss.
func TestFromProto_RestampRFC3339Nano(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Nanosecond)
	ts := now.Format(time.RFC3339Nano)
	msg := validMsg(ts)

	m, err := models.FromProto(msg)
	require.NoError(t, err)

	require.True(t, m.Timestamp.Equal(now),
		"Timestamp must round-trip RFC3339Nano: want %v, got %v", now, m.Timestamp)
	require.Equal(t, time.UTC, m.Timestamp.Location(), "Timestamp must be UTC")
}

// TestFromProto_RFC3339Fallback verifies that a whole-second RFC3339 string
// (no fractional part) is also accepted via the fallback parse path.
func TestFromProto_RFC3339Fallback(t *testing.T) {
	// Format with second granularity only — no sub-second component.
	ts := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	msg := validMsg(ts)

	m, err := models.FromProto(msg)
	require.NoError(t, err, "RFC3339 (no nanoseconds) must be accepted via fallback")
	require.False(t, m.Timestamp.IsZero(), "Timestamp must not be zero on fallback parse")
}

// TestFromProto_BadTimestamp verifies that an unparseable timestamp causes
// FromProto to return a non-nil error and the zero GpuMetric (T-03-TS).
func TestFromProto_BadTimestamp(t *testing.T) {
	msg := validMsg("not-a-time")

	m, err := models.FromProto(msg)
	require.Error(t, err, "unparseable timestamp must return an error")
	require.Zero(t, m, "zero GpuMetric must be returned on error")
}

// TestFromProto_AllFields verifies that every descriptive field round-trips
// from the proto getter to the corresponding GpuMetric field.
func TestFromProto_AllFields(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	msg := validMsg(ts)

	m, err := models.FromProto(msg)
	require.NoError(t, err)

	require.Equal(t, testMetricName, m.MetricName)
	require.Equal(t, testDevice, m.Device)
	require.Equal(t, testModelName, m.ModelName)
	require.Equal(t, testHostname, m.Hostname)
	require.Equal(t, testContainer, m.Container)
	require.Equal(t, testPod, m.Pod)
	require.Equal(t, testNamespace, m.Namespace)
	require.Equal(t, testLabelsRaw, m.LabelsRaw)
	require.InDelta(t, testValue, m.Value, 1e-9)
}

// TestInsertSQL_Shape asserts the structural invariants of InsertSQL:
//   - Targets the natural-key conflict columns: gpu_id, metric_name, timestamp.
//   - Contains DO NOTHING.
//   - Has exactly 11 positional parameters ($1..$11); $12 must be absent.
func TestInsertSQL_Shape(t *testing.T) {
	sql := strings.ToLower(models.InsertSQL)

	// Natural-key conflict target
	require.Contains(t, sql, "gpu_id", "conflict target must include gpu_id")
	require.Contains(t, sql, "metric_name", "conflict target must include metric_name")
	require.Contains(t, sql, "timestamp", "conflict target must include timestamp")

	// Idempotency clause
	require.Contains(t, sql, "do nothing", "InsertSQL must use ON CONFLICT DO NOTHING")

	// Positional parameter count guard
	require.Contains(t, sql, "$11", "InsertSQL must have at least 11 positional params")
	require.NotContains(t, sql, "$12", "InsertSQL must have exactly 11 positional params (no $12)")
}
