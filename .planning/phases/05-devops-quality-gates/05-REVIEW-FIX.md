---
phase: 05-devops-quality-gates
fixed_at: 2026-07-02T19:31:38Z
review_path: .planning/phases/05-devops-quality-gates/05-REVIEW.md
iteration: 1
findings_in_scope: 7
fixed: 7
skipped: 0
status: all_fixed
---

# Phase 5: Code Review Fix Report

**Fixed at:** 2026-07-02T19:31:38Z
**Source review:** .planning/phases/05-devops-quality-gates/05-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 7 (fix_scope: critical_warning — all 7 Warnings; 6 Info findings out of scope)
- Fixed: 7
- Skipped: 0

**Verification run after all fixes:** `make build` clean; full `make test` green
through the new `.env`-driven Docker path (including the testcontainers-backed
`pkg/db` integration tests — proving WR-01's env plumbing works end-to-end);
`helm lint deployments` and `helm template vantage deployments -f
deployments/values.yaml` clean after every chart edit; `bash -n` clean on both
scripts; `make -n` clean on every touched target.

## Fixed Issues

### WR-01: Makefile hardcodes a user-specific DOCKER_HOST and force-overrides the environment

**Files modified:** `Makefile`, `.gitignore`
**Commit:** 989c791
**Applied fix:** Implemented the agreed self-templating `.env` pattern exactly
as specified in the review: deleted the hardcoded
`export DOCKER_HOST := unix:///Users/ajitg/.rd/docker.sock` and
`TESTCONTAINERS_RYUK_DISABLED` lines; added the parse-time `.env`
template-creation block (`ifeq ($(wildcard .env),)` + `$(shell printf ...)` +
`$(warning ...)`), `include .env`, and `export DOCKER_HOST
TESTCONTAINERS_RYUK_DISABLED`; added a `check-env` target that hard-fails with
Rancher-Desktop/Docker-Desktop guidance when `DOCKER_HOST` is empty; wired
`check-env` as a prerequisite on exactly the Docker-dependent targets (`test`,
`coverage`, `e2e`, `dev-up`, `docker`, `docker-%`, `kind-up`, `kind-load`,
`deploy`, `soak`, `test-harness`) and NOT on pure-Go targets (`build`, `lint`,
`proto`, `swagger`); dropped the now-redundant inline env from the
`test-harness` recipe; rewrote the top-of-file comment to describe the `.env`
workflow; added `.env` to `.gitignore` (confirmed via `git check-ignore`).
Verified both paths live: empty `.env` → `make check-env` exits 1 with
guidance; filled `.env` → passes, and `make -n build` never invokes it.

### WR-02: OPS-03 smoke check is vacuous — the "targeted upgrade" is a no-op

**Files modified:** `scripts/smoke/phase05-kind.sh`
**Commit:** 0f441d0
**Applied fix:** Step 7 now captures mq's generation, forces a REAL
pod-template change, asserts `[ "$(gen mq)" -gt "$GEN_MQ" ]` (the missing
direction that exposed the no-op), keeps the three unchanged-generation
asserts for streamer/collector/gateway, and reverts the toggle afterwards so
re-runs start from the values default. **Deliberate adaptation from the
review's suggestion:** used `mq.image.pullPolicy=Never` (revert:
`IfNotPresent`) instead of the suggested `Always`. `vantage/mq:dev` exists
only inside the kind node (loaded via `kind load docker-image`, not pullable
from any registry) — with `pullPolicy=Always` the kubelet must successfully
pull before starting the container, so the new pod would sit in
ImagePullBackOff and the rollout would hang. `IfNotPresent → Never` is an
equally real pod-template diff and is guaranteed startable.
**Note for the live-verification record:** this strengthens the OPS-03
assertion as expected (flagged in advance as fine) and adds two extra mq
rollouts (toggle + revert) per smoke run — `make smoke-05` takes slightly
longer but leaves the cluster in its original values state.

### WR-03: migrate Job comment promises "do not restart" but backoffLimit defaults to 6

**Files modified:** `deployments/templates/migrate-job.yaml`
**Commit:** f53dd2a
**Applied fix:** Added `backoffLimit: 1` to the Job spec next to
`activeDeadlineSeconds`, with a comment explaining both halves of the review's
reasoning: why not the default 6 (a failing migration would spawn up to 7 pods
and surface `DeadlineExceeded` instead of the first real error) and why 1
rather than 0 (the `nc -z` init gate only proves Postgres accepts TCP; one
retry absorbs the accepting-but-not-query-ready startup window). Verified the
rendered Job via `helm template` and `helm lint`.

### WR-04: activeDeadlineSeconds:120 races first-install image pulls that kind-load never loads

**Files modified:** `deployments/templates/migrate-job.yaml`, `Makefile`
**Commit:** 73c0e54
**Applied fix:** Took the review's second option (raise the deadline +
document) rather than pre-loading `busybox:1.36` and the Bitnami postgresql
image into kind — pre-pulling would couple `kind-load` to the exact Bitnami
image tag inside chart 18.7.8 and add network pulls to a target that is
currently registry-free. Raised `activeDeadlineSeconds` to 300 and raised
`helm upgrade --install` from `--timeout 3m` to `--timeout 6m` (the helm
timeout must exceed the Job deadline so a genuinely stuck migration still
surfaces as the Job's named `DeadlineExceeded`, not Helm's own generic
timeout). Documented the cold-start expectation in both the Job's header
comment and the `helm-install` recipe. Verified via `helm template`/`helm
lint` and `make -n helm-install`.
**Note for the live-verification record:** today's live `make deploy`
verification ran with `--timeout 3m`/120s; the new bounds are strictly looser
(same behavior on a warm cache, longer worst-case bound on a cold one), so the
verification is not invalidated.

### WR-05: smoke port-forward hardcodes 8081 instead of using LOCAL_PORT

**Files modified:** `scripts/smoke/phase05-kind.sh`
**Commit:** 09b0be5
**Applied fix:** `kubectl port-forward "svc/${RELEASE}-gateway"
"${LOCAL_PORT}:8080"` — exactly as suggested; the forward and every curl check
now share the single `LOCAL_PORT` declaration.

### WR-06: soak depth-bound assertion silently passes if the MQ inspect endpoint dies mid-run

**Files modified:** `scripts/soak.sh`
**Commit:** fb57c47
**Applied fix:** The polling loop no longer maps fetch failure to depth 0.
`if DEPTH=$(inspect_field depth); then ... else` distinguishes a real reading
from a failed fetch; consecutive failures increment `INSPECT_FAILS` and the
third in a row fails loudly ("MQ inspect unreachable 3x in a row —
port-forward dead?"), while any successful read resets the counter (tolerating
transient blips like an MQ pod restart). The final `PRODUCED`/`CONSUMED` reads
now carry explicit `|| fail "MQ inspect unreachable reading ..."` guards
instead of dying as an opaque `set -e` exit. Verified with `bash -n`.

### WR-07: No .dockerignore — repeat finding, still unfixed since the previous review

**Files modified:** `.dockerignore` (new file — the fix explicitly requires
creating it)
**Commit:** 4432c17
**Applied fix:** Created `.dockerignore` with exactly the review's list
(`.git`, `.planning`, `.env`, `bin/`, `coverage.*`, `dcgm_metrics_*.csv`,
`deployments/`, `docs/`, `*.md`), including `.env` per the WR-01 cross-
reference so the machine-local file never enters a build context. Safety
check performed before writing: all five Dockerfiles need only the Go module
tree (`COPY go.mod go.sum` + `COPY . .` + `go build`), and dockerignore
patterns anchor at the context root, so `docs/` does NOT exclude `pkg/docs`
(the swag-generated package the gateway compiles in) — noted in the file's
header comment.

## Skipped Issues

None — all 7 in-scope findings were fixed.

---

_Fixed: 2026-07-02T19:31:38Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
