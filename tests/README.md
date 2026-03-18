# Legion Tests

Integration tests for Legion: Archon pulse/watcher loops, Vessel Driver ACP client, and `lg` CLI.

## Directory Structure

```
tests/
├── README.md              ← this file
├── spawn-cycle.sh         ← spawn cycle validation: Archon spawns and monitors vessel
└── .../                   ← integration tests TBD (end-to-end, review cycle)
```

## Spawn Cycle Test

**File:** `spawn-cycle.sh`

**What it tests:**
- Archon pulse loop picks up a READY issue from Beads
- Archon spawns a vessel container with correct env vars
- Vessel container starts, runs vessel-driver, and exits
- Archon watcher loop detects the exit
- Issue status transitions: `open` → `in_progress` → `closed`/`failed`
- Issue traces contain expected execution markers

**Usage:**

```bash
# From repo root, with docker compose stack already running:
bash tests/spawn-cycle.sh

# With debug output:
DEBUG=1 bash tests/spawn-cycle.sh

# Custom timeouts (in seconds):
TIMEOUT_SPAWN=120 TIMEOUT_COMPLETE=600 bash tests/spawn-cycle.sh
```

**Prerequisites:**
- Docker and Docker Compose installed and running
- Legion stack running: `docker compose up`
- `bd` (Beads CLI) on PATH
- `lg` (Legion CLI) on PATH

**Output:**
- `PASS` or `FAIL` exit code
- Logs written to `.test-logs/test-YYYYMMDD_HHMMSS.log`
- Archon container logs: `.test-logs/archon-YYYYMMDD_HHMMSS.log`
- Docker Compose logs: `.test-logs/docker-compose-YYYYMMDD_HHMMSS.log`

**Exit Codes:**
- `0` — Test passed
- `1` — Test failed (check `.test-logs/` for diagnostics)

## Test Patterns

Each test follows this structure:

1. **Prerequisites check** — ensure docker, bd, lg are available
2. **Setup** — create an issue, establish test context
3. **Execution** — run the behavior being tested
4. **Validation** — assert expected state transitions and side effects
5. **Cleanup & Logs** — save diagnostic logs for debugging

Tests use:
- `log`, `log_pass`, `log_fail`, `log_debug` for consistent output
- Timestamps and color coding for readability
- `.test-logs/` directory for all artifacts

## Future: Integration Tests

TODO:
- **vessel-acp.sh** — Vessel Driver ACP client validation (message framing, error paths)
- **end-to-end.sh** — Full cycle: issue → vessel → traces → branch → closed
- **lg-cli.sh** — `lg invoke`, `lg status`, `lg log` command validation

## Running Tests in CI

Add to GitHub Actions:

```yaml
- name: Run spawn cycle test
  run: |
    docker compose up -d
    bash tests/spawn-cycle.sh
    docker compose logs archon
```

Andariel will define CI integration requirements; Azmodan will wire up the GitHub Actions workflow once the test harness is validated.
