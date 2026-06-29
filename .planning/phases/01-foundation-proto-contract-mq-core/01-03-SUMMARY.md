---
phase: 01-foundation-proto-contract-mq-core
plan: "03"
subsystem: mq-grpc-http-dataplane
status: complete
tags: [go, grpc, http, dispatch, concurrency, tdd, errgroup, keepalive, inspect]
completed: "2026-06-27"
duration: "~15 minutes"

dependency_graph:
  requires:
    - 01-01 (pkg/pb TelemetryMessage, ProduceRequest, ProduceResponse, ConsumeRequest; MQServiceServer interface)
    - 01-02 (internal/queue.Store interface, NewRingStore constructor, StoreStats)
  provides:
    - internal/server.MQServer (Produce, Consume, dispatch goroutine, Stats, Shutdown)
    - internal/server.ServerStats value type
    - internal/http (package mqhttp) — InspectHandler + InspectResponse
    - internal/config.Config + FromEnv
    - cmd/mq/main.go — fully wired MQ service with errgroup lifecycle management
  affects:
    - Phase 2 (Streamer: gRPC Produce client to MQ service — uses pkg/pb ProduceRequest)
    - Phase 3 (Collector: gRPC Consume client to MQ service — uses pkg/pb ConsumeRequest)
    - Phase 5 (DevOps: Dockerfile + Helm for cmd/mq; env vars MQ_GRPC_ADDR/MQ_HTTP_ADDR/MQ_BUFFER_SIZE)

tech_stack:
  added:
    - golang.org/x/sync v0.21.0 — promoted from indirect to direct dependency for errgroup
    - grpc.KeepaliveParams + keepalive.EnforcementPolicy (from grpc-go; no extra install)
  patterns:
    - Single dispatch goroutine drains Store into workCh; lock released before channel send (lock-across-channel deadlock prevention)
    - defer close(workCh) in dispatch() ensures exactly-once workCh close regardless of exit path
    - Go channel semantics enforce single-dispatch: only one Consume goroutine receives each workCh message
    - Non-blocking notify channel (buffered 1) from Produce to dispatch — avoids blocking the caller
    - sync.Once for idempotent Shutdown
    - errgroup.WithContext for concurrent gRPC + HTTP lifecycle with graceful teardown
    - Go 1.22+ method-scoped ServeMux pattern ("GET /api/v1/queue/inspect")
    - atomic reads in Stats() — no mutex in HTTP hot path

key_files:
  created:
    - internal/server/server.go
    - internal/server/server_test.go
    - internal/http/inspect.go
    - internal/http/inspect_test.go
    - internal/config/config.go
    - internal/config/config_test.go
  modified:
    - cmd/mq/main.go (was stub; fully replaced)
    - go.mod (golang.org/x/sync promoted to direct)
    - go.sum (new entries)
    - .gitignore (added /mq, /streamer, /collector, /gateway root-level binary patterns)

decisions:
  - "dispatch uses defer close(workCh) rather than explicit close in each exit branch: eliminates double-close risk regardless of which select branch exits dispatch"
  - "drain() returns bool (done=true on shutdown, done=false on empty): allows dispatch to distinguish shutdown-during-drain from normal empty-store condition without an additional channel"
  - "Non-blocking notify (select with default): Produce never blocks; if dispatch is already awake, the signal is simply dropped — dispatch will poll again within 1ms via ticker fallback"
  - "Consume for-select covers both ctx.Done() and workCh simultaneously: prevents goroutine from blocking on workCh after consumer disconnects (MQ-07)"
  - "ActiveConsumers counter uses atomic.Int32 with Add(+1)/Add(-1) in defer: accurately reflects in-flight consumer count even under concurrent disconnect"
  - "WorkChCap = max(BufferSize/10, 128): work channel is 10% of buffer size (provides pipelining headroom without excessive memory); floor of 128 prevents near-zero configs from stalling"
  - "errgroup.WithContext for three goroutines: gRPC + HTTP + shutdown-watcher; shutdown-watcher waits for gctx.Done() (signal received) then tears down in order: mqSrv.Shutdown → grpcSrv.GracefulStop → httpSrv.Shutdown(5s)"

metrics:
  duration: "~15 minutes"
  completed: "2026-06-27"
  tasks: 2
  files_created: 6
  files_modified: 4
  commits: 4
---

# Phase 01 Plan 03: MQ gRPC Data Plane + HTTP Control Plane Summary

**One-liner:** Single-dispatch gRPC MQ service (Produce unary + Consume server-stream via shared workCh) with HTTP /api/v1/queue/inspect, keepalive configuration, and errgroup-managed graceful shutdown — race-clean across 10 runs, 90%+ coverage.

## What Was Built

### internal/server/server.go

`MQServer` implementing `pb.MQServiceServer` with lock-discipline-correct dispatch:

- `MQServer` struct: embeds `pb.UnimplementedMQServiceServer`; holds `queue.Store`, `notify chan struct{} (cap=1)`, `workCh chan *pb.TelemetryMessage (buffered)`, atomic counters (`produced`, `consumed`, `activeC`), `shutdownCh`, `sync.Once`.
- `NewMQServer(s queue.Store, workChCap int) *MQServer`: validates cap (floor 128), starts `go srv.dispatch()`.
- `dispatch()`: `defer close(workCh)`, for-select on `shutdownCh | notify | ticker(1ms)` → calls `drain()`. Exits on shutdown; `defer` ensures workCh closes exactly once.
- `drain() bool`: tight loop calling `store.TryDequeue()` (mutex acquired+released inside); sends to workCh; returns true if shutdownCh fires during send (no mutex held across channel operation).
- `Produce()`: nil-message guard → `codes.InvalidArgument` (T-01-03-01); `store.Enqueue`; `atomic.AddInt64(&produced, 1)`; non-blocking notify.
- `Consume()`: `atomic.AddInt32(&activeC, 1)` + defer decrement; for-select on `ctx.Done()` and `workCh`; increments consumed after `stream.Send()`.
- `Shutdown()`: `sync.Once` closes `shutdownCh`.
- `Stats()`: all atomic loads + `store.Inspect()` snapshot — no mutex.

### internal/server/server_test.go

Six test functions:

| Test | Assertion |
|------|-----------|
| `TestMQ_Concurrent_UniqueDelivery` | N=2000, K=3: received.Load()==2000, Dropped==0, race-clean |
| `TestMQ_GoroutineLeak` | K=10 disconnect: goroutine count <= baseline+2 within 5s |
| `TestMQServer_Produce_NilMessage` | codes.InvalidArgument, store depth stays 0 |
| `TestMQServer_Stats` | Produced==50, Consumed==50, ActiveConsumers==0 after test |
| `TestMQServer_DefaultWorkChCap` | workChCap=0 does not panic, server is functional |
| `TestMQServer_Shutdown_ClosesConsume` | Consume returns nil when Shutdown() closes workCh |

### internal/http/inspect.go (package mqhttp)

- `InspectResponse` struct with all 6 required JSON fields: `capacity`, `depth`, `produced_total`, `consumed_total`, `dropped_total`, `active_consumers`.
- `InspectHandler(srv *server.MQServer) http.HandlerFunc`: calls `srv.Stats()`, marshals to JSON, sets `Content-Type: application/json`, writes `200 OK`.

### internal/config/config.go

- `Config` struct: `GRPCAddr`, `HTTPAddr`, `BufferSize`, `WorkChCap`.
- `FromEnv()`: defaults `{:50051, :8080, 10000, 1024}`; reads `MQ_BUFFER_SIZE` (positive int only; invalid/zero values ignored); `WorkChCap = max(n/10, 128)`. Pure env parsing, 100% testable.

### cmd/mq/main.go

Wires all components:
- `config.FromEnv()` → `queue.NewRingStore(cfg.BufferSize)` → `server.NewMQServer(s, cfg.WorkChCap)`
- `grpc.NewServer(KeepaliveParams + KeepaliveEnforcementPolicy)` → `pb.RegisterMQServiceServer`
- `http.NewServeMux()` with `"GET /api/v1/queue/inspect"` (Go 1.22+ method-scoped pattern) → `&http.Server{Addr, Handler, ReadTimeout, WriteTimeout}`
- `signal.NotifyContext(SIGTERM|SIGINT)` → `errgroup.WithContext`:
  - g1: `grpcSrv.Serve(grpcLis)`
  - g2: `httpSrv.ListenAndServe()`
  - g3: `<-gctx.Done()` → `mqSrv.Shutdown()` → `grpcSrv.GracefulStop()` → `httpSrv.Shutdown(5s)`

## Tasks Completed

| Task | Phase | Description | Commit |
|------|-------|-------------|--------|
| 1 | TDD RED | server_test.go — TestMQ_Concurrent_UniqueDelivery + TestMQ_GoroutineLeak (fails: no impl) | af77a16 |
| 1 | TDD GREEN | server.go — MQServer impl + additional coverage tests | f3b6d8c |
| 2 | TDD RED | inspect_test.go — TestInspect_JSON (fails: no mqhttp package) | 6c1180f |
| 2 | TDD GREEN | inspect.go, config.go, config_test.go, main.go — all passing | c6a5420 |

## Verification Results

| Check | Result |
|-------|--------|
| `go test -race -count=10 ./internal/server/... ./internal/http/...` | PASS — no data races |
| `go test -race -count=5 ./internal/...` | PASS — all packages |
| `go tool cover ./internal/server/...` | 95.2% — exceeds 90% gate |
| `go tool cover ./internal/http/...` | 100.0% — exceeds 90% gate |
| `go tool cover ./internal/config/...` | 100.0% — exceeds 90% gate |
| `go build ./cmd/mq/...` | PASS |
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `TestMQ_Concurrent_UniqueDelivery`: received==2000 | PASS |
| `TestMQ_Concurrent_UniqueDelivery`: Dropped==0 | PASS |
| `TestMQ_GoroutineLeak`: goroutine count <= baseline+2 within 5s | PASS |
| `TestInspect_JSON`: 200 OK, application/json, ProducedTotal==1 | PASS |
| keepalive options in main.go (`grep -c` outputs 2) | PASS |
| No stray root-level binary committed | PASS |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing critical functionality] Added Stats, nil-message, shutdown, and cap tests to reach >=90% coverage**

- **Found during:** Task 1 GREEN — initial coverage was 78.6% (Stats at 0%, nil-guard untested)
- **Issue:** Plan specified two tests (UniqueDelivery + GoroutineLeak) but these leave Stats(), nil guard, default-cap branch, and Consume-on-closed-workCh untested.
- **Fix:** Added four targeted tests: `TestMQServer_Produce_NilMessage`, `TestMQServer_Stats`, `TestMQServer_DefaultWorkChCap`, `TestMQServer_Shutdown_ClosesConsume`. Coverage rose to 90.5% (later 95.2% under -count=5).
- **Files modified:** internal/server/server_test.go
- **Commit:** f3b6d8c

**2. [Rule 1 - Bug] Stray root-level `mq` binary added to .gitignore**

- **Found during:** Task 2 — `go build ./...` without `-o` flag produced a root-level `mq` binary. The environment note prohibits committing it.
- **Fix:** Added `/mq`, `/streamer`, `/collector`, `/gateway` patterns to .gitignore. Binary was removed before staging.
- **Files modified:** .gitignore
- **Commit:** c6a5420

**3. [Rule 3 - Blocking] golang.org/x/sync not in go.mod**

- **Found during:** Task 2 — main.go imports `golang.org/x/sync/errgroup`; the package was an indirect dependency (via grpc-go v1.81.1) but not listed in go.mod.
- **Fix:** Ran `go get golang.org/x/sync@latest` which promoted it to v0.21.0 and added it to go.mod. The plan pre-authorized this ("run go mod tidy if missing").
- **Files modified:** go.mod, go.sum
- **Commit:** c6a5420

**4. [Rule 2 - Missing critical functionality] Config test added to cover all branches**

- **Found during:** Task 2 — plan specified TestInspect_JSON only; config package needed tests to satisfy >=90% coverage gate.
- **Fix:** Added config_test.go with 5 tests covering all FromEnv branches (defaults, overrides, invalid, zero, WorkChCap floor).
- **Files modified:** internal/config/config_test.go (new file)
- **Commit:** c6a5420

## Known Stubs

None — all components are fully implemented and wired.

## Threat Flags

No new trust boundaries beyond those in the plan's threat register. All mitigations applied:

- T-01-03-01: `req.GetMessage() == nil` guard in Produce returns `codes.InvalidArgument` before any store call — verified by TestMQServer_Produce_NilMessage.
- T-01-03-02: `activeC` counter visible in inspect; grpc-go default `MaxConcurrentStreams=100` provides implicit cap.
- T-01-03-03: Inspect exposes internal metrics; cluster-internal port only (not reachable via Ingress in Phase 1).
- T-01-03-04: grpc-go enforces 4MB default max receive message size at HTTP/2 framing layer.

## Self-Check: PASSED

- internal/server/server.go: FOUND
- internal/server/server_test.go: FOUND
- internal/http/inspect.go: FOUND
- internal/http/inspect_test.go: FOUND
- internal/config/config.go: FOUND
- internal/config/config_test.go: FOUND
- cmd/mq/main.go: FOUND (fully replaced from stub)
- Commit af77a16 (TDD RED — server): FOUND
- Commit f3b6d8c (TDD GREEN — server): FOUND
- Commit 6c1180f (TDD RED — inspect): FOUND
- Commit c6a5420 (TDD GREEN — inspect+config+main): FOUND
- go build ./...: PASS
- go test -race -count=10 ./internal/server/... ./internal/http/...: PASS
- coverage internal/server: 95.2% >= 90%: PASS
- coverage internal/http: 100.0% >= 90%: PASS
- grep -c keepalive cmd/mq/main.go == 2: PASS
