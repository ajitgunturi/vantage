# Phase 5: DevOps + Quality Gates - Context

**Gathered:** 2026-07-02
**Status:** Ready for planning

<domain>
## Phase Boundary

Every microservice builds and deploys independently to a single Kubernetes cluster via Helm, and
the repository enforces its quality bar end-to-end. Deliverables: multi-stage Dockerfiles for all
service binaries (`build/*.Dockerfile`), a Helm umbrella chart at `deployments/` with a
self-contained sub-chart per service plus a PostgreSQL dependency, an in-cluster schema-migration
story, the kind-based local deploy workflow (`make kind-up / deploy / kind-down`), the phase-05
smoke script (full E2E through kind), a dedicated soak target, a live-infrastructure e2e test
harness (`make test-harness`), and closure of the QA-01/QA-04 quality requirements (unit tests
across all services, ≥90% coverage gate holding).

Requirements: OPS-01, OPS-02, OPS-03, OPS-04, OPS-05, QA-01, QA-04.

</domain>

<decisions>
## Implementation Decisions

### Helm topology & independence (OPS-02, OPS-03)
- **D-01 — Bitnami PostgreSQL via OCI.** Declare `bitnami/postgresql` (chart 18.7.8) as a
  `Chart.yaml` dependency pulled from `oci://registry-1.docker.io/bitnamicharts`. No in-repo
  postgres chart.
- **D-02 — One umbrella release with per-service flags.** Single release
  (`helm upgrade --install vantage deployments`, matching the existing Makefile target). Every
  service sub-chart exposes `enabled` and `image.tag` values. OPS-03 independence is demonstrated
  by `helm upgrade --reuse-values --set <svc>.image.tag=X` rolling ONLY that service (and by
  `enabled` flags deploying subsets).
- **D-03 — Self-contained sub-charts.** Each service sub-chart owns its full templates — no shared
  library chart. Duplication across the four charts is accepted to honor "strictly independent
  microservices".
- **D-04 — MQ single replica + `strategy: Recreate`** (locked by ROADMAP success criterion 2; not
  discussable). No rolling update of the in-memory broker.

### Image build & kind workflow (OPS-01, OPS-03)
- **D-05 — `kind load docker-image`** moves locally built images into the cluster (new
  `make kind-load` target). No local registry.
- **D-06 — Fixed `:dev` tag + `imagePullPolicy: IfNotPresent`.** USER CHOICE over the recommended
  `Never`: accepts that a forgotten `kind-load` surfaces as ImagePullBackOff (vantage/* images have
  no remote registry). Matches the Makefile's existing `vantage/<svc>:dev` tagging.
- **D-07 — `make deploy` composite target** = `docker` → `kind-load` → `helm-install`. `kind-up`
  stays separate (one-time cluster create). Each sub-step remains callable alone.
- **D-08 — Multi-stage Dockerfiles:** builder `golang:1.26-alpine`, final
  `gcr.io/distroless/static-debian12`, `CGO_ENABLED=0`. In-pod debugging via `kubectl debug`
  ephemeral containers (no shell in final images — by design).

### Schema migration in-cluster (carries Phase 2 D-06 forward)
- **D-09 — Helm pre-install/pre-upgrade hook Job** runs migrations to completion before service
  pods roll. Migrations run once per release operation, not per pod.
- **D-10 — Dedicated migrate image.** `build/migrate.Dockerfile` → `vantage/migrate:dev` (a 5th
  image in `make docker` / `kind-load`), wrapping the existing `cmd/migrate` binary. Service images
  stay single-binary.
- **D-11 — Umbrella chart owns the migration Job** (schema is a shared concern serving collector
  writes and gateway reads). Sub-charts stay purely per-service.

### Smoke & soak scope (QA-06 cadence, ROADMAP "soak-test")
- **D-12 — `make smoke-05` proves full E2E through kind:** helm install → migration Job complete →
  all pods Running → streamer publishing → port-forward gateway → `curl /api/v1/gpus` and
  `/telemetry` return real rows. Directly demonstrates success criterion 1.
- **D-13 — Smoke assumes cluster + deploy already exist** (`make kind-up deploy` run beforehand);
  fails fast with a clear message otherwise. Consistent with phases 1–4 smoke style
  (dependency-light, re-runnable).
- **D-14 — Dedicated `make soak` target,** separate from smoke: configurable duration and streamer
  replica count; asserts sustained flow (row counts grow, MQ inspect shows no runaway depth /
  loss). Supports the 10-concurrent-streamer requirement without bloating smoke.

### Live-infrastructure test harness (user-requested addition; QA "quality bar end-to-end")
- **D-15 — `make test-harness` runs a separate e2e suite against live infrastructure.** Distinct
  from `make test` (unit/integration), `make smoke` (human-run demo), and the coverage gate.
- **D-16 — Compose first, kind-pointable.** Default target is a docker-compose full stack (all
  five images + Postgres — extends the existing `docker-compose.yml` dev stack). The same suite is
  pointable at a kind/Helm deployment via env-provided base URLs (skipping stack startup).
- **D-17 — Testcontainers-driven Go suite.** Go tests (separate package, e2e build tag) use the
  `testcontainers-go` compose module to own the stack lifecycle programmatically. CAUTION: user
  runs Rancher Desktop — Ryuk must stay disabled (established project convention) and the compose
  module must be pinned to the same version as testcontainers-go core.
- **D-18 — Hermetic lifecycle:** up → test → down, teardown even on failure; `KEEP=1` escape hatch
  leaves the stack running for debugging. Excluded from `make test` and the coverage gate.
- **D-19 — Assertions: pipeline correctness E2E across real container boundaries** — row counts
  grow, gateway endpoints return data consistent with Postgres, MQ inspect counters reconcile
  (produced ≥ acked, no runaway depth). Kill/restart resilience scenarios are NOT in scope for the
  harness this phase.

### Claude's Discretion
- Coverage-gate mechanics for any new Go packages this phase adds (e2e suite must be excluded like
  `pkg/pb`/`pkg/docs`); whether `cmd/` stays outside the gate as today.
- Resource requests/limits, liveness/readiness probe details, Service types/ports in the charts.
- Helm chart linting (`helm lint`) and any chart-testing tooling.
- Exact soak defaults (duration, replica count) and env override names.
- Port-forward vs NodePort mechanics inside smoke-05.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Spec & role briefs
- `instructions.md` — canonical assignment spec (wins on any conflict).
- `.ai/agents/devops_qa.md` — DevOps/QA engineer brief: owns `build/`, `deployments/`, `Makefile`, `README.md`.
- `CLAUDE.md` — canonical layout (`build/*.Dockerfile`, `deployments/charts/` sub-chart per service + postgres + `values.yaml`), hard constraints (MQ single-replica, coverage ≥90%), stack pins (Bitnami chart 18.7.8, kind, distroless).

### Prior decisions that bind this phase
- `.planning/phases/02-storage-foundation-schema-connection-pool/02-CONTEXT.md` — D-06 (golang-migrate, one-shot migrate job anticipated for Phase 5), D-08/D-09 (living README + smoke suite cadence).
- `docs/adr/ADR-001-bidi-at-least-once-delivery.md` — MQ delivery semantics the deployed topology must preserve (single replica, redelivery on disconnect).

### Existing artifacts extended by this phase
- `Makefile` — `docker-%`, `kind-up`, `helm-install`, `kind-down`, `coverage`, `smoke-%` targets already stubbed; this phase fills them in and adds `kind-load`, `deploy`, `soak`, `test-harness`.
- `docker-compose.yml` — existing Postgres dev stack; test harness extends it to the full five-image stack.
- `scripts/smoke/` — phase smoke script convention (`phaseNN-*.sh`).

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `cmd/` has five binaries: `mq`, `streamer`, `collector`, `gateway`, `migrate` — the migrate binary (Phase 2) is the payload for D-10's dedicated image.
- `Makefile` already defines `SERVICES`, `docker-%` pattern rule (`build/$*.Dockerfile` → `vantage/$*:dev`), `helm upgrade --install vantage deployments -f deployments/values.yaml`, and the ≥90% coverage gate excluding `pkg/pb`/`pkg/docs`.
- `docker-compose.yml` + `make dev-up/dev-down` — the compose harness base.
- Existing smoke scripts in `scripts/smoke/` set the style for `phase05-*.sh`.

### Established Patterns
- Coverage gate: `go list ./internal/... ./pkg/...` minus generated packages, `-covermode=atomic -tags=integration`. New e2e code must not dilute it.
- Testcontainers on Rancher Desktop: Ryuk disabled, custom docker socket (project memory) — applies to the D-17 compose module.
- Conventional commits, no Co-Authored-By trailer; README grows every phase (D-08, Phase 2).

### Integration Points
- `build/` and `deployments/` directories do not exist yet — greenfield within a fixed layout.
- Config for services is env-driven (`internal/config`) — chart values must map to the env vars each service already reads.
- Gateway swagger (`make swagger` → `pkg/docs`) — gateway image build must not require regenerating docs at container build time unless wired deliberately.

</code_context>

<specifics>
## Specific Ideas

- User explicitly requested the test harness: "make test-harness should spin up all the containers
  and create a separate test suite to run tests against live infrastructure" — this is a
  first-class deliverable of the phase, not an optional extra.
- `imagePullPolicy: IfNotPresent` was a deliberate user override of the recommended `Never`.

</specifics>

<deferred>
## Deferred Ideas

- **Kill/restart resilience scenarios in the e2e harness** (restart collector mid-stream, restart
  MQ) — valuable, but deferred; Phase 6 (WAL crash durability, QA-05) is the natural home.
- **Local registry for kind** — revisit only if `kind load` becomes a bottleneck.

</deferred>

---

*Phase: 05-devops-quality-gates*
*Context gathered: 2026-07-02*
