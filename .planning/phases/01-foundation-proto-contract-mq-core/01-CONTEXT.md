# Phase 1: Foundation — Proto Contract + MQ Core - Context

**Gathered:** 2026-06-27
**Status:** Ready for planning
**Source:** Inline design decisions (3 forks resolved with user) + project research

<domain>
## Phase Boundary

This phase delivers the **proto contract** and the **custom in-memory message queue core** — nothing
downstream. In scope:
- `api/proto/mq.proto` defining the telemetry payload + the `Produce` (unary) and `Consume`
  (server-streaming) RPCs, generated into `pkg/pb` via `make proto` (protoc).
- The MQ service (`cmd/mq` entrypoint + `internal/` logic packages): a thread-safe bounded ring-buffer
  queue behind a `Store` interface, the gRPC server (data plane), and the HTTP `GET /api/v1/queue/inspect`
  control-plane endpoint.
- Race-detector + coverage tests proving correct concurrent delivery.

**Out of scope this phase:** Streamer, Collector, PostgreSQL/schema (Phase 2/3); WAL persistence
(Phase 6); Dockerfiles/Helm (Phase 5). The `Store` interface is built now so Phase 6's WAL backend
slots in without touching consumers, but the WAL itself is NOT implemented here.

</domain>

<decisions>
## Implementation Decisions (LOCKED)

### MQ internal primitive
- The queue is a **bounded ring buffer** guarded by `sync.Mutex`/`sync.RWMutex` (built from scratch —
  no third-party broker). This matches the brief's wording and satisfies MQ-04/MQ-05.
- Competing-consumer **unique delivery (MQ-03)**: each enqueued message is delivered to **exactly one**
  consumer. Implement via a single dispatch path off the ring buffer (e.g. a dispatch goroutine handing
  the next message to whichever consumer stream is ready) — NOT per-consumer broadcast/fan-out.

### Full-buffer behavior
- **Drop-oldest (MQ-05):** when the buffer is full, overwrite the oldest unconsumed slot. `Produce`
  therefore never blocks and does not return `ResourceExhausted` in the default in-memory mode.
- **Test implication:** the `N produced = N consumed` correctness test (QA-02) MUST size the buffer so
  the test workload never overflows — otherwise drop-oldest loses messages by design and the count
  assertion would fail for a non-bug reason. Loss is acceptable ONLY under sustained overflow.

### Store interface seam (MQ-08)
- MQ storage sits behind a `Store` interface; the **in-memory ring-buffer backend is the default**.
- The interface is shaped so a WAL-backed backend (Phase 6) can be added behind it. Do not implement
  the WAL now; just make the seam clean (enqueue/dequeue/inspect surface, no disk assumptions baked in).

### Proto tooling & contract
- **protoc** (already wired as `make proto` → `pkg/pb`, `paths=source_relative`). Single contract file
  `api/proto/mq.proto`. Do not introduce buf.
- Generated code lives in `pkg/pb` and is excluded from coverage gates (generated stubs).

### Transport
- gRPC data plane: `Produce` unary, `Consume` server-stream. Configure **server-side keepalive**
  parameters so long-lived `Consume` streams don't die silently behind NAT/LB (PITFALLS finding).
- HTTP control plane: stdlib `net/http`, single endpoint `GET /api/v1/queue/inspect` returning JSON
  (depth, capacity, produced/consumed counts, dropped count, active consumers). Shares the queue struct
  by pointer with the gRPC server (same process).
- Graceful consumer disconnect (MQ-07): honor stream context cancellation; `defer` cleanup so no
  goroutine leaks when a consumer drops.

### Quality gates (phase exit)
- `go test -race` clean, run repeatedly (`-count` high) — no data races, no deadlock from holding a
  lock across a channel/stream send (release the lock before dispatching).
- Delivery-count correctness: with K concurrent consumers and N produced, exactly N consumed total.
- ≥90% line coverage on the MQ logic packages (`internal/`), excluding `pkg/pb` and `cmd/`.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Authoritative spec & conventions
- `instructions.md` — assignment brief (MQ requirements, endpoints, constraints)
- `CLAUDE.md` — repo conventions, layout, hard constraints (incl. opt-in WAL note)
- `.planning/PROJECT.md` — project context + Key Decisions
- `.planning/REQUIREMENTS.md` — REQ-IDs for this phase (MQ-01..08, QA-02)

### Research (phase-relevant)
- `.planning/research/ARCHITECTURE.md` — MQ concurrency model, competing-consumer dispatch, service boundaries
- `.planning/research/PITFALLS.md` — MQ races/deadlock/leak pitfalls, gRPC keepalive, drop policy, test design
- `.planning/research/STACK.md` — grpc-go / protobuf / protoc versions
- `.planning/research/SUMMARY.md` — synthesized findings + Phase-1 exit criteria

</canonical_refs>

<specifics>
## Specific Ideas

- Module path `github.com/ajitg/vantage`; MQ entrypoint `cmd/mq/main.go` (thin wiring), real logic in
  `internal/` packages so the coverage gate measures logic, not main wiring.
- `/api/v1/queue/inspect` JSON should be `curl`-friendly and expose enough to debug delivery
  (depth, capacity, total produced, total consumed, total dropped, active consumer count).
- Proto: a `TelemetryMessage` carrying the DCGM fields (timestamp, metric_name, gpu_id, device, uuid,
  modelName, hostname, container, pod, namespace, value, labels_raw) — the streamer/collector reuse it.

</specifics>

<deferred>
## Deferred Ideas

- **WAL / disk persistence** → Phase 6 (opt-in, behind the `Store` interface built here).
- **Streamer, Collector, PostgreSQL schema** → Phase 2/3.
- **Dockerfile + Helm sub-chart for MQ** → Phase 5 (OPS reqs).
- **Configurable drop policy / richer inspect / backpressure modes** → v2 enhancements (ENH-01/06).

</deferred>

### Claude's Discretion

- Ring-buffer default capacity (make it configurable via env/flag), dispatch goroutine vs handoff
  channel internal detail, exact `internal/` package split, proto field numbering/types, and the precise
  shape of the `Store` interface methods — choose idiomatic Go, document briefly.

---

*Phase: 01-foundation-proto-contract-mq-core*
*Context gathered: 2026-06-27 via inline design decisions*
