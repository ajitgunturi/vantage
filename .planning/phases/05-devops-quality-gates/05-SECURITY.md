---
phase: 05
slug: devops-quality-gates
status: verified
# threats_open = count of OPEN threats at or above workflow.security_block_on severity (the blocking gate)
threats_open: 0
asvs_level: 1
created: 2026-07-02
---

# Phase 05 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.
> Register authored at plan time across 05-01..05-05 PLAN `<threat_model>` blocks;
> mitigations verified against the implementation at ASVS L1 (grep-depth evidence).

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| local build host → OCI base images | golang:1.26-alpine + gcr.io/distroless pulled from public registries | build inputs (pinned tags) |
| Makefile targets → local Docker socket | make drives docker/kind via unix:///…/.rd/docker.sock | image builds, cluster lifecycle |
| Helm values → running pods | DB credentials flow from values.yaml into Deployment/Job env | dev-only DSN (vantage/vantage) |
| Bitnami OCI registry → local chart cache | postgresql chart pulled from oci://registry-1.docker.io/bitnamicharts | chart tgz (pinned 18.7.8, Chart.lock) |
| in-cluster service-to-service | gRPC/HTTP/Postgres over ClusterIP (no TLS in kind) | telemetry rows, dev credentials |
| Go test process → Docker daemon | harness drives docker-compose lifecycle via the Rancher socket | container lifecycle commands |
| local shell → kind cluster | smoke/soak drive kubectl (port-forward, scale, exec) | cluster admin operations |
| Helm release lifecycle → cluster | hook-annotated migrate Job orders privileged in-cluster resources | DDL against shared database |

---

## Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation | Status |
|-----------|----------|-----------|----------|-------------|------------|--------|
| T-05-01 | Tampering | final Docker images | medium | mitigate | All 5 Dockerfiles: `FROM gcr.io/distroless/static-debian12 AS final`, CGO_ENABLED=0 static binary (build/*.Dockerfile:19) | closed |
| T-05-02 | Information Disclosure | build layers | low | accept | No secrets baked at build time; DSN injected at runtime via VANTAGE_DB_DSN | closed |
| T-05-SC-a | Tampering | base image pulls | medium | mitigate | Pinned tags golang:1.26-alpine + distroless/static-debian12 in all 5 Dockerfiles; no third-party runtime packages | closed |
| T-05-03 | Elevation of Privilege | in-memory MQ broker | high | mitigate | `replicas: 1` + `strategy: Recreate` HARDCODED in mq template (deployments/charts/mq/templates/deployment.yaml:14-16); not values-overridable | closed |
| T-05-04 | Information Disclosure | DB password in values.yaml | medium | accept | Local dev / kind only; same precedent as docker-compose.yml; prod would use Secret + valueFrom.secretKeyRef (out of v1 scope) | closed |
| T-05-05 | Elevation of Privilege | migration Job DB privileges | low | accept | Full-privilege migrate acceptable for dev/local; prod would use migration-scoped user (out of v1 scope) | closed |
| T-05-SC-b | Tampering | Bitnami postgresql OCI chart | medium | mitigate | Pinned 18.7.8 in deployments/Chart.lock (digest-locked) | closed |
| T-05-06 | Tampering | testcontainers Ryuk reaper | low | accept | Ryuk disabled per project convention (Rancher Desktop); explicit t.Cleanup teardown replaces it (D-18) | closed |
| T-05-07 | Denial of Service | orphaned compose stack | low | mitigate | Hermetic up→test→down: t.Cleanup + RemoveOrphans(true) + RemoveVolumes(true) (test/harness); KEEP=1 explicit debug opt-in | closed |
| T-05-SC-c | Tampering | testcontainers-go/modules/compose | medium | mitigate | Pinned v0.43.0 in go.mod, go.sum digest-locked; official sub-module (RESEARCH legitimacy audit: OK) | closed |
| T-05-08 | Information Disclosure | psql invocation in soak | low | accept | Dev-only DSN (vantage/vantage) against local kind; credentials not logged in assertion output | closed |
| T-05-09 | Denial of Service | leaked port-forward / scaled streamers | low | mitigate | `trap cleanup EXIT` kills port-forwards and scales streamer back to 1 (scripts/soak.sh:36-42); re-runnable | closed |
| T-05-SC-d | Tampering | shell scripts invoking cluster tooling | low | accept | Only pinned local tooling (kubectl/helm/curl); no package-manager installs | closed |
| T-05-05-01 | Denial of Service | migrate hook Job (self-inflicted deadlock) | high | mitigate | Root fix: `helm.sh/hook: post-install,post-upgrade` (migrate-job.yaml:26) — Postgres exists before wait-for-postgres; defense-in-depth `activeDeadlineSeconds: 300` (raised from planned 120 by WR-04 for cold pulls) | closed |
| T-05-05-02 | Denial of Service | helm-install silent hang | medium | mitigate | Explicit `--timeout 6m` on helm upgrade (Makefile:142; raised from planned 3m by WR-04) converts hangs into bounded, reported errors | closed |
| T-05-05-03 | Tampering | migration DDL re-run on post-upgrade | low | accept | golang-migrate is version-tracked/idempotent; hook re-run is a no-op against migrated schema | closed |
| T-05-05-SC | Tampering | npm/pip/cargo installs | low | accept | No package-manager installs in gap-closure plan; Bitnami chart dep unchanged | closed |

*Status: open · closed · open — below high threshold (non-blocking)*
*Severity: critical > high > medium > low — only open threats at or above workflow.security_block_on count toward threats_open*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

*Note: each plan's supply-chain threat was authored as `T-05-SC`; suffixes -a..-d disambiguate them here (plans 05-01..05-04 respectively).*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-05-01 | T-05-02 | No secrets in build layers; runtime env injection is the designed credential path | plan 05-01 threat model (user-approved plan) | 2026-07-02 |
| AR-05-02 | T-05-04 | Plaintext dev DSN in values.yaml is local-kind-only; production Secret pattern documented as out of v1 scope | plan 05-02 threat model (user-approved plan) | 2026-07-02 |
| AR-05-03 | T-05-05 | Full-privilege migrate Job acceptable for local dev; scoped migration user deferred to production hardening | plan 05-02 threat model (user-approved plan) | 2026-07-02 |
| AR-05-04 | T-05-06 | Ryuk reaper disabled (Rancher Desktop constraint); deterministic t.Cleanup teardown covers reaping | plan 05-03 threat model (user-approved plan) | 2026-07-02 |
| AR-05-05 | T-05-08 | Dev-only credentials in soak psql; never leaves local cluster | plan 05-04 threat model (user-approved plan) | 2026-07-02 |
| AR-05-06 | T-05-SC-d | Scripts restricted to pinned local tooling; no new supply-chain surface | plan 05-04 threat model (user-approved plan) | 2026-07-02 |
| AR-05-07 | T-05-05-03 | Idempotent migrations make hook re-runs safe by design | plan 05-05 threat model (user-approved plan) | 2026-07-02 |
| AR-05-08 | T-05-05-SC | Gap-closure plan introduced zero new dependencies | plan 05-05 threat model (user-approved plan) | 2026-07-02 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-02 | 17 | 17 | 0 | gsd-secure-phase (L1 short-circuit — plan-time register, all mitigations evidence-verified) |

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-02
