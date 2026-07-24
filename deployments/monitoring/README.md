# Monitoring & queue-depth autoscaling

All four services expose Prometheus metrics (`/metrics`; pods carry
`prometheus.io/*` scrape annotations). This directory wires those metrics into
the HPA API so the Collector scales on the broker's backlog instead of CPU.

## Why not CPU

A Collector bottlenecked on Postgres round-trips idles its CPU while the
broker's backlog grows — CPU-based autoscaling would never fire. The honest
scale signal for a consumer is the work still owed to it:
`mq_queue_depth + mq_retry_depth`, exported by the MQ and surfaced to the HPA
as the external metric `mq_queue_backlog`.

## Setup (kind / any cluster)

```sh
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts

# 1. Prometheus (scrapes the annotated pods)
helm install monitoring prometheus-community/kube-prometheus-stack \
  -n monitoring --create-namespace \
  --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false

# 2. Adapter (turns mq_queue_depth/mq_retry_depth into the HPA-visible
#    external metric mq_queue_backlog)
helm install prometheus-adapter prometheus-community/prometheus-adapter \
  -n monitoring -f deployments/monitoring/prometheus-adapter-values.yaml

# 3. Enable the Collector HPA
helm upgrade vantage deployments \
  --set collector.autoscaling.enabled=true

# Verify the metric is visible to the HPA API:
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/default/mq_queue_backlog" | python3 -m json.tool
```

Tuning: `collector.autoscaling.targetBacklogPerReplica` (default 2000) is the
backlog each replica is expected to absorb; scale-down is stabilized at 120s
so a brief drain doesn't thrash replicas. The Gateway keeps its CPU-based HPA
(request-bound service — CPU is the right signal there).
