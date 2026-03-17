#!/usr/bin/env bash
# Planner pre-acp: validate that the issue has sufficient context for planning.
set -euo pipefail
source /hooks/lib.sh
legion_require_env LEGION_ISSUE_ID LEGION_ROLE
legion_log INFO "planner vessel starting for issue ${LEGION_ISSUE_ID}"
