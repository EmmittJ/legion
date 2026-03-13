#!/bin/sh

# Initialize Beads with embedded Dolt and sync state from GitHub (refs/dolt/data).
# REPO_URL and GITHUB_TOKEN must be set (provided by docker-compose.yml).

if [ ! -f ".beads/config.yaml" ]; then
    bd init || true

    if [ -n "${GITHUB_TOKEN}" ] && [ -n "${REPO_URL}" ]; then
        # Inject OAuth2 token into the HTTPS URL for Dolt-over-git transport.
        AUTH_URL="$(echo "${REPO_URL}" | sed "s|https://|git+https://oauth2:${GITHUB_TOKEN}@|")"
        bd dolt remote add origin "${AUTH_URL}" || true
        bd dolt pull || true
    fi
fi

exec /archon
