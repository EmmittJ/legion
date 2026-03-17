#!/usr/bin/env bash
# Worker pre-acp: create and checkout the vessel branch before the ACP session.
set -euo pipefail
source /hooks/lib.sh

VESSEL_BRANCH="vessel/${LEGION_ISSUE_ID}"

if git show-ref --quiet "refs/heads/${VESSEL_BRANCH}"; then
    legion_log INFO "branch ${VESSEL_BRANCH} already exists, checking out"
    git checkout "${VESSEL_BRANCH}"
else
    legion_log INFO "creating vessel branch ${VESSEL_BRANCH}"
    git checkout -b "${VESSEL_BRANCH}"
fi
