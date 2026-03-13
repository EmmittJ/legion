#!/bin/sh
set -e

# Initialize Beads with GitHub as the Dolt git remote.
# Expects: GITHUB_TOKEN, REPO_URL

[ -z "${REPO_URL}" ] && echo '[archon-init] ERROR: REPO_URL is unset' && exit 1

# Configure git credential store so GITHUB_TOKEN authenticates git operations
if [ -n "${GITHUB_TOKEN}" ]; then
    GH_TOKEN="${GITHUB_TOKEN}" gh auth setup-git
fi

# Clone the repo so bd can init from the committed .beads/metadata.json
echo "[archon-init] Cloning repo..."
git clone "${REPO_URL}" /workspace

# Initialize Beads from inside the cloned repo -- bd finds .beads/metadata.json
# and connects to the existing Dolt history. No common-ancestor problem.
echo "[archon-init] Initializing Beads..."
cd /workspace && bd init --quiet
echo "[archon-init] Initialization complete. Starting Archon..."

exec /archon
