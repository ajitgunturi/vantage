---
phase: 05-devops-quality-gates
plan: 01
subsystem: infra
tags: [docker, distroless, multi-stage, makefile, kind, helm, rancher-desktop]

requires:
  - phase: 03-pipeline-streamer-collector-integration
    provides: "cmd/streamer, cmd/collector binaries (pipeline services to containerize)"
  - phase: 04-api-gateway-openapi-docs
    provides: "cmd/gateway binary + pkg/docs compiled-in OpenAPI"
  - phase: 02-storage-foundation-schema-connection-pool
    provides: "cmd/migrate one-shot migration binary (payload for migrate image)"
provides:
  - "Five multi-stage Dockerfiles: build/{mq,streamer,collector,gateway,migrate}.Dockerfile → vantage/<svc>:dev"
  - "Makefile exports: PATH (~/go/bin), DOCKER_HOST (Rancher socket), TESTCONTAINERS_RYUK_DISABLED"
  - "Makefile targets: kind-load, deploy (docker→kind-load→helm-install), dependency-update, soak, test-harness"
  - "DOCKER_IMAGES := $(SERVICES) migrate — 5-image build set; make build unchanged (4 binaries)"
affects: [05-02 helm charts, 05-03 test harness, 05-04 smoke and soak]

tech-stack:
  added: ["golang:1.26-alpine (builder stage)", "gcr.io/distroless/static-debian12 (final stage)"]
  patterns: ["two-stage Dockerfile with go.mod/go.sum layer caching + CGO_ENABLED=0 static build", "DOCKER_IMAGES superset of SERVICES keeps binary gate separate from image gate"]

key-files:
  created:
    - build/mq.Dockerfile
    - build/streamer.Dockerfile
    - build/collector.Dockerfile
    - build/gateway.Dockerfile
    - build/migrate.Dockerfile
  modified:
    - Makefile

key-decisions:
  - "docker aggregate target now builds DOCKER_IMAGES (5 images incl. migrate) so make deploy loads a complete set into kind"
  - "Exports placed at top of Makefile so every target inherits kind PATH + Rancher DOCKER_HOST without per-target prefixes"

patterns-established:
  - "Dockerfile naming build/<svc>.Dockerfile matches the existing docker-% pattern rule exactly"
  - "Client-only services (streamer, collector, migrate) carry no EXPOSE line"

requirements-completed: [OPS-01, OPS-05]

coverage:
  - id: D1
    description: "Five multi-stage distroless Dockerfiles build vantage/<svc>:dev for mq, streamer, collector, gateway, migrate"
    requirement: OPS-01
    verification:
      - kind: other
        ref: "make docker && docker images vantage/*:dev — all five images listed"
        status: pass
    human_judgment: false
  - id: D2
    description: "Makefile exposes proto/build/test/coverage/swagger plus new kind-load/deploy/dependency-update/soak/test-harness targets, all parsing via make -n"
    requirement: OPS-05
    verification:
      - kind: other
        ref: "make -n deploy soak test-harness kind-load dependency-update — all parse; make -n build shows 4-service loop unchanged"
        status: pass
    human_judgment: false

duration: 8min
completed: 2026-07-02
status: complete
---

# Phase 5 Plan 01: Dockerfiles + Makefile Plumbing Summary

**Five distroless multi-stage Docker images (mq, streamer, collector, gateway, migrate) plus Makefile deploy plumbing — kind-load, deploy, dependency-update, soak, test-harness targets with Rancher Desktop env exports**

## Performance

- **Duration:** 8 min
- **Started:** 2026-07-02T17:51:12Z
- **Completed:** 2026-07-02T17:59:00Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments
- All five vantage/*:dev images build via the existing docker-% pattern rule (verified: `make docker` exit 0, all five listed by `docker images`)
- Makefile now exports PATH (~/go/bin for kind), DOCKER_HOST (Rancher socket), and TESTCONTAINERS_RYUK_DISABLED for every target
- `make deploy` chains docker → kind-load → helm-install (D-07); helm-install gained a dependency-update prerequisite so the Bitnami OCI chart is pulled before install
- `make build` untouched — still builds only the four service binaries; migrate is image-only via DOCKER_IMAGES

## Task Commits

1. **Task 1: Five multi-stage Dockerfiles** - `e59c5d7` (feat)
2. **Task 2: Makefile exports + targets** - `eb5156e` (feat)

## Files Created/Modified
- `build/mq.Dockerfile` - EXPOSE 50051 8080 (gRPC + HTTP inspect)
- `build/streamer.Dockerfile` - client-only, no EXPOSE
- `build/collector.Dockerfile` - client-only, no EXPOSE
- `build/gateway.Dockerfile` - EXPOSE 8080; pkg/docs compiled in via Go import (no extra COPY)
- `build/migrate.Dockerfile` - one-shot job wrapping cmd/migrate
- `Makefile` - exports, DOCKER_IMAGES, kind-load/deploy/dependency-update/soak/test-harness, helm-install prereq

## Decisions Made
- `docker` aggregate target retargeted from $(SERVICES) to $(DOCKER_IMAGES) so the deploy chain builds/loads all five images — required by the plan's own must_have ("make docker builds five images") and 05-03's compose stack
- Deploy comment uses ASCII arrows (fixed a UTF-8 mojibake introduced during editing)

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Images + Make targets ready for 05-02 (Helm charts consume vantage/*:dev + dependency-update) and 05-03 (compose harness references the same images)
- kind cluster not yet created — smoke/deploy targets await 05-02 chart tree

---
*Phase: 05-devops-quality-gates*
*Completed: 2026-07-02*
