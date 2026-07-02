# Phase 5: DevOps + Quality Gates - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-02
**Phase:** 05-devops-quality-gates
**Areas discussed:** Helm topology & independence, Image build & kind workflow, Schema migration in-cluster, Smoke scope for phase 05, Live-infrastructure test harness

---

## Helm topology & independence

| Option | Description | Selected |
|--------|-------------|----------|
| Bitnami chart via OCI | Chart.yaml dependency on bitnami/postgresql 18.7.8 from oci://registry-1.docker.io/bitnamicharts | ✓ |
| Minimal in-repo postgres sub-chart | Own StatefulSet + Service + Secret; zero external dep | |
| You decide | Claude picks during planning | |

| Option | Description | Selected |
|--------|-------------|----------|
| One release + per-service flags | Single umbrella release; per-sub-chart `enabled` + `image.tag`; independence via targeted `--set <svc>.image.tag` upgrades | ✓ |
| Separate Helm release per service | Four releases (+postgres) installed individually | |
| Umbrella + installable sub-charts | Umbrella default, each sub-chart also standalone-installable | |

| Option | Description | Selected |
|--------|-------------|----------|
| Self-contained sub-charts | Each sub-chart owns its templates; duplication accepted for independence | ✓ |
| Shared library chart | Common library chart provides templates | |

**User's choice:** Bitnami via OCI; one umbrella release with per-service flags; self-contained sub-charts (all recommended options).

---

## Image build & kind workflow

| Option | Description | Selected |
|--------|-------------|----------|
| kind load docker-image | Built-in loader, zero extra infra | ✓ |
| Local registry container | registry:2 wired into kind | |

| Option | Description | Selected |
|--------|-------------|----------|
| :dev + pullPolicy Never | Fails loud if kind-load forgotten (recommended) | |
| :dev + pullPolicy IfNotPresent | More forgiving; missing image → ImagePullBackOff | ✓ |
| Content-addressed tags (git SHA) | Best rollout hygiene, more Make plumbing | |

| Option | Description | Selected |
|--------|-------------|----------|
| Yes: make deploy | docker → kind-load → helm-install composite | ✓ |
| Keep steps separate | README documents sequence only | |

| Option | Description | Selected |
|--------|-------------|----------|
| distroless/static + golang builder | golang:1.26-alpine builder, distroless-static final | ✓ |
| alpine final stage | Shell available, larger attack surface | |

**User's choice:** kind load; `:dev` + `IfNotPresent` (deliberate override of the recommended `Never`); `make deploy` composite; distroless.

---

## Schema migration in-cluster

| Option | Description | Selected |
|--------|-------------|----------|
| Helm pre-install/upgrade hook Job | Migrations run to completion before pods roll, once per release op | ✓ |
| Init container on collector | Re-runs per pod restart; couples schema to collector lifecycle | |
| Manual make target only | helm install alone wouldn't produce a working system | |

| Option | Description | Selected |
|--------|-------------|----------|
| Own image + Dockerfile | build/migrate.Dockerfile → vantage/migrate:dev | ✓ |
| Bundled into collector image | Two binaries in one image | |

| Option | Description | Selected |
|--------|-------------|----------|
| Umbrella chart owns the Job | Schema is a shared concern (collector writes, gateway reads) | ✓ |
| Collector sub-chart owns it | Writer-owns-schema; gateway-only deploys get no schema | |

**User's choice:** All recommended options.

---

## Smoke scope for phase 05

| Option | Description | Selected |
|--------|-------------|----------|
| Full E2E through kind | helm install → pods Running → port-forward gateway → curl returns real rows | ✓ |
| Deploy health only | Pods Ready + inspect reachable | |
| Two scripts: health + E2E | Separately runnable tiers | |

| Option | Description | Selected |
|--------|-------------|----------|
| Assume cluster + deploy exist | Fail fast if `make kind-up deploy` not run; consistent with phases 1–4 | ✓ |
| Self-orchestrating | Script does cluster+build+deploy+teardown itself | |

| Option | Description | Selected |
|--------|-------------|----------|
| Light soak in smoke | ~60s bounded soak inside smoke-05 | |
| Full soak deferred | Push to Phase 6+ | |
| Dedicated soak target | Separate `make soak`, configurable duration/replicas | ✓ |

**User's choice:** Full E2E smoke assuming existing deploy; dedicated `make soak` target.

---

## Live-infrastructure test harness (user-added area)

**Origin:** At the final check, the user requested via free text: "I want to build a test harness —
make test-harness should spin up all the containers and create a separate test suite to run tests
against live infrastructure." Folded in as a fifth area (fits the phase's quality-bar goal).

| Option | Description | Selected |
|--------|-------------|----------|
| docker-compose full stack | All five images + Postgres on localhost ports | |
| kind + Helm deployment | Tests the deployment artifact, slower | |
| Both, compose first | Compose default; suite pointable at kind via env base URLs | ✓ |

| Option | Description | Selected |
|--------|-------------|----------|
| Go tests, e2e build tag | Suite assumes stack provided externally | |
| Testcontainers-driven suite | Go tests own the compose stack lifecycle programmatically | ✓ |
| Shell-based assertions | Smoke-script style | |

| Option | Description | Selected |
|--------|-------------|----------|
| Up → test → down | Hermetic; teardown even on failure; KEEP=1 escape hatch | ✓ |
| Assume stack running | Operator brings stack up first | |

| Option | Description | Selected |
|--------|-------------|----------|
| Pipeline correctness E2E | Rows grow, gateway consistent with DB, MQ counters reconcile | ✓ |
| Correctness + resilience | Adds kill/restart scenarios | |
| You decide | Claude scopes during planning | |

**User's choice:** Both-compose-first; testcontainers-driven; hermetic lifecycle; correctness-only assertions.

---

## Claude's Discretion

- Coverage-gate handling of new e2e packages; whether `cmd/` stays outside the gate.
- Resource requests/limits, probes, Service types/ports in charts.
- Helm chart linting/testing tooling.
- Soak defaults (duration, replicas) and env override names.
- Port-forward vs NodePort mechanics inside smoke-05.

## Deferred Ideas

- Kill/restart resilience scenarios in the e2e harness → Phase 6 (WAL/crash durability, QA-05).
- Local registry for kind → only if `kind load` becomes a bottleneck.
