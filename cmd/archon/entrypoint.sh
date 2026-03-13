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
# stdin is non-TTY in a container — bd init --quiet skips all prompts automatically
cd /workspace && bd init --quiet
bd dolt remote add origin "git+${REPO_URL}" 2>/dev/null || true
bd dolt pull || echo "[archon-init] Warning: bd dolt pull failed"
echo "[archon-init] Beads initialized."
echo "[archon-init] Initialization complete. Starting Archon..."

exec /archon
