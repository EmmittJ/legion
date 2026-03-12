# PRD: Project Legion v2

> **Tagline:** "for we are many."  
> **Architecture:** K8s-Native Hybrid Orchestration on Beads  
> **Foundational Engine:** [Beads](https://github.com/steveyegge/beads) · [Dolt](https://www.dolthub.com/repositories/dolthub/dolt) · Kubernetes  
> **Federation:** [Wasteland](https://github.com/steveyegge/gastown) compatible  
> **Status:** Draft v2 — informed by [Gas Town](https://github.com/steveyegge/gastown) reference implementation

---

## 1. Vision & Executive Summary

Legion is a **headless, K8s-native multi-agent orchestration system** built on the Beads graph issue tracker. It coordinates fleets of ephemeral AI coding agents ("Vessels") that work in parallel across isolated Git worktrees, with a persistent Dolt-backed ledger ("Grimoire") as the single source of truth.

Legion exists in the same ecosystem as Gas Town — Steve Yegge's tmux-based multi-agent orchestrator — but takes a fundamentally different deployment posture:

| Axis                 | Gas Town                                | Legion                                 |
| -------------------- | --------------------------------------- | -------------------------------------- |
| Compute substrate    | tmux sessions on a single host          | K8s Jobs across a cluster              |
| Controller           | Human Overseer + Mayor agent            | Archon (headless K8s controller)       |
| Scaling model        | Vertical (bigger box)                   | Horizontal (more pods)                 |
| Session model        | Persistent tmux panes                   | Ephemeral run-to-completion containers |
| Identity persistence | Agent beads + `gt prime` context reload | Agent beads + Grimoire state reload    |
| Federation           | Wasteland (DoltHub remotes)             | Wasteland (same protocol)              |

Both systems share **Beads as the data plane** and can participate in the same **Wasteland federation**. Legion is not a replacement for Gas Town — it is a K8s-native topology within the same ecosystem.

### Core Entities

| Entity        | Category  | Definition                                                                        |
| ------------- | --------- | --------------------------------------------------------------------------------- |
| Grimoire      | State     | The persistent Beads ledger (Dolt + Git) tracking all intent, state, and history. |
| Archon        | Control   | The K8s controller that reconciles Grimoire state with cluster state.             |
| Vessel        | Compute   | An ephemeral K8s Job hosting a specific Identity (agent role).                    |
| Molecule      | Task      | A Beads issue representing a discrete unit of work.                               |
| Wisp          | Trace     | An execution record (LLM turn, CLI result, test output) within a Molecule.        |
| Formula       | Template  | A TOML-defined workflow template that expands into a Molecule graph.              |
| Convoy        | Batch     | A parallel execution group with reactive continuation feeding.                    |
| Shadow Branch | Isolation | A Git worktree branch providing conflict-free parallel work.                      |

---

## 2. Architecture

```
                    ┌─────────────────────────────────────────────┐
                    │              Wasteland Federation            │
                    │         (DoltHub remotes, stamps, rep)       │
                    └────────────────────┬────────────────────────┘
                                         │ sync
                    ┌────────────────────▼────────────────────────┐
                    │               Grimoire (Dolt)                │
                    │  ┌──────────┐ ┌──────────┐ ┌─────────────┐  │
                    │  │ Molecules│ │  Agents  │ │   Wisps     │  │
                    │  │ (beads)  │ │ (beads)  │ │  (traces)   │  │
                    │  └──────────┘ └──────────┘ └─────────────┘  │
                    │  ┌──────────┐ ┌──────────┐ ┌─────────────┐  │
                    │  │ Formulas │ │ Convoys  │ │ Merge Queue │  │
                    │  └──────────┘ └──────────┘ └─────────────┘  │
                    └────────────────────┬────────────────────────┘
                                         │ poll/write
                    ┌────────────────────▼────────────────────────┐
                    │                 Archon                       │
                    │          (K8s Deployment, 1 replica)         │
                    │                                              │
                    │  ┌─────────┐ ┌──────────┐ ┌─────────────┐  │
                    │  │ Pulse   │ │ Watcher  │ │  Refinery   │  │
                    │  │ Loop    │ │ (health) │ │ (merge mgr) │  │
                    │  └────┬────┘ └────┬─────┘ └──────┬──────┘  │
                    └───────┼───────────┼──────────────┼──────────┘
                            │           │              │
               ┌────────────▼───────────▼──────────────▼──────────┐
               │              Kubernetes Cluster                   │
               │                                                   │
               │  ┌─────────┐ ┌─────────┐ ┌─────────┐            │
               │  │ Wraith  │ │ Wraith  │ │Gauntlet │  ...N      │
               │  │ Job #1  │ │ Job #2  │ │ Job #3  │            │
               │  │ (worktree│ │(worktree│ │(worktree│            │
               │  │  /wt-1) │ │ /wt-2) │ │ /wt-3) │            │
               │  └─────────┘ └─────────┘ └─────────┘            │
               └──────────────────────────────────────────────────┘
```

### 2.1. Archon — The Controller

Archon is a headless K8s Deployment (single replica, leader-elected) that runs three concurrent loops:

1. **Preflight Loop** — Validates capability before committing to work. When a Formula is invoked or a Wasteland bounty is claimed, Preflight checks that every required Vessel image exists in the registry, every required credential is present in the secret store, and every required tool/runtime is reachable. A Formula does not become `READY` until Preflight passes. If it fails, the Formula is held in `preflight_failed` and the operator is notified — **Legion refuses work it cannot finish before it starts**.

2. **Pulse Loop** — Polls Grimoire for Molecules in `READY` state (all dependencies satisfied and preflight passed). Manifests them as K8s Jobs with the appropriate Vessel identity. Interval: configurable, default 5s.

3. **Watcher Loop** — Monitors running Vessel Jobs for completion, failure, or timeout. Updates Molecule status in Grimoire based on exit codes. Detects stuck agents and escalates or restarts.

4. **Refinery Loop** — Manages the merge queue. When a Molecule's Shadow Branch passes review and verification, Refinery attempts merge into `main` using a batch-then-bisect strategy (inspired by Gas Town's Refinery/bors).

Archon is **not an LLM agent**. It is deterministic Go code that speaks the K8s API and Beads CLI. It makes no judgment calls — it reconciles declared state (Grimoire) with observed state (cluster).

### 2.2. Propulsion: GUPP

Legion adopts the **Gas Town Universal Propulsion Principle (GUPP)**:

> _"If there is work on your Hook, YOU MUST RUN IT."_

Every Vessel, on startup, checks its Hook (assigned Molecule) in Grimoire and immediately begins execution. There is no polling, no waiting for instructions, no "what should I do?" phase. Vessels are propelled by their Hook.

When a Vessel completes its Molecule, it writes completion state to Grimoire and terminates. Archon detects the completion and feeds the next ready Molecule to the next Vessel. This creates a **reactive propulsion chain**: completion → dependency satisfaction → new READY Molecules → new Vessel Jobs.

---

## 3. Hybrid Workforce (Vessel Identities)

Legion classifies labor into two distinct Vessel types to ensure reliability and cost-efficiency.

### 3.1. Sentient Vessels (LLM-Driven)

Sentient Vessels communicate with an AI coding agent via the **[Agent Client Protocol (ACP)](https://agentclientprotocol.com)** — an open standard (Apache licensed, originated by Zed) that abstracts the agent implementation behind a uniform interface. The Vessel entrypoint runs a **Vessel Driver**, a lightweight ACP client. The agent (Copilot CLI, Claude Code, Gemini CLI, Goose, Codex, etc.) runs as an **ACP server** on the other side.

Swapping agent backends requires only changing which container image Archon selects at Job creation time. The Vessel Driver code, Grimoire schema, Formulas, and Pact files remain unchanged. **Legion does not care what is on the other side of the ACP connection.**

Each backend is a **different container image** with its own binary, auth environment variables, and resource profile. Archon maintains a backend registry (in Grimoire config) that maps a backend name to its image, required secrets, and default resource limits:

```
┌─────────────────────────────────────────────────────────┐
│  Archon: selects image + injects credentials at Job     │
│  creation time based on Formula/instance backend config  │
└───────────┬──────────────────────────────────────────────┘
            │ spawns one of:
            │
            ├─► legion/vessel-copilot   GITHUB_TOKEN
            │     └─ gh copilot --autopilot --yolo
            │
            ├─► legion/vessel-claude    ANTHROPIC_API_KEY
            │     └─ claude --dangerously-skip-permissions
            │
            ├─► legion/vessel-gemini    GOOGLE_API_KEY
            │     └─ gemini --yolo
            │
            ├─► legion/vessel-goose     GOOSE_PROVIDER + key
            │     └─ goose run
            │
            └─► legion/vessel-<custom>  (any ACP server)
                  └─ <custom entrypoint>

         Each container: Vessel Driver (ACP client) + one agent binary
                                    │
                     Agent Client Protocol (ACP)
                                    │
                              ACP server process
```

Each Vessel identity maps to a **Pact** — an agent profile markdown file in `.legion/agents/` in the target repo. The Vessel Driver passes the Pact as the system prompt when establishing the ACP session. Any ACP-compatible backend consumes it identically.

**ACP-compatible backends (non-exhaustive):**

| Backend            | Command                 | Auth env var           |
| ------------------ | ----------------------- | ---------------------- |
| GitHub Copilot CLI | `legion/vessel-copilot` | `GITHUB_TOKEN`         |
| Claude Code        | `legion/vessel-claude`  | `ANTHROPIC_API_KEY`    |
| Gemini CLI         | `legion/vessel-gemini`  | `GOOGLE_API_KEY`       |
| Goose (Square)     | `legion/vessel-goose`   | `GOOSE_PROVIDER` + key |
| Codex CLI          | `legion/vessel-codex`   | `OPENAI_API_KEY`       |

| Identity | Role | Pact | Gas Town Analog |
| -------- | ---- | ---- | --------------- |

| **Hierophant** | Architect/Expander. Analyzes high-level intent and seeds Grimoire with a Molecule dependency graph. | `.legion/agents/hierophant.md` | Mayor (planning) |
| **Wraith** | Worker. Writes code, docs, or refactors within a single Molecule's scope. Operates on a Shadow Branch via Git worktree. | `.legion/agents/wraith.md` | Polecat |
| **Inquisitor** | Reviewer. Performs peer review of Wraith output against the Molecule's acceptance criteria. | `.legion/agents/inquisitor.md` | Polecat (review mode) |

### 3.2. Deterministic Vessels (Code-Driven)

These Vessels run predefined scripts — no LLM involved.

| Identity      | Role                                                                                                | Gas Town Analog |
| ------------- | --------------------------------------------------------------------------------------------------- | --------------- |
| **Gauntlet**  | CI/Verification. Runs tests, linters, type-checkers, builds. Binary pass/fail.                      | (external CI)   |
| **Alchemist** | Merge/Deploy. Managed by Archon's Refinery loop. Executes the merge of Shadow Branches into `main`. | Refinery        |

### 3.3. Agent State Machine

Every Vessel transitions through states tracked in Grimoire:

```
spawning → working → done
                  ↘ stuck → escalated
                  ↘ awaiting-gate
```

| State           | Meaning                                                            |
| --------------- | ------------------------------------------------------------------ |
| `spawning`      | K8s Job created, container starting                                |
| `working`       | Vessel is actively executing its Molecule                          |
| `done`          | Molecule completed, Vessel terminated                              |
| `stuck`         | No progress detected within timeout window                         |
| `escalated`     | Stuck Vessel flagged for Archon intervention (restart or reassign) |
| `awaiting-gate` | Blocked on external condition (review approval, CI result)         |

---

## 4. Data Model: Grimoire

Grimoire is a Beads ledger backed by Dolt. All state lives here — Vessels are stateless and disposable.

### 4.1. Molecules (Work Units)

Molecules are Beads issues. They are the atomic unit of work in Legion.

```
Molecule {
  id:           string        // Beads hash ID (e.g., bd-a1b2c3)
  title:        string        // Human-readable description
  status:       MoleculeStatus
  type:         MoleculeType  // expand | implement | review | verify | merge
  assignee:     string        // Vessel identity (e.g., lg-wraith-03)
  shadow_branch: string       // Git branch name for this work
  formula_ref:  string        // Source Formula ID (if templated)
  convoy_ref:   string        // Parent Convoy ID (if batched)
  acceptance:   string        // Criteria for completion
  depends_on:   []string      // Molecule IDs that must complete first
  labels:       []string      // Tags for routing and filtering
  wisps:        []Wisp        // Execution trace
}
```

**Molecule Status Flow:**

```
open → hooked → in_progress → closed
                            → blocked
                            → tombstone (abandoned)
```

### 4.2. Wisps (Execution Traces)

Every significant action by a Vessel is recorded as a Wisp — an append-only trace within its Molecule.

```
Wisp {
  id:         string
  molecule_id: string
  vessel_id:  string
  timestamp:  datetime
  type:       WispType  // llm_turn | cli_exec | test_result | commit | review
  content:    text      // The actual trace content
  exit_code:  int       // For CLI/test wisps
  commit_sha: string    // For commit wisps
}
```

### 4.3. Formulas (Workflow Templates)

Formulas are TOML files that define reusable workflow patterns. They expand into Molecule dependency graphs.

```toml
# formulas/bugfix.formula.toml
description = "Standard bugfix workflow: reproduce, fix, test, review, merge"
formula = "bugfix"
type = "workflow"
version = 1

[[steps]]
id = "reproduce"
title = "Reproduce {{bug}}"
description = "Write a failing test that demonstrates the bug."
acceptance = "A test exists that fails on main and targets the reported behavior."

[[steps]]
id = "fix"
title = "Fix {{bug}}"
description = "Implement the minimal fix."
acceptance = "The reproduction test passes. No unrelated changes."
needs = ["reproduce"]

[[steps]]
id = "verify"
title = "Verify {{bug}} fix"
description = "Run full test suite."
acceptance = "All tests pass. No regressions."
needs = ["fix"]
vessel = "gauntlet"

[[steps]]
id = "review"
title = "Review {{bug}} fix"
description = "Peer review the fix against acceptance criteria."
acceptance = "Inquisitor approves the change."
needs = ["fix"]
vessel = "inquisitor"

[[steps]]
id = "merge"
title = "Merge {{bug}} fix"
description = "Merge shadow branch into main."
acceptance = "Clean merge. CI green."
needs = ["verify", "review"]
vessel = "alchemist"

[vars]
[vars.bug]
description = "Description of the bug to fix"
required = true

# Vessel registry: declares what this Formula needs before it can run.
# Archon Preflight validates these before the Formula becomes READY.
[vessel_registry]

  [vessel_registry.gauntlet]
  image = "legion/vessel-gauntlet-pytest:latest"  # Must exist in container registry
  probe = "pytest --version"                       # Command Archon runs to confirm it works
  credential = "none"                              # Deterministic — no LLM credential needed

  [vessel_registry.inquisitor]
  image = "legion/vessel-copilot:latest"           # Or any ACP-compatible backend
  credential = "copilot-token"                     # Must exist in legion-agent-tokens Secret

  [vessel_registry.wraith]
  image = "legion/vessel-copilot:latest"
  credential = "copilot-token"
```

**Formula Types** (adopted from Gas Town):

| Type        | Behavior                                             |
| ----------- | ---------------------------------------------------- |
| `workflow`  | Sequential steps with explicit dependency edges      |
| `convoy`    | Parallel execution with synthesis step               |
| `expansion` | Template-based step generation (Hierophant fills in) |
| `aspect`    | Multi-aspect parallel analysis                       |

### 4.4. Convoys (Parallel Batches)

A Convoy groups related Molecules for parallel execution. When all Molecules in a Convoy complete, a synthesis Molecule is automatically fed (reactive continuation).

```
Convoy {
  id:           string
  title:        string
  molecule_ids: []string   // Member Molecules
  synthesis_id: string     // Molecule to feed on completion
  status:       open | closed
  dispatch:     "vessels" | "fleet"  // How to execute
}
```

**Dispatch modes:**

- `vessels` (default): Archon spawns one K8s Job per Molecule. Each is an independent container with its own agent session, credentials, and resource limits. Full isolation.
- `fleet`: Archon spawns a single orchestrator Job whose agent backend supports native parallel subagent dispatch (e.g., Copilot CLI's `/fleet` command, or equivalent in other backends). The agent manages internal parallelism and dependency ordering. Best for tightly coupled subtasks where subagent context sharing is beneficial. Backend must support this mode — noted in the backend registry.

---

## 5. Formulas & Rites (Workflow System)

### 5.1. Formula Resolution

Formulas are resolved with three-tier precedence (matching Gas Town):

1. **Project-level:** `.legion/formulas/` in the target repository
2. **Town-level:** Legion instance configuration
3. **System-level:** Built-in formulas shipped with Legion

### 5.2. Built-in Formulas

| Formula    | Type     | Steps                                         | Description                             |
| ---------- | -------- | --------------------------------------------- | --------------------------------------- |
| `bugfix`   | workflow | reproduce → fix → verify → review → merge     | Standard bug resolution                 |
| `feature`  | workflow | design → implement → verify → review → merge  | New feature development                 |
| `shiny`    | workflow | design → implement → verify → review → merge  | "Engineer in a box" — the canonical way |
| `refactor` | workflow | analyze → implement → verify → review → merge | Code restructuring                      |
| `spike`    | convoy   | N parallel investigations → synthesis         | Research/exploration                    |
| `hotfix`   | workflow | fix → verify → merge                          | Emergency patch (skip review)           |

### 5.3. Invocation

### 5.4. Preflight & Capability Validation

This is the answer to: **"how do we take on work with an insane workflow?"** — you don't, until you can prove you can finish it.

Before any Formula transitions from `invoked` to `READY`, Archon's Preflight loop runs a full capability check. For novel or externally-sourced Formulas (e.g., a Wasteland bounty with custom steps), this means you may need to **build and register the required Vessel images first** — before accepting the work.

**Preflight checks (in order):**

```
1. Parse Formula TOML → enumerate every step's vessel type
2. For each vessel type:
   a. Image pull check  — does the image exist in the registry?
   b. Probe run        — spin up the image, run its probe command, expect exit 0
   c. Credential check — does the required secret key exist and is it non-empty?
3. For deterministic Vessels (Gauntlet, Alchemist):
   d. Tool smoke test  — run the actual tool with --version or equivalent
4. Record preflight result as a Wisp on the Formula's root Molecule
5. If ALL checks pass → Formula status: READY
6. If ANY check fails → Formula status: preflight_failed
                       → Operator notified with specific failing check
                       → Work is NOT accepted / bounty is NOT claimed
```

**Implication for custom Gauntlets:** If a Formula requires a `gauntlet` step that runs, say, a Rust test suite, you must first build and push `legion/vessel-gauntlet-rust:latest` and register it in the Formula's `[vessel_registry]` block. Archon will refuse the work until the probe passes. This forces the deterministic verification layer to exist before the non-deterministic work begins.

**The scaffolding workflow:**

```bash
# Before accepting unusual work, validate what you'd need:
lg preflight <formula-file>          # Dry-run preflight against a Formula
lg preflight --wasteland <bounty-id> # Preflight a Wasteland bounty before claiming

# Output:
#   ✓ vessel/wraith        legion/vessel-copilot:latest  (probe: ok)
#   ✗ vessel/gauntlet      legion/vessel-gauntlet-rust   (NOT FOUND in registry)
#   ✗ credential/claude    ANTHROPIC_API_KEY             (secret missing)
#
# Fix: build the missing Vessel image, add the credential, then re-run.

# Build a new deterministic Vessel image before taking on the work:
lg vessel build --name gauntlet-rust \
                --base legion/vessel-base \
                --probe "cargo test --version" \
                --dockerfile ./vessels/gauntlet-rust.dockerfile
lg vessel register gauntlet-rust legion/vessel-gauntlet-rust:latest

# Now preflight passes → safe to invoke
lg invoke <formula-or-bounty>
```

**Wasteland integration:** `lg wasteland claim` always runs preflight first. A bounty is never claimed unless Legion can guarantee completion capability. Reputation depends on delivery — claiming work you can't finish destroys trust.

---

```bash
# Expand a formula into a Molecule graph
lg invoke bugfix --bug="Login fails when email contains '+'"

# Invoke with a raw intent (Hierophant expands)
lg invoke "Add dark mode support to the settings page"

# Invoke a convoy for parallel exploration
lg invoke spike --question="What auth libraries support SAML?" --tracks=3
```

---

## 6. Merge Strategy: The Refinery

The Refinery is Archon's merge management subsystem. It prevents the "merge hell" problem that arises when many Wraiths land changes simultaneously.

### 6.1. Batch-Then-Bisect (from Gas Town)

1. **Queue:** Completed, reviewed Shadow Branches enter the merge queue.
2. **Batch:** Refinery attempts to merge N branches together.
3. **Test:** Run full CI (Gauntlet) on the batch.
4. **If green:** Fast-forward `main`. All N branches merged.
5. **If red:** Bisect the batch to find the failing branch. Eject it, retry the rest.

### 6.2. Merge Request Phases

```
ready → claimed → preparing → prepared → merging → merged
                                                  → rejected
                           → failed → ready (retry)
```

### 6.3. Conflict Resolution

- **Trivial conflicts** (non-overlapping hunks): Auto-resolved by Alchemist.
- **Semantic conflicts** (overlapping changes): Alchemist spawns a Wraith to resolve, then re-queues.
- **Structural conflicts** (file moves, renames): Escalated. Archon pauses the queue and flags for human review or Hierophant re-planning.

---

## 7. Git Isolation: The Forge

### 7.1. Shadow Branches

Every active Molecule operates on a unique Shadow Branch. Branch naming convention:

```
legion/<molecule-id>/<short-title>
# e.g., legion/bd-a1b2/fix-login-plus-sign
```

### 7.2. Worktree Layout

Each Vessel Job mounts the repository and creates a Git worktree for its Shadow Branch:

```
/repo                    # Bare clone (shared across PVCs or init containers)
/worktrees/bd-a1b2/      # Wraith #1's working directory
/worktrees/bd-c3d4/      # Wraith #2's working directory
/worktrees/bd-e5f6/      # Gauntlet's test directory
```

Worktrees share Git object storage, so cloning is near-instant. Each worktree has its own HEAD and index — full isolation.

### 7.3. Forge Lifecycle

1. **On Vessel spawn:** `git worktree add /worktrees/<id> -b legion/<id>/<title>`
2. **During work:** Vessel commits to its Shadow Branch. Each commit is recorded as a Wisp.
3. **On completion:** Vessel pushes Shadow Branch to remote, marks Molecule complete, exits.
4. **On merge:** Alchemist merges Shadow Branch into `main`, prunes worktree.
5. **On failure:** Archon cleans up worktree. Shadow Branch preserved for debugging.

---

## 8. Observability: The Witness

### 8.1. Health Monitoring

Archon's Watcher loop provides continuous health monitoring:

| Signal                | Detection                       | Action                                           |
| --------------------- | ------------------------------- | ------------------------------------------------ |
| No Wisps in N minutes | Vessel stopped producing traces | Mark `stuck`, restart                            |
| Exit code ≠ 0         | Vessel crashed                  | Record failure Wisp, retry or escalate           |
| K8s eviction          | Node pressure                   | Reschedule on different node                     |
| Timeout exceeded      | Molecule taking too long        | Mark `stuck`, escalate                           |
| Resource limit hit    | OOM or CPU throttle             | Record resource Wisp, restart with higher limits |

### 8.2. Dashboard Queries

Since Grimoire is Dolt (MySQL-compatible), standard SQL queries power dashboards:

```sql
-- Active Vessels and their Molecules
SELECT m.id, m.title, m.status, m.assignee, m.shadow_branch
FROM molecules m WHERE m.status = 'in_progress';

-- Stuck agents
SELECT m.id, m.title, m.assignee,
       TIMESTAMPDIFF(MINUTE, max(w.timestamp), NOW()) as minutes_silent
FROM molecules m JOIN wisps w ON w.molecule_id = m.id
WHERE m.status = 'in_progress'
GROUP BY m.id HAVING minutes_silent > 15;

-- Merge queue status
SELECT phase, COUNT(*) FROM merge_requests GROUP BY phase;

-- Formula success rates
SELECT formula_ref,
       COUNT(CASE WHEN status='closed' THEN 1 END) as completed,
       COUNT(*) as total
FROM molecules WHERE formula_ref IS NOT NULL
GROUP BY formula_ref;
```

### 8.3. Time-Travel Debugging

Dolt's version control means every Grimoire state is queryable at any point in history:

```sql
-- What did the Molecule graph look like when this merge failed?
SELECT * FROM molecules AS OF 'merge-attempt-abc123';

-- Diff between two points in time
SELECT * FROM dolt_diff('molecules', 'HEAD~5', 'HEAD');
```

---

## 9. Wasteland Federation

Legion participates in the Wasteland — the same federated work coordination network used by Gas Town instances.

### 9.1. What Legion Gets from Wasteland

- **Wanted Board:** Other rigs post bounties. Legion can claim and execute them autonomously.
- **Stamps:** Multi-dimensional reputation attestations. When Legion completes work, the requesting rig stamps it. Reputation accrues to the Legion instance.
- **Trust Routing:** Higher-trust rigs get access to higher-stakes work. Legion builds trust through consistent delivery.

### 9.2. How Legion Participates

```
┌──────────────┐         ┌──────────────┐         ┌──────────────┐
│  Gas Town A  │◄───────►│  Wasteland   │◄───────►│   Legion B   │
│  (tmux host) │  Dolt   │  (DoltHub)   │  Dolt   │  (K8s cluster│
│              │  sync   │              │  sync   │              │
└──────────────┘         └──────────────┘         └──────────────┘
```

- Legion syncs its local Grimoire with Wasteland via DoltHub (same Dolt remote mechanism Gas Town uses).
- The Archon includes a **Wasteland sync loop** that periodically checks the Wanted Board and auto-claims items matching Legion's capabilities.
- Completed work is pushed upstream. The requesting rig reviews and stamps.

### 9.3. Wasteland Data Model

Legion speaks the same schema:

| Table          | Purpose                                                                    |
| -------------- | -------------------------------------------------------------------------- |
| `stamps`       | Reputation attestations (author, subject, valence, confidence, skill_tags) |
| `wanted_items` | Bounty board — work available for claiming                                 |
| `claims`       | Active claims on wanted items                                              |
| `completions`  | Delivered work awaiting review                                             |
| `rigs`         | Registry of participating rigs                                             |

**Yearbook Rule:** A rig cannot stamp its own work. Reputation is always attested by others.

---

## 10. Concept Mapping: Legion ↔ Gas Town

For developers familiar with Gas Town, this table maps equivalent concepts:

| Legion        | Gas Town                  | Notes                                               |
| ------------- | ------------------------- | --------------------------------------------------- |
| Grimoire      | Beads DB (Dolt)           | Same underlying technology                          |
| Archon        | Mayor + Daemon (Boot)     | Legion collapses these into one headless controller |
| Vessel        | Agent session (tmux pane) | K8s Job vs tmux session                             |
| Wraith        | Polecat                   | Ephemeral worker agent                              |
| Hierophant    | Mayor (expansion mode)    | Planning/expansion role                             |
| Inquisitor    | Polecat (review mode)     | Peer review                                         |
| Gauntlet      | (external CI)             | Legion makes CI a first-class Vessel type           |
| Alchemist     | Refinery                  | Merge management                                    |
| Molecule      | Bead (issue)              | Same concept, different name                        |
| Wisp          | Activity/trace            | Execution record                                    |
| Formula       | Formula (TOML)            | Adopted directly from Gas Town                      |
| Convoy        | Convoy                    | Adopted directly from Gas Town                      |
| Shadow Branch | Worktree branch           | Same Git isolation strategy                         |
| Invocation    | `gt pour` / `gt hook`     | Work initiation                                     |
| GUPP          | GUPP                      | Same propulsion principle                           |
| Wasteland     | Wasteland                 | Same federation protocol                            |
| Forge         | (git operations)          | Legion names its Git subsystem                      |
| Pact          | CLAUDE.md / role template | System prompt for Vessel identity                   |

---

## 11. CLI: `lg`

The `lg` CLI is the human interface to Legion.

```bash
# === Invocation ===
lg invoke "Add dark mode"                    # Hierophant expands intent
lg invoke bugfix --bug="..."                 # Expand a named Formula
lg invoke spike --question="..." --tracks=5  # Parallel exploration

# === Observation ===
lg status                                    # Overview: running/queued/stuck/done
lg status <molecule-id>                      # Detail: wisps, branch, vessel
lg molecules --ready                         # What's unblocked
lg queue                                     # Merge queue state
lg log <molecule-id>                         # Wisp stream (tail -f equivalent)

# === Control ===
lg pause                                     # Pause the Pulse loop (no new Jobs)
lg resume                                    # Resume
lg cancel <molecule-id>                      # Cancel a Molecule (kills Vessel)
lg retry <molecule-id>                       # Reset to READY, re-manifest
lg escalate <molecule-id>                    # Flag for human attention

# === Formulas ===
lg formula list                              # Available formulas
lg formula show <name>                       # Show TOML definition
lg formula validate <file>                   # Validate a formula file

# === Wasteland ===
lg wasteland status                          # Federation sync state
lg wasteland wanted                          # Browse the Wanted Board
lg wasteland claim <item-id>                 # Claim a bounty
lg wasteland stamps                          # View reputation

# === Admin ===
lg config                                    # Show/edit Archon configuration
lg vessels                                   # List active Vessel pods
lg grimoire query "SELECT ..."               # Raw SQL against Grimoire
```

---

## 12. Deployment

### 12.1. Archon Deployment (K8s)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: legion-archon
  labels:
    app: legion
    component: controller
spec:
  replicas: 1
  selector:
    matchLabels:
      app: legion
      component: controller
  template:
    metadata:
      labels:
        app: legion
        component: controller
    spec:
      serviceAccountName: legion-archon-sa
      containers:
        - name: archon
          image: legion/archon:latest
          env:
            - name: GRIMOIRE_DSN
              valueFrom:
                secretKeyRef:
                  name: legion-secrets
                  key: grimoire-dsn
            - name: LEGION_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
          resources:
            requests:
              cpu: 250m
              memory: 256Mi
            limits:
              cpu: 500m
              memory: 512Mi
```

### 12.2. Vessel Job Template

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: "legion-wraith-${MOLECULE_ID}"
  labels:
    app: legion
    component: vessel
    identity: wraith
    molecule: "${MOLECULE_ID}"
spec:
  backoffLimit: 2
  activeDeadlineSeconds: 3600
  ttlSecondsAfterFinished: 300
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: vessel
          # Image selected by Archon at Job creation time.
          # e.g. legion/vessel-copilot, legion/vessel-claude, legion/vessel-gemini
          image: "${VESSEL_IMAGE}"
          env:
            - name: MOLECULE_ID
              value: "${MOLECULE_ID}"
            - name: GRIMOIRE_DSN
              valueFrom:
                secretKeyRef:
                  name: legion-secrets
                  key: grimoire-dsn
            - name: SHADOW_BRANCH
              value: "${SHADOW_BRANCH}"
            - name: BD_ACTOR
              value: "lg-${IDENTITY}-${INSTANCE}"
            - name: AGENT_MODEL
              value: "${AGENT_MODEL}" # optional; backend default if unset
            # Auth credential: env var name and secret key differ per backend.
            # Archon resolves the correct name/key from the backend registry
            # and emits the appropriate secretKeyRef at Job creation time.
            # Examples after resolution:
            #   GITHUB_TOKEN      → legion-agent-tokens/copilot-token
            #   ANTHROPIC_API_KEY → legion-agent-tokens/claude-token
            #   GOOGLE_API_KEY    → legion-agent-tokens/gemini-token
            - name: "${AGENT_AUTH_ENV_VAR}"
              valueFrom:
                secretKeyRef:
                  name: legion-agent-tokens
                  key: "${AGENT_TOKEN_KEY}"
          resources:
            requests:
              cpu: "1"
              memory: 2Gi
            limits:
              cpu: "2"
              memory: 4Gi
          volumeMounts:
            - name: repo
              mountPath: /repo
            - name: worktree
              mountPath: /worktree
      initContainers:
        - name: forge-init
          image: legion/forge-init:latest
          command: ["sh", "-c"]
          args:
            - |
              git clone --bare ${REPO_URL} /repo || true
              git -C /repo fetch --all
              git -C /repo worktree add /worktree -b ${SHADOW_BRANCH}
          volumeMounts:
            - name: repo
              mountPath: /repo
            - name: worktree
              mountPath: /worktree
      volumes:
        - name: repo
          persistentVolumeClaim:
            claimName: legion-repo-pvc
        - name: worktree
          emptyDir: {}
```

### 12.3. Vessel Base Image

Sentient Vessels are built on a **backend-agnostic base image**. No LLM weights are bundled and no specific agent CLI is hardcoded. The only constant is the **Vessel Driver** — a lightweight ACP client that orchestrates the agent session. The agent backend is installed in a derived image (or init container) and selected at runtime via `AGENT_BACKEND`.

**Base image** (`legion/vessel-base`):

```dockerfile
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    git curl jq ca-certificates python3 python3-pip \
    && rm -rf /var/lib/apt/lists/*

# Beads CLI — reads/writes Grimoire
RUN pip3 install --no-cache-dir beads-cli kubernetes --break-system-packages

# Vessel Driver — ACP client that drives the agent session
# Reads Molecule from Grimoire, establishes ACP session with backend,
# proxies file tool calls to the worktree, writes Wisps, exits on completion.
COPY vessel-driver /usr/local/bin/vessel-driver

WORKDIR /vessel
ENTRYPOINT ["/usr/local/bin/vessel-driver"]
```

**Derived images** layer in the agent backend — one per backend, all share the same base:

```dockerfile
# legion/vessel-copilot  — backend: GitHub Copilot CLI
FROM legion/vessel-base
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) \
         signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] \
         https://cli.github.com/packages stable main" \
      | tee /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*
RUN gh extension install github/gh-copilot

# legion/vessel-claude   — backend: Claude Code
FROM legion/vessel-base
RUN npm install -g @anthropic-ai/claude-code

# legion/vessel-gemini   — backend: Gemini CLI
FROM legion/vessel-base
RUN npm install -g @google/gemini-cli
```

Archon selects the correct image at Job creation time based on the instance's `agent_backend` config or a per-Formula override.

**Vessel Driver ACP flow** (GUPP):

```
1. Read Molecule from Grimoire (the Hook)
2. Load Pact from .legion/agents/<identity>.md in the worktree
3. Start ACP server:  vessel-driver spawn $AGENT_BACKEND
4. Open ACP session with Pact as system prompt + Molecule as first message
5. Proxy ACP tool calls → worktree file system
6. Stream ACP events → Wisps in Grimoire
7. On ACP session complete: push Shadow Branch, mark Molecule done, exit 0
8. On error: write failure Wisp, exit non-zero → Archon Watcher detects
```

**Pact files** live in the target repo under `.legion/agents/`:

```
.legion/agents/
  wraith.md       # Pact for Wraith: code implementation persona, tools, constraints
  inquisitor.md   # Pact for Inquisitor: review persona, approval criteria
  hierophant.md   # Pact for Hierophant: planning persona, Grimoire write access
```

Pact files are plain markdown consumed by any ACP server as a system prompt — they contain no backend-specific syntax.

### 12.4. Local Development (Docker Compose)

```yaml
services:
  grimoire-db:
    image: dolthub/dolt-sql-server:latest
    ports:
      - "3307:3306"
    environment:
      - DOLT_ROOT_PATH=/var/lib/dolt
    volumes:
      - grimoire-data:/var/lib/dolt

  archon:
    build:
      context: .
      dockerfile: Dockerfile.archon
    environment:
      - GRIMOIRE_DSN=mysql://root@grimoire-db:3306/legion
      - LEGION_MODE=local # Uses docker instead of K8s
    depends_on:
      - grimoire-db
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - repo-cache:/repo

volumes:
  grimoire-data:
  repo-cache:
```

---

## 13. Design Principles

### "for we are many."

1. **Horizontal over Vertical.** Instead of one powerful agent, manifest fifty specialized Vessels. Scaling is adding pods, not upgrading hardware.

2. **Deterministic over Sentient.** Use real code (Gauntlet) to verify AI output. Never trust an LLM to judge its own work. The Gauntlet is the ground truth.

3. **Ledger over Memory.** Vessels do not "remember" sessions — they read Grimoire. Crash recovery is free: restart the Job, it reads the same state. This is **nondeterministic idempotence** — the same input produces equivalent (not identical) output.

4. **Propulsion over Polling.** GUPP ensures Vessels immediately execute their Hook. No idle loops, no "what should I do?" prompts. Work flows through the dependency graph reactively.

5. **Ephemeral over Persistent.** Vessels are cattle, not pets. They spin up, do work, write state, and die. Identity persists in Grimoire, not in running processes.

6. **Federation over Isolation.** Legion doesn't work alone. It participates in Wasteland, earning reputation, claiming bounties, and collaborating with other rigs (including Gas Town instances).

7. **Prove Before You Pledge.** Legion never accepts work it cannot finish. Every Formula must pass Preflight before execution begins — required Vessel images exist, probes pass, credentials are present. For unusual workflows, you build the deterministic verification layer _before_ the non-deterministic work starts. Reputation is built on delivery, not on promises.

---

## 14. Milestones

### Phase 1: The First Manifestation

**Goal:** One Wraith Vessel completes one Molecule.

- [ ] Vessel registry schema in Grimoire (image, probe, credential mappings)
- [ ] Archon Preflight loop (validate before READY)
- [ ] `lg preflight` CLI (dry-run capability check)
- [ ] Archon reconciliation loop (Pulse)
- [ ] Grimoire schema (Molecules, Wisps, agent beads)
- [ ] Vessel base image with Beads CLI
- [ ] Forge init container (clone + worktree)
- [ ] `lg invoke` CLI (manual Molecule creation)
- [ ] `lg status` CLI (basic observation)

### Phase 2: The Many

**Goal:** Parallel Wraiths, Gauntlets, and the Refinery.

- [ ] Formula parser and expansion (TOML → Molecule graph)
- [ ] Multiple simultaneous Vessel Jobs
- [ ] Gauntlet Vessel (deterministic CI)
- [ ] Refinery merge queue (batch-then-bisect)
- [ ] Watcher health monitoring
- [ ] `lg queue` and `lg log` CLI

### Phase 3: Autonomy

**Goal:** Hierophant expansion, Convoys, full lifecycle.

- [ ] Hierophant Vessel (intent → Molecule graph)
- [ ] Inquisitor Vessel (peer review)
- [ ] Convoy parallel batches with reactive feeding
- [ ] Stuck detection and automatic retry/escalation
- [ ] Formula resolution (project → town → system tiers)
- [ ] Built-in formula library

### Phase 4: Federation

**Goal:** Wasteland participation.

- [ ] Wasteland sync loop in Archon
- [ ] Wanted Board browsing and claiming
- [ ] Stamp schema and reputation tracking
- [ ] DoltHub remote configuration
- [ ] `lg wasteland` CLI

### Phase 5: Gas City Compatibility

**Goal:** Legion as a portable Gas City topology.

- [ ] Export Legion's role definitions as Gas City capability profiles
- [ ] Accept Gas City TOML role definitions for custom Vessel identities
- [ ] Cross-topology work routing (Gas Town ↔ Legion via Wasteland)

---

## 15. Open Questions

1. **Shared repo PVC vs per-Job clone.** A shared PVC for the bare repo enables instant worktree creation but creates a storage bottleneck. Per-Job clone is simpler but slower. Benchmark needed.

2. **Credential pool and rotation.** Because Vessels are interchangeable via ACP, the auth mechanism is backend-specific: `GITHUB_TOKEN` for Copilot, `ANTHROPIC_API_KEY` for Claude, `GOOGLE_API_KEY` for Gemini, etc. At scale, N parallel Vessels sharing a single credential may hit rate limits or per-seat billing constraints. Archon should implement a **credential pool** tracked in Grimoire: a table of tokens per backend, each with a `checked_out_by` (Vessel Job ID) and `expires_at`. Archon checks out a credential before spawning a Job and releases it on Job completion or timeout. Short-term: single shared credential per backend with per-Molecule budget guards. Long-term: pool with Grimoire-tracked checkout/checkin, supporting credential rotation and per-backend concurrency limits.

3. **Cost controls.** How does Archon enforce token/compute budgets? Per-Molecule limits? Per-Formula limits? Circuit breakers on runaway Vessels?

4. **Human-in-the-loop gates.** Some Molecules may require human approval before proceeding (e.g., deploy to prod). How does `awaiting-gate` interact with external approval systems?

5. **Multi-repo orchestration.** Can a single Formula span multiple repositories? Or does each Legion instance bind to one repo?

6. **Archon HA.** Single-replica Archon is a SPOF. Leader election via K8s lease is straightforward but needs validation under failure scenarios.
