#!/usr/bin/env bash
# hooks/lib.sh — utility library for vessel hook scripts.
#
# PURPOSE: Provides shared helper functions (logging, result writing, env
# validation) for per-role hook scripts under hooks/<role>/.
#
# DISPATCH IS NOT HERE. Dispatch (which hooks to run and in what order) is
# handled exclusively by vessel-driver (Go). Do not add run_hooks, dispatch,
# or pipeline-orchestration functions to this file.
#
# USAGE: Source this file at the top of a hook script:
#
#   source /hooks/lib.sh
#
# FUNCTIONS:
#   legion_log LEVEL "message"         — formatted log line to stderr
#   legion_write_result STATUS ID MSG  — writes /workspace/result.json
#   legion_require_env VAR [VAR ...]   — exits 1 if any var is unset or empty

# ── legion_log ────────────────────────────────────────────────────────────────
# Writes a formatted log line to stderr.
#
# Usage:   legion_log INFO|WARN|ERROR "message text"
# Output:  [LEGION][INFO] message text
#
# All output goes to stderr so it does not pollute stdout (which some callers
# may parse as structured data).
legion_log() {
    local level="${1:-INFO}"
    local message="${2:-}"
    echo "[LEGION][${level}] ${message}" >&2
}

# ── legion_write_result ───────────────────────────────────────────────────────
# Writes /workspace/result.json with the standard vessel result schema.
#
# Usage:   legion_write_result STATUS ISSUE_ID ERROR_MSG
#
# Arguments:
#   STATUS     — "success" | "error" | "blocked" (any string vessel-driver expects)
#   ISSUE_ID   — the Beads issue ID (e.g. "ISSUE-123")
#   ERROR_MSG  — error description; pass "" on the success path
#
# Example:
#   legion_write_result success ISSUE-123 ""
#   legion_write_result error   ISSUE-456 "git push failed"
legion_write_result() {
    local status="${1:-error}"
    local issue_id="${2:-}"
    local error_msg="${3:-}"

    mkdir -p /workspace

    # Use printf for portable JSON construction without requiring jq at write time.
    printf '{"status":"%s","issue_id":"%s","error_message":"%s"}\n' \
        "$status" "$issue_id" "$error_msg" \
        > /workspace/result.json
}

# ── legion_require_env ────────────────────────────────────────────────────────
# Validates that all named environment variables are set and non-empty.
# Exits 1 on the first missing variable after logging the name.
#
# Usage:   legion_require_env VAR1 VAR2 ...
#
# Example:
#   legion_require_env LEGION_CONFIG_JSON GITHUB_TOKEN BEADS_DOLT_SERVER_HOST
legion_require_env() {
    local var
    for var in "$@"; do
        if [ -z "${!var:-}" ]; then
            legion_log ERROR "required env var '${var}' is unset or empty"
            exit 1
        fi
    done
}
