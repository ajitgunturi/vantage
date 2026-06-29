# Pitfalls Research

**Domain:** Elastic GPU Telemetry Pipeline — custom in-memory Go MQ, gRPC server-streaming, pgx/pgxpool batch inserts, PostgreSQL time-series schema, Kubernetes/Helm
**Researched:** 2026-06-27
**Confidence:** HIGH

---

## Critical Pitfalls

### Pitfall 1: Data Race on Ring Buffer Slice Header

**What goes wrong:**
The Go memory model allows the compiler and CPU to reorder loads and stores. A ring buffer implemented as a `[]byte` or `[]Message` slice is safe only when every read AND write of the slice header (pointer, length, capacity) is protected by the same mutex. A common mistake is holding `RLock()` for reads and `Lock()` for writes, but doing `copy(dst, buf[head:tail])` while another goroutine calls `append()` — even with `Lock()` held — because `append` may allocate a new backing array and update the slice header non-atomically relative to readers holding `RLock()`.

**Why it happens:**
Developers trust `sync.RWMutex` at the field level but treat the slice as an atomic value. In Go, a slice is a three-word struct (ptr, len, cap); reading it without a lock while a concurrent writer may reallocate it is a classic data race.

**How to avoid:**
Never use `RWMutex` for the slice header itself. Store messages in a fixed-capacity `[N]Message` array (or a pre-allocated slice with `make([]Message, 0, capacity)` that is never appended to after creation) and access it exclusively under a single `sync.Mutex` — not `RWMutex`. The ring buffer's `head` and `tail` indices can be `uint64` accessed via `atomic.LoadUint64` / `atomic.StoreUint64` only if and only if reads of individual slots are safe after the index is visible (requires memory barrier analysis). In practice, use a plain `sync.Mutex` around enqueue/dequeue — it is simpler and `go test -race` will catch any deviation.

**Warning signs:**
- `go test -race` reports a race on any field of the queue struct.
- Intermittent `index out of range` panics that disappear under reduced concurrency.
- Consumer sees garbled messages (partial overwrites visible mid-copy).

**Phase to address:**
Phase 1 (MQ implementation). The ring buffer concurrency design must be race-free before any other component touches it. Write the race-detector test suite alongside the MQ, not after.

---

### Pitfall 2: Goroutine Leak on Consumer Disconnect

**What goes wrong:**
Each `Consume` RPC spawns a goroutine that loops over the MQ and calls `stream.Send()`. When the client disconnects, `stream.Context()` is cancelled. If the loop does not observe `ctx.Done()` before attempting `stream.Send()`, it blocks on the send to a dead client and the goroutine leaks. With 10 Collector instances cycling through reconnect/disconnect, this accumulates hundreds of leaked goroutines that hold references to the queue's subscriber list — preventing GC of their message buffers.

**Why it happens:**
Developers write the happy path first: `for msg := range msgCh { stream.Send(msg) }`. The channel read blocks cleanly, but `stream.Send()` to a disconnected stream does NOT always return immediately — the gRPC send buffer may accept the write, deferring the error to the next send. The goroutine only discovers the problem after N buffered sends.

**How to avoid:**
Implement a per-subscriber `chan *pb.Message` (bounded, capacity ≈ 128) registered in the MQ's subscriber list. The dispatch goroutine does:
```go
select {
case ch <- msg:
default:
    // drop or count as slow-consumer drop
}
```
The consumer goroutine loops:
```go
for {
    select {
    case <-ctx.Done():
        mq.Unsubscribe(id)
        return ctx.Err()
    case msg, ok := <-ch:
        if !ok { return nil }
        if err := stream.Send(msg); err != nil {
            mq.Unsubscribe(id)
            return err
        }
    }
}
```
Always call `Unsubscribe` in a `defer` so it runs on panic too. Verify with `runtime.NumGoroutine()` assertions in tests.

**Warning signs:**
- `runtime.NumGoroutine()` grows monotonically in a test that creates and cancels 100 consumers.
- Memory usage climbs without bound in a 10-Collector soak test.
- gRPC server logs `transport: http2Server.HandleStreams failed to read frame: read ... use of closed network connection` with no corresponding goroutine cleanup.

**Phase to address:**
Phase 1 (MQ) and Phase 2 (Collector stream integration). Verify with a goroutine-count test in Phase 1; confirm no leaks end-to-end in Phase 2.

---

### Pitfall 3: Deadlock With sync.RWMutex and Subscriber Notification Under Lock

**What goes wrong:**
The MQ calls `Enqueue()` under a write lock, then, while still holding the lock, iterates the subscriber list and tries to send to subscriber channels. If a subscriber channel is full and the send is blocking, the write lock is held indefinitely. Any concurrent Enqueue or Dequeue call will deadlock. Similarly, calling `Unsubscribe()` from inside the subscriber goroutine while Enqueue holds the lock deadlocks on the nested lock acquisition.

**Why it happens:**
Notification logic is added incrementally: the queue starts as a simple ring buffer, then "fan-out to subscribers" is bolted on inside the same critical section. The path where a subscriber channel is full is exercised rarely in development (few producers, fast consumers) but reliably in production.

**How to avoid:**
Separate concerns: `Enqueue` holds the lock only long enough to write to the ring buffer and snapshot the subscriber list (a `[]chan *pb.Message` copy). Release the lock before iterating the snapshot and sending to channels. Use non-blocking sends (`select { case ch <- msg: default: }`) inside the notification loop so a full channel never blocks the Enqueue caller. Subscriber registration/unregistration should use a separate `sync.RWMutex` for the subscriber map, not the same mutex that guards the ring buffer.

**Warning signs:**
- `go test -race -timeout 30s` hangs and must be killed.
- `goroutine N [semacquire]` appears in `SIGQUIT` stack dumps.
- Adding a second test consumer causes the test suite to time out.

**Phase to address:**
Phase 1 (MQ). Lock hierarchy must be designed before code is written, not refactored in after a deadlock is observed.

---

### Pitfall 4: Lost or Duplicated Messages Across Competing Consumers

**What goes wrong:**
The spec says "multiple Collectors pulling from the streaming endpoint receive unique messages." This means work-queue semantics: each message goes to exactly one consumer, not broadcast/pub-sub. A naive implementation that gives every subscriber a reference to the same ring-buffer read pointer will deliver the same message to all consumers (pub-sub). Conversely, a single shared read pointer advanced atomically will drop messages if the ring buffer wraps around before slow consumers read them.

**Why it happens:**
"Unique messages per consumer" is ambiguous. Developers default to pub-sub (broadcast) because it is simpler — no coordination needed on message ownership. The delivery contract (each message to exactly one consumer, no duplication, no loss) requires explicit work-queue design.

**How to avoid:**
Implement true work-queue semantics: messages are dequeued from the ring buffer and dispatched round-robin (or to the first available consumer) into per-subscriber channels. Once dequeued, the slot is consumed. Track the number of active subscribers; if zero, messages accumulate in the ring buffer up to capacity, then the backpressure/drop policy kicks in (see Pitfall 5). Write an explicit test: two concurrent consumers, N messages produced; assert each consumer receives a disjoint subset totaling N with no duplicates.

**Warning signs:**
- Two Collector instances in a load test each receive 100% of messages (total throughput = 2x produced), indicating pub-sub.
- Messages are randomly missing when more than one Collector connects simultaneously.
- The ring buffer read index and subscriber dispatch share no coordination mechanism.

**Phase to address:**
Phase 1 (MQ semantics) and Phase 2 (end-to-end correctness test). The delivery contract test must pass before the Collector is considered integrated.

---

### Pitfall 5: Unbounded Memory Growth Without Backpressure or Drop Policy

**What goes wrong:**
Ten Streamers producing continuously fill the ring buffer faster than one Collector can drain it. Without a bounded ring buffer and an explicit drop policy, memory grows without bound until OOM-kill. Even with a bounded ring buffer, if the dispatch goroutine's per-subscriber channel is also unbounded or the ring buffer blocks on full rather than dropping, Enqueue callers block, goroutines pile up, and memory grows indirectly.

**Why it happens:**
A ring buffer that "blocks until there is space" is safe-feeling but backpressures the caller's goroutine. With 10 Streamer goroutines all blocked waiting for space, the gRPC server's goroutine pool saturates and new connections are refused.

**How to avoid:**
Define and implement an explicit drop policy at design time:
- Ring buffer: fixed capacity (e.g., 65536 messages). On full, drop the oldest message (overwrite head) and increment a `dropped_count` metric. This is the `LOSS_LESS_ON_SLOW_CONSUMER` pattern common in telemetry pipelines — losing old data is preferable to losing the pipeline.
- Per-subscriber channels: fixed capacity (e.g., 256). On full, drop and count.
- Expose `dropped_count` in the `/api/v1/queue/inspect` HTTP endpoint so the problem is observable.
- Set Kubernetes resource limits on the MQ pod (`memory: 256Mi` or appropriate) so OOM-kill is a last resort, not the primary backpressure mechanism.

**Warning signs:**
- MQ pod memory usage grows linearly with time in a soak test.
- `GET /api/v1/queue/inspect` shows `size` growing but `consumers` stable.
- Streamer Produce RPCs start timing out (blocked goroutines exhaust gRPC server threads).

**Phase to address:**
Phase 1 (MQ design). The drop policy must be a named, tested behavior, not an afterthought.

---

### Pitfall 6: gRPC Stream Context Cancellation Not Checked on Send

**What goes wrong:**
`stream.Send(msg)` returns an error when the client has disconnected, but the error is only visible on the *next* send after the client-side close is propagated. If the send loop does not check `stream.Context().Err()` before each send, it will attempt one extra send into a closed connection, receive the error, and then exit — fine for one message, but if the send loop is batching or the message is large, this is a wasted syscall. Worse, some implementations check the error from `Send()` but swallow it (`if err != nil { log.Println(err); continue }`) instead of returning it, causing the loop to run indefinitely on a dead connection.

**Why it happens:**
`stream.Send()` in the happy path never errors. Developers test with cooperative clients that disconnect cleanly, missing the case where the network cuts mid-stream.

**How to avoid:**
Every `Send()` call must propagate its error upward:
```go
if err := stream.Send(msg); err != nil {
    return status.Errorf(codes.Internal, "send failed: %v", err)
}
```
Use `select { case <-ctx.Done(): return ctx.Err() }` at the top of each loop iteration. Set `grpc.KeepaliveParams` on the server and `grpc.KeepaliveClientParams` on the Collector client to detect dead connections within seconds, not minutes.

**Warning signs:**
- Collector logs show no reconnect attempts even after the MQ is restarted.
- `ss.Context().Err()` is `context.Canceled` but the loop is still running.
- gRPC server goroutine count does not decrease after client disconnect.

**Phase to address:**
Phase 1 (MQ Consume handler) and Phase 2 (Collector stream client). Integration test: kill the Collector mid-stream; verify the MQ cleans up the subscriber within 5 seconds.

---

### Pitfall 7: gRPC Keepalive and NAT/Load-Balancer Timeout Killing Long-Lived Streams

**What goes wrong:**
The Consume RPC is a persistent server-side stream. Kubernetes Services are backed by kube-proxy (iptables/IPVS), which has a default TCP connection tracking timeout of 350 seconds (IPVS) or 432000 seconds (iptables, but NAT gateways are often 30–90 seconds). AWS NLB/ALB and most NAT gateways silently drop idle TCP connections after 60–350 seconds. If no messages flow and no keepalives are sent, the stream dies silently — the Collector blocks forever waiting for the next message, and the MQ's subscriber goroutine leaks.

**Why it happens:**
gRPC over HTTP/2 has application-layer keepalives, but they are disabled by default in `google.golang.org/grpc`. Developers test in `minikube` or `kind` (no NAT) and never see the problem.

**How to avoid:**
Configure keepalive on both server and client:
```go
// Server (MQ)
grpc.KeepaliveParams(keepalive.ServerParameters{
    MaxConnectionIdle: 5 * time.Minute,
    Time:              30 * time.Second,
    Timeout:           10 * time.Second,
})
grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
    MinTime:             15 * time.Second,
    PermitWithoutStream: true,
})

// Client (Collector)
grpc.WithKeepaliveParams(keepalive.ClientParameters{
    Time:                20 * time.Second,
    Timeout:             10 * time.Second,
    PermitWithoutStream: true,
})
```
Add exponential-backoff reconnect in the Collector on `Consume` stream error.

**Warning signs:**
- Collector silently stops receiving messages after ~5 minutes of low-volume traffic.
- MQ subscriber list grows (never unsubscribed) while active Collectors decline.
- No error logged by either side — the stream appears healthy from the application layer.

**Phase to address:**
Phase 2 (Collector stream client and MQ transport config). Verify with a 10-minute soak test against a Helm-deployed stack, not just in-process tests.

---

### Pitfall 8: pgxpool Size Mismatch and Connection Exhaustion

**What goes wrong:**
`pgxpool.New()` defaults to `MaxConns = max(4, runtime.NumCPU())`. With 10 Collector replicas each creating a pool, and each pool acquiring connections concurrently for batch inserts, 10 x 4 = 40 connections minimum. PostgreSQL's default `max_connections = 100`, minus superuser reserved connections, leaves ~97 for applications. At 10 Collectors with concurrent insert goroutines inside each, you hit `max_connections` and all `pool.Acquire()` calls queue up. Without a context timeout on `Acquire`, these goroutines wait indefinitely, starving the HTTP layer of goroutines.

**Why it happens:**
Pool sizing is done once per service without accounting for horizontal scaling. Each Collector engineer sets a "reasonable" pool size without knowing the Deployment's replica count.

**How to avoid:**
Set `MaxConns` as a function of the expected Collector replica count: `MaxConns = floor(pg_max_connections * 0.8 / collector_replicas)`. For 10 Collectors: `floor(97 * 0.8 / 10) = 7`. Enforce a context timeout on every `pool.Acquire(ctx)` — use a 5-second timeout minimum. Expose a `pgxpool.Pool.Stat()` metric in the Collector's health check so connection exhaustion is observable before it causes user-visible failures. Pass `MaxConnLifetime` and `MaxConnIdleTime` to ensure stale connections cycle, preventing "idle but unusable" connection accumulation.

**Warning signs:**
- `pgxpool.Pool.Stat().AcquireDuration()` spikes under load.
- PostgreSQL shows `pg_stat_activity` with many `idle in transaction` states.
- Collector RPC latency grows linearly with replica count.

**Phase to address:**
Phase 2 (Collector DB layer). Pool configuration must be parameterized via Helm values so it can be tuned at deploy time.

---

### Pitfall 9: Batch Insert With pgx.Batch Instead of CopyFrom

**What goes wrong:**
Using `pgx.Batch` to send multiple `INSERT INTO telemetry VALUES (...)` statements in a batch is 10–50x slower than `pgxpool.CopyFrom` for bulk time-series inserts. With 10 Streamers producing at CSV-parse speed, the Collector must absorb hundreds of rows per second. `pgx.Batch` with individual INSERTs round-trips N statement parses + executions; `CopyFrom` uses the PostgreSQL COPY protocol and is bounded by network + disk I/O only.

**Why it happens:**
`pgx.Batch` looks like "batch insert" in the name, so developers use it for bulk inserts without checking `pgxpool.CopyFrom`.

**How to avoid:**
Use `pool.CopyFrom(ctx, pgx.Identifier{"telemetry"}, []string{"gpu_id", "timestamp", "metric_name", ...}, pgx.CopyFromRows(rows))` for all bulk inserts in the Collector. Accumulate a batch of N messages (e.g., 100 or up to 50ms of data) in a local slice, then issue a single `CopyFrom`. This amortizes the per-call overhead. Fall back to single-row INSERT only for the final partial batch on graceful shutdown.

**Warning signs:**
- PostgreSQL `pg_stat_statements` shows thousands of `INSERT INTO telemetry` statements per second (one per row).
- Collector CPU is high but PostgreSQL CPU is low (client-side overhead, not server-side throughput).
- Batch insert latency grows super-linearly with batch size.

**Phase to address:**
Phase 2 (Collector persistence layer). Write a micro-benchmark (`BenchmarkCopyFrom` vs `BenchmarkBatchInsert`) to validate the choice and set a throughput floor.

---

### Pitfall 10: TIMESTAMP vs TIMESTAMPTZ — Silent Timezone Strip

**What goes wrong:**
If the `timestamp` column is defined as `TIMESTAMP WITHOUT TIME ZONE` (plain `TIMESTAMP`), PostgreSQL silently strips the timezone offset from Go's `time.Time` values before storing. A `time.Time` in UTC stores identically to one in America/New_York — both appear as the same wall clock value. Queries using `WHERE timestamp > NOW()` compare against `NOW()` which returns `TIMESTAMPTZ`, requiring an implicit cast; the behavior is correct for UTC clients but wrong for any non-UTC client or container with a non-UTC `TZ` environment variable.

**Why it happens:**
`TIMESTAMP` is the default in many tutorials. pgx/v5 correctly marshals `time.Time` to either type, so there is no error — the problem is invisible until a non-UTC deployment.

**How to avoid:**
Always declare `timestamp TIMESTAMPTZ NOT NULL`. pgx/v5 maps Go `time.Time` to `TIMESTAMPTZ` without any conversion. Verify the migration SQL explicitly: `created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`. Set the container `TZ=UTC` explicitly in the Dockerfile and Helm values to eliminate ambiguity. Add a schema test: insert a row with a known UTC timestamp, query it back, assert `row.Timestamp.UTC() == expected.UTC()`.

**Warning signs:**
- Time-window query `?start_time=2025-07-18T10:00:00Z` returns results that are off by hours.
- `psql -c "\d telemetry"` shows `timestamp without time zone`.
- The Go Streamer sets `time.Now()` but the DB stores a different value on non-UTC hosts.

**Phase to address:**
Phase 2 (schema design). Catch this in the initial migration — it cannot be fixed without a destructive `ALTER COLUMN ... TYPE` plus a data migration.

---

### Pitfall 11: Composite Index Not Used — Query Plan Ignores (gpu_id, timestamp DESC)

**What goes wrong:**
The composite index `(gpu_id, timestamp DESC)` is created but the query planner chooses a sequential scan when:
- The table has fewer than ~10,000 rows (planner estimates seq scan is cheaper).
- The query uses `ORDER BY timestamp ASC` (index is `DESC`; planner must scan backward — fine for small N, but may choose a different plan under the wrong statistics).
- The `gpu_id` filter has very high cardinality and the planner underestimates selectivity.
- The query does not include `gpu_id` in the `WHERE` clause (e.g., `GET /api/v1/gpus` without a specific ID) — the partial index is not applicable.

**Why it happens:**
Developers create the index and assume it is used. The index is only verified manually via `EXPLAIN ANALYZE` at data volume, not at test-data volume.

**How to avoid:**
Run `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` against a table seeded with at least 100,000 rows. The plan must show `Index Scan using telemetry_gpu_id_timestamp_idx`. For the `GET /api/v1/gpus` endpoint (no gpu_id filter), add a separate index on `timestamp DESC` alone, or accept a sequential scan (it returns aggregate/distinct data, not row-level scans). Add a `pg_hint_plan` test or a SQL comment `-- INDEX: telemetry_gpu_id_timestamp_idx` to document expected index usage and catch regressions.

**Warning signs:**
- `EXPLAIN` shows `Seq Scan on telemetry` for `WHERE gpu_id = $1 AND timestamp > $2`.
- Query latency is acceptable on the 1MB CSV dataset but degrades linearly as data grows.
- `pg_stat_user_indexes` shows `idx_scan = 0` for the composite index.

**Phase to address:**
Phase 2 (schema + DB layer) and Phase 3 (API Gateway query tuning). The index must be verified at meaningful data volume before the API is declared complete.

---

### Pitfall 12: Vanity Coverage — 90% Line Coverage Without Concurrency Path Coverage

**What goes wrong:**
Reaching 90% line coverage by testing constructors, getters, error-return branches, and the HTTP inspect handler, while the core MQ concurrency paths (concurrent Enqueue + Dequeue + Subscribe + Unsubscribe) are covered by a single-goroutine test. `go test -cover` counts lines, not concurrent execution paths. A data race that only manifests under concurrent access is invisible to coverage but catastrophic in production.

**Why it happens:**
Coverage is measured by the line coverage tool, which is oblivious to whether a line was exercised concurrently. Developers optimize for the metric, not the safety property.

**How to avoid:**
- For the MQ: write `TestMQConcurrent` that runs 5 producers and 5 consumers simultaneously for 10,000 messages, verifying delivery count with `sync.WaitGroup` and `atomic.Int64`. Run it exclusively with `go test -race -count=5 -parallel=4`. This is the only test that actually validates the concurrency contract.
- Separate coverage gates: the Makefile should have `test-race` (pass/fail only, no coverage report) and `test-cover` (coverage ≥90%). Never merge coverage stats from `-race` runs — the race detector instrument adds synthetic branches that inflate line coverage numbers.
- Use `go test -coverprofile=coverage.out` + `go tool cover -func=coverage.out` and grep for uncovered functions by name — ensure `Enqueue`, `Dequeue`, `Subscribe`, `Unsubscribe`, and the ring-buffer wraparound path all appear as covered.

**Warning signs:**
- `go test -race` is never run; all tests use `go test -cover` only.
- The MQ test file has only one goroutine and no `sync.WaitGroup`.
- Coverage report shows 90%+ but `go test -race` finds a race on the first attempt.

**Phase to address:**
Phase 1 (MQ testing). The race-detector test must be a Makefile prerequisite of the coverage gate — not an optional extra.

---

### Pitfall 13: Flaky Race-Detector Tests from time.Sleep Synchronization

**What goes wrong:**
Tests that use `time.Sleep(100 * time.Millisecond)` to "give goroutines time to start" are intermittently flaky under the race detector. The race detector adds ~5–20x overhead to memory accesses, changing real-world timing. A sleep that reliably works in 10ms real-time may be insufficient when the race detector makes operations take 200ms, causing the test goroutine to read a value before the writer goroutine has reached it.

**Why it happens:**
Goroutine synchronization with `time.Sleep` is cargo-culted from examples. It "works" in non-race mode because the timing overhead is lower.

**How to avoid:**
Never use `time.Sleep` to synchronize goroutines in tests. Use:
- `sync.WaitGroup` when waiting for goroutines to complete.
- Channels as explicit signals: `started := make(chan struct{})` + `close(started)` pattern.
- `testify/require.Eventually` with a 5-second timeout for async conditions (e.g., "message appears in queue within 5s").
The only acceptable `time.Sleep` in test code is inside `Eventually`'s polling loop, with a documented reason.

**Warning signs:**
- Tests pass 95% of the time locally but fail randomly in CI (`go test -race -count=10`).
- Test failure message is a timeout or a wrong-value assertion, not a race report.
- `grep -r "time.Sleep" ./*_test.go` returns hits in the MQ test files.

**Phase to address:**
Phase 1 (MQ unit tests). Enforce as a linter rule (`forbidigo` or custom `grep` in the Makefile's `test` target).

---

### Pitfall 14: testcontainers-go PostgreSQL Container Not Waited For Health

**What goes wrong:**
`testcontainers.Run(ctx, "postgres:16")` returns a container object immediately, but PostgreSQL takes 1–5 seconds to finish initialization inside the container. Running migrations against a container that has started the process but not yet accepted connections produces `connection refused` or `FATAL: the database system is starting up` errors. Tests re-run with a sleep or retry loop that is too short fail intermittently.

**Why it happens:**
Container "started" (process running) ≠ container "ready" (accepting connections). The `testcontainers-go` `WaitingFor` option must be set explicitly — it is not the default.

**How to avoid:**
```go
container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
    ContainerRequest: testcontainers.ContainerRequest{
        Image:        "postgres:16-alpine",
        Env:          map[string]string{"POSTGRES_PASSWORD": "test"},
        ExposedPorts: []string{"5432/tcp"},
        WaitingFor: wait.ForSQL("5432/tcp", "pgx", func(host string, port nat.Port) string {
            return fmt.Sprintf("postgres://postgres:test@%s:%s/postgres?sslmode=disable", host, port.Port())
        }).WithStartupTimeout(60 * time.Second),
    },
    Started: true,
})
```
Use `TestMain` to start a single shared container for the entire test package and `defer container.Terminate(ctx)` — do not start a container per test function. This reduces CI time from minutes to seconds.

**Warning signs:**
- Integration tests fail with `connection refused` intermittently but pass when re-run.
- CI logs show the container exiting before the test has finished.
- Each test function calls `testcontainers.Run` independently (one container per test).

**Phase to address:**
Phase 2 (Collector integration tests) and Phase 3 (API Gateway integration tests). Establish the `TestMain` + shared container pattern in Phase 2; reuse in Phase 3.

---

### Pitfall 15: Multi-Stage Docker Build Defeating Layer Cache

**What goes wrong:**
A Dockerfile that copies the entire source tree in one `COPY . .` before `go mod download` forces re-downloading all Go dependencies every time any source file changes. With 4 microservices and 10+ dependencies each, this adds 30–120 seconds to every CI build.

**Why it happens:**
The minimal Dockerfile is `COPY . . && go build`. The layer-cache optimization is a separate concern that developers address (or don't) after the build works.

**How to avoid:**
The canonical Go multi-stage pattern:
```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download          # cached as long as go.mod/go.sum don't change
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/service ./cmd/mq

FROM gcr.io/distroless/static-debian12
COPY --from=builder /bin/service /service
ENTRYPOINT ["/service"]
```
The distroless base image produces images ~8MB vs ~300MB for the `golang` base. Add `.dockerignore` to exclude `*.md`, `.git`, `deployments/`, and any testdata or CSV files.

**Warning signs:**
- `docker build` always shows `[builder 3/5] COPY . .` as a cache miss.
- Final image size is >100MB (the Go binary is typically 10–20MB; the rest is bloat).
- Build times in CI are >2 minutes per service.

**Phase to address:**
Phase 4 (DevOps). Establish the correct Dockerfile pattern in the first Dockerfile written and apply it to all four services.

---

### Pitfall 16: GOWORK File Breaking Container Builds

**What goes wrong:**
A `go.work` file in the repository root (used for local multi-module development) is copied into the Docker build context. The `go.work` file references local relative paths (e.g., `use ./pkg`) that do not exist inside the container's `/app` directory. The build fails with `go: cannot find module providing package: ...` or `go: open /path/to/local/module: no such file or directory`.

**Why it happens:**
`go.work` is created for local convenience and committed accidentally, or excluded from `.gitignore` but not from `.dockerignore`.

**How to avoid:**
This project uses a single Go module (`github.com/ajitg/vantage`) per the PROJECT.md decision, so a `go.work` file should not exist. If added for any reason (e.g., local tool development), add `go.work` and `go.work.sum` to `.dockerignore`. Add `GOWORK=off` as a build ARG in all Dockerfiles as a belt-and-suspenders defense: `RUN GOWORK=off go build ...`.

**Warning signs:**
- `docker build` fails with `cannot find module` but `go build` works locally.
- `ls -la` in the build context shows a `go.work` file.

**Phase to address:**
Phase 4 (DevOps). Add `.dockerignore` with `go.work` exclusion as the first file created in the DevOps phase.

---

### Pitfall 17: Conflating Readiness and Liveness Probes

**What goes wrong:**
Using the same endpoint (e.g., `/healthz`) for both the Kubernetes `readinessProbe` and `livenessProbe`. The MQ's liveness probe should check "is this process alive and not deadlocked?" The readiness probe should check "is this service ready to accept gRPC connections?" — which may require verifying that the gRPC listener is bound and the ring buffer is initialized. When liveness and readiness are identical, a pod that is starting up (not yet bound to its port) is killed by the liveness probe before it is ready — causing a restart loop.

**Why it happens:**
Developers copy the same probe definition for both fields in the Helm chart because it is faster, and the distinction seems academic.

**How to avoid:**
- `livenessProbe`: an `exec` or `httpGet` that requires the process to respond within 1 second — confirms goroutines are not deadlocked. For the MQ, a lightweight `GET /healthz` returning 200 is sufficient.
- `readinessProbe`: a probe that confirms the gRPC port is bound AND the HTTP control plane is accepting connections. Use `grpc_health_v1` (standard gRPC health protocol) for the gRPC service readiness — `grpc.health.v1.Health/Check` returning `SERVING`.
- Initial `initialDelaySeconds` must account for Go binary startup time (~100ms) plus migration time (Collector only: ~1s for first-run migrations).
- Set `failureThreshold: 3` for liveness (don't kill on transient hiccup) and `failureThreshold: 1` or `2` for readiness (remove from LB quickly).

**Warning signs:**
- Pods restart during deployment rollout without an obvious error (liveness probe killing a starting-up pod).
- Traffic reaches pods before they have finished initialization (readiness not strict enough).
- `kubectl describe pod` shows `Liveness probe failed` during a normal startup.

**Phase to address:**
Phase 4 (Helm). Define probe types explicitly in the first Helm values file; copy-paste between services with appropriate adjustments per service.

---

### Pitfall 18: MQ as Stateful Single-Replica Under Rolling Update Strategy

**What goes wrong:**
Kubernetes Deployments default to `strategy: RollingUpdate` with `maxUnavailable: 0, maxSurge: 1`. For a stateless service, this is correct — run the new pod before killing the old one. For the MQ, which holds all in-flight messages in memory, a rolling update creates two MQ pods simultaneously. Collectors connect to one; Streamers produce to another. Messages are split across two queues with no cross-replication. When the old MQ is killed, all Streamers and Collectors reconnect to the new pod — the old pod's in-flight messages are lost.

**Why it happens:**
The `RollingUpdate` strategy is the Kubernetes default. Developers apply a generic Helm chart template without accounting for the MQ's statefulness.

**How to avoid:**
Set `strategy: type: Recreate` in the MQ's Deployment spec. This kills the old pod before starting the new one — there is a brief downtime (acceptable per spec: in-memory, no clustering). Document this behavior in the Helm chart's `values.yaml` comments: `# MQ is stateful (in-memory); Recreate avoids split-brain during updates. Expect <10s downtime.` Also set `replicas: 1` as a hard constraint with a comment — adding replicas without implementing clustering will cause message duplication or loss.

**Warning signs:**
- `kubectl get pods` shows two MQ pods during `helm upgrade`.
- Collector logs show split message receipts during upgrades.
- `kubectl describe deployment mq` shows `RollingUpdateStrategy: 25% max unavailable, 25% max surge`.

**Phase to address:**
Phase 4 (Helm). Must be set in the initial MQ Deployment template — never left as the default.

---

### Pitfall 19: PostgreSQL Credentials in Helm Values as Plaintext

**What goes wrong:**
Hardcoding `POSTGRES_PASSWORD: mysecretpassword` in `deployments/values.yaml` — which is committed to git — exposes credentials. Even in a "reference implementation" context, this establishes a bad pattern that reviewers will flag, and the credentials may end up in container logs or `kubectl describe pod` output.

**Why it happens:**
It is faster to hardcode credentials for local development and "come back to secrets later." Later never comes.

**How to avoid:**
Create a Kubernetes Secret via Helm's `_helpers.tpl` or a separate `helm/templates/secret.yaml` that reads from `values.yaml`'s `postgresql.auth.password` field. In the Deployment, reference the Secret via `envFrom: secretRef`. Provide a `values.example.yaml` with placeholder values and add actual `values.yaml` to `.gitignore` if it contains real credentials. For this project, even a dummy password should be in a Secret — it demonstrates the correct pattern.

**Warning signs:**
- `grep -r "password" deployments/` returns a match in a non-secret YAML file.
- `kubectl exec ... -- env | grep POSTGRES` shows the password in the pod's environment list via `kubectl describe`.

**Phase to address:**
Phase 4 (Helm). Establish the Secret pattern in the first Helm chart template.

---

### Pitfall 20: Missing Resource Limits on the CSV Streamer Tight Loop

**What goes wrong:**
The Streamer reads a 1.1MB CSV in a tight loop, restamping timestamps and calling `Produce` via gRPC as fast as the connection allows. Without CPU limits in the Kubernetes Deployment, a single Streamer can saturate a CPU core. With 10 Streamer replicas, they saturate the node. The MQ's ring buffer fills faster than consumers can drain it, triggering the drop policy. The API Gateway and Collector share the node's CPU budget and starve.

**Why it happens:**
Resource limits feel like premature optimization when "the system should be fast." But the Streamer's tight loop is not bounded by I/O — it is compute-bounded.

**How to avoid:**
Add a configurable rate limiter in the Streamer (e.g., `golang.org/x/time/rate.NewLimiter`) with a default of 100 msg/s per instance, controlled by a Helm value (`streamer.rateLimit: 100`). Set Kubernetes resource limits: `cpu: "500m", memory: "64Mi"` per Streamer pod. The rate limiter is the primary control; resource limits are the safety net. Expose the rate as a metric for observability.

**Warning signs:**
- MQ's `dropped_count` grows immediately after Streamers start.
- `kubectl top pods` shows a Streamer pod at 100% CPU.
- The gRPC Produce call returns `ResourceExhausted` status codes.

**Phase to address:**
Phase 2 (Streamer) and Phase 4 (Helm values). Rate limiter in Phase 2; resource limits in Phase 4.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| `sync.Mutex` everywhere (no `RWMutex`) | Simpler reasoning, no lock hierarchy bugs | Lower read throughput under concurrent readers | Acceptable in Phase 1 MVP; optimize with `RWMutex` only if profiling shows contention |
| `pgx.Batch` instead of `CopyFrom` | Simpler code, familiar INSERT syntax | 10–50x lower throughput at scale | Never — `CopyFrom` is equally simple once understood |
| Skip gRPC health check service | Faster initial delivery | Manual probe config, Helm chart can't use `grpc` probe type cleanly | Never in a Helm deployment — add `grpc.health.v1` from Phase 1 |
| Hardcoded pool size (`MaxConns: 10`) | No math required | Connection exhaustion at scale, or wasted connections | Acceptable in Phase 1 single-Collector dev; must be parameterized before Helm chart |
| `time.Sleep` in tests | Quicker test writing | Flaky CI, masked race conditions | Never in goroutine synchronization |
| Single container for all integration tests (`docker-compose up`) | Simple local dev | CI containers not reproducible; port conflicts; no per-test isolation | Acceptable for local dev if testcontainers is used in CI |
| `swag init` run manually | Simpler Makefile | OpenAPI spec drifts from code | Never — must be a Makefile `build` prerequisite |

---

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|-----------------|
| gRPC + pgxpool in Collector | Open a new DB connection per gRPC message received | Acquire from the pool once per batch cycle; keep the connection for the batch duration only |
| `swag` + custom types | Using `interface{}` or `map[string]any` in handler signatures — `swag` cannot generate schema | Define concrete request/response structs with `swag` annotations; use `// @Success 200 {object} models.TelemetryResponse` |
| protoc-generated code + single Go module | Committing generated `.pb.go` files and having `go generate` also run in CI causes merge conflicts | Commit generated proto files; `make proto` is idempotent; CI runs `make proto && git diff --exit-code` to verify freshness |
| pgxpool + `TIMESTAMPTZ` | Using `time.Time` with `pgtype.Timestamp` (without TZ) in scan destination | Always use `pgtype.Timestamptz` or plain `time.Time` — pgx/v5 maps `time.Time` to `TIMESTAMPTZ` correctly |
| Helm + gRPC service | Defining the Service as `type: ClusterIP` with `port: 50051` but forgetting `appProtocol: h2c` | Set `appProtocol: h2c` on the Service port so Istio/envoy (if added later) handles HTTP/2 correctly |
| testcontainers + pgx migrations | Running `migrate.Up()` before the container is `SERVING` | Use `WaitingFor: wait.ForSQL(...)` and run migrations only after pool connection succeeds |

---

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Row-by-row INSERT in Collector | PostgreSQL `pg_stat_statements` shows thousands of identical INSERTs/sec; DB CPU high | Use `pgxpool.CopyFrom` with batches of 100–1000 rows | Immediately above 50 rows/sec throughput requirement |
| Unbounded Streamer loop rate | MQ drop count > 0 within seconds of start; node CPU saturated | Rate-limit in Streamer; size MQ ring buffer for expected throughput | 10 Streamers on a 4-core node |
| gRPC message size without compression | Protobuf payloads with many string fields (metric names, labels_raw) inflate over the wire | Enable gRPC compression: `grpc.UseCompressor(gzip.Name)` on client, `grpc.RPCCompressor(gzip.NewCompressor())` on server | Above 1000 Produce RPCs/sec on constrained network |
| No DB index on `gpu_id` alone | `GET /api/v1/gpus` (distinct gpu_ids) requires sequential scan even with composite index | Add `CREATE INDEX ON telemetry (gpu_id)` for the distinct-query; or use a separate `gpus` metadata table | Above 10M rows |
| JSON `labels_raw` stored as TEXT without index | Time-window query + label filter requires sequential scan over TEXT column | Store in `JSONB` with GIN index if label filtering is needed; currently out of scope but schema should use `JSONB` | First request with label filter at scale |

---

## "Looks Done But Isn't" Checklist

- [ ] **MQ Consume endpoint:** Verify goroutine count does not grow after 100 consumer connect/disconnect cycles — run `runtime.NumGoroutine()` assertions in `TestMQGoroutineLeak`.
- [ ] **gRPC keepalive:** Verify stream survives 10 minutes of idle (no messages) without reconnect — `TestConsumeStreamIdleSurvival` against a real TCP stack (not `bufconn`).
- [ ] **PostgreSQL TIMESTAMPTZ:** Run `SELECT pg_typeof(timestamp) FROM telemetry LIMIT 1` in CI; assert result contains `with time zone`.
- [ ] **Composite index usage:** Run `EXPLAIN SELECT * FROM telemetry WHERE gpu_id = 'GPU-0' AND timestamp > NOW() - INTERVAL '1 hour'` against 100,000 rows; assert `Index Scan` appears in the plan.
- [ ] **CopyFrom correctness:** Insert 1,000 rows via `CopyFrom`, then `SELECT COUNT(*) FROM telemetry`; assert count matches exactly (no silent truncation).
- [ ] **MQ drop policy:** Fill the ring buffer to capacity, then produce one more message; assert `inspect` endpoint reports `dropped_count: 1` and no panic.
- [ ] **Helm Recreate strategy:** `kubectl describe deployment mq | grep Strategy`; assert `Recreate` (not `RollingUpdate`).
- [ ] **Docker image size:** `docker image ls | grep vantage`; assert each image < 50MB.
- [ ] **swag generation is current:** CI runs `make swagger && git diff --exit-code docs/` to ensure generated spec matches annotations.
- [ ] **Race detector gate:** `make test-race` is a prerequisite of the `coverage` Makefile target, not an optional separate target.

---

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Data race discovered post-merge | HIGH | Run `go test -race -count=20 ./cmd/mq/...`; identify race with `-race` output; add mutex protection; re-run 100x to confirm clean |
| Goroutine leak in production | MEDIUM | Deploy `net/http/pprof` handler temporarily; capture `goroutine` profile; identify leaking goroutine stack; patch Unsubscribe path |
| `TIMESTAMP` instead of `TIMESTAMPTZ` in schema | HIGH | `ALTER TABLE telemetry ALTER COLUMN timestamp TYPE TIMESTAMPTZ USING timestamp AT TIME ZONE 'UTC'`; validate all stored data is UTC; requires maintenance window |
| Pool exhaustion in production | LOW | Increase `MaxConns` via Helm values and rolling restart; short-term: reduce Collector replicas |
| Helm `RollingUpdate` on MQ causing split-brain | MEDIUM | `kubectl rollout undo deployment/mq` to revert; patch Helm chart to `Recreate`; re-deploy with `helm upgrade` |
| `go test -race` flaky in CI | MEDIUM | Replace all `time.Sleep` with channel synchronization; increase `go test -count=20` to confirm stability |

---

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Data race on ring buffer | Phase 1 — MQ Implementation | `go test -race -count=50 ./cmd/mq/...` passes cleanly |
| Goroutine leak on disconnect | Phase 1 — MQ + Phase 2 — Collector integration | `TestMQGoroutineLeak` asserts stable goroutine count |
| Deadlock under notification | Phase 1 — MQ Implementation | `go test -race -timeout 30s` completes without hang |
| Lost/duplicated messages | Phase 1 — MQ semantics | Delivery-count correctness test: N produced = N consumed across K consumers |
| Unbounded memory growth | Phase 1 — MQ Design | Soak test shows stable `inspect.size` after ring buffer fills |
| gRPC context not checked | Phase 1 — MQ + Phase 2 — Collector | Integration test: disconnect mid-stream; verify clean exit within 5s |
| gRPC keepalive silent death | Phase 2 — Collector stream client | 10-minute idle stream test against deployed stack |
| pgxpool exhaustion | Phase 2 — Collector DB layer | Load test with 10 Collectors; no `pool.Acquire` timeouts |
| Batch INSERT vs CopyFrom | Phase 2 — Collector persistence | `BenchmarkCopyFrom` > 1000 rows/sec; `BenchmarkBatchInsert` baseline comparison |
| TIMESTAMP vs TIMESTAMPTZ | Phase 2 — Schema design | `pg_typeof` assertion in migration test |
| Composite index not used | Phase 2 — Schema + Phase 3 — API Gateway | `EXPLAIN ANALYZE` at 100k rows shows `Index Scan` |
| Vanity coverage | Phase 1–3 — All test phases | Concurrent MQ test in coverage run; `grep -v 'func.*Get\|Set\|New'` uncovered functions |
| Flaky race-detector tests | Phase 1 — MQ unit tests | `go test -race -count=20` passes 20/20 in CI |
| testcontainers not waited | Phase 2 — Integration test setup | `TestMain` with `WaitingFor: wait.ForSQL`; no `connection refused` in 50 CI runs |
| Docker layer cache | Phase 4 — DevOps | `docker build` after source change: only `COPY . .` layer is rebuilt, not `go mod download` |
| GOWORK in container | Phase 4 — DevOps | `docker build` with `GOWORK=off`; `.dockerignore` includes `go.work` |
| Readiness vs liveness | Phase 4 — Helm | `kubectl describe pod mq` shows distinct probe configs; no restart-loop on startup |
| MQ RollingUpdate split-brain | Phase 4 — Helm MQ chart | `kubectl describe deployment mq` shows `Recreate`; upgrade test shows no split messages |
| Plaintext credentials | Phase 4 — Helm | `grep -r "password" deployments/ values.yaml` returns no plaintext match |
| Streamer CPU saturation | Phase 2 — Streamer + Phase 4 — Helm | `kubectl top pods` shows Streamer < 500m CPU; `inspect.dropped_count` = 0 at default rate |

---

## Sources

- Go memory model: https://go.dev/ref/mem (synchronization guarantees for channels, sync primitives)
- `sync.RWMutex` deadlock patterns: Go issue tracker and `golangci-lint` `rowserrcheck` documentation
- `pgxpool` configuration: `jackc/pgx/v5` documentation, `pgxpool.Config` struct godoc
- PostgreSQL COPY protocol vs batch INSERT: PostgreSQL documentation §14.4 "Populating a Database"
- gRPC keepalive: `google.golang.org/grpc/keepalive` package, gRPC keepalive user guide
- `testcontainers-go` `WaitingFor`: testcontainers-go documentation, `wait.ForSQL` strategy
- Kubernetes probe differentiation: Kubernetes documentation §Configure Liveness, Readiness, Startup Probes
- Go multi-stage Docker best practices: Docker documentation, `distroless` project README
- gRPC server-streaming context: grpc.io server-side streaming documentation

---
*Pitfalls research for: Elastic GPU Telemetry Pipeline (Go MQ + gRPC + pgx/pgxpool + Kubernetes/Helm)*
*Researched: 2026-06-27*
