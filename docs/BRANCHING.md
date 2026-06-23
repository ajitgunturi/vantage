# Branching & CI Model

## Branches
| Branch | Purpose | Protected | CI on push? |
|--------|---------|-----------|-------------|
| `main` | Integration trunk. Always green, always deployable. | ✅ PR + `ci-success` required, no force-push, no deletion | ✅ on merge |
| `feat/mq` | Custom message queue module | ✅ no force-push, no deletion | ❌ |
| `feat/streamer` | Telemetry streamer module | ✅ no force-push, no deletion | ❌ |
| `feat/collector` | Telemetry collector module | ✅ no force-push, no deletion | ❌ |
| `feat/apigateway` | REST API gateway module | ✅ no force-push, no deletion | ❌ |
| `feat/k8s-infra` | Helm charts + kind infra | ✅ no force-push, no deletion | ❌ |

The five feature branches are **long-lived** (one per module) and are **never deleted** after a
merge — auto-delete-on-merge is disabled at the repo level.

## Workflow
```
commit ──► feat/<module>        (direct commits; no CI runs here)
   │
   └── open PR: feat/<module> ──► main
            │
            ├── CI runs: build-test (×4 modules) + helm-lint  →  ci-success gate
            │
            └── merge (allowed once ci-success is green)  ──►  CI runs again on main
```

- **Direct commits** land on a feature branch — fast, no CI noise.
- **Opening a PR to `main`** triggers the full build + unit tests (`.github/workflows/ci.yml`).
- **Merging to `main`** is blocked until the `ci-success` status check passes, then CI re-runs on
  `main` to confirm the integrated trunk is green.
- Feature branches keep accumulating work across many PRs; they are not throwaway.

## Why CI only on PRs and merges to main
Feature-branch pushes are intentionally excluded from CI to avoid burning Actions minutes on
in-progress work. The contract is enforced at the **integration boundary**: nothing reaches `main`
without a green `build-test` matrix. The single `ci-success` gate job aggregates the matrix so branch
protection has one stable required check, even as modules are added.

## Setup
Apply the whole model (repo, branches, rulesets) with:
```bash
VISIBILITY=private ./scripts/setup-github.sh   # or VISIBILITY=public
```
The script is idempotent — safe to re-run.
