---
name: conventional-commits
description: >
  Conventional Commits v1.0.0 specification for Legion. Apply when writing commit messages,
  reviewing commits, or opening pull requests. Duriel uses this skill for every commit.
  Covers: required format, all valid types, scope conventions for Legion, breaking change
  notation, and PR title rules. Activate for: any git commit, PR creation, or commit
  message review.
license: MIT
metadata:
  version: "1.0"
  project: legion
  spec: "https://www.conventionalcommits.org/en/v1.0.0/"
---

## Format

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

- Subject line **under 72 characters**
- Scope is optional; use it when the change is clearly scoped to one component
- Body explains *what* and *why*, not *how*
- Blank line between subject, body, and footers

## Types

| Type | When to use | SemVer |
|---|---|---|
| `feat` | New capability added | MINOR |
| `fix` | Bug repaired | PATCH |
| `docs` | Documentation only | — |
| `refactor` | Code reorganized, no behavior change | — |
| `test` | Tests added or fixed | — |
| `chore` | Maintenance, deps, tooling | — |
| `ci` | GitHub Actions / CI config | — |
| `perf` | Performance improvement | PATCH |
| `revert` | Reverts a previous commit | — |

## Legion Scopes

| Scope | Component |
|---|---|
| `archon` | `cmd/archon/` — pulse/watcher loops |
| `vessel` | `cmd/vessel-driver/` — vessel driver binary |
| `lg` | `cmd/lg/` — lg CLI |
| `acp` | `internal/acp/` — ACP client |
| `compose` | `docker-compose.yml` |
| `dockerfile` | Any Dockerfile |
| `beads` | `.beads/` or beads config |
| `agents` | `.github/agents/` |
| `skills` | `.github/skills/` |

## Examples

```
feat(archon): implement pulse loop with docker spawn

Polls Beads every 5s for READY issues and runs docker with
ISSUE_ID, DOLT_DSN, REPO_URL, and GITHUB_TOKEN env vars.

fix(vessel): update beads status before exit on error path

Previously the vessel could exit 1 without marking the issue
failed in Beads, leaving it stuck in in_progress.

docs: add MVP.md project layout section

chore(acp): add ACP client type definitions

test(archon): add watcher loop exit code handling tests

feat(archon)!: change watcher poll interval from 10s to configurable

BREAKING CHANGE: WATCHER_INTERVAL env var is now required.
Previously hardcoded to 10s.
```

## Breaking Changes

Two ways to signal a breaking change — use either or both:

```
feat(archon)!: description          ← ! after type/scope
```

```
BREAKING CHANGE: description        ← footer (blank line before)
```

## Branch Naming (Legion)

| Branch type | Pattern | Example |
|---|---|---|
| Vessel work | `legion/<issue-id>` | `legion/legion-wisp-4o1e` |
| Feature work | `feat/<short-desc>` | `feat/pulse-loop` |
| Bug fix | `fix/<short-desc>` | `fix/watcher-exit-code` |
| Docs | `docs/<short-desc>` | `docs/acp-reference` |

**Duriel never merges vessel branches** — those are human-reviewed and merged.
Duriel commits team tooling (agents, skills, compose) directly to `main`.

## PR Titles

PR title = same as commit subject line. Body follows the same type/scope/description
structure. Target is always `main` unless explicitly told otherwise.
