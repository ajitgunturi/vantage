# Branching & CI Model

> Solo-developer model (see [ADR-0008](adr/0008-simplified-solo-branching.md), which supersedes the
> earlier 5-branch model in ADR-0006). Protect `main`; everything else is ephemeral.

## Branches
| Branch | Purpose | Protected | CI |
|--------|---------|-----------|----|
| `main` | Integration trunk. Always green, always deployable. | ✅ PR + `ci-success` required, no force-push, no deletion | ✅ on PR + on merge |
| `feat/*`, `fix/*`, `chore/*` | Ephemeral working branches — one per change | ❌ unprotected | ✅ when a PR to `main` is open |

There are **no long-lived branches** other than `main`. Create a branch for a change, PR it, merge
when green, delete it.

## Workflow
```
git checkout -b feat/<thing> main
   │  ... commit work (pre-commit hook runs gofmt + lint) ...
   └── open PR -> main
            │
            ├── CI: build-test (×4 modules) + lint + helm-lint + logic-coverage  →  ci-success gate
            │
            └── merge (allowed once ci-success is green)  ──►  CI re-runs on main
                     └── delete the branch
```

- **`main` is protected**: no direct pushes; every change lands via a PR whose `ci-success` check is
  green.
- **CI triggers on `pull_request → main` and `push → main`** (`.github/workflows/ci.yml`) — source
  branch doesn't matter, so any ephemeral branch's PR is fully gated.
- Run `make hooks` once per clone to install the pre-commit hook (gofmt + golangci-lint).

## Setup
```bash
VISIBILITY=public ./scripts/setup-github.sh   # creates repo, pushes main, applies main-protection
```
Idempotent — safe to re-run.
