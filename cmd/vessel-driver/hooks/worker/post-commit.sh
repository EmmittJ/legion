#!/usr/bin/env bash
# Worker post-commit: push the vessel branch and open a PR.
set -euo pipefail
source /hooks/lib.sh

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
legion_log INFO "pushing vessel branch ${BRANCH}"
git push --set-upstream origin "${BRANCH}"

legion_log INFO "creating PR for issue ${LEGION_ISSUE_ID}"
gh pr create \
  --title "vessel: ${LEGION_ISSUE_ID}" \
  --body "Beads issue: ${LEGION_ISSUE_ID}

Automated vessel output. Review and merge if the changes look correct." \
  --head "${BRANCH}" \
  --base main