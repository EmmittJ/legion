#!/bin/sh
set -e

# Initialize Beads against the external Dolt SQL server if not already done.
# BEADS_DOLT_SERVER_HOST and BEADS_DOLT_SERVER_PORT must be set (provided by docker-compose.yml).
if [ ! -f ".beads/config.yaml" ]; then
    bd init
    if [ -n "${REPO_URL}" ]; then
        bd dolt remote add origin "${REPO_URL}"
    fi
fi

exec /archon
