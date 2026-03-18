#!/usr/bin/env bash
# Worker post-commit: push the vessel branch and open a PR.
set -euo pipefail
source /hooks/lib.sh

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
legion_log INFO "post-commit: cwd=$(pwd) branch=${BRANCH}"
legion_log INFO "post-commit: GH_TOKEN length=${#GH_TOKEN}"

legion_log INFO "pushing vessel branch ${BRANCH}"
git push --set-upstream origin "${BRANCH}"
legion_log INFO "push complete"

legion_log INFO "creating PR for issue ${LEGION_ISSUE_ID}"

# Fetch issue metadata from beads for a meaningful PR title and body.
ISSUE_JSON=$(bd show "${LEGION_ISSUE_ID}" --json 2>/dev/null || echo "[]")
ISSUE_TITLE=$(echo "$ISSUE_JSON" | jq -r '.[0].title // empty')
ISSUE_DESC=$(echo "$ISSUE_JSON" | jq -r '.[0].description // empty')

PR_TITLE="${ISSUE_TITLE:-${LEGION_ISSUE_ID}: implement changes}"
PR_BODY="Closes bead: ${LEGION_ISSUE_ID}

${ISSUE_DESC}

---
*Automated vessel output — review before merging.*"

if gh pr create \
  --title "$PR_TITLE" \
  --body "$PR_BODY" \
  --head "${BRANCH}" \
  --base main 2>&1; then
  legion_log INFO "PR created"
else
  legion_log WARN "PR creation failed (non-fatal) — check GitHub token has 'pull_requests: write' permission"
fi
