---
phase: 01-foundation-proto-contract-mq-core
plan: "02"
subsystem: mq-ring-buffer
status: complete
tags: [go, ring-buffer, concurrency, tdd, store-interface, grpc-seam]
completed: "2026-06-27"
duration: "~3 minutes"

dependency_graph:
  requires:
    - 01-01 (pkg/pb TelemetryMessage type used in all Store method signatures)
  provides:
    - internal/queue.Store interface (Enqueue/TryDequeue/Inspect/Close)
    - internal/queue.StoreStats struct (Depth/Capacity/Dropped)
    - internal/queue.RingStore — concrete in-memory backend
    - internal/queue.NewRingStore(capacity int) — constructor
    - QA-02 concurrent correctness test (N=2000, K=3, race-clean across 50 runs)
  affects:
    - 01-03-PLAN.md (gRPC server imports internal/queue and wires NewRingStore as Store backend)
    - Phase 6 WAL backend (slots in behind Store interface without changing consumers)

tech_stack:
  added:
    - github.com/stretchr/testify v1.11.1 promoted from indirect to direct dependency
    - sync/atomic.Int64 (Go 1.19+) for lock-free received counter in QA-02 test
  patterns:
    - Single sync.Mutex guards all RingStore state (sync.RWMutex rejected: TryDequeue mutates head/count on every call)
    - Compile-time interface assertion: var _ Store = (*RingStore)(nil)
    - Value-copy Inspect() snapshot prevents external mutation of internal state
    - Test context + explicit cancel() call for fast goroutine teardown in concurrent test

key_files:
  created:
    - internal/queue/store.go
    - internal/queue/ring_store.go
    - internal/queue/ring_store_test.go
  modified:
    - go.mod (testify promoted to direct dependency)
    - go.sum (testify transitive deps added by go mod tidy)

decisions:
  - "sync.Mutex over sync.RWMutex for RingStore: TryDequeue mutates head and count on every call, making a shared read-lock semantically incorrect — all paths are writes"
  - "Store.Enqueue returns bool (true=dropped) not error: in-memory backend is unconditional; boolean avoids forcing consumers to handle a never-occurring error"
  - "Inspect() returns StoreStats by value: snapshot copy prevents callers from racing on internal state through a shared pointer"
  - "NewRingStore panics on capacity <= 0: non-positive capacity is a programming error (equivalent to make([]T, 0)); panic is more appropriate than returning error for invariant violations"
  - "Test buffer at 2×N (4000 slots for 2000 messages): prevents drop-oldest from firing during correctness window; non-zero Dropped would indicate test design error, not ring buffer bug"
  - "Explicit cancel() call in QA-02 goroutines when received reaches N: ensures sibling goroutines exit via ctx.Done() within microseconds rather than spinning until 30s timeout"

metrics:
  duration: "~3 minutes"
  completed: "2026-06-27"
  tasks: 2
  files_created: 3
  files_modified: 2
  commits: 2
---

# Phase 01 Plan 02: Store Interface + RingStore Implementation Summary

**One-liner:** Thread-safe bounded ring buffer with drop-oldest semantics behind a `Store` interface seam, race-verified across 50 runs with N=2000 messages delivered to K=3 competing consumers with zero duplicates, zero drops.

## What Was Built

### internal/queue/store.go
Defines the `Store` interface (WAL seam per MQ-08) and `StoreStats` value type.
- `Store` interface: `Enqueue(*pb.TelemetryMessage) bool`, `TryDequeue() (*pb.TelemetryMessage, bool)`, `Inspect() StoreStats`, `Close() error`
- `StoreStats`: value type with `Depth int`, `Capacity int`, `Dropped int64`
- Godoc explains the WAL seam contract and the nil-msg precondition for Enqueue

### internal/queue/ring_store.go
Concrete in-memory implementation with drop-oldest overflow policy.
- `RingStore` struct: unexported fields `mu sync.Mutex`, `buf []*pb.TelemetryMessage`, `head int`, `tail int`, `count int`, `cap int`, `dropped int64`
- `NewRingStore(capacity int)` panics on `capacity <= 0` with descriptive message
- `Enqueue`: acquires lock, drops oldest on overflow (nil-clears slot for GC, advances head, increments dropped), writes to tail, returns bool indicating whether drop fired
- `TryDequeue`: acquires lock, returns `(nil, false)` on empty, otherwise copies head slot, nil-clears it (GC), advances head, decrements count, returns `(msg, true)`
- `Inspect`: returns `StoreStats` value copy under lock — snapshot cannot race with mutations
- `Close`: returns nil (no-op for in-memory backend)
- Compile-time assertion: `var _ Store = (*RingStore)(nil)` catches interface drift at build time

### internal/queue/ring_store_test.go (QA-02)
Six test functions covering all behavioral requirements:
- `TestRingStore_Enqueue`: Depth/Dropped/Capacity stats after two enqueues
- `TestRingStore_TryDequeue_Empty`: ok=false, msg=nil on empty buffer
- `TestRingStore_DropOldest`: verifies "b" is the oldest surviving message after dropping "a"
- `TestRingStore_WrapAround`: FIFO order preserved across ring wrap-around boundary
- `TestRingStore_Inspect`: Capacity/Depth/Dropped stats after one overflow
- `TestRingStore_Concurrent_UniqueDelivery`: K=3 goroutines, N=2000, buffer=4000; asserts received==2000 and dropped==0 under `-race -count=50`

## Tasks Completed

| Task | Description | Commit |
|------|-------------|--------|
| 1 (TDD RED) | ring_store_test.go — all 6 tests written; fails to compile (no impl yet) | fb3aaa5 |
| 1 (TDD GREEN) | store.go + ring_store.go — implementation; all tests pass under -race | 6707b46 |

## Verification Results

| Check | Result |
|-------|--------|
| `go build ./internal/queue/...` | PASS — compile-time assertion confirms RingStore satisfies Store |
| `go test -race -v ./internal/queue/...` | PASS — all 6 tests pass, no data races |
| `go test -race -count=50 -timeout=300s ./internal/queue/...` | PASS — clean across all 50 runs |
| `go test -covermode=atomic ./internal/queue/...` | 93.1% — exceeds 90% gate |
| `go build ./...` | PASS — full module builds without error |
| TestRingStore_DropOldest: first dequeue == "b" | PASS — drop-oldest discards "a", "b" is oldest survivor |
| TestRingStore_Concurrent_UniqueDelivery: received == 2000 | PASS |
| TestRingStore_Concurrent_UniqueDelivery: dropped == 0 | PASS |
| No disk/channel/goroutine/gRPC type in internal/queue/ | PASS — pure storage layer |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] go.sum missing testify transitive dependencies**

- **Found during:** TDD RED phase — first `go test` attempt failed with "missing go.sum entry for module providing package github.com/davecgh/go-spew/spew"
- **Issue:** testify was listed as `// indirect` in go.mod from plan 01-01 (no test files existed yet). The transitive deps (go-spew, go-difflib, yaml.v3) were not in go.sum.
- **Fix:** Ran `go mod tidy` which promoted testify to a direct dependency and populated go.sum with all transitive checksums.
- **Files modified:** go.mod (// indirect removed from testify line), go.sum (new entries added)
- **Impact:** None on correctness; all checksums match published versions.

**2. [Rule 2 - Missing critical functionality] Explicit cancel() call in concurrent test goroutines**

- **Found during:** Task 2 implementation — the plan spec used `defer cancel()` only (30-second context timeout). With K=3 consumers draining a buffer of 2000 items, 2 of the 3 goroutines would spin on an empty buffer for up to 30 seconds after the 2000th item was consumed, making `-count=50` infeasible within 300s.
- **Fix:** Added explicit `cancel()` call inside goroutines when `received.Add(1) >= N`, plus a secondary `received.Load() >= N` exit guard in the `default` select case. The `defer cancel()` is retained for cleanup. This is idiomatic Go — multiple cancel() calls are safe (idempotent).
- **Files modified:** internal/queue/ring_store_test.go
- **Impact:** Each test run completes in ~30ms instead of up to 30s; 50 runs finish in under 2 seconds.

## Known Stubs

None — all interface methods are fully implemented. `Close()` returns nil, which is the correct production behavior for the in-memory backend (documented in godoc as WAL-backend extension point).

## Threat Flags

No new network endpoints, auth paths, or trust-boundary-crossing file access introduced. All mitigations from the plan's threat register are applied:
- T-01-02-01: Enqueue precondition documented in Store godoc (enforcement deferred to MQServer.Produce in plan 01-03)
- T-01-02-02: NewRingStore panics with descriptive message on capacity <= 0
- T-01-02-03: Single sync.Mutex guards all RingStore state; verified by `go test -race -count=50`
- T-01-02-04: Accepted (internal service metric on cluster-internal port)

## Self-Check: PASSED

- internal/queue/store.go: FOUND
- internal/queue/ring_store.go: FOUND
- internal/queue/ring_store_test.go: FOUND
- Commit fb3aaa5 (TDD RED): FOUND
- Commit 6707b46 (TDD GREEN): FOUND
- go build ./internal/queue/...: PASS
- go test -race -count=50 ./internal/queue/...: PASS
- Coverage 93.1% >= 90%: PASS
