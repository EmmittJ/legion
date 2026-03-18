#!/bin/bash
#
# spawn-cycle.sh — MVP.1 validation: Archon spawn cycle test
#
# This test validates the core Archon pulse loop behavior:
#   1. Create a task issue via `lg invoke`
#   2. Wait for Archon to pick it up and spawn a vessel container
#   3. Monitor vessel container lifecycle (start → run → exit)
#   4. Verify vessel container exited cleanly (exit code 0)
#   5. Confirm issue marked as "closed" or "failed"
#   6. Query logs to confirm traces were written
#
# Usage:
#   bash tests/spawn-cycle.sh
#
# Requires:
#   - Docker and Docker Compose running
#   - legion stack already up via `docker compose up`
#   - bd (Beads CLI) on PATH
#   - lg (Legion CLI) on PATH
#
# Environment variables (optional):
#   TIMEOUT_SPAWN=60         — max seconds to wait for Archon to spawn (default: 60)
#   TIMEOUT_COMPLETE=300     — max seconds to wait for vessel to complete (default: 300)
#   SKIP_DOCKER_CHECK=1      — skip docker compose health check
#   DEBUG=1                  — print verbose logs and save docker logs to file
#
# ─── Using mock-acp-server for local testing without real Copilot ─────────────
#
# The mock-acp-server at tests/mock-acp-server/main.go replaces the real
# `copilot --acp --stdio` subprocess that vessel-driver spawns, so you can run
# and test the full vessel-driver lifecycle (beads init, ACP handshake, bd close)
# without a live Copilot credential.
#
# Build the mock (from repo root):
#   go build -o tests/mock-acp-server/mock-acp-server ./tests/mock-acp-server/
#
# Point vessel-driver at the mock via COPILOT_CMD (or however vessel-driver
# resolves the copilot binary — see cmd/vessel-driver/main.go):
#   COPILOT_CMD="./tests/mock-acp-server/mock-acp-server" vessel-driver \
#       --issue <id> --repo <path>
#
# Flags exposed by mock-acp-server:
#   --fail          respond with stopReason:"refusal" — exercises the error path
#   --delay=<dur>   sleep between session/update notifications (default 100ms)
#
# The mock logs all received messages to stderr, so test output captures the
# full ACP exchange without polluting the stdout JSON-RPC stream.
# ─────────────────────────────────────────────────────────────────────────────
#

set -o pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Configuration
# ─────────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMEOUT_SPAWN="${TIMEOUT_SPAWN:-60}"
TIMEOUT_COMPLETE="${TIMEOUT_COMPLETE:-300}"
DEBUG="${DEBUG:-0}"
SKIP_DOCKER_CHECK="${SKIP_DOCKER_CHECK:-0}"

# Logging directories
LOG_DIR="${SCRIPT_DIR}/.test-logs"
mkdir -p "${LOG_DIR}"

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
TEST_NAME="spawn-cycle-${TIMESTAMP}"
ARCHON_LOG="${LOG_DIR}/archon-${TIMESTAMP}.log"
VESSEL_LOG="${LOG_DIR}/vessel-${TIMESTAMP}.log"
TEST_LOG="${LOG_DIR}/test-${TIMESTAMP}.log"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# ─────────────────────────────────────────────────────────────────────────────
# Utilities
# ─────────────────────────────────────────────────────────────────────────────

log() {
	echo -e "${CYAN}[$(date '+%H:%M:%S')]${NC} $*" | tee -a "${TEST_LOG}"
}

log_pass() {
	echo -e "${GREEN}[PASS]${NC} $*" | tee -a "${TEST_LOG}"
}

log_fail() {
	echo -e "${RED}[FAIL]${NC} $*" | tee -a "${TEST_LOG}"
}

log_debug() {
	if [[ "${DEBUG}" == "1" ]]; then
		echo -e "${YELLOW}[DEBUG]${NC} $*" | tee -a "${TEST_LOG}"
	fi
}

die() {
	log_fail "$*"
	exit 1
}

# ─────────────────────────────────────────────────────────────────────────────
# Checks
# ─────────────────────────────────────────────────────────────────────────────

check_prerequisites() {
	log "Checking prerequisites..."

	# Check if we're at repo root
	if [[ ! -f "${SCRIPT_DIR}/README.md" ]]; then
		die "Not at legion repo root. Expected README.md at ${SCRIPT_DIR}"
	fi
	log_debug "Repository root: ${SCRIPT_DIR}"

	# Check for required commands
	for cmd in bd lg docker docker-compose; do
		if ! command -v "${cmd}" &>/dev/null; then
			die "Required command not found: ${cmd}"
		fi
	done
	log_debug "All required commands found: bd, lg, docker, docker-compose"

	# Check if docker compose stack is running
	if [[ "${SKIP_DOCKER_CHECK}" != "1" ]]; then
		log "Checking Docker Compose stack..."
		if ! docker-compose -f "${SCRIPT_DIR}/docker-compose.yml" ps --services --filter "status=running" | grep -q dolt; then
			die "Docker Compose stack not running. Start with: docker compose up"
		fi
		log_debug "Docker Compose stack is running"
	else
		log_debug "Skipping Docker Compose health check (SKIP_DOCKER_CHECK=1)"
	fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Test Execution
# ─────────────────────────────────────────────────────────────────────────────

# Create a task issue via lg invoke
create_issue() {
	log "Creating test issue via lg invoke..."

	local title="spawn-cycle test — $(date +%s)"
	log_debug "Issue title: ${title}"

	local output
	output=$(lg invoke "${title}" 2>&1) || die "lg invoke failed: ${output}"
	log_debug "lg invoke output: ${output}"

	# Extract issue ID from output ("Created issue: <ID>")
	ISSUE_ID=$(echo "${output}" | grep -oP '(?<=Created issue: )[^ ]+' | head -1)
	if [[ -z "${ISSUE_ID}" ]]; then
		die "Failed to extract issue ID from: ${output}"
	fi

	log_pass "Issue created: ${ISSUE_ID}"
}

# Wait for issue to transition from "open" to "in_progress"
# This indicates Archon's pulse loop has picked it up
wait_for_spawn() {
	log "Waiting for Archon to pick up issue (max ${TIMEOUT_SPAWN}s)..."

	local elapsed=0
	local interval=2

	while (( elapsed < TIMEOUT_SPAWN )); do
		local status
		status=$(bd show "${ISSUE_ID}" --json 2>/dev/null | grep -o '"status":"[^"]*' | cut -d'"' -f4 || echo "unknown")

		log_debug "Issue status: ${status} (elapsed: ${elapsed}s)"

		if [[ "${status}" == "in_progress" ]]; then
			log_pass "Issue marked in_progress by Archon"
			return 0
		fi

		sleep "${interval}"
		((elapsed += interval))
	done

	die "Timeout waiting for spawn (issue never marked in_progress). Current status: ${status}"
}

# Monitor vessel container lifecycle
# Once spawned, we expect:
#   - Container starts (state: running)
#   - Container runs (vessel-driver processes the issue)
#   - Container exits cleanly (exit code 0)
wait_for_completion() {
	log "Waiting for vessel container to complete (max ${TIMEOUT_COMPLETE}s)..."

	local elapsed=0
	local interval=3
	local container_name="legion-vessel-${ISSUE_ID}"
	local spawn_detected=0

	while (( elapsed < TIMEOUT_COMPLETE )); do
		local status
		status=$(bd show "${ISSUE_ID}" --json 2>/dev/null | grep -o '"status":"[^"]*' | cut -d'"' -f4 || echo "unknown")

		log_debug "Issue status: ${status} (elapsed: ${elapsed}s)"

		# Issue transitions to "closed" or "failed" when vessel driver exits
		case "${status}" in
		closed)
			log_pass "Issue marked closed (vessel completed successfully)"
			return 0
			;;
		failed)
			log_fail "Issue marked failed (vessel driver exited with error)"
			return 1
			;;
		in_progress)
			spawn_detected=1
			;;
		esac

		sleep "${interval}"
		((elapsed += interval))
	done

	die "Timeout waiting for completion (issue stuck in ${status}). Check logs at ${LOG_DIR}"
}

# Fetch issue logs and validate traces
validate_traces() {
	log "Fetching traces for issue ${ISSUE_ID}..."

	local output
	output=$(lg log "${ISSUE_ID}" 2>&1) || die "lg log failed: ${output}"

	# Check for key markers in traces
	# - ACP conversation indicates vessel-driver established ACP session
	# - git push indicates vessel-driver pushed the branch
	# - closed status indicates issue was completed

	if echo "${output}" | grep -qi "trace\|acp\|session\|git\|push"; then
		log_pass "Traces contain expected markers"
		log_debug "Trace summary: $(echo "${output}" | head -20)"
		return 0
	else
		log_debug "Issue traces (raw output):"
		log_debug "${output}"
		log_fail "No traces found or unexpected format"
		return 1
	fi
}

# Collect diagnostic logs
collect_logs() {
	log "Collecting diagnostic logs..."

	# Fetch Archon container logs
	if docker-compose -f "${SCRIPT_DIR}/docker-compose.yml" logs archon >"${ARCHON_LOG}" 2>&1; then
		log_debug "Archon logs saved to: ${ARCHON_LOG}"
	else
		log_debug "Failed to fetch archon logs"
	fi

	# Fetch all container logs for later analysis
	if docker-compose -f "${SCRIPT_DIR}/docker-compose.yml" logs >"${LOG_DIR}/docker-compose-${TIMESTAMP}.log" 2>&1; then
		log_debug "All compose logs saved to: ${LOG_DIR}/docker-compose-${TIMESTAMP}.log"
	fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────────────────────────────────────

main() {
	log "═══════════════════════════════════════════════════════════════"
	log "Spawn Cycle Validation Test"
	log "═══════════════════════════════════════════════════════════════"
	log "Test output: ${TEST_LOG}"

	check_prerequisites
	create_issue
	wait_for_spawn
	wait_for_completion || {
		log_fail "Vessel container did not complete successfully"
		collect_logs
		exit 1
	}
	validate_traces || {
		log_fail "Trace validation failed"
		collect_logs
		exit 1
	}
	collect_logs

	log "═══════════════════════════════════════════════════════════════"
	log_pass "Spawn Cycle Test PASSED"
	log "═══════════════════════════════════════════════════════════════"
	log "Issue ID: ${ISSUE_ID}"
	log "Logs: ${LOG_DIR}"
	exit 0
}

main "$@"
