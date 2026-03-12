---
name: git-best-practices
description: >
  Git workflow, branch hygiene, and commit practices for Legion. Apply when performing
  git operations, resolving merge conflicts, managing branches, or advising on git
  workflow. Covers Legion-specific branch naming, the vessel branch lifecycle, rebase
  vs merge guidance, and Duriel's commit workflow. Activate for: any git operation,
  branch creation, push decisions, or conflict resolution.
license: MIT
metadata:
  version: "1.0"
  project: legion
---

## Legion Branch Model

```
main                        ← always deployable; team tooling lands here
  └── legion/<issue-id>     ← vessel work; one branch per Beads issue
  └── feat/<short-desc>     ← feature work from human developers
  └── fix/<short-desc>      ← bug fix branches
```

**`legion/*` branches are owned by the vessel driver**, not Duriel. Vessel driver creates
them, pushes to them, and the human reviews + merges them. Duriel does not touch them.

## Vessel Branch Lifecycle

```
1. Vessel Driver: git clone $REPO_URL /workspace
2. Vessel Driver: git checkout -b legion/$ISSUE_ID
3. Vessel Driver: [work happens — ACP session writes files]
4. Vessel Driver: git add -A && git commit -m "feat: <issue title>"
5. Vessel Driver: git push origin legion/$ISSUE_ID
6. Vessel Driver: bd close $ISSUE_ID --reason "completed"
7. Human: reviews branch, merges or discards
```

## Duriel's Commit Workflow

```
1. git status                   ← confirm only expected files are dirty
2. git diff --stat              ← verify scope matches the brief
3. git add <specific files>     ← never `git add -A` blindly
4. git commit -m "<message>"    ← follow conventional-commits skill
5. git push origin main         ← direct to main for team tooling
```

**Stop condition**: if `git status` shows unexpected files, surface to Mephisto. Do not proceed.

## Rebase vs Merge

| Use case | Command | Why |
|---|---|---|
| Update feature branch from main | `git rebase origin/main` | Clean linear history |
| Merge vessel branch (human) | `git merge --no-ff legion/<id>` | Preserve merge commit for audit trail |
| Fix up local commits before push | `git rebase -i HEAD~N` | Squash noise, keep meaningful history |
| Already pushed to shared branch | **Don't rebase** | Rewrites history, breaks others |

Legion rule: **never force-push `main`**. Vessel branches can be force-pushed by the vessel driver before any human has reviewed them.

## Atomic Commits

One logical change per commit. Signs of a non-atomic commit:
- "and" in the subject line: `feat: add pulse loop and watcher loop`
- Different component files changed: `archon/main.go` + `lg/main.go` in one commit

Split into separate commits when this happens.

## Credential Safety

```bash
# Never commit these:
GITHUB_TOKEN=...
DOLT_DSN=...
ANTHROPIC_API_KEY=...

# Always in .gitignore or runtime env
```

Verify with `git diff --cached` before committing any config file.

## .gitignore for Legion

```gitignore
# Build artifacts
/archon
/vessel-driver
/lg

# Beads runtime (committed config is fine; dolt data is not)
.beads/dolt/

# Local env
.env
*.local
```

## Useful Commands

```bash
# Check what would be pushed
git log origin/main..HEAD --oneline

# Undo last commit (keep changes staged)
git reset --soft HEAD~1

# Discard local changes to a file
git checkout -- path/to/file

# See what changed in a commit
git show <hash> --stat

# Find which commit introduced a bug
git bisect start
git bisect bad HEAD
git bisect good <known-good-hash>
```

See [references/CONFLICTS.md](references/CONFLICTS.md) for merge conflict resolution patterns.
