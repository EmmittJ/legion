#!/usr/bin/env bash
# Reviewer pre-acp: validate review branch is set.
set -euo pipefail
source /hooks/lib.sh
legion_require_env LEGION_ISSUE_ID LEGION_ROLE LEGION_REVIEW_BRANCH
legion_log INFO "reviewer vessel starting for issue ${LEGION_ISSUE_ID} on branch ${LEGION_REVIEW_BRANCH}"