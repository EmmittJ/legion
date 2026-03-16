#!/usr/bin/env bash
# post-run.sh — vessel teardown: role-aware commit/push/close or clean exit.
#
# Reads /workspace/result.json written by vessel-driver.
#
# Required env vars:
#   LEGION_ROLE         — vessel role (worker | hierophant | inquisitor);
#                         exported by entrypoint.sh from LEGION_CONFIG_JSON.
#                         Defaults to "worker" if absent.
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

echo "post-run: issue=${ISSUE_ID} status=${STATUS} role=${LEGION_ROLE:-worker}"

# ── Beads env ─────────────────────────────────────────────────────────────────
export BEADS_DIR="/workspace/.beads"
export BEADS_DOLT_SERVER_USER="root"

# ── Role-aware teardown ───────────────────────────────────────────────────────
case "${LEGION_ROLE:-worker}" in

  worker)
    # ── Worker success path ───────────────────────────────────────────────────
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

      # Note: review bead creation is handled by Archon's createReviewBead function
      # when it observes vessel completion. Do NOT create it here — that would
      # produce a duplicate bead with no agent label that is never dispatched.

      # Close the issue.
      bd close "$ISSUE_ID" --reason "Vessel completed"

      echo "post-run: complete — branch ${BRANCH} pushed, issue ${ISSUE_ID} closed"
      exit 0
    fi

    # ── Worker error path ─────────────────────────────────────────────────────
    echo "post-run: vessel failed for ${ISSUE_ID}: ${ERROR_MSG}" >&2

    # Mark the issue blocked with a "failed" label so it surfaces in `bd list`.
    bd update "$ISSUE_ID" \
        --status=blocked \
        --add-label "failed" \
        --append-notes="vessel error: ${ERROR_MSG}" \
      || echo "post-run: WARNING — could not update issue ${ISSUE_ID}" >&2

    exit 1
    ;;

  hierophant)
    # Hierophant reviewed and either approved or requested changes.
    # Archon handles the state machine transition based on vessel exit code.
    # post-run must not close the bead — Archon does that.
    if [ "$STATUS" = "success" ]; then
      echo "post-run: hierophant vessel complete — Archon handles state machine"
      exit 0
    fi
    echo "post-run: hierophant vessel failed for ${ISSUE_ID}: ${ERROR_MSG}" >&2
    exit 1
    ;;

  inquisitor)
    # Inquisitor reviewed — same contract: Archon handles state machine.
    # post-run must not close the bead — Archon does that.
    if [ "$STATUS" = "success" ]; then
      echo "post-run: inquisitor vessel complete — Archon handles state machine"
      exit 0
    fi
    echo "post-run: inquisitor vessel failed for ${ISSUE_ID}: ${ERROR_MSG}" >&2
    exit 1
    ;;

  *)
    echo "post-run: unknown role: ${LEGION_ROLE}" >&2
    exit 1
    ;;

esac
