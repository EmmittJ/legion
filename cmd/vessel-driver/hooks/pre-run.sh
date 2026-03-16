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
[ -n "$REPO_URL" ] || { echo "pre-run: ERROR — REPO_URL is empty (check LEGION_CONFIG_JSON)"  >&2; exit 1; }

echo "pre-run: issue=${ISSUE_ID} repo=${REPO_URL}"

# ── 1. Clone ─────────────────────────────────────────────────────────────────
# Use git credential store so GITHUB_TOKEN never appears in git error messages,
# process listings, or log aggregator output.
git config --global credential.helper store
printf 'https://x-access-token:%s@github.com\n' "$GITHUB_TOKEN" > /root/.git-credentials

git clone "$REPO_URL" /workspace
cd /workspace

# ── 2. Checkout vessel branch ─────────────────────────────────────────────────
git checkout -b "vessel/${ISSUE_ID}"

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

echo "pre-run: complete — branch vessel/${ISSUE_ID} ready"
