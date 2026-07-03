# ADR-008: Chart-Enforced Single Replica and Post-Install Migration Hook

**Status:** Accepted (owner-approved deviation from the original plan item D-09, which placed DB migrations as a pre-install Helm hook)
**Date:** 2026-07-02
**Phase:** 5 — DevOps + Quality Gates
**Backfilled:** 2026-07-03
**Related:** [ADR-001](ADR-001-bidi-at-least-once-delivery.md) (single-replica broker invariant this chart decision enforces)

## Context

Phase 5 (plan 05-05) wired the four services and Postgres into a Helm release
with one sub-chart per service. Two independent chart design problems were
discovered during integration:

**Problem A — MQ replica count:** The MQ is an in-memory, single-replica broker
(ADR-001). If `replicas` is an overridable Helm value (the Helm convention), any
`helm upgrade --set mq.replicas=2` call silently creates a second MQ instance.
The second instance does not share in-memory state with the first. Two active MQ
replicas partition the message stream rather than replicating it — a split-brain
condition where each instance independently processes a different subset of messages
so neither has the complete picture. This violates the uniqueness guarantee in
ADR-001 and breaks the Collector's dedup logic (ADR-007). The brief explicitly
prohibits MQ clustering.

**Problem B — Migration hook deadlock (deviation from plan item D-09):** The Phase 5
plan originally placed the DB migration Kubernetes Job as a `pre-install,pre-upgrade`
Helm hook (plan item D-09, which was locked — meaning it had been approved and was
not expected to change). A `pre-install` hook runs **before** Helm installs the
release's own manifests. When the migration Job's init container waits for Postgres
to be ready, and Postgres itself is deployed as part of the same release, a deadlock
forms: the hook waits for Postgres, and Helm withholds Postgres until the hook
completes.

Discovery: the `helm install` timed out with the init container stuck in
`PodInitializing`. Moving the hook to `post-install,post-upgrade` resolves the
deadlock — Helm installs Postgres first, then runs the hook after the manifests
are live. Services tolerate the brief pre-migration window because they retry on
startup.

Both changes are documented as deviations from locked decisions in PROJECT.md Key
Decisions (rows: migrate hook, replicas:1). Owner-approved per STATE.md Key
Decisions (Phase 5).

Sources: plan 05-05, PROJECT.md Key Decisions (migrate hook row, replicas:1 row),
STATE.md Key Decisions (Phase 5), STATE.md ## Decisions (replicas+Recreate row,
bounded-loud-failure convention WR-04).

## Decision

Two linked chart-layer decisions:

**A. MQ `replicas: 1` and `strategy: Recreate` hardcoded in the chart template.**
The MQ sub-chart `Deployment` manifest sets `spec.replicas: 1` as a literal
integer, not a `{{ .Values.mq.replicas }}` template expression. The update
strategy is `Recreate` (not `RollingUpdate`) so that a chart upgrade terminates
the existing pod before starting the replacement — preventing a transient
two-replica window during upgrades. These values are **not exposed in
`values.yaml`** and cannot be overridden. This enforces the ADR-001 single-replica
broker invariant at the chart layer without requiring consumers to remember the
constraint.

**B. DB migration Job hook moved to `post-install,post-upgrade`.**
The `helm.sh/hook` annotation is changed from `pre-install,pre-upgrade` to
`post-install,post-upgrade`. Helm installs all chart manifests (including
Postgres) first, then runs the Job. The Job's `initContainer` waits for Postgres
to be ready, which is now guaranteed to be scheduled. `activeDeadlineSeconds: 300`
on the Job spec + `helm install --timeout 6m` (a project operational convention,
catalogued as WR-04, requiring all deployments to carry a hard timeout) converts
silent hangs into named, bounded errors — a cold image pull that takes longer than
5 minutes surfaces as a Job `DeadlineExceeded` event rather than an indefinite
`helm install` block.

## Consequences

- **Positive:** An operator cannot accidentally break the MQ invariant via a
  values override or a `kubectl scale` on the Deployment — the chart template
  itself encodes the constraint.
- **Positive:** `Recreate` strategy ensures zero overlap between old and new MQ
  pods during upgrades — no split-brain window, even briefly.
- **Positive:** The migration deadlock is eliminated; `helm install` on a clean
  cluster completes without manual intervention.
- **Positive:** The hard-timeout convention (WR-04) means problems surface as named
  Kubernetes events within 5–6 minutes rather than silently blocking the
  operator's terminal.
- **Negative:** Making `replicas` non-overridable is an opinionated Helm
  convention — experienced Helm users may expect to set this via values. The
  constraint must be documented in the chart's `README` and is justified here.
- **Negative:** `Recreate` strategy introduces a brief MQ downtime window during
  upgrades (old pod terminated before new pod is ready). For a single-replica
  in-memory broker with no persistence, this window causes in-flight messages
  to be lost. Accepted: the WAL backend (ADR-009) is the planned remedy.
- **Negative:** Moving to `post-install` means services may start and attempt MQ
  connections before the migration completes. Services must tolerate startup retry
  — which they do via exponential backoff on the gRPC dial.

## Alternatives Considered

| Alternative | Trade-off |
|---|---|
| `replicas` as a values knob (Helm convention) | Conventional but dangerous: any `--set mq.replicas=2` splits the in-memory broker's state across two uncoordinated instances. Violates ADR-001. |
| Keep migration as `pre-install` hook (plan item D-09) | Original intent — migration before services start. Deadlocks against a fresh cluster where Postgres is in the same release. Requires a separate Postgres pre-installed release, which defeats the single-release convenience goal. |
| No Job timeout / no `--timeout` on helm install | Silently hangs on cold image pulls or slow cluster scheduling. Named bounded errors are better than indefinite blocks in CI and manual deploy workflows. |
| `RollingUpdate` strategy for MQ | Would briefly run two MQ replicas during the rollover — exactly the split-brain condition being prevented. |
