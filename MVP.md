> **STATUS: COMPLETE** — The MVP loop was proven on 2026-03-17. Legion autonomously implemented,
> reviewed, and merged PR #8 without human intervention. See `docs/ROADMAP.md` for what comes next.

# Legion MVP

> **Goal:** Archon picks up a Beads issue and a Wraith completes it autonomously.

---

## Scope

Three Go binaries. One Docker Compose stack. One agent backend.

| Component         | What it does                                                        |
| ----------------- | ------------------------------------------------------------------- |
| **Archon**        | Polls Beads for READY issues → spawns Docker container → watches it |
| **Vessel Driver** | Reads assigned issue → starts ACP session → writes traces → exits   |
| **`lg` CLI**      | `lg invoke "task"` creates an issue; `lg status` shows state        |

### What's In

- Archon: Pulse loop (spawn) + Watcher loop (monitor)
- Single identity: Wraith (code worker)
- Single ACP backend: GitHub Copilot (swap later by changing image)
- Local Docker Compose (Dolt + Archon + Vessel containers)
- Git branches for isolation (`legion/<issue-id>`)
- Issue lifecycle: `open → in_progress → closed | failed`
- Agent output streamed to Beads as execution traces

### What's Cut

| Feature                | Why cut                                    | When to add                |
| ---------------------- | ------------------------------------------ | -------------------------- |
| Hierophant (planner)   | Human writes molecules for now             | When manual gets tedious   |
| Inquisitor (reviewer)  | Human reviews                              | After Wraith proves useful |
| Gauntlet (CI)          | Human runs tests                           | After first successful run |
| Alchemist (merge)      | Human merges                               | With Refinery              |
| Formulas / Convoys     | No templated workflows yet                 | After MVP validates        |
| Worktrees              | Plain `git checkout -b` is fine            | At scale                   |
| Refinery (merge queue) | Single Wraith doesn't need a queue         | With parallelism           |
| Wasteland federation   | No inter-rig work                          | Way later                  |
| Preflight validation   | Trust that the image works                 | Before production          |
| Kubernetes             | Docker Compose is enough to prove the loop | When ready for real deploy |
| Multiple backends      | One backend proves ACP works               | Trivial to add later       |

---

## Architecture

```
Human                       lg CLI                    Beads (Dolt)
  │                           │                           │
  │── lg invoke "fix bug" ───►│── bd create ─────────────►│
  │                           │                           │
  │                           │                    Archon (Pulse loop)
  │                           │                           │
  │                           │            polls READY ◄──│
  │                           │                           │
  │                           │            spawns ────────►  Docker container
  │                           │                              │
  │                           │                         Vessel Driver
  │                           │                              │
  │                           │                         1. Read issue from Beads
  │                           │                         2. git clone + checkout branch
  │                           │                         3. Start ACP session (Copilot)
  │                           │                         4. Wraith works on issue
  │                           │                         5. Write traces to Beads
  │                           │                         6. Push branch, mark done, exit
  │                           │                              │
  │                           │                    Archon (Watcher loop)
  │                           │                           │
  │                           │            detects done ◄──│
  │                           │            updates Beads ──►│
  │                           │                           │
  │── lg status ─────────────►│── bd list ───────────────►│
  │◄── shows done ────────────│◄─────────────────────────│
  │                           │                           │
  │── (human reviews + merges branch)                     │
```

---

## Components

### 1. Archon

A Go binary that runs two loops:

**Pulse Loop** (every 5s):

1. Query Beads: `bd ready --json`
2. For each READY issue not already running:
   - Set status to `in_progress`, set `assigned_to`
   - `docker run` the vessel image with env vars:
     - `ISSUE_ID` — which issue to work on
     - `DOLT_DSN` — Dolt connection string
     - `REPO_URL` — Git repo to clone
     - `GITHUB_TOKEN` — agent credential

**Watcher Loop** (every 10s):

1. List running vessel containers
2. For each: check if still alive
3. If exited 0: issue already marked done by vessel driver
4. If exited non-zero: mark issue `failed` in Beads
5. If running too long (>1hr default): mark `stuck`

**Not a K8s controller.** No `controller-runtime`, no reconciliation, no CRDs. Just a Go binary that shells out to `docker` and `bd`. Keep it stupid simple.

### 2. Vessel Driver

A Go binary baked into the vessel container image. Runs on container start:

```
1. Read ISSUE_ID from env
2. bd show $ISSUE_ID --json  →  get title, description, acceptance criteria
3. git clone $REPO_URL /workspace
4. git checkout -b legion/$ISSUE_ID
5. Start ACP server:  copilot --acp --stdio  (NDJSON over stdio)
6. ACP: InitializeRequest
7. ACP: NewSessionRequest (cwd=/workspace, inject beads MCP)
8. ACP: PromptRequest with issue description as user message
9. Stream SessionUpdate events → write traces to Beads via bd
10. On PromptResponse (end_turn):
    - git add + commit on branch
    - git push origin legion/$ISSUE_ID
    - bd close $ISSUE_ID --reason "completed"
    - exit 0
11. On error:
    - bd update $ISSUE_ID --status failed
    - exit 1
```

### 3. `lg` CLI

Thin wrapper around `bd` with Legion-specific defaults:

```bash
lg invoke "Fix the login bug"
  # → bd create "Fix the login bug" --description="..." -t task --json

lg status
  # → bd list --status open --json  (formatted as a table)

lg log <issue-id>
  # → shows traces for that issue (query Dolt directly or bd show)
```

That's it for MVP. Three commands.

---

## Docker Compose

```yaml
services:
  dolt:
    image: dolthub/dolt-sql-server:latest
    ports:
      - "3307:3306"
    volumes:
      - dolt-data:/var/lib/dolt

  archon:
    build:
      context: .
      dockerfile: cmd/archon/Dockerfile
    environment:
      - DOLT_DSN=mysql://root@dolt:3306/legion
      - REPO_URL=${REPO_URL}
      - VESSEL_IMAGE=legion/vessel-copilot:latest
    depends_on:
      - dolt
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock

volumes:
  dolt-data:
```

---

## Vessel Image

```dockerfile
# legion/vessel-copilot
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    git curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Beads CLI
RUN curl -L https://github.com/steveyegge/beads/releases/latest/download/bd-linux-amd64 \
    -o /usr/local/bin/bd && chmod +x /usr/local/bin/bd

# Copilot CLI — standalone binary, started as ACP server via `copilot --acp --stdio`
# https://docs.github.com/en/copilot/reference/copilot-cli-reference/acp-server
# Install path TBD: check https://github.com/github/copilot-cli for release artifacts
COPY copilot /usr/local/bin/copilot

# Vessel Driver (ACP client)
COPY vessel-driver /usr/local/bin/vessel-driver

ENTRYPOINT ["/usr/local/bin/vessel-driver"]
```

---

## Project Layout

```
legion/
├── MVP.md                  ← this file
├── cmd/
│   ├── archon/             ← Archon binary
│   │   └── main.go
│   ├── vessel-driver/      ← Vessel Driver binary
│   │   └── main.go
│   └── lg/                 ← lg CLI
│       └── main.go
├── internal/
│   └── acp/                ← ACP JSON-RPC client (~200 lines)
│       └── client.go
├── docker-compose.yml
├── Dockerfile.vessel-copilot
└── archive/                   ← design docs (archive)
```

---

## Milestones

1. **Archon spawns a container** — Pulse loop creates a Docker container when a READY issue exists. Container runs, exits. Archon detects exit.

2. **Vessel Driver talks ACP** — Driver starts Copilot, sends InitializeRequest, gets capabilities back. Prove the stdio JSON-RPC works.

3. **End-to-end: issue in, branch out** — `lg invoke "add a README"` → Archon spawns Wraith → Wraith writes README → pushes branch → issue closed. Human reviews + merges.

---

## Success Criteria

A human runs:

```bash
lg invoke "Add a health check endpoint to the API server"
```

And later sees:

```bash
lg status
# bd-42  "Add a health check endpoint..."  closed  ✓

git branch -r
# origin/legion/bd-42
```

The branch contains a working health check endpoint. Human merges it.

**That's the MVP.**
