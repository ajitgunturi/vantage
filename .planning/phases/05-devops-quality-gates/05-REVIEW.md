---
phase: 05-devops-quality-gates
reviewed: 2026-07-03T00:00:00Z
depth: standard
files_reviewed: 20
files_reviewed_list:
  - Makefile
  - build/collector.Dockerfile
  - build/gateway.Dockerfile
  - build/migrate.Dockerfile
  - build/mq.Dockerfile
  - build/streamer.Dockerfile
  - deployments/Chart.yaml
  - deployments/charts/collector/templates/deployment.yaml
  - deployments/charts/gateway/templates/deployment.yaml
  - deployments/charts/gateway/templates/service.yaml
  - deployments/charts/mq/templates/deployment.yaml
  - deployments/charts/mq/templates/service.yaml
  - deployments/charts/streamer/templates/configmap.yaml
  - deployments/charts/streamer/templates/deployment.yaml
  - deployments/templates/migrate-job.yaml
  - deployments/values.yaml
  - scripts/smoke/phase05-kind.sh
  - scripts/soak.sh
  - test/harness/harness_test.go
  - testdata/fixture.csv
findings:
  critical: 0
  warning: 7
  info: 6
  total: 13
status: issues_found
---

# Phase 5: Code Review Report (re-review after 05-05 gap closure)

**Reviewed:** 2026-07-03
**Depth:** standard
**Files Reviewed:** 20
**Status:** issues_found

## Summary

Re-review after plan 05-05 moved the Helm migrate Job from `pre-install` to
`post-install,post-upgrade` with `activeDeadlineSeconds: 120`, and added
`--timeout 3m` to `helm-install`. The hook-deadlock fix is sound: post-install
hooks run after regular manifests are applied, so the `wait-for-postgres` init
container can resolve the Postgres Service — no circular wait. The prior race
concern (services starting before schema exists) is mitigated in code: the
collector runs `db.Migrate` itself at startup (`cmd/collector/main.go:30`), so
the hook Job is a belt-and-braces shared-schema owner, and golang-migrate's
advisory locking makes the concurrent migration safe.

No Critical findings. Seven Warnings remain: a machine-specific `DOCKER_HOST`
hardcoded into the committed Makefile, a vacuous OPS-03 assertion in the smoke
script, three robustness gaps in the new hook Job and scripts, a latent
port-forward desync bug, and the still-unfixed `.dockerignore` finding carried
from the previous review.

## Warnings

### WR-01: Makefile hardcodes a user-specific DOCKER_HOST and force-overrides the environment

**File:** `Makefile:25-26`
**Issue:** `export DOCKER_HOST := unix:///Users/ajitg/.rd/docker.sock` embeds one
developer's absolute home path in a committed file. Because it is an
unconditional `:=` export, it also *overrides* any `DOCKER_HOST` the caller
already exported — directly contradicting the top-of-file comment
(`Makefile:5-14`) that instructs users to set these variables themselves. Every
`make` target breaks for any other machine, Docker Desktop user, or CI runner.
The `test-harness` target (`Makefile:143`) uses `$$HOME` for the same path,
proving the intent and highlighting the inconsistency.
**Fix:**
```make
export DOCKER_HOST ?= unix://$(HOME)/.rd/docker.sock
export TESTCONTAINERS_RYUK_DISABLED ?= true
```
`?=` respects the caller's environment; `$(HOME)` removes the hardcoded user.

### WR-02: OPS-03 smoke check is vacuous — the "targeted upgrade" is a no-op

**File:** `scripts/smoke/phase05-kind.sh:104-112`
**Issue:** Step 7 runs `helm upgrade --reuse-values --set mq.image.tag=dev`, but
the tag is *already* `dev` (deployments/values.yaml:9). The rendered manifests
are byte-identical, so **no** Deployment's generation changes — mq included.
The assertions that streamer/collector/gateway generations are unchanged pass
trivially, and `kubectl rollout status` on an already-rolled-out mq returns
immediately. The check proves "a no-op upgrade rolls nothing," not "an mq-only
change rolls only mq." The script never asserts mq's generation *incremented*,
which would have exposed this.
**Fix:** Force a real mq pod-template change and assert both directions:
```bash
GEN_MQ=$(gen mq)
helm upgrade --reuse-values --set mq.image.pullPolicy=Always "$RELEASE" deployments >/dev/null
[ "$(gen mq)" -gt "$GEN_MQ" ] || fail "mq did not roll — OPS-03 check is a no-op"
# ... assert others unchanged, then revert:
helm upgrade --reuse-values --set mq.image.pullPolicy=IfNotPresent "$RELEASE" deployments >/dev/null
```

### WR-03: migrate Job comment promises "do not restart" but backoffLimit defaults to 6

**File:** `deployments/templates/migrate-job.yaml:31` (spec block lines 23-30)
**Issue:** `restartPolicy: Never` only prevents in-place container restarts. The
Job's `backoffLimit` is unset, so it defaults to **6** — a failing migration
spawns up to 7 pods inside the 120s deadline, and the eventual error surfaced
to Helm is `DeadlineExceeded` rather than the first migration failure. This
contradicts the inline comment "fail loudly; do not restart on migration
error" and muddies diagnosis of a genuinely broken migration.
**Fix:** Add `backoffLimit: 1` to the Job spec (line 24, next to
`activeDeadlineSeconds`). Note: keep it ≥1 rather than 0, because the
`nc -z` init gate only proves Postgres accepts TCP — Postgres can accept
connections during startup/recovery before it is query-ready, so one retry
absorbs that window.

### WR-04: activeDeadlineSeconds:120 races first-install image pulls that kind-load never loads

**File:** `deployments/templates/migrate-job.yaml:24`, `Makefile:131-135`
**Issue:** `kind-load` loads only the five `vantage/*:dev` images. On a fresh
cluster, the kind node must pull `bitnami/postgresql` (Chart.yaml:22, ~150MB+)
and `busybox:1.36` (migrate-job.yaml:38) from Docker Hub during the deploy.
`activeDeadlineSeconds` starts counting when the hook Job is created and
includes pod scheduling, the busybox pull, and the entire init-container wait
for Postgres — whose own StatefulSet is still pulling its image. On a slow
network or under Docker Hub rate limiting, 120s is exceeded, the Job is
killed, and the Helm release is marked FAILED even though nothing is actually
broken. The live verification passed on a warm image cache; a cold cache is
the risk case.
**Fix:** Either add `busybox:1.36` and the pinned bitnami postgresql image to
the `kind-load` loop (pre-pull + `kind load docker-image`), or raise
`activeDeadlineSeconds` to comfortably cover a cold pull (e.g. 300, still
inside a raised `--timeout`), and document the cold-start expectation.

### WR-05: smoke port-forward hardcodes 8081 instead of using LOCAL_PORT

**File:** `scripts/smoke/phase05-kind.sh:65` (vs. declaration at line 24)
**Issue:** `kubectl port-forward "svc/${RELEASE}-gateway" 8081:8080` ignores the
`LOCAL_PORT` variable. All curl checks use `${LOCAL_PORT}`; if anyone changes
`LOCAL_PORT=8081` at line 24 (e.g. to dodge a busy port), the forward still
binds 8081 and every subsequent check probes the wrong port — failing with the
misleading message "pipeline not flowing."
**Fix:**
```bash
kubectl port-forward "svc/${RELEASE}-gateway" "${LOCAL_PORT}:8080" >/dev/null 2>&1 &
```

### WR-06: soak depth-bound assertion silently passes if the MQ inspect endpoint dies mid-run

**File:** `scripts/soak.sh:84-90`
**Issue:** In the polling loop, `DEPTH=$(inspect_field depth || echo 0)` maps
*any* failure — dead port-forward, MQ pod restart, JSON parse error — to
depth 0. If the `kubectl port-forward` (PID from line 61) drops at t=10s of a
600s soak, every remaining sample reads 0, the "depth stayed bounded"
assertion passes vacuously, and `MAX_DEPTH` is reported as if monitoring
worked. The failure only surfaces at line 95 (`PRODUCED=$(inspect_field ...)`)
as an opaque `set -e` exit with no `fail` message.
**Fix:** Distinguish fetch failure from a real 0 reading:
```bash
DEPTH=$(inspect_field depth) || { FAILS=$((FAILS+1));
  [ "$FAILS" -ge 3 ] && fail "MQ inspect unreachable ${FAILS}x — port-forward dead?"; continue; }
FAILS=0
```
Also wrap the final `PRODUCED`/`CONSUMED` reads (lines 95-96) with
`|| fail "..."` so a dead endpoint fails loudly there too.

### WR-07: No .dockerignore — repeat finding, still unfixed since the previous review

**Files:** `build/collector.Dockerfile:13`, `build/gateway.Dockerfile:13`, `build/migrate.Dockerfile:13`, `build/mq.Dockerfile:13`, `build/streamer.Dockerfile:13`
**Issue:** Carried forward verbatim from the previous review (WR-01) and
confirmed still absent. `COPY . .` in all five Dockerfiles ships the full repo
as build context: `.git/`, `bin/`, `coverage.out`, and the local gitignored
`dcgm_metrics_20250718_134233.csv` (present at repo root). Slow context
uploads, cache-busting on unrelated changes, and repo history baked into
intermediate layers.
**Fix:** Add `.dockerignore`:
```
.git
.planning
bin/
coverage.*
dcgm_metrics_*.csv
deployments/
docs/
*.md
```

## Info

### IN-01: `make test` lacks `-count=1` despite project convention

**File:** `Makefile:67`
**Issue:** `.claude/CLAUDE.md` states the test target "should always pass
`-race`; `-count=1` disables caching to catch flaky tests." `-race` is
present; `-count=1` is not (e2e and test-harness targets have it).
**Fix:** Add `-count=1` to the `test` target's `go test` invocation.

### IN-02: Coverage gate scope excludes cmd/ from the ≥90% floor

**File:** `Makefile:70-71`
**Issue:** The hard constraint reads "≥90% line coverage across the module,"
but the gate lists only `./internal/... ./pkg/...`. Excluding thin
composition-root `main.go` files is a defensible, comment-documented choice —
flagging so the divergence from the constraint's wording is a recorded
decision, not an accident.
**Fix:** None required if intentional; consider noting it in PROJECT.md Key
Decisions.

### IN-03: `.PHONY` on pattern rules has no effect

**File:** `Makefile:30-32`
**Issue:** `smoke-%` and `docker-%` in the `.PHONY` list are ignored — Make
does not support .PHONY for pattern rules. Harmless today (no files named
`docker-mq` etc.), but the declaration gives false confidence.
**Fix:** Remove them from `.PHONY` or add a comment; optionally add
`.FORCE`-style prerequisites if collision-proofing is wanted.

### IN-04: No readiness/liveness probes or resource requests on any vantage Deployment

**Files:** `deployments/charts/{mq,streamer,collector,gateway}/templates/deployment.yaml`
**Issue:** Verified via grep: zero probes and zero `resources:` blocks across
all four sub-charts. The gateway Service will route to a pod the moment it is
Running, before the HTTP listener is up; `kubectl wait --for=condition=available`
in the smoke script only proves Running, not serving. Acceptable for a kind
dev chart, but a gap for a "reference-quality" deployment.
**Fix:** Add a readiness probe on gateway (`GET /api/v1/gpus` or a health
endpoint, port 8080) and mq (TCP 50051), plus modest resource requests.

### IN-05: `make -j deploy` can interleave its prerequisites

**File:** `Makefile:137`
**Issue:** `deploy: docker kind-load helm-install` relies on left-to-right
serial execution. Under `make -j`, the three prerequisites have no declared
ordering, so `helm-install` can run before images are built/loaded.
**Fix:** Chain them: `kind-load: docker` won't fit the target's standalone use,
so use a recursive recipe: `deploy: ; $(MAKE) docker && $(MAKE) kind-load && $(MAKE) helm-install`.

### IN-06: Carried informational items from the previous review (still accurate)

**Files:** `scripts/soak.sh:54-56`, `test/harness/harness_test.go:95,110,128`, `deployments/templates/migrate-job.yaml:50`, `deployments/values.yaml:37`
**Issue:** (a) soak.sh hardcodes the Bitnami pod name `vantage-postgresql-0`
and the dev password inline — breaks under a different release name;
(b) harness uses `http.Get` with no client timeout — a hung gateway stalls to
the go-test timeout, and pollGPUs retries HTTP 5xx and connection-refused
identically; (c) migrate-job hardcodes `imagePullPolicy: IfNotPresent` while
the four services template it from values; (d) plaintext dev credentials in
values.yaml — documented LOCAL DEV ONLY, accepted threat T-05-04.
**Fix:** All optional for this phase; (b) is the cheapest win — a shared
`http.Client{Timeout: 10 * time.Second}`.

---

_Reviewed: 2026-07-03_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
