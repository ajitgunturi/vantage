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

> **Important:** the vantage pods expose metrics via `prometheus.io/*` **pod annotations**
> only — no ServiceMonitor/PodMonitor CRDs are shipped (that is future work). Out of the box,
> kube-prometheus-stack discovers targets exclusively through those CRDs and **ignores
> annotations**, so it must be installed with an `additionalScrapeConfigs` entry that implements
> annotation-based discovery (step 1 below). Without it, no vantage metrics are scraped and the
> HPA metric never materializes.

First, write the annotation-scrape values file:

```yaml
# scrape-values.yaml — annotation-based pod discovery for kube-prometheus-stack
prometheus:
  prometheusSpec:
    additionalScrapeConfigs:
      - job_name: vantage-annotated-pods
        kubernetes_sd_configs:
          - role: pod
        relabel_configs:
          # Keep only pods that opt in via prometheus.io/scrape: "true"
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
            action: keep
            regex: "true"
          # Honor a custom metrics path (prometheus.io/path), default /metrics
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
            action: replace
            target_label: __metrics_path__
            regex: (.+)
          # Honor the advertised port (prometheus.io/port)
          - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
            action: replace
            regex: ([^:]+)(?::\d+)?;(\d+)
            replacement: $1:$2
            target_label: __address__
          - source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
          - source_labels: [__meta_kubernetes_pod_name]
            target_label: pod
```

```sh
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts

# 1. Prometheus — MUST include the annotation-scrape config above
helm install monitoring prometheus-community/kube-prometheus-stack \
  -n monitoring --create-namespace \
  -f scrape-values.yaml \
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
