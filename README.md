# Legion

Legion is an autonomous coding agent system. You file an issue; Legion works it. Archon polls [Beads](https://github.com/steveyegge/beads) for ready issues, spawns a Docker vessel container for each one, and the vessel runs GitHub Copilot CLI via the ACP JSON-RPC protocol. The vessel clones your repo, checks out a `legion/<issue-id>` branch, drives Copilot to implement the task, pushes the branch, and closes the issue. You review and merge.

---

## Architecture

| Binary | Role | Connects to |
|---|---|---|
| **`archon`** | Pulse loop: polls Beads for `ready` issues, spawns vessel containers. Watcher loop: detects exits, marks issues `closed` or `failed`. | Beads (via `bd`), Docker socket |
| **`vessel-driver`** | Runs inside the vessel container. Reads the issue from Beads, starts a Copilot ACP session over stdio, drives the agent, writes traces back to Beads, pushes the branch. | Beads (via `bd`), Copilot CLI (ACP/stdio), Git |
| **`lg`** | Operator CLI. Three commands: create issues, watch status, read traces. Shells out to `bd`. | Beads (via `bd`) |

```
Operator          lg CLI               Beads (Dolt)          Archon
   │                │                       │                   │
   ├─ lg invoke ───►├─ bd create ──────────►│                   │
   │                │                       │◄── pulse (5s) ────┤
   │                │                       │                   ├─ docker run vessel
   │                │                       │                   │       │
   │                │                       │◄── bd show ───────┼── vessel-driver
   │                │                       │◄── bd trace ──────┤       │
   │                │                       │◄── bd close ──────┼───────┤
   │                │                       │                   │◄── watcher (10s)
   ├─ lg status ───►├─ bd list ────────────►│                   │
   ├─ lg log <id> ──►├─ bd show ────────────►│                   │
```

---

## Prerequisites

| Requirement | Notes |
|---|---|
| Docker + Docker Compose | Vessel containers and the Archon service run in Docker. |
| [`bd` CLI](https://github.com/steveyegge/beads) | On `PATH`. Beads is the issue and trace store. |
| GitHub token | Needs Copilot access. Set as `GITHUB_TOKEN`. |
| Initialized Beads repo | Run `bd init` in your repo before starting Legion. |
| Git | Available inside vessel containers and on the host for reviewing results. |

**Windows users:** See [**WSL Setup Guide**](docs/SETUP-WSL.md) for instructions on running the test harness in Windows Subsystem for Linux (WSL2).

---

## Quick Start

```bash
# 1. Start Dolt (database) and Archon
REPO_URL=https://github.com/your-org/your-repo \
GITHUB_TOKEN=ghp_xxx \
docker compose up -d

# 2. File a task — Archon picks it up automatically
lg invoke "Add a health check endpoint to the API server"
# Created issue: legion-4ab

# 3. Watch it work
lg status
# ID          TITLE                                        STATUS       ASSIGNED_TO
# ──          ─────                                        ──────       ───────────
# legion-4ab  Add a health check endpoint to the API...   in_progress  wraith

# 4. Read the vessel's trace when it's done
lg log legion-4ab
# Issue: legion-4ab — Add a health check endpoint to the API server [closed]
# Traces (12):
#   [1] 2025-01-15T14:23:01Z  Starting ACP session
#   [2] 2025-01-15T14:23:04Z  Cloned repo, checked out legion/legion-4ab
#   ...

# 5. Review and merge the branch
git fetch origin
git checkout legion/legion-4ab
# review, test, merge
```

---

## Configuration

All configuration is passed as environment variables to `docker compose`.

| Variable | Required | Default | Description |
|---|---|---|---|
| `REPO_URL` | ✓ | — | Git remote URL for vessels to clone. |
| `GITHUB_TOKEN` | ✓ | — | GitHub PAT with Copilot access. Injected into vessel containers. |
| `VESSEL_IMAGE` | | `legion/vessel-copilot:latest` | Docker image to spawn for each issue. |
| `ARCHON_TIMEOUT` | | `3600` | Seconds before Archon marks a vessel as `stuck`. |
| `VESSEL_TIMEOUT` | | — | Per-vessel timeout (set inside the image; inherits `ARCHON_TIMEOUT`). |
| `VESSEL_MODEL` | | `gpt-5-mini` | Copilot model passed to the ACP session. |

---

## Building from Source

**Requirements:** Go 1.21+, Docker.

```bash
# Build all three binaries (host OS)
go build -o archon.exe        ./cmd/archon
go build -o vessel-driver.exe ./cmd/vessel-driver
go build -o lg.exe            ./cmd/lg

# Build the vessel image
# vessel-driver must be built as a Linux binary first
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o vessel-driver ./cmd/vessel-driver
docker build -f Dockerfile.vessel-copilot -t legion/vessel-copilot:latest .

# Build the Archon service image (used by docker compose)
docker compose build
```

`lg` can be run directly from the host — it only needs `bd` on `PATH`.

---

## Troubleshooting & Container Logs

During development, view container logs to debug Archon, Dolt, or vessel issues:

```bash
# All services
docker compose logs -f

# Specific service
docker compose logs -f archon
docker compose logs -f dolt

# Common errors
docker compose logs archon | grep -i error
```

For detailed debugging guide, health checks, and error patterns, see [**`docs/TROUBLESHOOTING.md`**](docs/TROUBLESHOOTING.md).

---

## Open Issues / Known Limitations

The autonomous loop is proven. See docs/ROADMAP.md for what comes next.

| Gap | Status |
|---|---|
| Auto-merge via Inquisitor | Enabled — Inquisitor reviews and merges PRs autonomously |
| Human writes task descriptions | No planner agent yet |
| No CI gating | Human runs tests after reviewing the branch |
| Single vessel at a time | Archon will queue if a second issue goes ready during an active run |
| No retry on ACP failure | Failed vessels mark the issue `failed`; re-queue manually with `bd update <id> --status=open` |
| Linux vessels only | `vessel-driver` is built for `linux/amd64`; Archon runs wherever Docker is available |

Track work in Beads: `bd list` to see all issues, `bd create "..." --type=task` to file new ones.
