# Legion

Legion is an autonomous coding system: **file a bead; Legion works it.** Archon reconciles beads that want work against vessels doing work. A vessel is summoned per bead, the animus possesses it and drives an ACP harness against a clone of the target repo, the branch is pushed, and the vessel is reaped.

## Vocabulary — "agent" is banned as a bare noun

| Term | Meaning |
|---|---|
| **Bead** | Unit of work. A tracked task in Beads (embedded Dolt, `bd` CLI). |
| **Vessel** | Unit of execution. A container image with an ACP-speaking harness baked in (`vessel-copilot`, `vessel-claude`, …). The *body*, instrument included. |
| **Harness** | Internal term: the ACP CLI inside a vessel image (Copilot CLI, Claude Code, …). The *instrument*. |
| **Animus** | Legion's in-container binary: possesses the vessel, drives the harness over ACP, serves scoped MCP tools back to the model. The *spirit*. |
| **Persona** | A custom agent defined in the *target repo* that the harness loads by name. The *face*. Legion never parses persona files. |
| **Archon** | Reconciler daemon. **Summons** vessels for ready beads, **reaps** exits. The only thing that closes or fails a bead. |

## Layout

| Path | Responsibility |
|---|---|
| `internal/telemetry` | OTel foundation (exporters, propagation, `legion.*` conventions, slog). Every binary initializes it. No naked `log.Printf`. |
| `internal/bead` | `Bead` domain type + `bd` wrapper. Routing labels: `vessel:<name>`, `persona:<name>`. |
| `internal/vessel` | Container primitive on `github.com/docker/go-sdk`: spec, lifecycle, logs, exit harvesting. |
| `internal/acp` | Session layer over `github.com/coder/acp-go-sdk` (ACP v1). |
| `cmd/archon` | Reconciler daemon. |
| `cmd/animus` | In-vessel entrypoint: clone → branch `legion/<bead-id>` → ACP session → push. |
| `cmd/lg` | Operator CLI: `init`, `invoke`, `status`, `log`. |
| `images/` | `vessel-base` + per-harness Dockerfiles. |
| `docs/` | Architecture, ADRs, guides. No transient markdown in repo root. |

## Rules

- **Beads is the source of truth.** Track all work with `bd` (`bd ready`, `bd create`, `bd update --claim`, `bd close`). No markdown TODOs.
- **Observability is non-negotiable.** One root span per bead; W3C traceparent propagated Archon → vessel env → animus → ACP/MCP. Attrs: `legion.bead.id`, `legion.vessel.name`, `legion.persona`, `legion.harness`.
- **Legion owns zero persona/agent-config formats.** Prompts come from the bead; the harness reads the target repo's own AGENTS.md/personas.
- Conventional Commits. One logical change per commit.
- Build: `go build ./...` · Test: `go test ./...` · Vet: `go vet ./...`

## Session completion

Before ending a session: close/update beads, run quality gates, `git pull --rebase && git push`, verify clean status. Work is not done until pushed.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:970c3bf2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   bd dolt push
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->
