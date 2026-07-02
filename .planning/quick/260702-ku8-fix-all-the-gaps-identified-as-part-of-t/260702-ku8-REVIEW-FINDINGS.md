# Mid-assignment review findings — input for quick task 260702-ku8

Source: 4-agent review (MQ, pipeline, gateway, deliverables audit) of phases 1–4, verified against
the assignment PDF (`GPU Telemetry Pipeline Message Queue.pdf`). Every finding below was verified
against the code; the critical one was re-verified line-by-line by the orchestrator.

Gates at review time: build ✅ · `go test -race` ✅ · coverage 90.4% (floor 90) ✅ · lint ✅ (but see G-2).
Integration tests need: `DOCKER_HOST=unix:///Users/ajitg/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true`.

---

## CRITICAL

### C-1 — Collector acks batches even when the DB insert fails → silent data loss
`internal/collector/collector.go:57-84` (`persistBatch`), `:148-162` (`flush`).
`persistBatch` has **no non-nil return path**: `br.Exec()` errors are logged-and-continued and the
function ends `return nil`, so the `if err := persistBatch(...)` check in `flush` is dead code and
every message is acked regardless of persist outcome. A 30s Postgres outage = every batch consumed
in that window acked → MQ discards → data permanently lost. Violates "ack only after successful
persist" (COLL contract) and the project's core "no message loss" claim.
Also: the comment "the rest of the batch always lands" is wrong — pgx v5 `SendBatch` runs the whole
batch in ONE implicit transaction; an error aborts it and rolls back earlier rows too.
**Fix:** accumulate/detect Exec errors (and check `br.Close()`), return the error WITHOUT acking.
The broker's requeue-on-disconnect (`internal/server/server.go:188-200`) redelivers; ON CONFLICT
absorbs partial-persist duplicates. Keep the existing deliberate ack-of-bad-proto-rows behavior
(poison messages must still be acked — add a comment saying it's deliberate).
On flush error the Collector should treat the stream as failed (return from Consume → Run reconnect
loop redelivers), not spin persisting the same batch forever.
**Test:** add a unit/integration test that fails `SendBatch` (e.g. cancelled ctx, closed pool, or a
constraint-violating row + genuine error) and asserts NO acks were sent and the error propagates.

## MAJOR

### M-1 — e2e "exactly-once" test is tautological and orphaned from gates
`test/e2e/pipeline_test.go:335-352`. Assertion `count(*) == count(DISTINCT gpu_id,metric_name,timestamp)`
is true by construction (unique index `uq_gpu_metrics_natural_key` makes duplicates unstorable);
only completeness check is `count(*) > 0`. Fixture has 200 rows, all distinct keys → **assert
`count(*) == 200` exactly** to genuinely prove no-loss. Also the test is tagged `integration` but no
Makefile target runs `./test/e2e/...` (`make coverage` lists only `./internal/... ./pkg/...`), yet
`README.md:291` claims it runs automatically. **Fix:** strengthen assertion + add an `e2e` (or
extend `coverage`/`test-integration`) Makefile target that runs it; fix the README claim.

### M-2 — MQ missed-wakeup race: consumer parks with a deliverable message in store
`internal/server/server.go:260-275`. Notify channel is fetched AFTER the failed `TryDequeue`; a
Produce in the gap closes the old channel and installs a new one → consumer waits on the new
channel; last message of a burst stalls until the next Produce. **Fix (standard pattern):**
`ch := s.notifyChan()` BEFORE `TryDequeue`; wait on `ch` only if dequeue misses. Same race applies
to the `notifyAll()` wakeup in disconnect-cleanup (line ~200). **Test:** produce-after-consumer-parks
(single consumer, produce one message after the consumer is blocked waiting, assert delivery).

### M-3 — MQ shutdown hangs: GracefulStop blocks forever on live Consume streams; shutdownCh is dead code
`cmd/mq/main.go:85-92`, `internal/server/server.go:77,325-327`. `GracefulStop` does NOT cancel
server-side stream contexts; Consume streams are infinite; NOTHING reads `s.shutdownCh` (write-only).
SIGTERM → hang until SIGKILL; `httpSrv.Shutdown` never reached. The comment at server.go:322-324
misstates grpc-go semantics — fix it too. **Fix:** add `case <-s.shutdownCh: return status.Error(codes.Unavailable, "shutting down")`
to the send loop's blocking selects (existing defer requeues leases), AND/OR race GracefulStop
against a timeout falling back to `Stop()` in main. **Test:** shutdown-under-active-stream completes
within a bound.

### M-4 — Gateway silently truncates at MaxRows; DESC ordering undocumented
`internal/gateway/handler.go:112`, `pkg/db/read.go:122,138`. >MaxRows rows → plain 200 array of
exactly MaxRows with no signal. Spec says "return ALL telemetry entries ordered by time".
**Fix:** set an `X-Truncated: true` header (and ideally `X-Row-Limit: <n>`) when `len(rows) == maxRows`;
document in swag annotations (`@Header`) and regenerate swagger (`make swagger`); add one README
sentence justifying newest-first (DESC) ordering. Keep DESC (documented decision) — do not flip order.

### M-5 — Natural-key µs-collision can silently drop distinct datapoints at 10-streamer scale-out
Streamer stamps ns (`internal/streamer/streamer.go:70`) but TIMESTAMPTZ stores µs → effective key
`(gpu_id, metric_name, µs)`. Two streamers restamping the same (gpu,metric) in the same µs → ON
CONFLICT DO NOTHING silently keeps one. **Decision (locked): document, don't change schema.** Write
a short ADR (docs/adr/, next number after existing) stating the collision window, why it's accepted
(idempotency for redelivery outweighs the statistically rare cross-producer µs collision; fix path =
producer-id in key), and reference it from README's design-considerations section.

### M-6 — Streamer has no retry: one MQ blip kills every streamer instance
`internal/streamer/streamer.go:138-140` → error propagates → `cmd/streamer/main.go:48-50` log.Fatal.
**Fix:** retry-with-backoff around Produce failures (or around `Stream` in `Run`), mirroring the
Collector's reconnect loop: exponential backoff base 100ms cap 5s, ctx-aware sleep, exit only on
ctx cancel. **Test:** produce failure → retries → succeeds after MQ "recovers" (fake client).

## MEDIUM

### G-1 — Collector reconnect backoff never escalates
`internal/collector/collector.go:220`. `grpc.NewClient` is lazy/never fails → `backoff` reset at top
of every iteration → constant 100ms retry forever; documented exponential backoff (T-03-03b) never
engages. **Fix:** reset backoff only after a Consume attempt that survived some minimum duration
(e.g. >5s) or after a successful first Recv.

### G-2 — `make lint` swallows golangci-lint failures
`Makefile:80`: `golangci-lint run ./... 2>/dev/null || go vet ./...` — nonzero exit from lint
FINDINGS silently falls back to go vet. **Fix:** branch on binary presence, not exit code:
`if command -v golangci-lint >/dev/null; then golangci-lint run ./...; else go vet ./...; fi`.

### G-3 — Coverage/e2e Docker requirements undocumented in Makefile
`DOCKER_HOST`/`TESTCONTAINERS_RYUK_DISABLED` live only in test-file comments. **Fix:** document at
top of Makefile (comment) + README testing section; also fix README:409 drift ("internal/ packages"
→ internal + pkg).

### G-4 — AI-usage/prompts documentation ABSENT and UNPLANNED (assignment deliverable!)
The PDF requires detailed AI-usage documentation (how repo/code/tests/build-env were bootstrapped,
prompts used, where prompts fell short needing manual intervention) — heavily weighted success
criterion. Nothing exists; `instructions.md` and `.planning/REQUIREMENTS.md`/ROADMAP dropped it.
**Fix:** (a) add requirement DOC-02 to `.planning/REQUIREMENTS.md` (traceable, so later phases keep
it alive); (b) create `docs/AI_USAGE.md` documenting the actual workflow honestly: the project is
built with Claude Code + the GSD framework — per-phase discuss/plan/execute/verify with planning
artifacts in `.planning/phases/*/` (plans = the prompts), agent role briefs in `.ai/agents/`,
ADRs for design decisions; include a per-phase table (phases 1–4) linking to the plan files, and a
"where AI fell short / manual interventions" section seeded from STATE.md decisions (e.g. the
Phase 01.1 silent-loss reproduction that triggered ADR-001, swag runtime version mismatch, Rancher
Docker socket env). (c) add a README section "AI-Assisted Development" summarizing + linking to it.

## MINOR (fix if cheap, skip if risky)

- MQ: disconnect cleanup requeues before draining pending acks (`server.go:189-210`) — drain ackCh
  and apply acks FIRST, then requeue survivors; also removes the dead `if _, ok := leases[id]` branch.
- MQ: clean client `CloseSend()` → handler returns io.EOF → client sees codes.Unknown. Translate:
  `if errors.Is(recvFinalErr, io.EOF) { return nil }` (`server.go:232-233`).
- MQ: `creditCeiling()` doc says it caps memory but returns max(cap,1000) (`server.go:37-40,296-305`)
  — fix the comment to state the ceiling only guards the small-cap case (do NOT change behavior).
- Collector: `Consume` leaks recv goroutine on early error return (`collector.go:107,137-139`) —
  add `ctx, cancel := context.WithCancel(ctx); defer cancel()` at top of Consume.
- Gateway: duplicated godoc block `pkg/db/read.go:65-92` (lines repeated verbatim) — delete the dup.
- Gateway: no per-request DB deadline — add chi `middleware.Timeout` or `context.WithTimeout` (~10s)
  around handler DB calls.
- Streamer: `time.Sleep(loopDelayMS)` not ctx-aware (`streamer.go:142`) — use a ctx-aware wait.
- db: `Migrate` scheme rewrite misses `postgresql://` DSNs (`pkg/db/db.go:78`) — handle both schemes.
- e2e/test drift: leftover `fmt.Printf` at `internal/gateway/integration_test.go:167`; near-vacuous
  window assertion at integration_test.go:266-267 (assert first row IS the newest in-window row).

## OUT OF SCOPE for this quick task (defer to Phase 5)
- Dockerfiles, Helm charts, kind (Phase 5 as planned).
- /healthz//readyz endpoints (Phase 5 prep — note in ROADMAP if convenient, don't build now).
- Changing gpu_metrics schema / natural key (M-5 is document-only).
- Pagination for the gateway (M-4 is header-signal only).

## Constraints for the fix work
- TDD where the finding has a listed test; all gates must pass afterward:
  `make build && make test && make coverage && make lint` (coverage floor 90%; integration tests need
  the Rancher Docker env above).
- Conventional Commits, atomic per logical unit, NO Co-Authored-By trailer.
- `go test -race` must stay green; MQ changes especially need -race runs on ./internal/server/...
- Do not break documented decisions: drop-oldest ring, DESC ordering, ON CONFLICT DO NOTHING,
  bidi credit/ack protocol (ADR-001), swag-generated OpenAPI (regenerate via `make swagger` if
  annotations change — never hand-edit pkg/docs).
