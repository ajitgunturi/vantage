#!/usr/bin/env bash
# Idempotent GitHub bootstrap for the vantage repo (solo-dev model — see docs/adr/0008).
#
# Creates the repo, pushes main, and applies a single Ruleset:
#   - main : protected — PR required, `ci-success` status check required, no force-push, no deletion
#
# Work happens on ephemeral, unprotected branches (feat/*, fix/*, chore/*) that are PR'd to main
# and deleted after merge. No long-lived feature branches.
#
# Safe to re-run: skips what already exists. Requires `gh auth login` (scopes: repo, workflow).
set -euo pipefail

OWNER="ajitgunturi"
REPO="vantage"
SLUG="${OWNER}/${REPO}"
VISIBILITY="${VISIBILITY:-private}"   # override: VISIBILITY=public ./scripts/setup-github.sh

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

# 4. Apply the main Ruleset (delete-then-create by name → idempotent).
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

apply_ruleset "main-protection" "$MAIN_RULESET"

say "Done. main is protected — PRs require the 'ci-success' check to merge."
say "Work on ephemeral branches (feat/*, fix/*, chore/*), PR to main, delete after merge."
