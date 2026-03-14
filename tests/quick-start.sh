#!/bin/bash
# Quick-start for MVP.1 spawn cycle test
# Run from repo root: bash tests/quick-start.sh

set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "╔════════════════════════════════════════════════════════════╗"
echo "║  Legion MVP.1 Spawn Cycle Test — Quick Start              ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""
echo "Prerequisites:"
echo "  1. Docker & Docker Compose installed"
echo "  2. bd (Beads CLI) on PATH"
echo "  3. lg (Legion CLI) on PATH"
echo ""

# Check for docker compose
if ! command -v docker-compose &>/dev/null && ! command -v docker &>/dev/null; then
    echo "❌ Docker / Docker Compose not found. Install and retry."
    exit 1
fi

echo "✓ Docker found"

# Check for bd
if ! command -v bd &>/dev/null; then
    echo "❌ bd (Beads CLI) not found. Install from https://github.com/steveyegge/beads"
    exit 1
fi

echo "✓ Beads CLI (bd) found"

# Check for lg
if ! command -v lg &>/dev/null; then
    echo "❌ lg (Legion CLI) not found. Build: go build -o lg ./cmd/lg"
    exit 1
fi

echo "✓ Legion CLI (lg) found"
echo ""

# Check docker compose status
echo "Checking Docker Compose stack..."
if ! docker-compose -f "${REPO_ROOT}/docker-compose.yml" ps --services 2>/dev/null | grep -q dolt; then
    echo ""
    echo "⚠ Docker Compose stack not running."
    echo "Start it with:"
    echo ""
    echo "  docker compose up -d"
    echo ""
    exit 1
fi

echo "✓ Docker Compose stack is running"
echo ""

# Run the test
echo "Starting MVP.1 Spawn Cycle Test..."
echo ""

if bash "${REPO_ROOT}/tests/spawn-cycle.sh"; then
    echo ""
    echo "╔════════════════════════════════════════════════════════════╗"
    echo "║ ✓ MVP.1 Test PASSED                                       ║"
    echo "╚════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Next steps:"
    echo "  - Review logs in .test-logs/"
    echo "  - Check the issue traces: lg log <issue-id>"
    echo "  - Verify branch was pushed: git branch -r"
    echo ""
else
    echo ""
    echo "╔════════════════════════════════════════════════════════════╗"
    echo "║ ❌ MVP.1 Test FAILED                                       ║"
    echo "╚════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Diagnostics:"
    echo "  - Review logs in .test-logs/"
    echo "  - Check docker logs: docker compose logs archon"
    echo "  - Re-run with DEBUG=1 bash tests/spawn-cycle.sh"
    echo ""
    exit 1
fi
