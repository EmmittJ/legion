# Legion Agent Team

> The Seven Great Evils, assembled to build Legion.
> Mephisto is the orchestrator. Route all requests to him first.

## Team Roster

| Character | Role | File | Use For |
|---|---|---|---|
| **Mephisto** | Orchestrator | `.github/agents/mephisto.agent.md` | Default — all requests start here |
| **Diablo** | Archon Binary Engineer | `.github/agents/diablo.agent.md` | Archon Go binary: pulse/watcher loops, Docker spawning |
| **Baal** | Vessel Driver Engineer | `.github/agents/baal.agent.md` | Vessel-driver binary: ACP client, git ops, container entrypoint |
| **Azmodan** | Platform/DevOps Engineer | `.github/agents/azmodan.agent.md` | Docker Compose, Dockerfiles, vessel image hierarchy |
| **Belial** | Code Reviewer | `.github/agents/belial.agent.md` | Code review against acceptance criteria before any merge |
| **Andariel** | QA/Test Engineer | `.github/agents/andariel.agent.md` | Test harness, build validation, bug hunting |
| **Duriel** | Scribe | `.github/agents/duriel.agent.md` | Commits, branches, pull requests |

## Constitutional Rules

1. **Mephisto orchestrates — always.** Never invoke a specialist directly for new work; route through Mephisto.
2. **Belial reviews before Duriel commits.** No code becomes history without passing through the Inquisitor.
3. **Nothing is implemented without a brief.** Builders work from explicit briefs from Mephisto — not vibes.
4. **Beads is the source of truth.** All work is tracked as Beads issues. `bd ready` before planning anything.
5. **One logical change per commit.** Duriel does not bundle unrelated work.
6. **The `lg` CLI is the human interface.** `bd` is for the team. `lg` is for humans.

## Skills

- `.github/skills/beads/` — persistent memory, issue tracking, agent coordination
- `.github/skills/routing/` — team roster and routing rules

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

### Auto-Sync

bd automatically syncs via Dolt:

- Each write auto-commits to Dolt history
- Use `bd dolt push`/`bd dolt pull` for remote sync
- No manual export/import needed!

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
