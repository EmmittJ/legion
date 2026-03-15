#!/usr/bin/env bash
# entrypoint.sh — top-level vessel pipeline orchestrator.
#
# Runs three stages in order:
#   1. /hooks/pre-run.sh   — clone repo, checkout branch, claim issue, write context
#   2. /vessel-driver      — pure ACP adapter; reads config, runs session, writes result.json
#   3. /hooks/post-run.sh  — commit/push on success; mark failed on error
#
# post-run.sh MUST always execute so the issue is marked correctly regardless of
# whether vessel-driver succeeded or failed.  vessel-driver writes result.json with
# status="error" on any die() path, so post-run.sh reads that and marks the issue
# blocked.  We use `|| true` to prevent set -e from aborting after a non-zero exit.
#
# Env wiring: BEADS_DOLT_SERVER_* are exported here so that every child process
# (pre-run.sh, vessel-driver, post-run.sh) inherits them without each hook having
# to redeclare them.  BEADS_DOLT_SERVER_USER is set per-hook since it does not
# need to be part of the top-level env.

set -euo pipefail

# Wire DOLT_HOST/PORT → BEADS_DOLT_SERVER_* for all bd calls in the pipeline.
# bd v0.60.0+ honours these env vars at startup, overriding config.yaml defaults.
export BEADS_DOLT_SERVER_HOST="${DOLT_HOST:-dolt}"
export BEADS_DOLT_SERVER_PORT="${DOLT_PORT:-3306}"

/hooks/pre-run.sh
/vessel-driver || true   # always continues; result.json carries status="error" on failure
/hooks/post-run.sh       # always runs; exits 1 on error path, 0 on success
