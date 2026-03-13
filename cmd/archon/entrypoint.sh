#!/bin/sh
set -e

# Initialize Beads with GitHub as the Dolt git remote.
# Expects: GITHUB_TOKEN, REPO_URL

[ -z "${REPO_URL}" ] && echo '[archon-init] ERROR: REPO_URL is unset' && exit 1

BD_REMOTE="${BD_REMOTE:-git+$(echo "${REPO_URL}" | sed 's|\.git$||').git}"

echo "[archon-init] Initializing Beads..."

# Configure gh so git operations authenticate without embedding the token in URLs
if [ -n "${GITHUB_TOKEN}" ]; then
    GH_TOKEN="${GITHUB_TOKEN}" gh auth setup-git
fi

# Initialize local Dolt database on first boot (idempotent: skipped if already initialised)
if [ ! -f /app/.beads/metadata.json ]; then
    RETRY_COUNT=0
    MAX_RETRIES=10
    while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
        if bd init --quiet 2>/dev/null; then
            echo "[archon-init] Beads initialized."
            break
        fi
        RETRY_COUNT=$((RETRY_COUNT + 1))
        echo "[archon-init] Attempt $RETRY_COUNT/$MAX_RETRIES failed. Retrying in 2s..."
        sleep 2
    done
    if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
        echo "[archon-init] WARNING: Failed to initialize Beads after ${MAX_RETRIES} attempts, continuing anyway."
    fi
fi

# Add GitHub as the Dolt git remote (idempotent)
bd dolt remote add origin "${BD_REMOTE}" 2>/dev/null || true

# Pull latest issues from GitHub
bd dolt pull || echo "[archon-init] Warning: Dolt pull failed (may be first boot)"

echo "[archon-init] Initialization complete. Starting Archon..."
exec /archon
