# Legion Lexicon

> Defines Legion-specific terminology. Terms inherited directly from Gas Town
> (Beads, Molecule, Wisp, Formula, Convoy, GUPP, Wasteland) are not redefined here —
> see the [Gas Town article](https://steve-yegge.medium.com/welcome-to-gas-town-4f25ee16dd04)
> and [Beads repo](https://github.com/steveyegge/beads) for their canonical definitions.

---

## Legion-Specific Entities

| Term              | Definition                                                                                                                                                                                                                   |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Archon**        | The headless K8s controller that reconciles Beads state with cluster state. Deterministic Go code; makes no judgment calls. Replaces Gas Town's Mayor + Daemon with a single controller.                                     |
| **Vessel**        | An ephemeral K8s Job hosting a specific Identity. Beads and GUPP are the same; the runtime is a container instead of a tmux session.                                                                                         |
| **Forge**         | Legion's Git isolation subsystem — worktree creation, shadow branch lifecycle, cleanup on merge or failure.                                                                                                                  |
| **Shadow Branch** | A per-Molecule Git worktree branch. Naming: `legion/<molecule-id>/<short-title>`. Every active Molecule owns one.                                                                                                            |
| **Pact**          | A markdown file in `.legion/agents/<identity>.md` passed as the system prompt when establishing an ACP session. Equivalent to Gas Town's role templates / CLAUDE.md files. Any ACP-compatible agent consumes it identically. |

---

## Vessel Identities

### Sentient Vessels (LLM-Driven)

| Identity       | Role                                                                                                | Pact            | ACP Session Mode |
| -------------- | --------------------------------------------------------------------------------------------------- | --------------- | ---------------- |
| **Hierophant** | Architect/Expander. Analyzes high-level intent and seeds Grimoire with a Molecule dependency graph. | `hierophant.md` | `architect`      |
| **Wraith**     | Worker. Writes code, docs, or refactors within a single Molecule's scope on a Shadow Branch.        | `wraith.md`     | `code`           |
| **Inquisitor** | Reviewer. Performs peer review of Wraith output against the Molecule's acceptance criteria.         | `inquisitor.md` | `ask`            |

### Deterministic Vessels (Code-Driven)

| Identity      | Role                                                                                   | Notes   |
| ------------- | -------------------------------------------------------------------------------------- | ------- |
| **Gauntlet**  | CI/Verification. Runs tests, linters, type-checkers, builds. Binary pass/fail.         | No LLM. |
| **Alchemist** | Merge/Deploy. Executes merge of Shadow Branches into `main`. Managed by Refinery loop. | No LLM. |

---

## Archon Loops

| Loop          | Responsibility                                                                                                                             |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| **Preflight** | Validates capability before committing to work. Checks images exist, credentials present, probes pass. Blocks READY until all checks pass. |
| **Pulse**     | Polls Beads for READY Molecules (all dependencies satisfied + preflight passed). Spawns Vessel Jobs.                                       |
| **Watcher**   | Monitors running Jobs for completion, failure, timeout, or stuck state. Updates Beads accordingly.                                         |
| **Refinery**  | Manages the merge queue. Batch-then-bisect strategy for landing Vessel output into `main`.                                                 |

---

## Dispatch Modes (Legion Extension to Convoy)

Convoys are Gas Town's concept. Legion adds one distinction in how they execute:

| Mode      | Behavior                                                                                            |
| --------- | --------------------------------------------------------------------------------------------------- |
| `vessels` | One K8s Job per Molecule. Full isolation. Default.                                                  |
| `fleet`   | Single orchestrator Job whose agent manages internal parallelism natively. Backend must support it. |

---

## Gas Town ↔ Legion Concept Map

Only non-obvious mappings where the name or behavior differs:

| Legion     | Gas Town                  | Difference                                                                                              |
| ---------- | ------------------------- | ------------------------------------------------------------------------------------------------------- |
| Archon     | Mayor + Daemon            | Legion collapses both into one headless K8s controller                                                  |
| Vessel     | Agent session (tmux pane) | K8s Job (ephemeral) vs persistent tmux session                                                          |
| Wraith     | Polecat                   | Same role; container vs tmux                                                                            |
| Hierophant | Mayor (expansion mode)    | Dedicated Identity in Legion; Mayor wore both hats in Gas Town                                          |
| Inquisitor | Polecat (review mode)     | Dedicated Identity in Legion; no direct Gas Town equivalent                                             |
| Gauntlet   | (external CI)             | First-class Vessel type in Legion; not a named role in Gas Town                                         |
| Alchemist  | Refinery                  | Same algorithm; Archon-managed in Legion vs dedicated Gas Town role                                     |
| Pact       | CLAUDE.md / role template | Same purpose; `.legion/agents/<identity>.md`                                                            |
| GUPP       | GUPP                      | Same principle — push-based in Legion (Archon spawns Jobs) vs pull-based in Gas Town (agent polls hook) |
