#!/usr/bin/env bash
# Worker pre-acp: create and checkout the vessel branch before the ACP session.
# If LEGION_WORK_BRANCH is set (rework vessel), fetch and check out that branch
# instead of creating a new vessel/ branch.
set -euo pipefail
source /hooks/lib.sh

if [ -n "${LEGION_WORK_BRANCH:-}" ]; then
    VESSEL_BRANCH="${LEGION_WORK_BRANCH}"
    legion_log INFO "rework vessel: using work branch ${VESSEL_BRANCH}"
    git fetch origin "${VESSEL_BRANCH}"
    git checkout "${VESSEL_BRANCH}"
else
    VESSEL_BRANCH="vessel/${LEGION_ISSUE_ID}"
    if git show-ref --quiet "refs/heads/${VESSEL_BRANCH}"; then
        legion_log INFO "branch ${VESSEL_BRANCH} already exists, checking out"
        git checkout "${VESSEL_BRANCH}"
    else
        legion_log INFO "creating vessel branch ${VESSEL_BRANCH}"
        git checkout -b "${VESSEL_BRANCH}"
    fi
fi