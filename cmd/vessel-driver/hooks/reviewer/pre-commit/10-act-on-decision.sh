#!/usr/bin/env bash
# Reviewer pre-commit: act on Inquisitor decision (APPROVE or REJECT).
set -euo pipefail
source /hooks/lib.sh

legion_require_env LEGION_ISSUE_ID LEGION_CONFIG_JSON

DECISION_FILE=/workspace/.legion/decision.json
STATE_FILE=/workspace/.legion/review_state.json

for f in "$DECISION_FILE" "$STATE_FILE"; do
    if [ ! -f "$f" ]; then
        legion_log ERROR "required file missing: $f"
        exit 1
    fi
done

DECISION=$(jq -r '.decision' "$DECISION_FILE")
REASON=$(jq -r '.reason' "$DECISION_FILE")
PR_NUMBER=$(jq -r '.pr_number' "$STATE_FILE")
ORIGINAL_ID=$(jq -r '.original_issue_id' "$STATE_FILE")
WORK_BRANCH=$(jq -r '.branch' "$STATE_FILE")
REWORK_COUNT=$(jq -r '.rework_count' "$STATE_FILE")

MAX_REWORK=$(echo "$LEGION_CONFIG_JSON" | jq -r '.max_rework // 3')
DELETE_ON_MERGE=$(echo "$LEGION_CONFIG_JSON" | jq -r '.delete_branch_on_merge // true')

case "$DECISION" in
  APPROVE)
    legion_log INFO "APPROVE — merging PR #${PR_NUMBER}"
    if ! gh pr merge --merge "$PR_NUMBER" 2>&1; then
        STATE=$(gh pr view "$PR_NUMBER" --json state --jq '.state' 2>/dev/null || echo "UNKNOWN")
        if [ "$STATE" = "MERGED" ]; then
            legion_log WARN "PR #${PR_NUMBER} already merged — continuing"
        else
            legion_log ERROR "gh pr merge failed and PR is not merged (state: ${STATE})"
            exit 1
        fi
    fi
    if [ "$DELETE_ON_MERGE" = "true" ]; then
        git push origin --delete "$WORK_BRANCH" \
          || legion_log WARN "branch delete failed (non-fatal)"
    fi
    bd close "$LEGION_ISSUE_ID" --reason "Approved — merged via PR #${PR_NUMBER}"
    ;;

  REJECT)
    NEXT_COUNT=$((REWORK_COUNT + 1))
    if [ "$NEXT_COUNT" -gt "$MAX_REWORK" ]; then
        legion_log WARN "REJECT — max rework (${MAX_REWORK}) reached; escalating to human"
        bd update "$ORIGINAL_ID" --add-label "escalate:human"
        bd close "$LEGION_ISSUE_ID" \
          --reason "Rejected — max rework (${MAX_REWORK}) reached; escalated to human"
    else
        legion_log INFO "REJECT — creating rework issue (attempt ${NEXT_COUNT}/${MAX_REWORK})"
        ORIGINAL_TITLE=$(bd show "$ORIGINAL_ID" --json | jq -r '.[0].title')
        REWORK_LABELS="role:worker,discovered-from:${ORIGINAL_ID},original-issue:${ORIGINAL_ID},work-branch:${WORK_BRANCH},review-rework-count:${NEXT_COUNT},dispatch:auto"
        bd create "Rework: ${ORIGINAL_TITLE}" \
          --description="Rejection reason: ${REASON}" \
          --labels "$REWORK_LABELS" \
          -t task -p 1 --json
        bd close "$LEGION_ISSUE_ID" \
          --reason "Rejected — rework issue created (attempt ${NEXT_COUNT}/${MAX_REWORK})"
    fi
    ;;

  *)
    legion_log ERROR "unrecognized decision value: '${DECISION}' (expected APPROVE or REJECT)"
    exit 1
    ;;
esac