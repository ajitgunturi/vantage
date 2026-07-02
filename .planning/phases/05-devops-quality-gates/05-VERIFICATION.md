---
phase: 05-devops-quality-gates
status: human_needed
verified: 2026-07-02
score: 15/17 must-haves verified (2 require live kind cluster — human)
requirements:
  OPS-01: verified
  OPS-02: verified (chart-level; live install = human item 1)
  OPS-03: verified (template-level; live rollout = human item 1)
  OPS-04: verified
  OPS-05: verified
  QA-01: verified
  QA-04: verified
human_verification:
  - item: "kind E2E: make kind-up && make deploy && make smoke-05 — all pods Running, migration hook completed, gateway serves real rows, targeted mq upgrade rolls only MQ"
  - item: "Endurance: make soak (optionally SOAK_STREAMERS=10) — rows grow, produced>=acked, bounded depth"
---

# Phase 5 Verification — DevOps + Quality Gates

**Goal:** Every microservice builds and deploys independently to a single Kubernetes cluster via Helm, and the repository enforces its quality bar end-to-end.

## Goal-Backward Check

### Success Criterion 1 — Dockerfiles + Helm sub-charts + PostgreSQL dep; independent build/deploy
| Truth | Evidence | Status |
|---|---|---|
| Five multi-stage Dockerfiles build vantage/*:dev | `make docker` exit 0; 5 images in `docker images` | VERIFIED |
| Every final image distroless, static CGO_ENABLED=0 binary | All Dockerfiles: 2 FROM stages, distroless final | VERIFIED |
| Umbrella + 4 self-contained sub-charts + Bitnami OCI dep | `helm dependency update` pulled 18.7.8; Chart.lock; `helm lint` 0-failed | VERIFIED |
| Independent deploy via enabled/image.tag | `--set streamer.enabled=false` omits streamer objects; per-svc image.tag templated | VERIFIED |
| `helm install` brings all pods Running | requires live kind cluster | HUMAN (item 1) |

### Success Criterion 2 — MQ single replica + Recreate
| Truth | Evidence | Status |
|---|---|---|
| replicas: 1 + strategy: Recreate hardcoded literals | grep in mq template; rendered output; values.yaml clean | VERIFIED |

### Success Criterion 3 — Makefile targets + tests + coverage gate
| Truth | Evidence | Status |
|---|---|---|
| proto/build/test/coverage/swagger targets | all present and used through the phase | VERIFIED |
| Unit tests span all services (QA-01) | `make test` green across internal/ + pkg/ with -race | VERIFIED |
| ≥90% coverage gate (QA-04) | `make coverage` → 90.3% (min 90), harness excluded | VERIFIED |

### Additional phase deliverables
| Truth | Evidence | Status |
|---|---|---|
| test-harness proves E2E vs live containers, hermetic | TestHarness green 16.3s; no residual containers; e2e tag isolation | VERIFIED |
| smoke-05 + soak scripts correct + discoverable | bash -n; make smoke-% rule matches; all AC greps pass | VERIFIED (live run = human item 1/2) |
| README Phase-5 workflow (DOC-01) | Phase-5 section + status table updated, prior sections intact | VERIFIED |
| Makefile env exports (kind PATH, Rancher socket, Ryuk) | exports present; used by every gate run this phase | VERIFIED |

## Cross-Cutting
- Regression gate: full `make test` green after all waves — no prior-phase regressions
- `make lint` clean; code review: 0 critical, 1 warning (WR-01 .dockerignore, advisory)
- CONTEXT.md decisions D-01..D-19 all implemented (spot-checked during execution); deferred ideas (kill/restart scenarios, local registry) correctly absent

## Verdict

All automatable must-haves pass. Phase completion gates on the two human items (live kind deploy + soak), by design (D-13: smoke assumes an operator-created cluster).
