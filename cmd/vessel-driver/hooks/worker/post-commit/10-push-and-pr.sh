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
if gh pr create \
  --title "vessel(${LEGION_ISSUE_ID}): implement changes" \
  --body "Beads issue: ${LEGION_ISSUE_ID}

Automated vessel output. Review and merge if the changes look correct." \
  --head "${BRANCH}" \
  --base main 2>&1; then
  legion_log INFO "PR created"
else
  legion_log WARN "PR creation failed (non-fatal) — check GitHub token has 'pull_requests: write' permission"
fi
