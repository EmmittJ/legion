#!/bin/sh

# Initialize Beads and point it at the external Dolt SQL server.
# BEADS_DOLT_SERVER_HOST and BEADS_DOLT_SERVER_PORT must be set (provided by docker-compose.yml).

if [ ! -f ".beads/config.yaml" ]; then
    bd init || true

    # Override the default 127.0.0.1 connection to use the external Dolt container.
    DOLT_HOST="${BEADS_DOLT_SERVER_HOST:-dolt}"
    DOLT_PORT="${BEADS_DOLT_SERVER_PORT:-3306}"
    DOLT_USER="${BEADS_DOLT_SERVER_USER:-root}"

    bd dolt set host "${DOLT_HOST}" || true
    bd dolt set port "${DOLT_PORT}" || true
    bd dolt set user "${DOLT_USER}" || true

    if [ -n "${REPO_URL}" ]; then
        bd dolt remote add origin "${REPO_URL}" || true
    fi
fi

exec /archon
