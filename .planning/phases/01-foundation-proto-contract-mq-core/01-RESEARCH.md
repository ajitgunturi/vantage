# Phase 1: Foundation — Proto Contract + MQ Core — Research

**Researched:** 2026-06-27
**Domain:** Custom bounded ring-buffer message queue in Go; gRPC server-streaming; protoc code generation; HTTP control plane
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- MQ internal primitive: bounded **ring buffer** guarded by `sync.Mutex`/`sync.RWMutex` (built from scratch — no broker). Satisfies MQ-04/MQ-05.
- Competing-consumer unique delivery (MQ-03): each message to **exactly one** consumer via a single dispatch path (not fan-out/pub-sub).
- Full-buffer policy: **drop-oldest** (overwrite oldest unconsumed slot). `Produce` never blocks or returns `ResourceExhausted` in the default in-memory mode.
- Proto tooling: **protoc** (already wired in `make proto` → `pkg/pb`, `paths=source_relative`). Do not introduce buf.
- Single proto file: `api/proto/mq.proto`. Generated code in `pkg/pb/`.
- gRPC: `Produce` unary + `Consume` server-stream with server keepalives.
- HTTP control plane: stdlib `net/http`, single endpoint `GET /api/v1/queue/inspect`.
- MQ storage behind a `Store` interface; in-memory ring buffer is the default.
- Consumer disconnect handled gracefully via context cancellation + defer cleanup.
- Quality gates: `go test -race` clean; N produced = N consumed across K consumers; ≥90% line coverage on `internal/` packages.

### Claude's Discretion

- Ring-buffer default capacity (configurable via env/flag).
- Dispatch goroutine vs handoff channel internal detail.
- Exact `internal/` package split.
- Proto field numbering/types.
- Precise shape of the `Store` interface methods.

### Deferred Ideas (OUT OF SCOPE)

- WAL / disk persistence (Phase 6).
- Streamer, Collector, PostgreSQL schema (Phase 2/3).
- Dockerfile + Helm sub-chart for MQ (Phase 5).
- Configurable drop policy / richer inspect / backpressure modes (ENH-01/06).
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| MQ-01 | `Produce` unary gRPC RPC accepts a telemetry payload and enqueues it | Section: gRPC Server Wiring; Code Examples: Produce handler |
| MQ-02 | `Consume` server-streaming gRPC RPC delivers enqueued messages to a collector | Section: gRPC Server Wiring; Code Examples: Consume handler |
| MQ-03 | Each message delivered to exactly one consumer — no duplication | Section: Competing-Consumer Dispatch; dispatch goroutine + shared workCh design |
| MQ-04 | In-memory thread-safe queue built from native Go concurrency only | Section: Ring Buffer Design; RingStore with sync.Mutex |
| MQ-05 | Bounded buffer with drop-oldest policy when full; no unbounded memory growth | Section: Ring Buffer Design; Enqueue with drop-oldest |
| MQ-06 | HTTP `GET /api/v1/queue/inspect` returns JSON queue summary | Section: HTTP Inspect Handler; InspectResponse shape |
| MQ-07 | Consumer disconnect handled gracefully; no goroutine leak | Section: gRPC Server Wiring; Consume handler with ctx.Done() + defer |
| MQ-08 | MQ storage behind a `Store` interface; in-memory is the default | Section: Store Interface Shape |
| QA-02 | Race-detector tests proving N produced = N consumed across K consumers | Section: Validation Architecture; TestMQ_Concurrent_UniqueDelivery |
</phase_requirements>

---

## Summary

Phase 1 delivers the foundational proto contract and the custom in-memory message queue service. The entire system's critical path runs through this phase: nothing downstream can compile or test without the generated `pkg/pb` types, and no concurrent delivery correctness can be assumed without a race-verified MQ core.

The central engineering challenge is a **bounded ring buffer with drop-oldest semantics, delivering to competing consumers with exactly-once-per-message semantics**. The design uses a two-stage delivery pipeline: a single dispatch goroutine owns the ring buffer read path and transfers messages into a bounded shared work channel; Consume goroutines compete for messages on that work channel (Go's channel guarantee ensures exactly one goroutine receives each send). This completely separates the "store" concern (ring buffer + mutex) from the "dispatch" concern (channel competition), enabling clean lock discipline where no mutex is ever held across a channel send.

The gRPC transport adds keepalive configuration (required to prevent silent stream death under NAT/Kubernetes kube-proxy), and the HTTP control plane adds a single inspect endpoint sharing the `*MQServer` pointer for zero-overhead state reads.

**Primary recommendation:** Implement `internal/mq/store/` (Store interface + RingStore), `internal/mq/server/` (MQServer with dispatch goroutine), and `internal/mq/config/` as separate packages so the 90% coverage gate measures logic rather than main-wiring. Run `go test -race -count=10 ./internal/...` as the Phase 1 exit gate.

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Message storage (ring buffer) | MQ Service (internal) | — | State machine lives entirely inside the MQ process; no external storage |
| Telemetry ingestion (Produce) | MQ gRPC Server | — | gRPC unary RPC is the ingest boundary; MQ owns enqueue logic |
| Message delivery (Consume) | MQ gRPC Server | — | Server-streaming RPC; MQ owns dispatch + send; collector is a passive receiver |
| Drop-oldest policy | MQ Service (internal/store) | — | Policy lives in Enqueue implementation; hidden behind Store interface |
| Admin visibility (inspect) | MQ HTTP Server | — | Reads shared state via pointer; same process as gRPC server |
| Proto contract definition | `api/proto/mq.proto` | `pkg/pb` (generated) | Schema is the cross-service boundary; generated code is not logic |
| Concurrent delivery correctness | MQ dispatch goroutine | Go channel semantics | Work channel dequeue is atomic by the Go memory model |

---

## Standard Stack

### Core Libraries for Phase 1

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `google.golang.org/grpc` | v1.81.1 | gRPC runtime, Produce/Consume handlers, keepalive server options | Sole maintained Go gRPC implementation; keepalive types required |
| `google.golang.org/protobuf` | v1.36.11 | Protobuf v3 runtime; generated types in `pkg/pb` | Replaces deprecated `github.com/golang/protobuf`; required by grpc-go |
| `golang.org/x/sync/errgroup` | (transitive via grpc-go) | Manage dual gRPC+HTTP server goroutines | Standard pattern; error propagation + context cancellation |

All other Phase 1 code uses the Go standard library only: `sync`, `sync/atomic`, `net/http`, `encoding/json`, `context`, `os/signal`.

### Test Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/stretchr/testify` | v1.11.1 | `require.Equal`, `require.Eventually` for goroutine-leak checks | All unit + concurrent tests |

### What NOT to Add in Phase 1

| Avoid | Why |
|-------|-----|
| `github.com/golang/protobuf` | Deprecated; grpc-go depends on `google.golang.org/protobuf` |
| Any third-party message queue (NATS, Redis, etc.) | Explicitly out of scope per spec; MQ is the assignment artifact |
| `bufbuild/buf` | CONTEXT.md locked decision: use protoc |
| `sync.RWMutex` for the ring buffer | Ring buffer needs exclusive write on every read-adjacent state (head, tail, count); RWMutex adds complexity with no read-path benefit here |

### Installation

```bash
go get google.golang.org/grpc@v1.81.1
go get google.golang.org/protobuf@v1.36.11
go get github.com/stretchr/testify@v1.11.1
# keepalive and errgroup come transitively; run go mod tidy after
```

Toolchain (not in go.mod — install via `make tools`):
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# protoc binary itself must be installed separately (apt/brew: protobuf)
```

---

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `google.golang.org/grpc` | pkg.go.dev | 10+ yrs | Tens of millions/month | github.com/grpc/grpc-go | OK | Approved |
| `google.golang.org/protobuf` | pkg.go.dev | 5+ yrs | Tens of millions/month | github.com/protocolbuffers/protobuf-go | OK | Approved |
| `github.com/stretchr/testify` | pkg.go.dev | 12+ yrs | 100M+/month | github.com/stretchr/testify | OK | Approved |

**Packages removed due to SLOP verdict:** none
**Packages flagged as suspicious:** none

All three packages are canonical, authoritative Go ecosystem libraries confirmed by official documentation and training data. [ASSUMED] — registry legitimacy not re-verified via `gsd-tools query package-legitimacy` in this session (gsd-tools is available but search providers are disabled in config); these packages are core Go ecosystem dependencies with no hallucination risk.

---

## Architecture Patterns

### System Architecture Diagram (Phase 1 scope)

```
Produce caller (future Streamer)
    │  gRPC unary: ProduceRequest{TelemetryMessage}
    ▼
┌───────────────────────────────────────────────────────────────┐
│  cmd/mq/main.go  (thin wiring only)                           │
│                                                               │
│  internal/mq/server.MQServer                                  │
│  ┌──────────────────────────────────────────────┐             │
│  │  Produce() → store.Enqueue() → notify signal  │            │
│  └──────────────────────────────────────────────┘             │
│                          │ signal (chan struct{}, cap=1)       │
│                          ▼                                     │
│  ┌──────────────────────────────────────────────┐             │
│  │  dispatch goroutine (single)                  │             │
│  │  loop: notify | ticker → TryDequeue → workCh  │            │
│  └───────────────────────┬──────────────────────┘             │
│                          │ workCh (chan *pb.TelemetryMessage)  │
│          ┌───────────────┼───────────────┐                    │
│          ▼               ▼               ▼                    │
│  Consume-1()       Consume-2()     Consume-3()                │
│  select workCh     select workCh   select workCh              │
│  + ctx.Done()      + ctx.Done()    + ctx.Done()               │
│  → stream.Send()   → stream.Send() → stream.Send()            │
│          │               │               │                    │
│          └───────────────┼───────────────┘                    │
│                          │ gRPC server-stream frames           │
└──────────────────────────┼────────────────────────────────────┘
                           ▼
              Consume callers (future Collectors)

   ┌───────────────────────────────────┐
   │  internal/mq/store.RingStore      │ ← store.Enqueue (write under mutex)
   │  pre-allocated [cap]*TelMsg       │   head/tail/count indices
   │  drop-oldest when count==cap      │ ← store.TryDequeue (read under mutex)
   └───────────────────────────────────┘

HTTP :8080 (/api/v1/queue/inspect)
   reads atomics from MQServer + Inspect() from store
   no lock held in HTTP hot path
```

**Critical data-flow invariant:** The mutex is acquired only inside `Enqueue` and `TryDequeue`. It is released before any channel send or gRPC stream write. This eliminates all deadlock risk from holding a lock across a blocking operation.

### Recommended Project Structure

```
api/
└── proto/
    └── mq.proto              # Single proto contract; protoc input

pkg/
└── pb/                        # Generated by make proto; excluded from coverage gate
    ├── mq.pb.go              #   message types (TelemetryMessage, ProduceRequest, ProduceResponse, ConsumeRequest)
    └── mq_grpc.pb.go         #   MQServiceServer interface, MQServiceClient, Unimplemented*

internal/
└── mq/
    ├── store/
    │   ├── store.go           # Store interface + StoreStats struct
    │   └── ring_store.go      # RingStore: pre-allocated ring buffer, sync.Mutex, drop-oldest
    ├── server/
    │   ├── grpc_server.go     # MQServer: Produce, Consume, dispatch goroutine, Shutdown
    │   └── http_server.go     # HTTP inspect handler + InspectResponse struct
    └── config/
        └── config.go          # Config struct: BufferSize, WorkChCap, GRPCAddr, HTTPAddr (env/flag)

cmd/
└── mq/
    └── main.go                # Thin wiring: parse config → build store → build MQServer →
                               # start gRPC + HTTP + SIGTERM → graceful shutdown
```

**Why this split:** `internal/mq/` packages are the only packages that carry logic; `cmd/mq/main.go` is pure wiring (imports internal, wires together, handles OS signals). The Makefile's coverage gate measures `./internal/...` to exclude the thin main wrapper and the generated `pkg/pb/` stubs.

---

### Pattern 1: Ring Buffer with Drop-Oldest (Store interface + RingStore)

**What:** A pre-allocated fixed-capacity slice used as a circular FIFO. When full, the oldest slot is overwritten rather than blocking or returning an error. A single `sync.Mutex` guards all head/tail/count state.

**Why `sync.Mutex` (not `sync.RWMutex`):** Every `TryDequeue` advances `head` and decrements `count` — it is a mutating operation. There is no "read-only" path on the ring buffer state. `RWMutex` would not help and adds lock-hierarchy complexity.

**Store interface (WAL-safe seam):**

```go
// internal/mq/store/store.go
// Source: idiomatic Go interface design; no disk assumptions

package store

import "github.com/ajitg/vantage/pkg/pb"

// StoreStats is safe to copy; all fields are value types.
type StoreStats struct {
    Depth    int   // messages currently buffered
    Capacity int   // total slots (-1 if unbounded, e.g., WAL backend)
    Dropped  int64 // cumulative messages overwritten by drop-oldest
}

// Store is the pluggable storage backend for the MQ.
// The in-memory RingStore is the default; a WAL-backed Store slots in
// behind this interface in Phase 6 without changing any consumer code.
//
// Contract:
//   - Enqueue never blocks. Drop-oldest when at capacity; returns true if a message was dropped.
//   - TryDequeue is non-blocking: returns (nil, false) when empty.
//   - Close releases any resources (no-op for in-memory backend).
type Store interface {
    Enqueue(msg *pb.TelemetryMessage) (dropped bool)
    TryDequeue() (*pb.TelemetryMessage, bool)
    Inspect() StoreStats
    Close() error
}
```

**RingStore implementation:**

```go
// internal/mq/store/ring_store.go

package store

import (
    "sync"
    "github.com/ajitg/vantage/pkg/pb"
)

type RingStore struct {
    mu      sync.Mutex
    buf     []*pb.TelemetryMessage // pre-allocated; never grows
    head    int                    // index of oldest item
    tail    int                    // index where next item is written
    count   int                    // current number of items
    cap     int                    // maximum capacity
    dropped int64                  // guarded by mu; no need for atomic
}

func NewRingStore(capacity int) *RingStore {
    if capacity <= 0 {
        panic("store: capacity must be positive")
    }
    return &RingStore{
        buf: make([]*pb.TelemetryMessage, capacity),
        cap: capacity,
    }
}

// Enqueue adds msg to the ring buffer. If the buffer is full, the oldest
// message is overwritten (drop-oldest). Always returns immediately.
func (r *RingStore) Enqueue(msg *pb.TelemetryMessage) bool {
    r.mu.Lock()
    defer r.mu.Unlock()

    dropped := false
    if r.count == r.cap {
        // Overwrite oldest: advance head and free slot for GC
        r.buf[r.head] = nil
        r.head = (r.head + 1) % r.cap
        r.count--
        r.dropped++
        dropped = true
    }

    r.buf[r.tail] = msg
    r.tail = (r.tail + 1) % r.cap
    r.count++
    return dropped
}

// TryDequeue removes and returns the oldest message. Returns (nil, false)
// if the buffer is empty. Non-blocking.
func (r *RingStore) TryDequeue() (*pb.TelemetryMessage, bool) {
    r.mu.Lock()
    defer r.mu.Unlock()

    if r.count == 0 {
        return nil, false
    }
    msg := r.buf[r.head]
    r.buf[r.head] = nil // clear pointer for GC
    r.head = (r.head + 1) % r.cap
    r.count--
    return msg, true
}

func (r *RingStore) Inspect() StoreStats {
    r.mu.Lock()
    defer r.mu.Unlock()
    return StoreStats{
        Depth:    r.count,
        Capacity: r.cap,
        Dropped:  r.dropped,
    }
}

func (r *RingStore) Close() error {
    return nil // in-memory; nothing to flush
}
```

**Invariants verified by `go test -race -count=10`:**
- Head never overtakes tail (and vice versa) under concurrent access.
- `count` always equals `len(non-nil slots)`.
- `dropped` increments by 1 for each overflow, never races.

---

### Pattern 2: Competing-Consumer Dispatch (MQServer)

**What:** A single dispatch goroutine owns the ring buffer read path. It transfers messages from the ring buffer into a bounded shared work channel. All registered `Consume` goroutines compete for messages on that work channel. Go's channel send-receive semantics guarantee exactly one goroutine receives each sent message — this provides work-queue unique delivery without any additional deduplication logic.

**Lock discipline (critical):**

```
Produce path:   acquire mu → write ring buf → release mu → signal notify (no lock held)
Dispatch path:  receive notify (no lock) → acquire mu → TryDequeue → release mu → send workCh (no lock held)
Consume path:   receive workCh (no lock) → send gRPC stream (no lock)
```

**No mutex is ever held when sending to any channel or gRPC stream.** This is the rule that eliminates all deadlock risk.

```go
// internal/mq/server/grpc_server.go

package server

import (
    "context"
    "sync"
    "sync/atomic"
    "time"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"

    "github.com/ajitg/vantage/internal/mq/store"
    "github.com/ajitg/vantage/pkg/pb"
)

type MQServer struct {
    pb.UnimplementedMQServiceServer

    store  store.Store
    notify chan struct{}             // buffered(1): signals dispatch goroutine
    workCh chan *pb.TelemetryMessage // buffered(WorkChCap): competing consumers read here

    produced int64 // atomic counter
    consumed int64 // atomic counter
    activeC  int32 // atomic counter (active Consume streams)

    shutdownCh chan struct{}
    once       sync.Once
}

func NewMQServer(s store.Store, workChCap int) *MQServer {
    srv := &MQServer{
        store:      s,
        notify:     make(chan struct{}, 1),
        workCh:     make(chan *pb.TelemetryMessage, workChCap),
        shutdownCh: make(chan struct{}),
    }
    go srv.dispatch()
    return srv
}

// dispatch is the single goroutine that owns the ring buffer read path.
// It uses a notify channel (capacity 1) for low-latency wake-up and a
// 1ms ticker as a fallback to catch any missed notifications (e.g., items
// buffered before the dispatch goroutine started).
//
// LOCK DISCIPLINE: TryDequeue acquires+releases mu internally.
// workCh send happens AFTER mu is released. No lock is ever held across
// a channel operation.
func (s *MQServer) dispatch() {
    ticker := time.NewTicker(time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-s.shutdownCh:
            close(s.workCh)
            return
        case <-s.notify:
        case <-ticker.C:
        }
        // Drain ring buffer into workCh.
        // TryDequeue acquires+releases mu; no lock held during workCh send.
        for {
            msg, ok := s.store.TryDequeue()
            if !ok {
                break
            }
            select {
            case s.workCh <- msg:
                // Message delivered to work channel; consumer goroutines compete for it.
            case <-s.shutdownCh:
                close(s.workCh)
                return
            }
        }
    }
}

func (s *MQServer) Produce(ctx context.Context, req *pb.ProduceRequest) (*pb.ProduceResponse, error) {
    if req.GetMessage() == nil {
        return nil, status.Error(codes.InvalidArgument, "message must not be nil")
    }

    s.store.Enqueue(req.Message) // drop-oldest on overflow; never blocks
    atomic.AddInt64(&s.produced, 1)

    // Signal dispatch goroutine (non-blocking; duplicate signals are safe,
    // the ticker acts as a fallback if this signal is lost).
    select {
    case s.notify <- struct{}{}:
    default:
    }

    return &pb.ProduceResponse{Accepted: true}, nil
}

func (s *MQServer) Consume(req *pb.ConsumeRequest, stream pb.MQService_ConsumeServer) error {
    atomic.AddInt32(&s.activeC, 1)
    defer atomic.AddInt32(&s.activeC, -1)

    ctx := stream.Context()
    for {
        select {
        case <-ctx.Done():
            // Consumer disconnected or context cancelled — clean exit, no goroutine leak.
            return ctx.Err()
        case msg, ok := <-s.workCh:
            if !ok {
                // workCh closed: graceful MQ shutdown.
                return nil
            }
            if err := stream.Send(msg); err != nil {
                // Client disconnected mid-stream. The message is lost (no persistence per spec).
                // Return error so gRPC records the failed stream.
                return err
            }
            atomic.AddInt64(&s.consumed, 1)
        }
    }
}

// Shutdown signals the dispatch goroutine to exit and closes the work channel.
// Safe to call multiple times.
func (s *MQServer) Shutdown() {
    s.once.Do(func() {
        close(s.shutdownCh)
    })
}
```

**Unique-delivery guarantee mechanism:** Go's channel semantics guarantee that each value sent into `workCh` is received by exactly one goroutine. When multiple `Consume` goroutines are simultaneously blocked on `select { case msg := <-s.workCh: ... }`, the Go runtime selects exactly one to receive each message — this is a fundamental language guarantee, not an application-level lock.

---

### Pattern 3: gRPC Server Wiring with Keepalive

**What:** gRPC server on `:50051` with server keepalive parameters to prevent silent stream death when long-lived `Consume` streams idle behind Kubernetes kube-proxy (IPVS default timeout: 350s; AWS NLB: 350s; many NAT gateways: 60s).

```go
// cmd/mq/main.go

import (
    "google.golang.org/grpc"
    "google.golang.org/grpc/keepalive"
)

grpcSrv := grpc.NewServer(
    // Server sends HTTP/2 PINGs to keep NAT/LB connection alive.
    grpc.KeepaliveParams(keepalive.ServerParameters{
        MaxConnectionIdle: 5 * time.Minute,  // idle time before ping
        Time:              30 * time.Second, // ping interval
        Timeout:           10 * time.Second, // wait for ping ack before closing
    }),
    // Enforcement policy: reject clients that ping too aggressively.
    grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
        MinTime:             15 * time.Second, // min interval between client pings
        PermitWithoutStream: true,             // allow pings even with no active streams
    }),
)
pb.RegisterMQServiceServer(grpcSrv, mqSrv)
```

**Import path:** `google.golang.org/grpc/keepalive` — part of the grpc-go module; no separate install.

**Why `PermitWithoutStream: true`:** The Consume stream may be idle (no messages flowing) for minutes. Without this flag, the server rejects keepalive pings on idle connections, causing Collector clients to time out.

---

### Pattern 4: HTTP Inspect Handler (Shared State)

**What:** `net/http` ServeMux on a separate port (`:8080`). Handler reads atomics from `*MQServer` directly — no mutex in the hot path. JSON serialized via `encoding/json`. The gRPC server and HTTP server share the same `*MQServer` pointer; they run in the same OS process.

```go
// internal/mq/server/http_server.go

package server

import (
    "encoding/json"
    "net/http"
    "sync/atomic"
)

// InspectResponse is the canonical JSON shape for GET /api/v1/queue/inspect.
// All field names are stable (do not rename without coordination with callers).
type InspectResponse struct {
    Capacity        int   `json:"capacity"`
    Depth           int   `json:"depth"`
    ProducedTotal   int64 `json:"produced_total"`
    ConsumedTotal   int64 `json:"consumed_total"`
    DroppedTotal    int64 `json:"dropped_total"`
    ActiveConsumers int32 `json:"active_consumers"`
}

func (s *MQServer) HandleInspect(w http.ResponseWriter, r *http.Request) {
    stats := s.store.Inspect() // acquires + releases store mu internally
    resp := InspectResponse{
        Capacity:        stats.Capacity,
        Depth:           stats.Depth,
        ProducedTotal:   atomic.LoadInt64(&s.produced),
        ConsumedTotal:   atomic.LoadInt64(&s.consumed),
        DroppedTotal:    stats.Dropped,
        ActiveConsumers: atomic.LoadInt32(&s.activeC),
    }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    _ = json.NewEncoder(w).Encode(resp)
}
```

**Example response:**
```json
{
  "capacity": 10000,
  "depth": 342,
    "produced_total": 58210,
  "consumed_total": 57868,
  "dropped_total": 100,
  "active_consumers": 3
}
```

**Wire-up in cmd/mq/main.go:**
```go
mux := http.NewServeMux()
mux.HandleFunc("GET /api/v1/queue/inspect", mqSrv.HandleInspect)
httpSrv := &http.Server{
    Addr:         cfg.HTTPAddr, // default ":8080"
    Handler:      mux,
    ReadTimeout:  5 * time.Second,
    WriteTimeout: 10 * time.Second,
}
```

Note: `"GET /api/v1/queue/inspect"` with method prefix requires Go 1.22+ ServeMux (satisfied; go.mod declares `go 1.26`).

---

### Pattern 5: Proto Contract (mq.proto)

**What:** Single proto file defining the 12-field DCGM telemetry payload and the two RPCs. CSV column names map to proto field names via snake_case conversion.

```protobuf
// api/proto/mq.proto
syntax = "proto3";

package mq.v1;

option go_package = "github.com/ajitg/vantage/pkg/pb";

// TelemetryMessage carries a single DCGM GPU metric reading.
// Fields map to DCGM CSV columns: timestamp, metric_name, gpu_id, device,
// uuid, modelName (→ model_name), Hostname (→ hostname), container, pod,
// namespace, value, labels_raw.
//
// WARNING: Field numbers are stable once deployed. Never renumber fields.
// Adding new fields is backwards compatible; removing or renumbering is not.
message TelemetryMessage {
    string timestamp   = 1;  // ISO 8601 UTC; restamped by Streamer with time.Now().UTC().Format(time.RFC3339)
    string metric_name = 2;  // e.g., "DCGM_FI_DEV_GPU_UTIL"
    string gpu_id      = 3;  // e.g., "0"
    string device      = 4;  // e.g., "nvidia0"
    string uuid        = 5;  // GPU UUID
    string model_name  = 6;  // e.g., "NVIDIA H100 80GB HBM3"
    string hostname    = 7;
    string container   = 8;
    string pod         = 9;
    string namespace   = 10;
    double value       = 11; // metric value (GPU utilization %, memory bytes, etc.)
    string labels_raw  = 12; // raw Prometheus label string
}

message ProduceRequest {
    TelemetryMessage message = 1;
}

message ProduceResponse {
    bool accepted = 1; // true = enqueued (or drop-oldest fired); false only on validation error
}

// ConsumeRequest is intentionally minimal. consumer_id is optional; used only
// for server-side logging to correlate which Collector instance holds a stream.
message ConsumeRequest {
    string consumer_id = 1;
}

service MQService {
    // Produce enqueues a single telemetry message.
    // Never blocks. Drop-oldest fires silently when the buffer is full.
    rpc Produce(ProduceRequest) returns (ProduceResponse);

    // Consume opens a persistent server-side stream.
    // Exactly one consumer receives each message (work-queue semantics).
    // The stream remains open until the client disconnects or the server shuts down.
    rpc Consume(ConsumeRequest) returns (stream TelemetryMessage);
}
```

**protoc invocation** (already in Makefile `make proto`):
```makefile
proto:
    protoc -I api/proto \
        --go_out=pkg/pb --go_opt=paths=source_relative \
        --go-grpc_out=pkg/pb --go-grpc_opt=paths=source_relative \
        api/proto/*.proto
```

Generates `pkg/pb/mq.pb.go` and `pkg/pb/mq_grpc.pb.go`. The `paths=source_relative` option places output files directly in `pkg/pb/` (not in a subdirectory mirroring the proto directory structure).

**Why `string timestamp` instead of `google.protobuf.Timestamp`:** Avoids importing `google/protobuf/timestamp.proto` and the `timestamppb` Go package for Phase 1. The Streamer restamps with `time.Now().UTC().Format(time.RFC3339)` and the Collector parses with `time.Parse(time.RFC3339, msg.Timestamp)`. If sub-millisecond precision is required, switch to `google.protobuf.Timestamp` in Phase 2 — the proto field number (1) stays stable.

---

### Pattern 6: Graceful Shutdown (Signal Handling)

```go
// cmd/mq/main.go

ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer stop()

g, gctx := errgroup.WithContext(ctx)

g.Go(func() error {
    return grpcSrv.Serve(grpcLis)
})
g.Go(func() error {
    return httpSrv.ListenAndServe()
})
g.Go(func() error {
    <-gctx.Done()
    mqSrv.Shutdown()               // closes workCh; dispatch goroutine exits
    grpcSrv.GracefulStop()         // waits for in-flight RPCs to finish
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    return httpSrv.Shutdown(shutdownCtx)
})

if err := g.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
    log.Fatal(err)
}
```

**Shutdown sequence:**
1. SIGTERM received → `gctx` cancelled
2. `mqSrv.Shutdown()` → closes `shutdownCh` → dispatch goroutine exits → closes `workCh`
3. All `Consume` goroutines unblock from `case msg, ok := <-s.workCh` with `ok=false` → return nil (clean exit)
4. `grpcSrv.GracefulStop()` → waits for any in-flight `Produce` RPCs to complete
5. `httpSrv.Shutdown()` → drains HTTP connections

---

### Pattern 7: Config (Env-First, Flag Fallback)

```go
// internal/mq/config/config.go

package config

import (
    "os"
    "strconv"
)

type Config struct {
    GRPCAddr   string // default ":50051"
    HTTPAddr   string // default ":8080"
    BufferSize int    // default 10000; MQ_BUFFER_SIZE env
    WorkChCap  int    // default 1024; set to BufferSize/10 minimum
}

func FromEnv() Config {
    cfg := Config{
        GRPCAddr:   ":50051",
        HTTPAddr:   ":8080",
        BufferSize: 10000,
        WorkChCap:  1024,
    }
    if v := os.Getenv("MQ_BUFFER_SIZE"); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n > 0 {
            cfg.BufferSize = n
            cfg.WorkChCap = max(n/10, 128)
        }
    }
    if v := os.Getenv("MQ_GRPC_ADDR"); v != "" {
        cfg.GRPCAddr = v
    }
    if v := os.Getenv("MQ_HTTP_ADDR"); v != "" {
        cfg.HTTPAddr = v
    }
    return cfg
}
```

---

### Anti-Patterns to Avoid

- **Holding mutex across channel send:** `mu.Lock(); store.buf[tail] = msg; notify <- struct{}{}; mu.Unlock()` — the channel send may block (if notify is full) while the lock is held, blocking all other producers. Fix: release mu before signaling.

- **`sync.RWMutex` on the ring buffer:** Every `TryDequeue` modifies `head` and `count` — it is a write operation. Using RWMutex creates a false sense of read-write separation. Use a single `sync.Mutex`.

- **Per-consumer subscriber channels for work-queue delivery:** This implements pub-sub (fan-out), not work-queue. The dispatch goroutine would need to decide which consumer gets each message. Use a shared `workCh` instead — Go channels guarantee one-receiver-per-send.

- **`time.Sleep` in goroutine synchronization tests:** The race detector adds 5–20x overhead; sleep-based sync fails under `-race`. Use `sync.WaitGroup` or channel signals for goroutine coordination in tests.

- **Checking `stream.Context().Err()` without also selecting on `<-ctx.Done()`:** `stream.Send()` returns an error only after a full round-trip through the gRPC layer, which may buffer writes. The `<-ctx.Done()` select arm detects cancellation proactively on every loop iteration.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| gRPC server keepalive | Custom TCP heartbeat goroutine | `grpc.KeepaliveParams` + `keepalive.ServerParameters` | NAT/LB timeout detection requires HTTP/2 layer ping, not application-layer polling; wrong layer risks double-ping or no-ping |
| Atomic counters | `sync.Mutex` around `int64` | `sync/atomic` (`atomic.AddInt64`, `atomic.LoadInt64`) | Mutex for single-int operations is 3–5x slower under contention; atomics for stats counters are idiomatic Go |
| Context cancellation propagation in gRPC stream | Polling `stream.Context().Err()` with `time.Sleep` | `select { case <-stream.Context().Done(): return ... }` | Polling burns CPU and adds latency; select is zero-cost until cancellation |
| HTTP multiplexing with path variables | Custom router | stdlib `net/http.ServeMux` (Go 1.22+) | MQ has exactly one HTTP endpoint with no path variables; ServeMux with `"GET /api/v1/queue/inspect"` method+path syntax is sufficient |
| JSON serialization for inspect | Manual `fmt.Sprintf` | `encoding/json.NewEncoder(w).Encode(resp)` | Escapes special chars, handles nil, produces valid JSON; Sprintf is error-prone |

---

## Common Pitfalls

### Pitfall 1: Lock Held Across Channel Send (Deadlock)
**What goes wrong:** Acquiring the ring buffer mutex in `Enqueue`, then (while still holding the lock) signaling the dispatch goroutine's notify channel. If the notify channel is full (capacity 1, already signaled), the send blocks. The mutex is held indefinitely. Any concurrent `TryDequeue` or `Inspect` call deadlocks on `mu.Lock()`.

**Root cause:** Channel send inside a locked section without checking if the channel operation could block.

**Prevention:** Signal notify with a non-blocking `select { case notify <- struct{}{}: default: }`. The dispatch goroutine also has a 1ms ticker fallback, so a dropped signal is harmless.

**Detection:** `go test -race -timeout 10s -count=5` hangs → sends `SIGQUIT` → goroutine dump shows `[semacquire]` on `mu`.

---

### Pitfall 2: Goroutine Leak on Consumer Disconnect
**What goes wrong:** A `Consume` goroutine blocks on `<-s.workCh` waiting for the next message. The client disconnects. `stream.Context()` is cancelled. But the goroutine is blocked on the channel receive, not on `stream.Send()`, so it never checks the cancelled context.

**Prevention:** Always `select` on both `workCh` and `ctx.Done()`. The Consume implementation above uses this pattern. Verify with `TestMQ_GoroutineLeak`: assert `runtime.NumGoroutine()` returns to baseline within 5 seconds after cancelling all consumer contexts.

**Detection:** `runtime.NumGoroutine()` grows monotonically in a test that creates and cancels 100 consumers.

---

### Pitfall 3: N Produced ≠ N Consumed in Correctness Test (False Failure)
**What goes wrong:** The correctness test produces N messages with a buffer smaller than N. Drop-oldest fires during the test, reducing consumed count below N. The test fails with `expected 1000, got 987` — not a bug, but a test design error.

**Prevention:** Size the ring buffer to at least 2×N in the correctness test. The buffer must NEVER overflow during the assertion window. Document this constraint in the test with a comment.

**Detection:** Test fails with `consumed < produced`; `inspect.dropped_total` > 0 during the test.

---

### Pitfall 4: Data Race on Ring Buffer Slice
**What goes wrong:** Using a dynamically-growing slice (`append`) for the ring buffer instead of a pre-allocated fixed-capacity slice. `append` may reallocate the backing array and update the slice header non-atomically. Any concurrent access (even under RLock) to the old pointer causes a data race.

**Prevention:** Pre-allocate with `make([]*pb.TelemetryMessage, capacity)` in `NewRingStore`. Never append. Access only under `sync.Mutex`. The race detector catches this immediately.

**Detection:** `go test -race` reports a race on the `buf` field.

---

### Pitfall 5: gRPC Stream Silent Death Without Keepalive
**What goes wrong:** Long-lived `Consume` streams die silently when Kubernetes kube-proxy (IPVS, 350s timeout) or NAT gateways (60–90s) drop idle TCP connections. The Collector continues blocking on `RecvMsg` waiting for the next message. The MQ's dispatch goroutine continues sending to `workCh`, but `stream.Send` eventually times out or returns an error. Without keepalive, this is undetectable for minutes.

**Prevention:** Configure `grpc.KeepaliveParams` on the server (see Pattern 3). The Collector client must also configure `keepalive.ClientParameters` (`Time: 20s, Timeout: 10s, PermitWithoutStream: true`) — this is a Phase 2 concern for the Collector client, but the server-side configuration is established here in Phase 1.

**Detection:** Consumer stops receiving messages after 5 minutes of low-volume traffic; MQ active_consumers metric does not decrement.

---

## Code Examples

### Complete Consume Loop (Context-Aware)

```go
// Source: idiomatic Go gRPC server-streaming pattern; keepalive types from
// google.golang.org/grpc/keepalive; stream interface from pkg/pb generated code

func (s *MQServer) Consume(req *pb.ConsumeRequest, stream pb.MQService_ConsumeServer) error {
    atomic.AddInt32(&s.activeC, 1)
    defer atomic.AddInt32(&s.activeC, -1)

    ctx := stream.Context()
    for {
        select {
        case <-ctx.Done():
            // Client disconnected or server shutdown propagated via context.
            // No goroutine leak: this goroutine exits cleanly.
            return ctx.Err()
        case msg, ok := <-s.workCh:
            if !ok {
                // workCh closed by MQServer.Shutdown(): graceful exit.
                return nil
            }
            if err := stream.Send(msg); err != nil {
                // Send failed (network error, client gone). Message is lost;
                // acceptable per spec (in-memory, no persistence in Phase 1).
                return err
            }
            atomic.AddInt64(&s.consumed, 1)
        }
    }
}
```

### Concurrent Correctness Test

```go
// internal/mq/server/grpc_server_test.go
// Proves: N messages produced = N messages consumed across K concurrent consumers.
// Buffer > N ensures drop-oldest NEVER fires (non-bug loss would mask real bugs).

func TestMQ_Concurrent_UniqueDelivery(t *testing.T) {
    const (
        N          = 2000  // messages to produce
        K          = 3     // concurrent consumers
        bufferSize = N * 2 // oversized: zero drops during test
    )
    s := store.NewRingStore(bufferSize)
    srv := NewMQServer(s, N)
    defer srv.Shutdown()

    var received atomic.Int64
    var wg sync.WaitGroup
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()

    // Start K consumers, each draining from workCh directly
    // (unit test: bypass gRPC transport; test server logic only)
    for i := 0; i < K; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                case msg, ok := <-srv.workCh:
                    if !ok {
                        return
                    }
                    _ = msg
                    received.Add(1)
                    if received.Load() == N {
                        cancel() // all received; signal others to stop
                    }
                }
            }
        }()
    }

    // Produce N messages
    for i := 0; i < N; i++ {
        srv.Produce(context.Background(), &pb.ProduceRequest{
            Message: &pb.TelemetryMessage{MetricName: fmt.Sprintf("m%d", i)},
        })
    }

    wg.Wait()
    require.Equal(t, int64(N), received.Load(),
        "each message must be delivered to exactly one consumer; total must equal N")
    require.Equal(t, int64(0), s.Inspect().Dropped,
        "zero drops: buffer was sized to prevent non-bug loss")
}
```

### Goroutine Leak Test

```go
func TestMQ_GoroutineLeak(t *testing.T) {
    s := store.NewRingStore(1000)
    srv := NewMQServer(s, 100)
    defer srv.Shutdown()

    baseline := runtime.NumGoroutine()

    // Simulate K consumers connecting and disconnecting
    const K = 10
    var wg sync.WaitGroup
    for i := 0; i < K; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            ctx, cancel := context.WithCancel(context.Background())
            // Cancel immediately to simulate consumer disconnect
            go func() {
                time.Sleep(10 * time.Millisecond)
                cancel()
            }()
            _ = srv.Consume(&pb.ConsumeRequest{}, &mockConsumeStream{ctx: ctx})
        }()
    }
    wg.Wait()

    require.Eventually(t, func() bool {
        return runtime.NumGoroutine() <= baseline+2 // +2 tolerance for dispatch goroutine
    }, 5*time.Second, 50*time.Millisecond,
        "goroutine count must return to baseline after all consumers disconnect")
}
```

### Drop-Oldest Test

```go
func TestRingStore_DropOldest(t *testing.T) {
    s := store.NewRingStore(3)

    msg := func(name string) *pb.TelemetryMessage {
        return &pb.TelemetryMessage{MetricName: name}
    }

    s.Enqueue(msg("a"))
    s.Enqueue(msg("b"))
    s.Enqueue(msg("c"))
    dropped := s.Enqueue(msg("d")) // buffer full: "a" is dropped

    require.True(t, dropped, "fourth enqueue must report drop")
    require.Equal(t, int64(1), s.Inspect().Dropped)
    require.Equal(t, 3, s.Inspect().Depth)

    // Dequeue order: "b", "c", "d" — "a" was dropped
    got, ok := s.TryDequeue()
    require.True(t, ok)
    require.Equal(t, "b", got.MetricName, "oldest surviving message after drop-oldest is 'b'")
}
```

---

## Don't Hand-Roll (Summary Table)

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HTTP/2 keepalive pings | Application-layer heartbeat goroutine | `keepalive.ServerParameters` via `grpc.KeepaliveParams` | Must be HTTP/2 layer; grpc-go handles this correctly |
| Concurrent int64 counter | `sync.Mutex`-guarded `int64` | `atomic.AddInt64` / `atomic.LoadInt64` | 3–5x faster; idiomatic for stats/metrics |
| Per-consumer channel fan-out | Map of subscriber channels | Shared `workCh` + channel compete semantics | Fan-out implements pub-sub (every consumer gets every message); work-queue requires shared channel |
| Goroutine-safe ring buffer without mutex | Lock-free ring buffer with atomics | `sync.Mutex` + fixed slice | Lock-free ring buffers require careful memory barrier analysis; sync.Mutex is provably correct and `go test -race` verifies it |
| JSON serialization | `fmt.Sprintf` | `encoding/json` | Handles escaping, nil fields, type coercion correctly |

---

## Runtime State Inventory

Not applicable — this is a greenfield phase. No runtime state to migrate.

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go 1.26 | All packages | ✓ | 1.26 (go.mod declares `go 1.26`) | — |
| `protoc` binary | `make proto` | [ASSUMED] — not verified in this session | system install needed | `apt install protobuf-compiler` / `brew install protobuf` |
| `protoc-gen-go` | `make proto` | installed by `make tools` | matches grpc-go module | `make tools` installs it |
| `protoc-gen-go-grpc` | `make proto` | installed by `make tools` | matches grpc-go module | `make tools` installs it |
| Docker | Phase 5 (not Phase 1) | — | — | Not needed in Phase 1 |

**Missing dependencies with no fallback:**
- `protoc` binary must be installed on the development machine before `make proto` will succeed. `make tools` does not install it (installs only the Go plugins). Add installation instruction to README or Makefile.

**Verification command:**
```bash
protoc --version  # expect: libprotoc 3.21+
```

---

## Validation Architecture

> `workflow.nyquist_validation: true` — this section is required.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing package (stdlib) + `github.com/stretchr/testify` v1.11.1 |
| Config file | none — standard `go test` discovery |
| Quick run command | `go test -race ./internal/mq/...` |
| Full suite command | `go test -race -count=5 -covermode=atomic -coverprofile=coverage.out ./internal/...` |
| Coverage report | `go tool cover -func=coverage.out` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|--------------|
| MQ-04 | Ring buffer is thread-safe (no data races) | unit + race detector | `go test -race ./internal/mq/store/...` | ❌ Wave 0 |
| MQ-05 | Drop-oldest fires when buffer full; Enqueue never blocks | unit | `go test ./internal/mq/store/ -run TestRingStore_DropOldest` | ❌ Wave 0 |
| MQ-03 | Each message to exactly one consumer | concurrent unit | `go test -race ./internal/mq/server/ -run TestMQ_Concurrent_UniqueDelivery` | ❌ Wave 0 |
| MQ-07 | Consumer disconnect causes no goroutine leak | unit | `go test ./internal/mq/server/ -run TestMQ_GoroutineLeak` | ❌ Wave 0 |
| MQ-06 | Inspect returns correct JSON shape | unit (httptest) | `go test ./internal/mq/server/ -run TestMQ_Inspect` | ❌ Wave 0 |
| MQ-08 | Store interface is satisfied by RingStore | compilation | `go build ./...` (compile-time check) | ❌ Wave 0 |
| QA-02 | N produced = N consumed across K consumers | concurrent unit | `go test -race -count=10 ./internal/mq/server/ -run TestMQ_Concurrent` | ❌ Wave 0 |

### Test Coverage Strategy

- `pkg/pb/` — generated code; exclude from coverage gate. No test file.
- `cmd/mq/main.go` — thin wiring; aim for smoke-test coverage via integration test. Exclude from 90% gate.
- `internal/mq/store/` — target 100% (simple, deterministic logic; full coverage achievable).
- `internal/mq/server/` — target ≥90%. Cover: Produce, Consume (normal path, client disconnect, server shutdown), dispatch goroutine, Shutdown.
- `internal/mq/config/` — target 100% (pure env-parsing logic).

**Makefile adjustment needed:** The coverage gate should measure `./internal/...` only, not `./...` (which includes generated `pkg/pb/` and `cmd/` wiring). Update coverage target:

```makefile
coverage: test
    @go tool cover -func=coverage.out | grep -E "^total"
    # Measure only internal/ packages for the 90% gate
    go test -race -covermode=atomic -coverprofile=coverage.out ./internal/...
    ...
```

### Sampling Rate

- **Per task commit:** `go test -race ./internal/mq/...`
- **Per wave merge:** `go test -race -count=5 ./internal/...`
- **Phase gate:** Full suite green + `go tool cover` showing ≥90% on `./internal/...` before `/gsd-verify-work`

### Wave 0 Gaps

- [ ] `internal/mq/store/ring_store_test.go` — covers MQ-04, MQ-05 (basic enqueue/dequeue, drop-oldest, wrap-around, inspect)
- [ ] `internal/mq/server/grpc_server_test.go` — covers MQ-03, MQ-07, QA-02 (concurrent delivery, goroutine leak, dispatch)
- [ ] `internal/mq/server/http_server_test.go` — covers MQ-06 (inspect JSON shape via `net/http/httptest`)
- [ ] `internal/mq/config/config_test.go` — covers Config parsing from env vars
- [ ] Proto compilation: `make proto` must succeed before any Go test compiles

---

## Security Domain

> `security_enforcement: true`, `security_asvs_level: 1` — section required.

### Applicable ASVS Categories (Phase 1 scope)

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | MQ is an internal service in the Kubernetes cluster; no external-facing auth in scope (see Out of Scope) |
| V3 Session Management | no | gRPC streams are transport-level; no session tokens |
| V4 Access Control | no | Single-process service; all access via gRPC/HTTP on cluster-internal ports |
| V5 Input Validation | **yes** | Validate `req.Message != nil` before enqueue; return `codes.InvalidArgument`; never panic on nil proto fields |
| V6 Cryptography | no | No encryption at rest or in transit in Phase 1 (internal k8s service mesh may add TLS later) |

### Known Threat Patterns for MQ gRPC + HTTP

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Nil proto message in Produce RPC | Tampering | Check `req.GetMessage() != nil`; return `codes.InvalidArgument`; do not pass nil to store.Enqueue |
| Unbounded message size (large `labels_raw`) | DoS | gRPC default max message size is 4MB; protobuf enforces per-field size via encoding length; acceptable for telemetry payloads |
| HTTP inspect endpoint exposure | Information Disclosure | Bind to cluster-internal port only; do not expose via Ingress or NodePort; document in Helm values |
| Goroutine exhaustion via consumer connections | DoS | `activeC` counter provides visibility; gRPC connection limits apply (`grpc.MaxConcurrentStreams` option if needed) |

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `protoc` binary is available on the developer machine (not installed by `make tools`) | Environment Availability | `make proto` fails; pkg/pb never generated; nothing compiles |
| A2 | Go 1.22+ `net/http.ServeMux` method+path pattern syntax is available (declared in go.mod as `go 1.26`) | HTTP Inspect Handler | `"GET /api/v1/queue/inspect"` route registration fails on Go < 1.22; fallback: use `mux.HandleFunc("/api/v1/queue/inspect", ...)` with manual method check |
| A3 | `google.golang.org/grpc` v1.81.1 is resolvable from pkg.go.dev (versions from STACK.md) | Standard Stack | `go get` fails; use latest resolvable version |
| A4 | `workCh` capacity of 1024 (default) is sufficient for the test workload | Pattern 2: Dispatch | If workCh fills, dispatch goroutine blocks; drop-oldest fires in ring buffer; increase capacity |

**If this table is empty:** N/A — four assumptions documented above.

---

## Open Questions

1. **protoc binary installation in CI**
   - What we know: `make tools` installs `protoc-gen-go` and `protoc-gen-go-grpc` Go plugins but not the `protoc` binary itself.
   - What's unclear: Whether the CI environment (if any) has `protoc` pre-installed.
   - Recommendation: Add `which protoc || (apt-get install -y protobuf-compiler)` to the Makefile `tools` target or document it in README. Phase 1 plan should include a task to verify `protoc --version` returns cleanly.

2. **Coverage gate scope**
   - What we know: Current Makefile `coverage` target runs `go test ./...` and measures total coverage.
   - What's unclear: Whether generated `pkg/pb/` code inflates or deflates the total (generated stubs have many functions, most uncovered).
   - Recommendation: Scope the coverage gate to `./internal/...` in the `coverage` Makefile target. The planner should include a task to update the Makefile `coverage` target with `./internal/...` instead of `./...`.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `github.com/golang/protobuf` | `google.golang.org/protobuf` | 2020 | Old package is deprecated; grpc-go v1.x depends on the new module; do not import old package directly |
| `grpc.Dial()` | `grpc.NewClient()` | grpc-go v1.58 | `grpc.Dial` is deprecated; use `grpc.NewClient` for client construction in Collector/Streamer (Phase 2/3) |
| `net/http` path-only routing | `net/http.ServeMux` with method+path patterns | Go 1.22 | `"GET /api/v1/queue/inspect"` syntax works in Go 1.22+; no need for chi or gorilla/mux for a single endpoint |
| `go.sum` v2 format | Same format, `go mod tidy` manages it | — | No change needed; single module |

**Deprecated/outdated:**
- `grpc.Dial`: deprecated in grpc-go v1.58 — use `grpc.NewClient` in Streamer/Collector clients.
- `github.com/golang/protobuf`: deprecated — never import directly.

---

## Sources

### Primary (HIGH confidence)
- `instructions.md` — authoritative assignment brief (MQ requirements, endpoints, delivery semantics) [CITED: project file]
- `CLAUDE.md` — repo conventions, layout, hard constraints [CITED: project file]
- `.planning/phases/01-foundation-proto-contract-mq-core/01-CONTEXT.md` — locked decisions for this phase [CITED: project file]
- `.planning/REQUIREMENTS.md` — requirement IDs MQ-01..08, QA-02 [CITED: project file]
- Go specification on channel memory model (goroutine-safe send/receive) [ASSUMED: training knowledge]
- `google.golang.org/grpc/keepalive` package — `ServerParameters`, `EnforcementPolicy` types [ASSUMED: training knowledge, verified against PITFALLS.md]

### Secondary (MEDIUM confidence)
- `.planning/research/ARCHITECTURE.md` — MQ concurrency model, service boundaries, competing-consumer pattern [CITED: project file]
- `.planning/research/PITFALLS.md` — 20 catalogued pitfalls with phase mapping and warning signs [CITED: project file]
- `.planning/research/STACK.md` — library versions verified against pkg.go.dev (Jun 2026) [CITED: project file]
- `.planning/research/SUMMARY.md` — synthesized findings + Phase-1 exit criteria [CITED: project file]

### Tertiary (LOW confidence)
- DCGM CSV column inspection (`head -3 dcgm_metrics_20250718_134233.csv`) — proto field mapping [VERIFIED: local file]

---

## Metadata

**Confidence breakdown:**
- Ring buffer design and lock discipline: HIGH — idiomatic Go; race detector verifiable
- Store interface shape: HIGH — derived from locked decisions in CONTEXT.md
- gRPC keepalive types: HIGH — exact type names from PITFALLS.md + grpc-go documentation
- Proto field mapping: HIGH — CSV headers inspected directly from source file
- Library versions: MEDIUM — from STACK.md which verified against pkg.go.dev (Jun 2026)
- protoc availability: LOW (ASSUMED) — not verified in this session

**Research date:** 2026-06-27
**Valid until:** 2026-07-27 (grpc-go releases frequently; verify version before installing)
