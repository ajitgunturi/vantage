---
phase: 06-production-hardening-assignment-alignment
plan: "06"
subsystem: helm-k8s
tags: [helm, kubernetes, probes, resources, hpa, scaling, ops]
requirements: [OPS-07, OPS-08, OPS-09]
status: complete

dependency_graph:
  requires: ["06-02", "06-03", "06-04", "06-05"]
  provides: [k8s-probes, k8s-resources, gateway-hpa]
  affects: [deployments/values.yaml, deployments/charts/*/templates/deployment.yaml, deployments/charts/gateway/templates/hpa.yaml]

tech_stack:
  added: []
  patterns:
    - values-configurable probes via {{- with .Values.livenessProbe }} guard
    - {{- toYaml .Values.resources | nindent 12 }} resources block pattern
    - values-gated HPA via {{- if .Values.autoscaling.enabled }}

key_files:
  created:
    - deployments/charts/gateway/templates/hpa.yaml
  modified:
    - deployments/values.yaml
    - deployments/charts/mq/templates/deployment.yaml
    - deployments/charts/gateway/templates/deployment.yaml
    - deployments/charts/streamer/templates/deployment.yaml
    - deployments/charts/collector/templates/deployment.yaml

decisions:
  - probe timings: initialDelaySeconds 30 for mq/streamer/collector; 60 for gateway (absorbs post-install migrate Job + postgres startup)
  - failureThreshold 6 at periodSeconds 10 gives 60s grace on kind slow starts
  - MQ replicas:1 + strategy:Recreate remain hardcoded literals (not values-templated) per ADR-001
  - autoscaling/v2 HPA (not v1) for gateway; disabled by default; no HPA for any other service

metrics:
  duration: 211s
  completed: "2026-07-03T05:08:42Z"
  tasks_completed: 3
  files_changed: 6
---

# Phase 06 Plan 06: Helm Probes + Resources + Gateway HPA Summary

**One-liner:** Kubernetes-native liveness/readiness probes, resource requests/limits, and a values-gated autoscaling/v2 HPA wired into all four Helm sub-charts, pointing probes at the OBS-01 health endpoints.

## What Was Built

### Task 1: Per-service values — resources, probes, gateway autoscaling (8072989)

Added to `deployments/values.yaml` under each service key:

- **mq:** cpu 100m/500m, mem 64Mi/256Mi; liveness+readiness on `port: http` (`:8080`), initialDelaySeconds 30
- **streamer:** cpu 50m/100m, mem 16Mi/64Mi; liveness+readiness on `port: health` (`:9000`), initialDelaySeconds 30
- **collector:** cpu 50m/100m, mem 16Mi/64Mi; liveness+readiness on `port: health` (`:9001`), initialDelaySeconds 30
- **gateway:** cpu 100m/200m, mem 32Mi/128Mi; liveness+readiness on `port: http` (`:8080`), initialDelaySeconds 60
- **gateway only:** autoscaling block with `enabled: false`, minReplicas 1, maxReplicas 3, targetCPUUtilizationPercentage 80

### Task 2: Probe + resources wiring in all four deployment templates (a5cef39)

Updated all four container specs after the `env:` block:
- `resources: {{- toYaml .Values.resources | nindent 12 }}`
- `{{- with .Values.livenessProbe }}` / `{{- with .Values.readinessProbe }}` guards rendering httpGet path/port, timing values from values
- **streamer:** added `ports: [{name: health, containerPort: 9000}]` and `STREAMER_HEALTH_ADDR: ":9000"` env
- **collector:** added `ports: [{name: health, containerPort: 9001}]` and `COLLECTOR_HEALTH_ADDR: ":9001"` env
- **MQ:** `replicas: 1` and `strategy: Recreate` remain hardcoded literals — invariant untouched

### Task 3: Gateway HPA template (values-gated) (952e379)

Created `deployments/charts/gateway/templates/hpa.yaml`:
- Wrapped in `{{- if .Values.autoscaling.enabled }}` — emits nothing by default
- `apiVersion: autoscaling/v2` (not v1)
- `scaleTargetRef`: apps/v1 Deployment `{{ .Release.Name }}-gateway`
- Metrics: Resource/cpu Utilization targeting `targetCPUUtilizationPercentage`
- No HPA added to mq, streamer, or collector sub-charts

## Verification Results

| Check | Result |
|-------|--------|
| `helm template vantage deployments` renders clean | PASS |
| `helm lint deployments` | PASS (0 failures, 1 INFO: icon recommended) |
| `grep -c 'requests:' values.yaml` == 4 | PASS (4) |
| All 4 services have livenessProbe + readinessProbe in rendered output | PASS |
| Streamer renders `name: health` containerPort 9000 + STREAMER_HEALTH_ADDR | PASS |
| Collector renders `name: health` containerPort 9001 + COLLECTOR_HEALTH_ADDR | PASS |
| MQ `strategy:` count == 1 (hardcoded) | PASS |
| MQ `replicas: 1` hardcoded (not .Values) | PASS |
| Default render has 0 HorizontalPodAutoscaler | PASS |
| `--set gateway.autoscaling.enabled=true` renders exactly 1 HPA | PASS |
| HPA apiVersion: autoscaling/v2 | PASS |
| HPA scaleTargetRef name: vantage-gateway | PASS |

Note: `helm template | grep -c 'livenessProbe:'` returns 5 (not 4) because the Bitnami postgresql sub-chart includes its own livenessProbe. All 4 vantage service probes are confirmed present in the rendered output — the extra count is a preexisting chart artifact.

## Deviations from Plan

None — plan executed exactly as written.

## Threat Surface Scan

No new network endpoints, auth paths, or trust boundaries introduced. All changes are Helm YAML templating only.

## Known Stubs

None.

## Self-Check: PASSED

- [x] `deployments/values.yaml` exists with 4 `requests:` blocks
- [x] `deployments/charts/gateway/templates/hpa.yaml` created
- [x] All 4 deployment templates modified with resources + probes
- [x] Commits 8072989, a5cef39, 952e379 verified in git log
