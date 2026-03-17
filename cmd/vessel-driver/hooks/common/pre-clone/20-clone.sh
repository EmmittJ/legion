#!/usr/bin/env bash
# Common pre-clone: clone the issue repo into /workspace.
# Runs after 10-validate-env.sh so required env vars are guaranteed present.
set -euo pipefail
source /hooks/lib.sh

# If /workspace already has a .git (e.g. re-run), skip clone.
if [ -d "/workspace/.git" ]; then
    legion_log INFO "workspace already cloned, skipping"
    exit 0
fi

# Set up git credentials via gh so private repos authenticate correctly.
gh auth setup-git

legion_log INFO "cloning repo: ${REPO_URL} → /workspace"
git clone "${REPO_URL}" /workspace
legion_log INFO "clone complete"
