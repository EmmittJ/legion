# Legion Agent Team

> The Seven Great Evils, assembled to build Legion.
> Mephisto is the orchestrator. Route all requests to him first.

## Team Roster

| Character | Role | File | Use For |
|---|---|---|---|
| **Mephisto** | Orchestrator | `.github/agents/mephisto.agent.md` | Default — all requests start here |
| **Andariel** | Architect/Design Lead | `.github/agents/andariel.agent.md` | Architecture, acceptance criteria, component contracts, peer review |
| **Diablo** | Archon Binary Engineer | `.github/agents/diablo.agent.md` | Archon Go binary: pulse/watcher loops, Docker spawning |
| **Baal** | Vessel Driver Engineer | `.github/agents/baal.agent.md` | Vessel-driver binary: ACP client, git ops, container entrypoint |
| **Azmodan** | Platform/DevOps Engineer | `.github/agents/azmodan.agent.md` | Docker Compose, Dockerfiles, vessel image hierarchy |
| **Belial** | Operator CLI Engineer | `.github/agents/belial.agent.md` | `lg` CLI binary: invoke, status, log subcommands |
| **Duriel** | Scribe | `.github/agents/duriel.agent.md` | Commits, branches, pull requests |

## Constitutional Rules

1. **Mephisto orchestrates — always.** Never invoke a specialist directly for new work; route through Mephisto.
2. **Andariel designs before builders implement.** Nothing gets built without acceptance criteria and component contracts.
3. **Peer review before Duriel commits.** Any specialist who did not author the change reviews it — Andariel preferred for cross-domain.
4. **Nothing is implemented without a brief.** Builders work from explicit briefs from Mephisto — not vibes.
5. **Beads is the source of truth.** All work is tracked as Beads issues. `bd ready` before planning anything.
6. **One logical change per commit.** Duriel does not bundle unrelated work.
7. **The `lg` CLI is the human interface.** `bd` is for the team. `lg` is for humans.
8. **Documentation lives in `docs/`.** User-facing guides, architecture docs, setup guides → `docs/*.md`. No transient markdown files, no temporary planning docs in the repo root.

## Skills

- `.github/skills/beads/` — persistent memory, issue tracking, agent coordination
- `.github/skills/routing/` — team roster and routing rules
- `.github/skills/work-cycle/` — session start/end discipline

## File Ownership

This repo is maintained by Legion's own agents. Two categories of files exist:

### Shipped to end users (via `lg init`)

These files are embedded in the `lg` binary and copied to the user's repo when they run `lg init`. **Keep them generic and portable — no repo-specific content.**

| Source (in this repo) | Destination (in user's repo) |
|---|---|
| `cmd/lg/pacts/agents/wraith.agent.md` | `.github/agents/wraith.agent.md` |
| `cmd/lg/pacts/agents/hierophant.agent.md` | `.github/agents/hierophant.agent.md` |
| `cmd/lg/pacts/agents/oracle.agent.md` | `.github/agents/oracle.agent.md` |
| `cmd/lg/pacts/agents/inquisitor.agent.md` | `.github/agents/inquisitor.agent.md` |
| `cmd/lg/pacts/agents/hermes.agent.md` | `.github/agents/hermes.agent.md` |
| `cmd/lg/pacts/config/archon.toml` | `.legion/archon.toml` |
| `cmd/lg/pacts/config/routes.toml` | `.legion/routes.toml` |
| `cmd/lg/pacts/skills/legion/SKILL.md` | `.github/skills/legion/SKILL.md` |

### Legion-owned only (not shipped)

These files are specific to this repo. They may contain Legion-specific content, directory layouts, and conventions.

**Agent pacts (Legion's team):**
- `.github/agents/mephisto.agent.md` — orchestrator
- `.github/agents/andariel.agent.md` — architect/design lead
- `.github/agents/diablo.agent.md` — Archon binary engineer
- `.github/agents/baal.agent.md` — vessel-driver engineer
- `.github/agents/azmodan.agent.md` — platform/DevOps engineer
- `.github/agents/belial.agent.md` — operator CLI engineer
- `.github/agents/duriel.agent.md` — scribe

**Local customizations of shipped pacts** (override the portable defaults):
- `.github/agents/wraith.agent.md` — Legion's wraith with codebase orientation
- `.github/agents/hierophant.agent.md` — Legion's hierophant
- `.github/agents/oracle.agent.md` — Legion's oracle
- `.github/agents/inquisitor.agent.md` — Legion's inquisitor
- `.github/agents/hermes.agent.md` — Legion's hermes

**Skills (all Legion-internal):**
- `.github/skills/beads/` — persistent memory, issue tracking
- `.github/skills/routing/` — team roster and routing rules
- `.github/skills/work-cycle/` — session start/end discipline
- `.github/skills/acp-protocol/` — ACP JSON-RPC implementation guide
- `.github/skills/conventional-commits/` — commit message conventions
- `.github/skills/docker-best-practices/` — Docker patterns
- `.github/skills/git-best-practices/` — git workflow
- `.github/skills/github-actions/` — CI workflow patterns
- `.github/skills/go-best-practices/` — idiomatic Go patterns
- `.github/skills/orchestrate/` — orchestration patterns

**Rule: when editing `cmd/lg/pacts/` files, write for a generic repo. When editing `.github/agents/` files, Legion-specific content is fine.**

## Project

This team implements **Legion MVP** as defined in `MVP.md`.

Target: `lg invoke "Add a health check endpoint"` → Archon spawns Wraith → Wraith pushes branch → issue closed.

<!-- BEGIN BEADS INTEGRATION -->
## Issue Tracking with bd (beads)

**IMPORTANT**: This project uses **bd (beads)** for ALL issue tracking. Do NOT use markdown TODOs, task lists, or other tracking methods.

### Why bd?

- Dependency-aware: Track blockers and relationships between issues
- Git-friendly: Dolt-powered version control with native sync
- Agent-optimized: JSON output, ready work detection, discovered-from links
- Prevents duplicate tracking systems and confusion

### Quick Start

**Check for ready work:**

```bash
bd ready --json
```

**Create new issues:**

```bash
bd create "Issue title" --description="Detailed context" -t bug|feature|task -p 0-4 --json
bd create "Issue title" --description="What this issue is about" -p 1 --deps discovered-from:bd-123 --json
```

**Claim and update:**

```bash
bd update <id> --claim --json
bd update bd-42 --priority 1 --json
```

**Complete work:**

```bash
bd close bd-42 --reason "Completed" --json
```

### Issue Types

- `bug` - Something broken
- `feature` - New functionality
- `task` - Work item (tests, docs, refactoring)
- `epic` - Large feature with subtasks
- `chore` - Maintenance (dependencies, tooling)

### Priorities

- `0` - Critical (security, data loss, broken builds)
- `1` - High (major features, important bugs)
- `2` - Medium (default, nice-to-have)
- `3` - Low (polish, optimization)
- `4` - Backlog (future ideas)

### Workflow for AI Agents

1. **Check ready work**: `bd ready` shows unblocked issues
2. **Claim your task atomically**: `bd update <id> --claim`
3. **Work on it**: Implement, test, document
4. **Discover new work?** Create linked issue:
   - `bd create "Found bug" --description="Details about what was found" -p 1 --deps discovered-from:<parent-id>`
5. **Complete**: `bd close <id> --reason "Done"`

### Beads Storage Model

Beads uses a **local Dolt database** (`.beads/dolt/`) with the GitHub repo as the git remote. There is no separate Dolt SQL server to run or manage.

- The Dolt database lives at `.beads/dolt/lg/` — version-controlled, git-backed
- The GitHub repo (`refs/dolt/data`) is the remote — same repo, no extra infra
- Each `bd` write auto-commits to local Dolt history
- Sync with `bd dolt push` / `bd dolt pull`

**For Docker containers (Archon, vessels):**

Each container initialises its own local Dolt at startup by pulling from the GitHub remote:

```sh
bd init                                              # create local db on first boot
bd dolt remote add origin git+https://github.com/EmmittJ/legion.git
bd dolt pull                                         # get current issues
# ... do work ...
bd dolt push                                         # publish changes back
```

`gh auth setup-git` (called with `GH_TOKEN` set) handles HTTPS auth — no token in URLs.

**Never** run a shared Dolt SQL server or mount the host's `.beads/` into a container. Each environment manages its own local copy; GitHub is the coordination point.

See: https://docs.dolthub.com/concepts/dolt/git/remotes

### Important Rules

- ✅ Use bd for ALL task tracking
- ✅ Always use `--json` flag for programmatic use
- ✅ Link discovered work with `discovered-from` dependencies
- ✅ Check `bd ready` before asking "what should I work on?"
- ❌ Do NOT create markdown TODO lists
- ❌ Do NOT use external issue trackers
- ❌ Do NOT duplicate tracking systems

For more details, see README.md and docs/QUICKSTART.md.

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds

<!-- END BEADS INTEGRATION -->
