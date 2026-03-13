#!/bin/sh

set -e

# Initialize Beads with Dolt SQL server backend.
# Expects environment variables:
#   - BEADS_DOLT_SERVER_HOST (e.g., "dolt")
#   - BEADS_DOLT_SERVER_PORT (e.g., "3306")
#   - GITHUB_TOKEN (optional, for syncing from GitHub)
#   - REPO_URL (optional, for syncing from GitHub)

DOLT_HOST="${BEADS_DOLT_SERVER_HOST:-dolt}"
DOLT_PORT="${BEADS_DOLT_SERVER_PORT:-3306}"
DB_NAME="beads"

echo "[archon-init] Initializing Beads with Dolt SQL server at ${DOLT_HOST}:${DOLT_PORT}..."

# Retry bd init up to 10 times with 2-second waits.
# bd init will automatically detect the dolt server if it's running on the standard ports
RETRY_COUNT=0
MAX_RETRIES=10
while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if bd init \
        --server-host="${DOLT_HOST}" \
        --server-port="${DOLT_PORT}" \
        --database="${DB_NAME}" \
        --quiet 2>/dev/null; then
        echo "[archon-init] Beads initialized successfully."
        break
    fi
    RETRY_COUNT=$((RETRY_COUNT + 1))
    echo "[archon-init] Attempt $RETRY_COUNT/$MAX_RETRIES failed. Retrying in 2s..."
    sleep 2
done

if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
    echo "[archon-init] WARNING: Failed to initialize Beads after ${MAX_RETRIES} attempts, but continuing anyway."
fi

# Verify that Beads is working (non-fatal if it fails)
echo "[archon-init] Verifying Beads connectivity..."
if ! bd list --all >/dev/null 2>&1; then
    echo "[archon-init] WARNING: Beads list command failed, but continuing."
fi

# Optional: sync from GitHub if credentials are provided
if [ -n "${GITHUB_TOKEN}" ] && [ -n "${REPO_URL}" ]; then
    echo "[archon-init] Syncing Beads state from GitHub..."
    # Inject OAuth2 token into the HTTPS URL for Dolt-over-git transport
    AUTH_URL="$(echo "${REPO_URL}" | sed "s|https://|git+https://oauth2:${GITHUB_TOKEN}@|")"
    bd dolt remote add origin "${AUTH_URL}" 2>/dev/null || true
    bd dolt pull || echo "[archon-init] Warning: Dolt pull failed (may be first sync)"
else
    echo "[archon-init] Skipping GitHub sync (GITHUB_TOKEN and/or REPO_URL not provided)"
fi

echo "[archon-init] Initialization complete. Starting Archon..."
exec /archon
