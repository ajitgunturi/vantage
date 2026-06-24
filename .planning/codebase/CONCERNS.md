# Codebase Concerns

**Analysis Date:** 2026-06-24

## Dominant Risk: Core Logic Not Yet Implemented

**What's critical:**
This is a **monorepo scaffold stage** (MONOREPO SCAFFOLD per STATE.md). The tree, modules, proto stubs, and tooling infrastructure are in place, but **zero service logic is implemented**:
- MQ broker durable segment log (`mq/broker/`) — empty directory
- MQ client library (`mq/client/`) — empty directory
- All service `cmd/*/main.go` files — not yet created (STATE.md NEXT checkpoint)
- Collector → PostgreSQL upsert logic — not implemented
- Streamer CSV loop + production logic — not implemented
- API Gateway handlers for `/gpus`, `/gpus/{id}/telemetry` — not implemented
- Helm umbrella chart + sub-chart templates — directory structure only, no YAML content
- Dockerfiles — not created
- README — not written

**Impact:** Project risk is **LATENT until TDD begins**. When the first real code lands, all of the below concerns become ACTIVE.

**Files affected:** Every module has empty `internal/` directories or placeholder structures.

---

## Open Design Decisions (Not Closed)

These decisions MUST be resolved before schema/implementation choices become load-bearing:

### GPU Identity: UUID vs gpu_id vs hostname:gpu_id
- **Status:** ADR-0005 is *Proposed*, not Accepted
- **Issue:** The `{id}` path param in `GET /api/v1/gpus/{id}/telemetry` must resolve to something, and the Postgres PK + MQ partition key depend on this choice.
- **Files affected:** `apigateway/` (URL routing), `collector/` (schema + upsert), `mq/` (partition routing for GPU messages)
- **Leaning:** UUID (globally unique, stable) per PROJECT.md §5 #1 and ADR-0005, but **not yet confirmed**.
- **Risk if wrong:** If we implement UUID-based routing and later pivot to `hostname:gpu_id`, we rewrite schema + partition routing + all lookups. **Freeze this before `collector/` persistence code lands.**

### MQ Persistence Depth and Fidelity
- **Status:** ADR-0001 *Accepted* (append-only segment log direction); persistence depth/effort **TBD at build time**
- **Issue:** How much durability? Just the committed offsets, or full message bodies? Segment roll size? fsync batching policy?
- **Files affected:** `mq/broker/` (log/index/recovery implementation)
- **Risk:** Underestimating durability cost could force a pivot mid-implementation (e.g., switching to embedded KV if hand-rolled log is too fiddly).
- **Mitigation:** Start with a minimal log (offset index + segment file rollover at 100MB), test recovery, iterate.

### Streamer Cadence
- **Status:** OPEN — implementation detail deferred to streamer build (PROJECT.md §5 #4)
- **Issue:** Per-row interval? Batch every N rows? Loop the CSV with a fixed delay?
- **Files affected:** `streamer/` (main loop + timing logic)
- **Risk:** If loop is too fast, it can overwhelm the MQ; if too slow, won't demonstrate the pipeline under load.
- **Guidance:** Design for **backpressure**. Let the MQ block the streamer when queue depth grows (part of "producer > consumer" perf scenario). Test all three ratios before performance characterization is called done.

### OpenAPI Generator Choice
- **Status:** OPEN — `swaggo/swag` vs `oapi-codegen` (PROJECT.md §5 #6)
- **Issue:** Which tool? Swagger comments in handler code vs separate OpenAPI YAML?
- **Files affected:** `apigateway/cmd/` (Makefile target `openapi` currently stubbed; handler code annotation style)
- **Risk:** Low — both are viable; decide early so handler stubs follow the right pattern.
- **Decision point:** When `apigateway/cmd/main.go` is created, choose and document in PROMPT_HISTORY.

---

## Coverage Gates Pass Trivially (Fail-Open Until Logic)

**What's happening:**
The Makefile + CI coverage gates (90% line, 100% branch on logic) are designed to **skip gracefully until code lands**:
- `scripts/coverage-gate.sh` exits 0 if a coverage profile doesn't exist or has no statements (lines 14–16)
- CI job `build-test` checks if testable packages exist; if none, it logs "no testable packages yet — skip" and exits 0 (ci.yml ~60)
- Branch-coverage gate (`scripts/logic-coverage.sh`) only runs once gobco can find real logic

**Impact:** ✓ Tests are passing, ✓ CI is green — **but gates are vacuously true**. The moment TDD adds logic, the gates become ACTIVE and ENFORCE. **This is intentional and correct**, but creates a false sense of coverage until real tests land.

**Mitigation:** Before merging logic code, verify:
1. `make cover` reports actual coverage numbers (not "no profile")
2. `make cover-check` passes with coverage > 90%
3. `make cover-logic` runs and enforces 100% branch coverage on new business logic

**Files affected:** All modules will accumulate coverage profiles once TDD starts.

---

## Durable Segment Log Implementation Risk

**What's pending:**
The MQ broker must implement a **Kafka-lite append-only segment log with crash recovery** (ADR-0001, Accepted). This is the "hard part" and the honest proof of "custom MQ".

**Implementation risks:**

### Partial writes and fsync semantics
- **Problem:** If a producer crashes mid-write or broker crashes before fsync, what happens to partially written messages?
- **Approach needed:** Write protocol must be atomic at some granule (frame-based or record-delimited); fsync must happen AFTER a complete record is written and offset tracked.
- **Test requirement:** TDD must include crash-recovery tests — kill the broker mid-write and verify recovery doesn't lose or duplicate committed messages.

### Segment roll and index rebuild
- **Problem:** Segments grow; need to roll over to new files. On startup, rebuild in-memory offset index from segment files.
- **Test requirement:** Test roll-over at edge cases (exactly at segment boundary, mid-record) and index rebuild consistency.

### Consumer-group offset tracking
- **Problem:** Consumer groups share partitions; each group tracks its offset per partition. Offsets must persist so a restarted consumer continues from where it left off.
- **Approach:** Store offsets in a separate topic or dedicated KV; persist to disk.
- **Test requirement:** Verify offset commit is durable and survives broker restart.

**Files affected:** `mq/broker/` (once created)

**Mitigation:** TDD with unit tests for each piece before integration.

---

## Idempotent Collector Upserts Not Yet Designed

**What's critical:**
At-least-once delivery means collectors may see the same message twice. Idempotent writes are **mandatory** (PROJECT.md §5 #1, CLAUDE.md conventions).

**Issue:** The Postgres schema is not yet defined. The upsert key MUST be `(uuid, metric_name, ts)` per CLAUDE.md, but:
- What if the schema doesn't define a unique constraint on `(uuid, metric_name, ts)`? Duplicate inserts will fail.
- What if the value column is nullable and changes between retries? Upsert is ambiguous.

**Files affected:** `collector/` (schema, upsert logic)

**Approach:**
1. Define the schema with a unique constraint on `(uuid, metric_name, ts)` before TDD the upsert logic.
2. Use Postgres `ON CONFLICT (uuid, metric_name, ts) DO UPDATE` or similar to handle redelivery.
3. Test: Send the same message twice; verify exactly one row is inserted (or updated).

**Mitigation:** ADR-0002 confirms PostgreSQL; schema design is a prerequisite for TDD.

---

## Backpressure Not Yet Validated

**What's pending:**
The assignment requires testing three producer/consumer ratios (PROJECT.md §4.3):
- producers > consumers (queue grows; test backpressure)
- producers < consumers (consumers idle; test throughput at max rate)
- producers = consumers (balanced; test latency)

**Risk:** If the MQ client library doesn't implement backpressure (blocking on full buffers), producers can OOM the broker. If buffering is unbounded, a slow consumer can cause the broker to run out of memory.

**Files affected:** `mq/client/` (producer/consumer lib), `streamer/cmd/` (use producer), `collector/cmd/` (use consumer)

**Test requirement:** Scale tests that drive all three ratios and verify:
- Queue depth grows without OOM when producers > consumers
- Latency increases predictably under load
- Broker memory usage stays bounded

**Mitigation:** Design the client library with bounded buffers and blocking semantics from the start.

---

## Helm Charts and Dockerfiles Not Yet Created

**What's missing:**
- `Dockerfile` per service (`mq/`, `streamer/`, `collector/`, `apigateway/`)
- Helm sub-charts per service
- Helm umbrella chart (`k8s-infra/helm/telemetry/Chart.yaml` and templates)
- PostgreSQL sub-chart dependency

**Impact:** `make kind-up`, `make kind-load`, `make helm-install` are all stubbed in the Makefile (lines 69–77). CI job `helm-lint` gracefully skips (ci.yml ~110). **Once binaries are built, Dockerfiles are straightforward**, but chart templates are a second phase.

**Files affected:** `k8s-infra/helm/telemetry/`, and Dockerfile additions to each module

**Mitigation:** Plan Dockerfile + chart scaffolding once main binaries compile.

---

## README and AI-Usage Documentation Not Yet Written

**What's required (per PROJECT.md §1 deliverables):**
- Comprehensive README: architecture & design writeup, build/packaging, install workflow, sample user workflow, and **how AI assistance was used**.
- AI-usage doc: bootstrapping repo/code/tests/build env, with **exact prompts used** and **notes on where prompts fell short** and needed manual intervention.

**Impact:** These are **required deliverables** for the assignment to be considered complete. Currently, only `docs/adr/` and `docs/PROMPT_HISTORY` exist; no user-facing README or comprehensive AI-usage guide.

**Files affected:** Missing `README.md` (root), missing or sparse `docs/PROMPT_HISTORY` or `docs/AI-USAGE.md`

**Mitigation:** Reserve a final phase for README + AI-usage writeup. Capture prompts and interventions in PROMPT_HISTORY as they happen.

---

## Multi-Module Build Isolation: Docker Parity Risk

**What's in place:**
Each module has its own `go.mod` with explicit `replace` directives so it builds standalone (`GOWORK=off`) in Docker, matching how CI builds (ci.yml lines 34–36). This is correct for avoiding surprise cross-module dependency coupling.

**Risk:** If a developer forgets to use `GOWORK=off` in Docker build or runs `go build ./...` from the repo root without respecting the Makefile, builds may accidentally pass locally but fail in CI.

**Mitigation:** Makefile targets enforce `GOWORK=off` (line 33), and CI proves it. **Developer docs must emphasize: always run `make build` or explicit module builds, never global `go build ./...`.**

**Files affected:** `Makefile`, module `go.mod` files (via `replace` directives)

---

## Limited to 10 Streamer + 10 Collector Instances (Exercise Ceiling)

**What's a constraint, not a risk:**
The assignment caps producers and consumers at **10 instances each** (PROJECT.md §1 hard constraint). This is a **scope boundary**, not a bug, but it drives performance testing and MQ design.

**Implication:**
- Partition count ≥ 10 (so each collector can own ≥ 1 partition in steady state)
- Max throughput is bounded by MQ capacity under 10×10 load
- Perf harness tests only (10,2), (2,10), (5,5) — not 100×100

**Mitigation:** Design for scale-out mindset (partitions, consumer groups, offset tracking) even though the exercise stops at 10.

---

## Potential Risks Not Yet Active (But Watch for Them)

### MQ Transport Frame Framing
- **Status:** ADR-0004 commits to gRPC streaming (Accepted). This is settled.
- **Watch:** gRPC unary vs streaming; streaming is better for throughput but needs backpressure. Verify stream context handling on producer timeout.

### PostgreSQL Connection Pooling
- **Status:** No code yet; `pgx` is locked in CLAUDE.md.
- **Watch:** Collector pool size must match max collector instances (10). Design pool sizing before TDD.

### Prometheus Metrics Cardinality
- **Status:** Metrics collection expected per PROJECT.md §4.3 (perf harness).
- **Watch:** Avoid unbounded cardinality (e.g., per-GPU labels without limits). Scope metrics to the dashboard's needs.

### Graceful Shutdown
- **Status:** Not yet addressed.
- **Watch:** All services must drain in-flight messages and commit offsets on SIGTERM. TDD must include shutdown tests.

---

## Summary

| Category | Status | Impact |
|----------|--------|--------|
| **Core logic** | Not implemented | LATENT — gates pass vacuously |
| **GPU id semantics** | ADR Proposed, not Accepted | HIGH — freezes schema before too late |
| **MQ persistence depth** | Direction set, effort TBD | MEDIUM — revisit at broker build |
| **Segment log + recovery** | Complex, unvalidated | HIGH — needs crash recovery tests |
| **Idempotent upserts** | Schema not yet defined | HIGH — prerequisite for TDD |
| **Backpressure** | Not yet validated | MEDIUM — test in perf harness |
| **Dockerfiles + Helm** | Not created | MEDIUM — scaffolding once binaries work |
| **README + AI-usage** | Not written | MEDIUM — required deliverable, final phase |
| **10-instance ceiling** | By design | LOW — scope boundary |

---

*Concerns audit: 2026-06-24*
