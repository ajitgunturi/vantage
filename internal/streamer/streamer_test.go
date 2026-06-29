// Package streamer — unit tests.
// In-package (package streamer) so they can access the unexported recordToProto
// and dialMQ helpers directly alongside the exported Stream / Run / FromEnv API.
package streamer

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/ajitg/vantage/pkg/pb"
)

// --- fake gRPC client implementations ------------------------------------

// fakeProducer satisfies pb.MQServiceClient. Produce records every message
// under a mutex so tests can assert counts and payloads safely under -race.
type fakeProducer struct {
	mu    sync.Mutex
	count int
	msgs  []*pb.TelemetryMessage
}

func (f *fakeProducer) Produce(_ context.Context, in *pb.ProduceRequest, _ ...grpc.CallOption) (*pb.ProduceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	f.msgs = append(f.msgs, in.GetMessage())
	return &pb.ProduceResponse{Accepted: true}, nil
}

func (f *fakeProducer) Consume(_ context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pb.ConsumeClientMsg, pb.TelemetryMessage], error) {
	return nil, fmt.Errorf("consume not supported in fakeProducer")
}

// cancelProducer cancels a context once it has accumulated >= threshold messages.
type cancelProducer struct {
	mu        sync.Mutex
	count     int
	msgs      []*pb.TelemetryMessage
	cancel    context.CancelFunc
	threshold int
}

func (f *cancelProducer) Produce(_ context.Context, in *pb.ProduceRequest, _ ...grpc.CallOption) (*pb.ProduceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	f.msgs = append(f.msgs, in.GetMessage())
	if f.count >= f.threshold {
		f.cancel()
	}
	return &pb.ProduceResponse{Accepted: true}, nil
}

func (f *cancelProducer) Consume(_ context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pb.ConsumeClientMsg, pb.TelemetryMessage], error) {
	return nil, fmt.Errorf("consume not supported in cancelProducer")
}

// --- CSV fixture helpers --------------------------------------------------

// validRecord returns a valid 12-column DCGM CSV data row (no header).
// Column layout: [0]=timestamp [1]=metric_name [2]=gpu_id [3]=device [4]=uuid
// [5]=model_name [6]=hostname [7]=container [8]=pod [9]=namespace [10]=value [11]=labels_raw
func validRecord(uuid, metricName, value string) []string {
	return []string{
		"2025-07-18T20:42:34Z", // col 0: timestamp (discarded by restamp)
		metricName,              // col 1: metric_name
		"0",                     // col 2: gpu_id (ordinal)
		"nvidia0",               // col 3: device
		uuid,                    // col 4: uuid (GPU UUID)
		"NVIDIA H100 80GB HBM3", // col 5: model_name
		"hostname1",             // col 6: hostname
		"",                      // col 7: container
		"",                      // col 8: pod
		"",                      // col 9: namespace
		value,                   // col 10: value
		"",                      // col 11: labels_raw
	}
}

// writeTempCSV writes a CSV file into t.TempDir() and returns its path.
// The header row is always the canonical 12-column DCGM header.
// Data rows are passed as string slices and joined with commas.
func writeTempCSV(t *testing.T, rows [][]string) string {
	t.Helper()
	path := t.TempDir() + "/fixture.csv"
	var sb strings.Builder
	sb.WriteString("Timestamp,metric_name,gpu_id,device,uuid,model_name,hostname,container,pod,namespace,value,labels_raw\n")
	for _, row := range rows {
		sb.WriteString(strings.Join(row, ","))
		sb.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(path, []byte(sb.String()), 0o600))
	return path
}

// --- Config / FromEnv tests ----------------------------------------------

// TestFromEnv_Defaults asserts that FromEnv returns correct defaults
// when no environment variables are set.
func TestFromEnv_Defaults(t *testing.T) {
	t.Setenv("STREAMER_MQ_ADDR", "")
	t.Setenv("STREAMER_CSV_PATH", "")
	t.Setenv("STREAMER_LOOP_DELAY_MS", "")

	cfg := FromEnv()
	require.Equal(t, ":50051", cfg.MQAddr)
	require.Equal(t, "", cfg.CSVPath)
	require.Equal(t, 1, cfg.LoopDelayMS)
}

// TestFromEnv_Override asserts that FromEnv picks up environment variable overrides.
func TestFromEnv_Override(t *testing.T) {
	t.Setenv("STREAMER_MQ_ADDR", "localhost:9090")
	t.Setenv("STREAMER_CSV_PATH", "/tmp/test.csv")
	t.Setenv("STREAMER_LOOP_DELAY_MS", "100")

	cfg := FromEnv()
	require.Equal(t, "localhost:9090", cfg.MQAddr)
	require.Equal(t, "/tmp/test.csv", cfg.CSVPath)
	require.Equal(t, 100, cfg.LoopDelayMS)
}

// TestFromEnv_InvalidDelayKeptAsDefault asserts that an invalid
// STREAMER_LOOP_DELAY_MS keeps the default (1).
func TestFromEnv_InvalidDelayKeptAsDefault(t *testing.T) {
	t.Setenv("STREAMER_LOOP_DELAY_MS", "not-a-number")
	cfg := FromEnv()
	require.Equal(t, 1, cfg.LoopDelayMS)
}

// --- dialMQ test ---------------------------------------------------------

// TestDialMQ_ReturnsConn asserts dialMQ returns a non-nil connection for a
// well-formed address (grpc.NewClient is lazy — no actual connection is made).
func TestDialMQ_ReturnsConn(t *testing.T) {
	conn, err := dialMQ(":50051")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())
}

// --- Run tests -----------------------------------------------------------

// TestRun_RequiresCSVPath asserts that Run returns an error when CSVPath is empty.
func TestRun_RequiresCSVPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Run(ctx, Config{MQAddr: ":50051", CSVPath: ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "CSVPath")
}

// TestRun_ErrorsOnMissingCSV asserts that Run propagates the file-open error
// when the CSV does not exist. This also exercises the dialMQ → Stream path.
func TestRun_ErrorsOnMissingCSV(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Run(ctx, Config{
		MQAddr:  ":50051",
		CSVPath: "/tmp/vantage-does-not-exist-test.csv",
	})
	require.Error(t, err)
}

// --- recordToProto tests -------------------------------------------------

// TestRecordToProto_RestampRFC3339Nano asserts that recordToProto restamps the
// Timestamp with time.Now().UTC().Format(time.RFC3339Nano) — not the CSV value.
func TestRecordToProto_RestampRFC3339Nano(t *testing.T) {
	before := time.Now().UTC()
	row := validRecord("GPU-restamp-test", "DCGM_FI_DEV_GPU_UTIL", "42.5")
	msg, err := recordToProto(row)
	after := time.Now().UTC()

	require.NoError(t, err)
	require.NotNil(t, msg)

	// Must parse as RFC3339Nano.
	ts, parseErr := time.Parse(time.RFC3339Nano, msg.GetTimestamp())
	require.NoError(t, parseErr, "Timestamp must be parseable as RFC3339Nano, got: %q", msg.GetTimestamp())

	// Must be UTC.
	require.Equal(t, time.UTC, ts.Location())

	// Must end in 'Z' (UTC suffix).
	require.True(t, strings.HasSuffix(msg.GetTimestamp(), "Z"), "Timestamp must end in 'Z', got: %q", msg.GetTimestamp())

	// Must be within the test window — not the original CSV timestamp.
	require.False(t, ts.Before(before), "Timestamp must not predate the test start")
	require.False(t, ts.After(after.Add(5*time.Second)), "Timestamp must not be more than 5s after the test end")

	// Original CSV timestamp must have been discarded.
	require.NotEqual(t, "2025-07-18T20:42:34Z", msg.GetTimestamp(),
		"Timestamp must be restamped, not the original CSV value")
}

// TestRecordToProto_FieldMapping asserts correct column → proto field mapping.
func TestRecordToProto_FieldMapping(t *testing.T) {
	row := validRecord("GPU-uuid-abc123", "DCGM_FI_DEV_MEM_UTIL", "99.9")
	msg, err := recordToProto(row)
	require.NoError(t, err)
	require.NotNil(t, msg)

	require.Equal(t, "GPU-uuid-abc123", msg.GetUuid(), "Uuid must be col4 (GPU UUID)")
	require.Equal(t, "0", msg.GetGpuId(), "GpuId must be col2 (ordinal)")
	require.Equal(t, "DCGM_FI_DEV_MEM_UTIL", msg.GetMetricName(), "MetricName must be col1")
	require.InDelta(t, 99.9, msg.GetValue(), 1e-9, "Value must be ParseFloat(col10)")
}

// TestRecordToProto_BadValue asserts that a non-numeric value column returns an error.
func TestRecordToProto_BadValue(t *testing.T) {
	row := validRecord("GPU-bad", "DCGM_FI_DEV_GPU_UTIL", "not-a-float")
	msg, err := recordToProto(row)
	require.Error(t, err, "non-numeric value column must produce an error")
	require.Nil(t, msg, "on error, message must be nil")
}

// --- Stream tests --------------------------------------------------------

// TestStream_SkipsMalformed creates a CSV with a header, two valid rows, and
// one row with 11 fields (malformed). Stream(once=true) must publish exactly 2
// messages and return nil — malformed row is skipped, no panic (STREAM-04, T-03-02a).
func TestStream_SkipsMalformed(t *testing.T) {
	// 11-field row — wrong column count, rejected by FieldsPerRecord=12.
	malformed := []string{"only", "eleven", "cols", "here", "no", "value", "skip", "this", "row", "please", "ok"}
	path := writeTempCSV(t, [][]string{
		validRecord("GPU-valid-1", "METRIC_A", "1.0"),
		malformed,
		validRecord("GPU-valid-2", "METRIC_B", "2.0"),
	})

	fake := &fakeProducer{}
	err := Stream(context.Background(), fake, path, 0, true)

	require.NoError(t, err, "Stream must return nil even when rows are malformed")
	fake.mu.Lock()
	count := fake.count
	fake.mu.Unlock()
	require.Equal(t, 2, count, "exactly 2 valid rows must be published (malformed row skipped)")
}

// TestStream_LoopsUntilCancel asserts that Stream(once=false) loops through the
// CSV multiple times and returns context.Canceled when the context is cancelled
// (STREAM-01).
func TestStream_LoopsUntilCancel(t *testing.T) {
	path := writeTempCSV(t, [][]string{
		validRecord("GPU-loop-1", "METRIC_A", "1.0"),
		validRecord("GPU-loop-2", "METRIC_B", "2.0"),
		validRecord("GPU-loop-3", "METRIC_C", "3.0"),
	})

	ctx, cancel := context.WithCancel(context.Background())
	fake := &cancelProducer{cancel: cancel, threshold: 6} // cancel after >= 2 full passes

	err := Stream(ctx, fake, path, 0, false)

	require.ErrorIs(t, err, context.Canceled, "Stream must return context.Canceled when cancelled")
	fake.mu.Lock()
	count := fake.count
	fake.mu.Unlock()
	require.GreaterOrEqual(t, count, 6,
		"Stream must complete at least 2 full CSV passes before cancellation")
}

// TestStream_Concurrent10 launches 10 goroutines each calling Stream(once=true)
// over the same temp CSV against a shared fakeProducer. Under -race it asserts
// total published == 10 × validRowCount and no data race (STREAM-05).
func TestStream_Concurrent10(t *testing.T) {
	const goroutines = 10
	const validRows = 3

	path := writeTempCSV(t, [][]string{
		validRecord("GPU-c-1", "METRIC_A", "1.0"),
		validRecord("GPU-c-2", "METRIC_B", "2.0"),
		validRecord("GPU-c-3", "METRIC_C", "3.0"),
	})

	fake := &fakeProducer{}
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = Stream(ctx, fake, path, 0, true)
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		require.NoError(t, e, "goroutine %d must not error", i)
	}
	fake.mu.Lock()
	total := fake.count
	fake.mu.Unlock()
	require.Equal(t, goroutines*validRows, total, "total published must be 10 × validRowCount")
}
