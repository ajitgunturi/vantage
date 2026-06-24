# Testing Patterns

**Analysis Date:** 2026-06-24

## Test Framework

**Runner:**
- Go's built-in `testing` package (standard library)
- Version: Go 1.26 (per `go.mod` files)
- Config: No additional test configuration file (uses native `go test` command)

**Assertion Library:**
- `github.com/stretchr/testify` — Planned in PROJECT.md § 6 and CLAUDE.md line 25, not yet added
- Future: Use testify's `assert` and `require` packages for readable assertions
- Current: Native Go testing (no assertions yet since no business logic code exists)

**Run Commands:**
```bash
make test                    # Run unit tests for all modules (race detector + coverage)
make cover                   # Print per-module coverage totals (post-test)
make cover-check             # Enforce >= 90% line coverage gate
make cover-logic             # Enforce 100% branch coverage on internal/ packages (gobco)
make cover-html              # Generate per-module HTML coverage reports
make hooks                   # Install pre-commit hook (run once per clone)
```

## Test File Organization

**Location:**
- Co-located with source code: `*_test.go` files live in the same package directory as the code they test
- Example: `internal/stream/processor.go` tested by `internal/stream/processor_test.go`

**Naming:**
- Test files: `<package_or_component>_test.go` (e.g., `broker_test.go`, `upsert_test.go`)
- Test functions: `Test<Function|Behavior>` (e.g., `TestProduceMessage`, `TestConsumerRecovers`)
- Subtests: Use `t.Run("description", func(t *testing.T) { ... })` for grouped assertions

**Structure:**
```
module-root/
├── internal/
│   ├── consume/
│   │   ├── consumer.go        # Source code
│   │   └── consumer_test.go   # Tests
│   ├── store/
│   │   ├── upsert.go
│   │   └── upsert_test.go
│   └── [other packages]
├── cmd/
│   └── binary/main.go         # No tests here (thin wiring)
└── gen/
    └── [generated code]       # Excluded from coverage
```

## Test Structure

**Suite Organization (once testable code exists):**
```go
func TestConsumerSubscribe(t *testing.T) {
    // Setup
    consumer := NewConsumer(...)
    defer consumer.Close()
    
    // Test
    t.Run("subscribes to topic", func(t *testing.T) {
        // Arrange
        topic := "test-topic"
        
        // Act
        err := consumer.Subscribe(topic)
        
        // Assert (once testify is added: require.NoError(t, err))
        if err != nil {
            t.Fatalf("unexpected error: %v", err)
        }
    })
    
    t.Run("rejects empty topic name", func(t *testing.T) {
        err := consumer.Subscribe("")
        if err == nil {
            t.Fatal("expected error for empty topic")
        }
    })
}
```

**Patterns:**
- **Setup:** `NewX()` to create test fixtures; `defer cleanup()` to release resources
- **Teardown:** Explicit cleanup in defer blocks (e.g., `defer broker.Close()`)
- **Assertion:** Native Go `if err != nil { t.Fatal(...) }` or t.Errorf(...)`; testify (once added) for readability

## TDD (Test-First Approach)

**Coverage Gates Enforced:**
1. **Line Coverage:** 90% threshold (via `make cover-check`)
   - Applies to all `internal/` packages
   - Excludes: `gen/` (generated code), `cmd/` (thin wiring)
   - Grace period: Modules with no testable code yet skip this gate

2. **Branch/Logic Coverage:** 100% threshold (via `make cover-logic` + gobco)
   - Applies to all `internal/` packages once code exists
   - Condition coverage: Every `if`, `switch`, loop branch must be exercised
   - Grace period: Modules with no logic packages skip until first package lands

**Workflow (as documented in CLAUDE.md):**
- **TDD for business logic:** Write test first, implement to pass test
- **Business logic targets:** 
  - MQ segment log durability and recovery (`mq/internal/`)
  - Collector upserts with idempotency (`collector/internal/store/`)
  - API handlers (`apigateway/internal/`)
  - Streamer CSV parsing and timestamp re-stamping (`streamer/internal/`)
- **Non-targets:** `cmd/` (main wiring) and `gen/` (generated proto stubs) are excluded

## Mocking

**Framework:** 
- Testify's `mock` package (planned, not yet added)
- Currently: Manual interface-based mocking (Go idiom)

**Patterns (future, once testify is added):**
```go
// Manual interface mocking (current approach)
type mockBroker struct {
    produceFunc func(...) error
}

func (m *mockBroker) Produce(...) error {
    return m.produceFunc(...)
}

// Testify mock (future)
import "github.com/stretchr/testify/mock"

m := new(mock.Mock)
m.On("Produce", mock.Anything).Return(nil)
```

**What to Mock:**
- External dependencies: PostgreSQL (use test database or `pgx` MockConn), gRPC services, file I/O
- Example: Collector tests mock database via a test Postgres instance or stub

**What NOT to Mock:**
- Core business logic (segment log, upsert idempotency, offset management)
- Internal package methods — test them directly to verify correctness
- Standard library functions unless necessary

## Fixtures and Factories

**Test Data (planned):**
Once business logic code exists, establish factories:
```go
// Example factory pattern (not yet in codebase)
func newTestMessage(uuid, metricName string, value float64) *Message {
    return &Message{
        UUID:       uuid,
        MetricName: metricName,
        Value:      value,
        Timestamp:  time.Now(),
    }
}
```

**Location:**
- Create a `testdata/` directory at module root if large fixtures are needed (e.g., CSV sample files)
- Helper functions in `*_test.go` files for small factories
- Example: `collector/testdata/sample_metrics.csv` for parser tests

## Coverage

**Requirements:**
- **Line:** >= 90% on all `internal/` packages (gate: `make cover-check`)
- **Branch:** 100% on all `internal/` packages (gate: `make cover-logic`)
- **Grace period:** Modules with no logic code yet are skipped; once first logic package is committed, gates become mandatory

**View Coverage:**
```bash
# Per-module coverage percentages
make cover

# Enforce gate (fails if any module below 90%)
make cover-check

# Branch coverage via gobco
make cover-logic

# Generate HTML reports (one per module)
make cover-html
# Output: mq/coverage.html, streamer/coverage.html, collector/coverage.html, apigateway/coverage.html
```

**CI Enforcement (`.github/workflows/ci.yml`):**
- Line coverage gate (90%) runs for each module in the `build-test` job
- Branch coverage gate (100%) runs in the `logic-coverage` job
- Both must pass for `ci-success` check (required to merge to main)

## Test Types

**Unit Tests:**
- Scope: Individual functions and methods in `internal/` packages
- Approach: Fast, deterministic, no external dependencies (or mocked)
- Examples:
  - `TestSegmentLogAppend` — Segment log append and flush to disk
  - `TestUpsertIdempotency` — Collector upsert (uuid, metric_name, ts) correctness
  - `TestParseMetricCSV` — Streamer CSV parser
  - `TestProducerToConsumer` — End-to-end MQ produce/consume
- Convention: One test file per logical component (`broker_test.go`, `consumer_test.go`)

**Integration Tests:**
- Scope: Multiple components interacting (e.g., broker + producer + consumer, or collector + PostgreSQL)
- Approach: Test real I/O (test database, in-memory broker); no mocks
- Not yet implemented (pending business logic)
- When added: Place in separate `*_integration_test.go` files or use build tags (`// +build integration`)

**E2E Tests:**
- Framework: Not used yet (deferred to system testing phase)
- Possible future approach: `kind` cluster with Helm charts, Docker images, and full services
- Would test: Streamer → MQ → Collector → PostgreSQL → API Gateway REST endpoints

## Race Detection

**Tool:** Go's built-in race detector (`-race` flag)

**Usage:**
```bash
# Included in make test target
go test -race ./...

# Or manually
cd mq && GOWORK=off go test -race ./...
```

**Integration:** Every unit test in CI runs with `-race` flag (see `.github/workflows/ci.yml` line 49)

## Async Testing

**Pattern (once business logic exists):**
```go
func TestConsumerStreamAsync(t *testing.T) {
    consumer := NewConsumer(...)
    defer consumer.Close()
    
    // Use channels for async assertions
    results := make(chan Message, 100)
    done := make(chan struct{})
    
    go func() {
        for msg := range consumer.Consume(ctx) {
            results <- msg
        }
        close(done)
    }()
    
    // Producer pushes messages
    producer.Produce(msg1)
    producer.Produce(msg2)
    
    // Assert received
    received := <-results
    // (validate with testify.assert once added)
    
    close(results)
    <-done
}
```

**Timeout handling:** Always use `context.WithTimeout()` to prevent hanging tests

## Error Testing

**Pattern (once business logic exists):**
```go
func TestUpsertFailure(t *testing.T) {
    t.Run("returns error on duplicate key", func(t *testing.T) {
        // Setup with database constraint violation
        store := NewStore(testDB)
        
        // Act — try to insert duplicate (uuid, metric_name, ts)
        err := store.Upsert(uuid, "DCGM_FI_DEV_GPU_UTIL", ts, 85.5)
        if err != nil {
            t.Fatalf("first upsert failed: %v", err)
        }
        
        // Should update, not error (idempotency)
        err = store.Upsert(uuid, "DCGM_FI_DEV_GPU_UTIL", ts, 90.0)
        if err != nil {
            t.Errorf("second upsert (idempotent) failed: %v", err)
        }
    })
}
```

## Test Coverage Reporting in CI

**Artifact Upload (`.github/workflows/ci.yml` lines 56–61):**
- Each module's `coverage.out` is uploaded as a CI artifact after tests pass
- Accessible via GitHub Actions UI for local inspection
- Useful for tracking coverage trends over time

**Gate Failure:**
- If any module's coverage drops below 90% AND has testable code, `build-test` job fails
- If any `internal/` package's branch coverage is not 100%, `logic-coverage` job fails
- Both must succeed for `ci-success` gate (required to merge)

## Coverage Gate Logic

**Fail-Open-Until-Code Pattern (key design):**
Both gates use "fail-open" semantics:

1. **Coverage gate (line coverage)** — `scripts/coverage-gate.sh`:
   - If `coverage.out` doesn't exist or is empty → SKIP (no code yet)
   - If `coverage.out` has statements → CHECK percentage >= 90%
   - Applied to each module independently; early modules can skip while others test

2. **Logic coverage gate (branch coverage)** — `scripts/logic-coverage.sh`:
   - If no `internal/*.go` files exist in any module → SKIP entire check
   - Once first `internal/` package appears → ENFORCE 100% for that package
   - Applied module by module; scale gradually as code lands

**Consequence:** Monorepo can bootstrap without tests; once TDD adds the first logic package, gates become strict. No "debt accumulation" window where uncovered code ships.

## Test Execution in Makefile

**Key targets:**

```bash
# src: mq/, streamer/, collector/, apigateway/
test:
    for each module:
        if module has testable packages (not gen/, cmd/):
            GOWORK=off go test -race -coverprofile=coverage.out <pkgs>
        else:
            echo "no testable packages yet — skip"

cover:
    print per-module coverage totals from coverage.out

cover-check:
    COVERAGE_THRESHOLD=90 bash scripts/coverage-gate.sh mq/coverage.out streamer/coverage.out ...

cover-logic:
    bash scripts/logic-coverage.sh  # runs gobco on all internal/* directories
```

**Module isolation:** `GOWORK=off` ensures each module's tests use that module's `go.mod` (Docker-parity)

## Conventions for Test Organization

**Naming test functions clearly:**
- `TestX` — Basic functionality test
- `TestX_<behavior>` — Test specific behavior (e.g., `TestProducer_HandlesBackpressure`)
- `TestX_<error>` — Error/edge case test (e.g., `TestConsumer_ErrorOnEmptyTopic`)

**Group related tests in subtests:**
```go
func TestBroker(t *testing.T) {
    broker := setupBroker(t)
    defer broker.Close()
    
    t.Run("CreateTopic creates a new topic", func(t *testing.T) { ... })
    t.Run("CreateTopic rejects duplicate names", func(t *testing.T) { ... })
    t.Run("Produce appends to log", func(t *testing.T) { ... })
}
```

**Use table-driven tests for variants:**
```go
func TestParseMetric(t *testing.T) {
    tests := []struct {
        name  string
        line  string
        want  *Metric
        err   bool
    }{
        {"valid row", "...", &Metric{...}, false},
        {"bad UUID", "...", nil, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test with tt.line, expect tt.want or tt.err
        })
    }
}
```

---

*Testing analysis: 2026-06-24*
