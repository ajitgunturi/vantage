# Phase 6: Production Hardening + Assignment Alignment - Context

**Gathered:** 2026-07-03
**Status:** Ready for planning
**Source:** PRD Express Path (.planning/phases/06-production-hardening-assignment-alignment/06-REVIEW.md)

<domain>
## Phase Boundary

Close the nine findings of the 2026-07-03 code-review / assignment-alignment audit (06-REVIEW.md)
so the delivered system satisfies every assignment deliverable a grader can check: verbatim
AI-prompt documentation, Kubernetes-native health/elasticity (health endpoints, probes, resource
limits, HPA), paginated telemetry reads, CI-enforced quality gates, and structured logging.

**Hard fence:** No changes to MQ delivery semantics (ADR-001), the single-replica MQ invariant
(`replicas: 1` + `strategy: Recreate`, hardcoded), or WAL durability (that is Phase 7). The
existing ≥90% coverage gate, race-detector suite, and smoke harness must keep passing.

</domain>

<decisions>
## Implementation Decisions

### AI-prompt documentation (F-01 · DOC-02 · Critical)
- Create `docs/AI_PROMPTS.md`: a verbatim prompt log — table/sections of *stage / tool / prompt
  (or session-representative prompt) / outcome / where it fell short + manual intervention*.
- Cover the assignment's four named aspects: repo bootstrap, code, unit tests, build env.
- Mine `.planning/` phase artifacts (PLAN/SUMMARY/RESEARCH/VERIFICATION, ADR-001/002, the
  4-agent review at `.planning/quick/260702-ku8`) for the real prompt history — including
  honest failure notes (C-1 ack-on-persist bug, M-2 missed wakeup, helm pre-install deadlock).
- Link from README's AI-assistance section and from `docs/AI_USAGE.md`.

### Health endpoints (F-02 · OBS-01 · High)
- MQ: `/healthz` (process live) + `/readyz` (gRPC server serving) on the existing control plane
  ServeMux (`:8080`).
- Gateway: `/healthz` + `/readyz` (readiness = DB pool ping with short timeout) on chi.
- Streamer/Collector: minimal `net/http` health listener (new port, env-configurable);
  readiness = MQ stream connected (collector) / CSV open + MQ reachable (streamer).
- Health endpoints excluded from swagger (they are operational, not part of the documented API).

### Helm probes (F-03 · OPS-07 · High)
- livenessProbe + readinessProbe in every sub-chart deployment template, values-configurable
  (path/port/timings), pointing at the OBS-01 endpoints.
- Promotes v2 ENH-03 to v1.

### Resource requests/limits (F-04 · OPS-08 · High)
- `resources.requests`/`limits` defaults in every sub-chart `values.yaml`, overridable.
- MQ limit sized against ring capacity × message size + credit ceiling.

### Scaling story (F-05 · OPS-09 · Medium)
- HPA (`autoscaling/v2`, CPU-based, values-gated `hpa.enabled`, default off) for the gateway.
- Streamer/collector: documented `kubectl scale`/`--set replicas` workflow in README; optional
  HPA toggle acceptable but not required.
- MQ chart untouched w.r.t. replicas/strategy — ADR-001 invariant.

### Pagination (F-06 · API-05 · Medium)
- `limit`/`offset` query params on `GET /api/v1/gpus/{id}/telemetry`, pushed down into SQL
  (`LIMIT`/`OFFSET` on the composite-index path).
- Pagination metadata in the response (e.g. envelope or headers with `limit`, `offset`,
  `has_next`) — exact shape is Claude's discretion, but it must be documented in swagger.
- Existing `VANTAGE_GATEWAY_MAX_ROWS` cap becomes the `limit` ceiling; `X-Truncated` semantics
  reconciled with pagination (may be superseded).
- swag annotations updated; `make swagger` regenerated; spec committed.

### CI (F-07 · QA-07 · Medium)
- GitHub Actions workflow (`.github/workflows/ci.yml`) on push + pull_request: `make build`,
  `make test` (-race), `make coverage` (≥90% gate), `make lint`.
- Makefile remains the single source of truth — CI only invokes make targets.
- No Docker/kind jobs required in CI (local `make smoke`/`make soak` remain the e2e path).

### Structured logging (F-08 · OBS-02 · Low)
- Migrate stdlib `log` → `log/slog` (stdlib, no new dependency) across `cmd/*` + `internal/*`.
- JSON handler, level via env, per-service `service` attribute.
- Keep message content/fields consistent with existing logs; DSN-never-logged rule preserved.

### README accuracy (F-09 · DOC-03 · Low)
- Fix stale claim: migration Job runs via `post-install,post-upgrade` hook (not pre-install).
- Document the new surfaces: health endpoints, probes, HPA toggle, scaling workflow, pagination
  params (DOC-01 living-README cadence).

### Claude's Discretion
- Exact pagination response shape (envelope vs headers) — must be swagger-documented.
- Health listener port numbers for streamer/collector.
- slog handler wiring details (shared helper in pkg/ vs per-service setup) — respect the
  directory-ownership rule (`pkg/` is the only shared surface).
- CI job topology (single job vs matrix) as long as all four make gates run.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Audit source
- `.planning/phases/06-production-hardening-assignment-alignment/06-REVIEW.md` — the nine findings (F-01..F-09), severities, requirement mapping

### Invariants this phase must not break
- `docs/adr/ADR-001-bidi-at-least-once-delivery.md` — MQ delivery semantics + single-replica invariant
- `deployments/charts/mq/templates/deployment.yaml` — hardcoded `replicas: 1` + `Recreate`
- `Makefile` — gate targets (`build`, `test`, `coverage`, `lint`, `swagger`) CI must reuse

### Surfaces being extended
- `internal/http/inspect.go` + `cmd/mq/main.go` — MQ control plane (add healthz/readyz)
- `internal/gateway/server.go`, `internal/gateway/handler.go`, `pkg/db/read.go` — gateway + SQL (pagination, health)
- `internal/streamer/streamer.go`, `internal/collector/collector.go` — health listeners
- `deployments/charts/*/templates/`, `deployments/charts/*/values.yaml` — probes, resources, HPA
- `docs/AI_USAGE.md`, `README.md` — docs to link/correct

</canonical_refs>

<specifics>
## Specific Ideas

- Assignment text driving DOC-02: "detailed information of **all the prompts used** and a good
  description of **where a prompt fell short** that required manual intervention" — failure
  honesty is explicitly graded; do not sanitize.
- Gateway readiness must not hammer the DB: cheap `Ping` with short timeout, optionally cached.
- Probe defaults must tolerate slow kind startup (initialDelaySeconds generous enough that
  `helm install --timeout 6m` still converges).

</specifics>

<deferred>
## Deferred Ideas

- Prometheus `/metrics` endpoint + tracing — v2 (ENH), not assignment-required.
- WAL durability / crash recovery — Phase 7 (DUR-01/02, QA-05).
- Auth/TLS/rate limiting — out of assignment scope.

</deferred>

---

*Phase: 06-production-hardening-assignment-alignment*
*Context gathered: 2026-07-03 via PRD Express Path*
