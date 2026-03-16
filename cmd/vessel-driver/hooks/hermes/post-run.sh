#!/usr/bin/env bash
# cmd/vessel-driver/hooks/hermes/post-run.sh — Hermes post-flight: apply role label.
#
# Hermes has completed its routing decision. It wrote the chosen role to
# /workspace/result.json under the "role" field. This script:
#   1. Reads result.json and extracts the role
#   2. Applies the role:* label to the issue via bd update
#   3. Cleans up gracefully if result.json is missing (vessel-driver timed out)
#
# Exits 0 on success, allowing Archon to continue.
# Exits 1 on critical error (should not happen in normal flow).

set -euo pipefail

RESULT_FILE="/workspace/result.json"

# ── Handle missing result.json ────────────────────────────────────────────────
# If vessel-driver timed out or died before writing result.json, exit cleanly
# so post-run.sh does not block teardown. Archon will observe the timeout.
if [ ! -f "$RESULT_FILE" ]; then
  echo "hermes post-run: WARNING — result.json not found; vessel-driver may have timed out" >&2
  exit 0
fi

RESULT=$(cat "$RESULT_FILE")

# ── Extract role and issue_id ─────────────────────────────────────────────────
ROLE=$(printf '%s' "$RESULT" | jq -r '.role // empty')
ISSUE_ID=$(printf '%s' "$RESULT" | jq -r '.issue_id // empty')

# Exit cleanly if role is empty; Hermes may not always emit a role (edge cases).
if [ -z "$ROLE" ]; then
  echo "hermes post-run: role field is empty in result.json; skipping label update"
  exit 0
fi

if [ -z "$ISSUE_ID" ]; then
  echo "hermes post-run: ERROR — issue_id is empty in result.json" >&2
  exit 1
fi

echo "hermes post-run: issue=${ISSUE_ID} role=${ROLE}"

# ── Setup Beads context ───────────────────────────────────────────────────────
export BEADS_DIR="/etc/legion/.beads"
export BEADS_DOLT_SERVER_USER="root"

# ── Apply the role label ──────────────────────────────────────────────────────
# Update the issue with the routing decision.
# Use || true to handle edge cases where bd update fails (e.g., issue already closed).
echo "hermes post-run: applying label role:${ROLE} to issue ${ISSUE_ID}"
bd update "$ISSUE_ID" --add-label "role:${ROLE}" --remove-label "hermes:classifying" 2>/dev/null || true

echo "hermes post-run: complete — issue ${ISSUE_ID} labeled as role:${ROLE}"
exit 0
