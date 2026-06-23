#!/usr/bin/env bash
# Idempotent GitHub bootstrap for the vantage repo.
#
# Creates the repo, pushes main + the 5 long-lived feature branches, and applies
# Rulesets implementing the agreed branching model (see docs/adr/0006 + docs/BRANCHING.md):
#   - main          : protected — PR required, `ci-success` status check required, no force-push, no deletion
#   - feat/*        : protected — no force-push, no deletion (direct commits allowed, no required CI)
#   - feature branches are NEVER auto-deleted on merge
#
# Safe to re-run: skips what already exists. Requires `gh auth login` (scopes: repo, workflow, delete_repo).
set -euo pipefail

OWNER="ajitgunturi"
REPO="vantage"
SLUG="${OWNER}/${REPO}"
VISIBILITY="${VISIBILITY:-private}"   # override: VISIBILITY=public ./scripts/setup-github.sh
FEATURE_BRANCHES=(feat/mq feat/streamer feat/collector feat/apigateway feat/k8s-infra)

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }

# 1. Create the repo if absent.
if gh repo view "$SLUG" >/dev/null 2>&1; then
  say "Repo $SLUG already exists — skipping creation."
else
  say "Creating $VISIBILITY repo $SLUG"
  gh repo create "$SLUG" "--$VISIBILITY" --source=. --remote=origin --disable-wiki
fi

# Ensure origin remote points at the repo.
if ! git remote get-url origin >/dev/null 2>&1; then
  git remote add origin "https://github.com/${SLUG}.git"
fi

# 2. Repo settings: never auto-delete head branches on merge.
say "Disabling auto-delete of merged branches"
gh api -X PATCH "repos/${SLUG}" -F delete_branch_on_merge=false >/dev/null

# 3. Push main (must happen BEFORE the main ruleset, or the push is blocked).
say "Pushing main"
git push -u origin main

# 4. Create + push the 5 long-lived feature branches off main.
for br in "${FEATURE_BRANCHES[@]}"; do
  if git show-ref --verify --quiet "refs/heads/${br}"; then
    say "Local branch ${br} exists"
  else
    say "Creating local branch ${br}"
    git branch "${br}" main
  fi
  say "Pushing ${br}"
  git push -u origin "${br}"
done

# 5. Apply Rulesets (delete-then-create by name → idempotent).
apply_ruleset() {
  local name="$1" payload="$2"
  local existing
  existing=$(gh api "repos/${SLUG}/rulesets" --jq ".[] | select(.name==\"${name}\") | .id" 2>/dev/null || true)
  if [ -n "$existing" ]; then
    say "Replacing existing ruleset '${name}' (id ${existing})"
    gh api -X DELETE "repos/${SLUG}/rulesets/${existing}" >/dev/null
  fi
  say "Creating ruleset '${name}'"
  printf '%s' "$payload" | gh api -X POST "repos/${SLUG}/rulesets" --input - >/dev/null
}

read -r -d '' MAIN_RULESET <<'JSON' || true
{
  "name": "main-protection",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/heads/main"], "exclude": [] } },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": false,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": false
      } },
    { "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [ { "context": "ci-success" } ]
      } }
  ]
}
JSON

read -r -d '' FEATURE_RULESET <<'JSON' || true
{
  "name": "feature-protection",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/heads/feat/**"], "exclude": [] } },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" }
  ]
}
JSON

apply_ruleset "main-protection" "$MAIN_RULESET"
apply_ruleset "feature-protection" "$FEATURE_RULESET"

say "Done. Branches: main + ${FEATURE_BRANCHES[*]}"
say "main requires a PR with the 'ci-success' check; feat/* protected from deletion/force-push."
