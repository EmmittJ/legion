#!/bin/sh
set -e

# Initialize Beads with GitHub as the Dolt git remote.
# Expects: GITHUB_TOKEN, REPO_URL
# Expects (set by compose): BEADS_DOLT_SERVER_HOST, BEADS_DOLT_SERVER_PORT

[ -z "${REPO_URL}" ]                && echo '[archon-init] ERROR: REPO_URL is unset'                && exit 1
[ -z "${BEADS_DOLT_SERVER_HOST}" ]  && echo '[archon-init] ERROR: BEADS_DOLT_SERVER_HOST is unset'  && exit 1
[ -z "${BEADS_DOLT_SERVER_PORT}" ]  && echo '[archon-init] ERROR: BEADS_DOLT_SERVER_PORT is unset'  && exit 1

# Configure git credential store so GITHUB_TOKEN authenticates git operations
if [ -n "${GITHUB_TOKEN}" ]; then
    GH_TOKEN="${GITHUB_TOKEN}" gh auth setup-git
fi

# Clone the repo so bd can init from the committed .beads/metadata.json
echo "[archon-init] Cloning repo..."
git clone "${REPO_URL}" /workspace

# These are not set by compose — add them now.
# BEADS_DIR: the clone path isn't known until this script runs.
# BEADS_DOLT_SERVER_USER: not wired in compose; default to root.
export BEADS_DIR="/workspace/.beads"
export BEADS_DOLT_SERVER_USER="${BEADS_DOLT_SERVER_USER:-root}"

# Re-export all BEADS_* vars injected by compose into the process environment
# so bd subprocesses inherit them.  This is future-proof: any new BEADS_* var
# added to compose is picked up automatically.
for _var in $(env | grep '^BEADS_' | cut -d= -f1); do
    export "$_var"
done

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
