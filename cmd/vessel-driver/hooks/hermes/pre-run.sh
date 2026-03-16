#!/usr/bin/env bash
# cmd/vessel-driver/hooks/hermes/pre-run.sh — Hermes pre-flight setup.
#
# Hermes does not clone a repository. Instead, it reads a single unlabeled bead
# (issue) from the Beads Dolt server and emits a routing decision.
#
# No git operations. No context.json file writing (Hermes does not modify the repo).
# Just set up Beads connectivity and prepare /workspace/.legion for the router vessel.
#
# Required env vars (set by Archon at container spawn time):
#   LEGION_CONFIG_JSON  — marshalled VesselConfig JSON (contains issue_id, repo_url, etc.)
#   GITHUB_TOKEN        — token (not used by Hermes, but present for compatibility)
#
# Env vars wired by entrypoint.sh (from DOLT_HOST/DOLT_PORT):
#   BEADS_DOLT_SERVER_HOST
#   BEADS_DOLT_SERVER_PORT

set -euo pipefail

# Parse LEGION_CONFIG_JSON for issue_id (Hermes must have this to read the bead).
ISSUE_ID=$(printf '%s' "$LEGION_CONFIG_JSON" | jq -r '.issue_id // empty')
[ -n "$ISSUE_ID" ] || { echo "hermes pre-run: ERROR — ISSUE_ID is empty (check LEGION_CONFIG_JSON)" >&2; exit 1; }

echo "hermes pre-run: routing issue=${ISSUE_ID}"

# ── Setup Beads context ───────────────────────────────────────────────────────
# Hermes reads from the Beads server (not a git clone), so BEADS_DIR points to
# the stub config in /etc/legion/.beads (created at image build time).
export BEADS_DIR="/etc/legion/.beads"
export BEADS_DOLT_SERVER_USER="root"

# ── Prepare workspace ─────────────────────────────────────────────────────────
# Create /workspace/.legion directory so the routing context can be written
# if needed (though Hermes typically does not modify files, this is for symmetry
# with the worker vessel pipeline).
mkdir -p /workspace/.legion

# ── Verify Dolt connectivity ──────────────────────────────────────────────────
# Test that Beads can reach the Dolt server before vessel-driver runs.
echo "hermes pre-run: checking Dolt connectivity at ${BEADS_DOLT_SERVER_HOST}:${BEADS_DOLT_SERVER_PORT}"
bd help > /dev/null || { echo "hermes pre-run: ERROR — bd command failed" >&2; exit 1; }

echo "hermes pre-run: complete — ready to route issue=${ISSUE_ID}"
