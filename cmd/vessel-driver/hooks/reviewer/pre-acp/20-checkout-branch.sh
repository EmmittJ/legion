#!/usr/bin/env bash
# Reviewer pre-acp: fetch and checkout the review branch.
set -euo pipefail
source /hooks/lib.sh

legion_require_env LEGION_REVIEW_BRANCH

legion_log INFO "fetching review branch ${LEGION_REVIEW_BRANCH}"
git fetch origin "${LEGION_REVIEW_BRANCH}"
git checkout "${LEGION_REVIEW_BRANCH}"
legion_log INFO "checked out review branch ${LEGION_REVIEW_BRANCH}"