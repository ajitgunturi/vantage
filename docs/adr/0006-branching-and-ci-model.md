# ADR-0006: Long-lived per-module branches with CI-gated trunk

- Status: Accepted
- Date: 2026-06-24

## Context
The repo needs a delivery process. There are 5 natural workstreams (one per module). We want `main`
to stay always-green and never be broken, but we also do not want CI noise on every in-progress
commit, and we want the decision history (many PRs) preserved rather than squashed away with deleted
branches.

## Decision
- **One long-lived, protected feature branch per module**: `feat/mq`, `feat/streamer`,
  `feat/collector`, `feat/apigateway`, `feat/k8s-infra`. Never deleted (repo auto-delete disabled).
- **`main` is protected**: merges only via PR; the `ci-success` status check must pass; no
  force-push; no deletion.
- **CI (`.github/workflows/ci.yml`) runs only on `pull_request` → main and `push` → main.** Direct
  pushes to `feat/*` run nothing — enforced by simply not listing those branches in the workflow.
- **`feat/*` branches are protected** against force-push and deletion, but allow direct commits (no
  required PR into them) so day-to-day work is friction-free.
- CI = a `build-test` matrix over the 4 Go modules (`GOWORK=off`, build + `go test -race -cover`) plus
  `helm-lint`, aggregated by a single required `ci-success` gate job.

## Driving Prompt
> vantage will have the main branch protected. We will have 5 independent feature branches. All the
> commits go to main and every merge to main will trigger a build and run unit tests - all PRs to
> have a build action that does the same as well so we do not break anything - independent branches
> will never have builds. All feature branches should be protected too. There will be too many PRs
> that will be merged and we do not want to delete these feature branches.

## Consequences
- (+) `main` cannot break: the integration boundary is the only place CI gates, and it is mandatory.
- (+) No CI spend on in-progress feature work; faster local iteration.
- (+) Full PR/branch history preserved for the interview panel (nothing deleted).
- (−) Wildcard protection for `feat/*` requires GitHub **Rulesets** (classic branch protection can't
  match patterns) — handled in `scripts/setup-github.sh`.
- (−) Long-lived branches can drift from `main`; mitigated by `strict_required_status_checks_policy`
  (branch must be up to date before merge).
- (−) Solo dev: required approvals set to 0 (status check is the real gate), since one can't approve
  one's own PR.

## Alternatives considered
- **Trunk-based with short-lived throwaway branches** — simplest, but the user explicitly wants
  long-lived per-module branches that are never deleted.
- **CI on every branch push** — rejected: wastes Actions minutes on in-progress work; the user wants
  builds only at the main boundary.
- **Classic branch protection** — rejected for `feat/*`: no wildcard support; Rulesets used instead.
