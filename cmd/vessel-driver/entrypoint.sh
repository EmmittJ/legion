#!/usr/bin/env bash
# entrypoint.sh — thin env shim; execs vessel-driver.
#
# Responsibility: wire Archon-injected env vars and exec the vessel-driver binary.
# vessel-driver owns the full lifecycle (hook dispatch, ACP session, result writing).
# Do NOT add pre-run, post-run, or run_hooks logic here.

set -euo pipefail

# Wire DOLT_HOST/PORT → BEADS_DOLT_SERVER_* so vessel-driver and any hook scripts
# it invokes can reach the Dolt server without re-parsing LEGION_CONFIG_JSON.
export BEADS_DOLT_SERVER_HOST="${DOLT_HOST:-dolt}"
export BEADS_DOLT_SERVER_PORT="${DOLT_PORT:-3306}"

# Copilot CLI expects GH_TOKEN; Archon injects GITHUB_TOKEN — alias it here.
export GH_TOKEN="${GITHUB_TOKEN}"

# Derive LEGION_ROLE from LEGION_CONFIG_JSON for hooks that need it.
# Defaults to "worker" when role_name is absent (backwards-compatible).
export LEGION_ROLE=$(echo "$LEGION_CONFIG_JSON" | jq -r '.role_name // "worker"')

exec /vessel-driver
