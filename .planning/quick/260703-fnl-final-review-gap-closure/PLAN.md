# Quick Task — Final-Review Gap Closure (pre-submission)

**Created:** 2026-07-03 · **Branch:** `adr-backfill` (ride the existing v1-submission branch, before the PR opens)
**Trigger:** Final code review of the codebase against `instructions.md` (four parallel verifiers, one per
assignment agent role). Verdict: **all hard spec requirements PASS** — proto contract, from-scratch MQ with
ADR-001 at-least-once semantics, restamping streamer, ack-after-persist collector, schema + composite index,
three exact gateway endpoints, fully swag-generated OpenAPI, multi-stage Dockerfiles, Helm sub-charts,
Makefile gates (coverage measured live at 90.6% ≥ 90). The items below are the residual gaps.

---

## Tier 1 — fix before opening the submission PR

### T1.1 Spec/doc drift: WAL is deferred, but two docs still say "built in Phase 6"
- `instructions.md:24` — "adds crash durability in Phase 6" → reword to: deferred post-v1 by owner
  directive 2026-07-03 (renumbered Phase 7); see ADR-009 + `docs/FUTURE.md`.
- `CLAUDE.md:83` — "built in Phase 6" → same rewording (interface seam shipped; backend deferred).
- Rationale: the spec's own deviation register currently promises an artifact that deliberately does not
  exist. CLAUDE.md's rule ("if anything conflicts with the spec — fix the conflicting artifact") applies.

### T1.2 Stale `.planning/STATE.md` metrics block (lines ~38–41)
- Progress bar + "Phases complete: 6/8 (1, 01.1, 2, 3, 4, 5)" omits Phase 6, which line 29 records as
  COMPLETE (5/5 SC). "Requirements delivered: 40/52" still counts Phase-6 requirements
  (DOC-02/03, OBS-01/02, OPS-07/08/09, API-05, QA-07) as remaining.
- Fix: phases complete 7/8 (1, 01.1, 2, 3, 4, 5, 6); requirements delivered 49/52 (remaining
  DUR-01/02, QA-05 — Phase 7, deferred post-v1). One source of truth — make the block agree with line 29–31.

### T1.3 Streamer elasticity is code-proven but not declarative in Helm
- Spec: "Support running up to 10 instances concurrently." Code + `TestStream_Concurrent10` pass; but
  `deployments/charts/streamer/templates/deployment.yaml:11` hardcodes `replicas: 1` with no values knob —
  scale-out only via manual `kubectl scale` (README:107-113).
- Fix: add `replicaCount: 1` to the streamer sub-chart values (surfaced through `deployments/values.yaml`),
  template it in the Deployment. Same treatment for collector (also hardcoded 1). Leave MQ untouched —
  its hardcoded `replicas: 1` + `Recreate` is the ADR-008 invariant, not a gap.
- Verify: `helm template deployments --set streamer.replicaCount=10 | grep replicas` renders 10;
  `make lint` on charts if a target exists, else `helm lint deployments`.

## Tier 2 — polish (same branch, cheap, reviewer-visible)

### T2.1 Remove dead code that promises its own removal
- `internal/config/config.go:19-26` — `WorkChCap`, marked `// Deprecated … will be removed when cmd/mq is
  fully rewired`; cmd/mq is rewired, field is never read. Remove field + the assertions in
  `config_test.go:23,37,63`.
- `api/proto/mq.proto` — `ConsumeRequest` message kept "for backwards compatibility … removed in a future
  cleanup phase"; zero references. Remove, `make proto` to regen `pkg/pb`.

### T2.2 Stale comment: `pkg/models/telemetry.go:50` references `CopyFromRows`
- Actual persistence path is `pgx.Batch`/`SendBatch` with `ON CONFLICT` (ADR-007 documents why CopyFrom
  was rejected). Fix the comment to match reality / point at ADR-007.

### T2.3 Coverage headroom (gate passes at 90.6% — 0.6pt margin)
- Below-bar packages: `pkg/db` 85.1%, `internal/collector` 86.8%. Any small addition to either could sink
  the module gate. Add targeted unit tests (error paths in `pkg/db/read.go` / `db.go`, collector flush
  edge cases) to lift both above ~90 and buy margin. Time-boxed; not a spec requirement (gate is module-level).

## Noted, no action (deliberate decisions — do not "fix")
- Streamer chart mounts a fixture CSV via ConfigMap in kind (real 1.1MB DCGM CSV is gitignored per brief).
- Poison-message ack-and-skip in collector (documented anti-stall choice; ON CONFLICT absorbs redelivery).
- `cmd/*` mains untested and excluded from gate (thin wiring by design).
- Untracked `.planning/quick/260703-ilo-backfill-missing-adrs/PLAN.md` — commit it for the record alongside
  its already-committed SUMMARY, or delete if intentionally local. Decide at commit time.

## Gate (definition of done)
`make build && make test && make coverage && make lint` all green; `helm lint`/`helm template` clean with
replicaCount overrides; STATE.md metrics agree with its own status lines; no remaining "Phase 6" WAL claims:
`rg -n "Phase 6" instructions.md CLAUDE.md` returns only correct/updated wording.

## Commits (Conventional, atomic)
1. `docs: reconcile WAL deferral — instructions.md + CLAUDE.md say Phase 7 deferred (ADR-009)`
2. `docs(gsd): fix stale STATE.md metrics — Phase 6 complete, 49/52 requirements`
3. `feat(helm): values-driven replicaCount for streamer + collector sub-charts`
4. `chore(mq): remove deprecated WorkChCap config + dead ConsumeRequest proto message`
5. `docs(models): fix stale CopyFromRows comment — SendBatch per ADR-007`
6. `test(db,collector): lift package coverage above 90% for gate headroom` (if T2.3 done)
