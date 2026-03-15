#!/usr/bin/env bash
# post-run.sh — vessel teardown: commit + push if success; mark failed if error.
#
# Reads /workspace/result.json written by vessel-driver.
#
# Required env vars:
#   LEGION_CONFIG_JSON  — marshalled VesselConfig JSON (for review_enabled flag)
#
# Env vars wired by entrypoint.sh (from DOLT_HOST/DOLT_PORT):
#   BEADS_DOLT_SERVER_HOST
#   BEADS_DOLT_SERVER_PORT
#
# Exits 0 on success path, 1 on error path.

set -euo pipefail

RESULT_FILE="/workspace/result.json"

# ── Read result ───────────────────────────────────────────────────────────────
if [ ! -f "$RESULT_FILE" ]; then
  echo "post-run: ERROR — result.json not found; vessel-driver exited before writing it" >&2
  exit 1
fi

RESULT=$(cat "$RESULT_FILE")
ISSUE_ID=$(printf '%s' "$RESULT" | jq -r '.issue_id')
STATUS=$(printf '%s' "$RESULT"  | jq -r '.status')
BRANCH=$(printf '%s' "$RESULT"  | jq -r '.branch        // empty')
ERROR_MSG=$(printf '%s' "$RESULT" | jq -r '.error_message // empty')

echo "post-run: issue=${ISSUE_ID} status=${STATUS}"

# ── Beads env ─────────────────────────────────────────────────────────────────
export BEADS_DIR="/workspace/.beads"
export BEADS_DOLT_SERVER_USER="root"

# ── Success path ──────────────────────────────────────────────────────────────
if [ "$STATUS" = "success" ]; then
  cd /workspace

  # Stage all changes made by the ACP session.
  git add -A

  # Commit only if there is something staged; agent may have made no file changes.
  if ! git diff --cached --quiet; then
    git commit -m "vessel(${ISSUE_ID}): implement changes"
  else
    echo "post-run: nothing to commit — agent made no file changes"
  fi

  # Push the branch.  The credential store written by pre-run.sh handles auth.
  # Trap push failure: mark the issue blocked so it doesn't stay open indefinitely.
  if ! git push origin "${BRANCH}"; then
    bd update "$ISSUE_ID" \
        --status=blocked \
        --add-label "failed" \
        --append-notes="vessel error: git push failed for ${BRANCH}" \
      || true
    exit 1
  fi

  # Optionally create a review task when review_enabled == true.
  REVIEW_ENABLED=$(printf '%s' "$LEGION_CONFIG_JSON" | jq -r '.review_enabled // false')
  if [ "$REVIEW_ENABLED" = "true" ]; then
    REVIEW_DESC="Review vessel work for ${ISSUE_ID} on branch ${BRANCH}"
    bd create "Review: ${ISSUE_ID}" \
        --description="$REVIEW_DESC" \
        -t task \
      || echo "post-run: WARNING — could not create review task for ${ISSUE_ID}" >&2
  fi

  # Close the issue.
  bd close "$ISSUE_ID" --reason "Vessel completed"

  echo "post-run: complete — branch ${BRANCH} pushed, issue ${ISSUE_ID} closed"
  exit 0
fi

# ── Error path ────────────────────────────────────────────────────────────────
echo "post-run: vessel failed for ${ISSUE_ID}: ${ERROR_MSG}" >&2

# Mark the issue blocked with a "failed" label so it surfaces in `bd list`.
bd update "$ISSUE_ID" \
    --status=blocked \
    --add-label "failed" \
    --append-notes="vessel error: ${ERROR_MSG}" \
  || echo "post-run: WARNING — could not update issue ${ISSUE_ID}" >&2

exit 1
