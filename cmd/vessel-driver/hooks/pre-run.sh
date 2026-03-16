#!/usr/bin/env bash
# pre-run.sh — vessel pre-flight: clone repo, checkout branch, claim issue,
# write context files for vessel-driver.
#
# Required env vars (set by Archon at container spawn time):
#   LEGION_CONFIG_JSON  — marshalled VesselConfig JSON
#   GITHUB_TOKEN        — token with repo + copilot scopes
#
# Env vars wired by entrypoint.sh (from DOLT_HOST/DOLT_PORT):
#   BEADS_DOLT_SERVER_HOST
#   BEADS_DOLT_SERVER_PORT
#   BEADS_DOLT_SERVER_USER

set -euo pipefail

# ── Parse config ─────────────────────────────────────────────────────────────
# Use `// empty` so jq emits "" (not the string "null") when a field is absent.
ISSUE_ID=$(printf '%s' "$LEGION_CONFIG_JSON" | jq -r '.issue_id // empty')
REPO_URL=$(printf '%s' "$LEGION_CONFIG_JSON"  | jq -r '.repo_url  // empty')

[ -n "$ISSUE_ID" ] || { echo "pre-run: ERROR — ISSUE_ID is empty (check LEGION_CONFIG_JSON)" >&2; exit 1; }

echo "pre-run: issue=${ISSUE_ID} role=${LEGION_ROLE:-worker}"

# ── HERMES CASE: No git clone, just Beads setup ──────────────────────────────
# Hermes is Legion's routing vessel. It does not clone or modify repositories.
# It reads a single bead from Dolt and emits a routing decision (role:* label).
# Exit early; skip all git operations.
if [ "${LEGION_ROLE:-worker}" = "hermes" ]; then
    # Hermes reads from the Beads server, not a git clone.
    # BEADS_DIR points to the stub config baked into the image.
    export BEADS_DIR="/etc/legion/.beads"
    export BEADS_DOLT_SERVER_USER="root"
    
    # Prepare workspace directory for symmetry with the worker pipeline.
    mkdir -p /workspace/.legion
    
    echo "pre-run: hermes mode — no git clone, routing via Beads (issue=${ISSUE_ID})"
    exit 0
fi

# ── All other roles (worker, hierophant, inquisitor, weaver) require a repo ───
[ -n "$REPO_URL" ] || { echo "pre-run: ERROR — REPO_URL is empty (check LEGION_CONFIG_JSON)"  >&2; exit 1; }

echo "pre-run: repo=${REPO_URL}"

# ── 1. Clone ─────────────────────────────────────────────────────────────────
# Use git credential store so GITHUB_TOKEN never appears in git error messages,
# process listings, or log aggregator output.
git config --global credential.helper store
printf 'https://x-access-token:%s@github.com\n' "$GITHUB_TOKEN" > /root/.git-credentials

git clone "$REPO_URL" /workspace
cd /workspace

# ── 2. Checkout vessel branch ─────────────────────────────────────────────────
# Workers create a new feature branch from the cloned default branch.
# Hierophant and inquisitor vessels operate on an existing branch (the worker's
# vessel/${ISSUE_ID} branch already exists) — skip branch creation for them.
if [ "${LEGION_ROLE:-worker}" = "worker" ]; then
    git checkout -b "vessel/${ISSUE_ID}"
fi

# ── 3. Configure git user for commits made inside the vessel ─────────────────
git config user.email "vessel@legion"
git config user.name "Vessel"

# ── 4. Bootstrap Beads ───────────────────────────────────────────────────────
# BEADS_DIR must point to the .beads directory inside the clone so bd resolves
# project config (config.yaml, metadata.json) from the committed files.
# BEADS_DOLT_SERVER_* are already exported by entrypoint.sh.
export BEADS_DIR="/workspace/.beads"
export BEADS_DOLT_SERVER_USER="root"

# BEADS_DOLT_SERVER_* vars (set by entrypoint.sh) point to the cluster's Dolt
# server — no local Dolt workspace needed. BEADS_DIR + server vars are sufficient.
echo "pre-run: beads dir=${BEADS_DIR}, dolt server=${BEADS_DOLT_SERVER_HOST}:${BEADS_DOLT_SERVER_PORT}"

# ── 5. Claim the issue ────────────────────────────────────────────────────────
bd update "$ISSUE_ID" --claim \
  || echo "pre-run: could not claim ${ISSUE_ID} (may already be claimed)" >&2

# ── 6. Write context files for vessel-driver ─────────────────────────────────
mkdir -p /workspace/.legion

# context.json — issue title + description used to build the ACP prompt.
# bd show --json emits a JSON array: [{"id":"...","title":"...","description":"...",...}]
bd show "$ISSUE_ID" --json > /workspace/.legion/context.json

# issue.json — full VesselConfig blob for the ACP agent to reference as context.
printf '%s\n' "$LEGION_CONFIG_JSON" > /workspace/.legion/issue.json

echo "pre-run: complete — branch vessel/${ISSUE_ID} ready (role=${LEGION_ROLE:-worker})"
