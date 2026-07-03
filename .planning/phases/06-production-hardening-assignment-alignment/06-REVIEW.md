# Phase 6 Review: Production Hardening + Assignment Alignment

**Source:** Code review + assignment-alignment audit (2026-07-03) against the assignment spec
(`GPU Telemetry Pipeline Message Queue.pdf` / `instructions.md`), covering deliverables,
success criteria, and the "elastic, scalable and stable" framing.

**Context:** Phases 1–5 delivered the core system: broker-side at-least-once MQ (ADR-001),
exactly-once pipeline via idempotent upsert, ≥90% enforced coverage, race-proven concurrency,
kind/Helm deployment. The gaps below are not core-correctness defects — they are
deliverable-checklist and production-operability items a grader walks through with the
assignment in hand.

---

## Findings

### F-01 · CRITICAL · AI-prompt documentation is narrative-only

**Assignment text:** "Please provide detailed information of **all the prompts used** and a good
description of **where a prompt fell short** that required manual intervention." Also under
Deliverables: "How was the project / repo bootstrapped? If AI was used, document the prompts."
(repeated for code, unit tests, build env).

**Current state:** `docs/AI_USAGE.md` describes AI usage at the summary level (what AI did,
human oversight, orchestration model). There is **no verbatim prompt log** — no reproducible
prompt-by-prompt record with outcomes and failure notes.

**Fix:** A dedicated prompt log (`docs/AI_PROMPTS.md`): table of *stage / tool / verbatim prompt
(or session-representative prompt) / outcome / where it fell short + manual intervention*.
Mine `.planning/` phase artifacts (PLAN/SUMMARY/VERIFICATION, ADRs, the 4-agent review at
`.planning/quick/260702-ku8`) for the real prompt history — bootstrapping, proto design, MQ
concurrency, collector batching, swag, Helm, the C-1 ack-on-persist fix. Honest failure notes
are explicitly graded ("where a prompt fell short"). Link from README + AI_USAGE.md.

### F-02 · HIGH · No health endpoints on any service

**Assignment framing:** "elastic, scalable and **stable** telemetry pipeline"; k8s deployability
is a core deliverable. Stable pods without liveness/readiness signaling is a contradiction —
Kubernetes cannot detect a wedged broker or a gateway with a dead DB pool.

**Current state:** grep for `healthz|readyz|livez` across the repo: zero hits. No HTTP health
surface on gateway, MQ, streamer, or collector.

**Fix:**
- **MQ:** `/healthz` (process live) + `/readyz` (gRPC server serving) on the existing control
  plane (`:8080` ServeMux).
- **Gateway:** `/healthz` + `/readyz` (readiness = DB pool ping with short timeout) on chi.
- **Streamer / Collector:** minimal `net/http` health listener (they currently expose no HTTP);
  readiness = MQ stream connected (collector) / CSV open + MQ reachable (streamer).

### F-03 · HIGH · Helm charts have no liveness/readiness probes

**Current state:** No `livenessProbe`/`readinessProbe` in any sub-chart template
(`deployments/charts/*/templates/deployment.yaml`).

**Fix:** Wire probes into every sub-chart, values-configurable, pointing at F-02 endpoints.
For streamer/collector use the new health listener port. (Promotes v2 requirement ENH-03 to v1.)

### F-04 · HIGH · Helm charts have no resource requests/limits

**Current state:** No `resources:` blocks anywhere in the charts. Pods are BestEffort — first
evicted under node pressure, and HPA-by-CPU is impossible without requests.

**Fix:** Sensible defaults per service in each sub-chart's `values.yaml` (requests + limits),
overridable. MQ limit sized against ring capacity × message size + credit ceiling.

### F-05 · MEDIUM · No horizontal-scaling story beyond `--set replicas`

**Assignment text:** "dynamically scale up/down the number of Streamers/Collectors" (≤10).

**Current state:** Scaling is manual (`--set replicas=N`, soak script). No HPA anywhere; no
documented scaling workflow in README.

**Fix:**
- HPA (`autoscaling/v2`, CPU-based, values-gated `hpa.enabled`) for the **gateway** (the
  stateless read tier where autoscaling is meaningful).
- Streamer/collector: documented `kubectl scale` / `--set replicas` workflow (README) — their
  scaling is load-generation/consumption capacity, correctness already proven to 10 instances.
  HPA optional via the same values toggle.
- MQ stays hardcoded single-replica + `Recreate` (ADR-001 invariant — do not touch).

### F-06 · MEDIUM · Telemetry endpoint has no pagination

**Assignment text:** "Return **all** telemetry entries for a specific GPU" — with a looping
streamer, "all" grows without bound; the current answer is a hard row cap.

**Current state:** `VANTAGE_GATEWAY_MAX_ROWS` (default 1000) + `X-Truncated`/`X-Row-Limit`
headers. A client cannot retrieve the remainder — truncation without continuation.

**Fix:** `limit`/`offset` query params on `GET /api/v1/gpus/{id}/telemetry`, pushed down into
SQL (`LIMIT $n OFFSET $m` on the composite-index path), response envelope or headers carrying
pagination metadata (`limit`, `offset`, `has_next`). Keep the max-rows cap as the `limit`
ceiling. Update swag annotations + regenerate spec. (Reverses the REQUIREMENTS.md
out-of-scope entry — deliberate, documented.)

### F-07 · MEDIUM · No CI pipeline

**Current state:** `.github/` has no `workflows/`. All gates (build/test -race/coverage ≥90%/
lint) are Makefile-local — nothing enforces them on push/PR to the protected main branch.

**Fix:** GitHub Actions workflow on push + PR: `make build`, `make test`, `make coverage`,
`make lint` (and `make swagger` drift check). Single job is sufficient; the Makefile is the
source of truth — CI just invokes it.

### F-08 · LOW · Unstructured logging (stdlib `log`)

**Assignment bonus:** "Clear logging and error handling."

**Current state:** All services use stdlib `log` with prefixed messages.

**Fix:** Migrate to `log/slog` (stdlib, no new dependency): JSON handler, level via env,
per-service `service` attribute. Mechanical sweep across `cmd/*` + `internal/*`; keep messages
and fields consistent with existing content.

### F-09 · LOW · README staleness — migration hook description

**Current state:** README Phase-5 section says the migration Job runs via a **pre-install**
hook; the actual template (after the 05-05 deadlock fix) is `post-install,post-upgrade`.

**Fix:** Correct the README; while there, add the new health/probe/HPA/pagination workflow
docs from F-02..F-06 (README is a living deliverable — DOC-01 cadence).

---

## Explicitly out of scope for this phase

- WAL durability / crash recovery — moved to **Phase 7** (unchanged in content).
- Prometheus `/metrics`, tracing — noted as v2 candidates (ENH), not assignment-required.
- Auth/TLS/rate limiting — out of assignment scope (unchanged).
- Any change to MQ delivery semantics or the single-replica invariant.

## Requirement mapping

| Finding | Requirement | Severity |
|---|---|---|
| F-01 | DOC-02 | Critical |
| F-02 | OBS-01 | High |
| F-03 | OPS-07 | High |
| F-04 | OPS-08 | High |
| F-05 | OPS-09 | Medium |
| F-06 | API-05 | Medium |
| F-07 | QA-07 | Medium |
| F-08 | OBS-02 | Low |
| F-09 | DOC-03 | Low |
