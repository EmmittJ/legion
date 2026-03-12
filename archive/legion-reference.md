# Legion Implementation Reference

_Maps PRD concepts to concrete implementations in Beads, Gas Town, and ACP._

> This document is a companion to `legion-v2.md`. It identifies what already exists,
> what can be reused directly, and what requires new implementation.

---

## Table of Contents

1. [ACP Protocol Reference](#1-acp-protocol-reference)
2. [Beads → Legion Mapping](#2-beads--legion-mapping)
3. [Gas Town → Legion Mapping](#3-gas-town--legion-mapping)
4. [Feasibility Assessment](#4-feasibility-assessment)
5. [Concrete Code References](#5-concrete-code-references)

---

## 1. ACP Protocol Reference

**Source:** `agentclientprotocol/agent-client-protocol` (GitHub, v0.11.2, Apache-2.0)
**Transport:** JSON-RPC over stdio (local), HTTP/WebSocket (remote — WIP)
**SDKs:** Rust (primary), TypeScript, Python, Kotlin, Java

### 1.1. Protocol Lifecycle (Vessel Driver Uses This)

The Vessel Driver (`vessel-driver`) is the ACP **client**. The AI agent running
inside the container (Copilot, Claude, Gemini, etc.) is the ACP **server**.

```
vessel-driver                    ACP agent (server)
     │                                │
     │── InitializeRequest ──────────>│   capabilities exchange
     │<── InitializeResponse ─────────│   agentInfo, protocolVersion
     │                                │
     │── NewSessionRequest ──────────>│   cwd, mcpServers, env
     │<── NewSessionResponse ─────────│   sessionId
     │                                │
     │── PromptRequest ──────────────>│   userMessage (from Wisp)
     │<── SessionUpdate stream ───────│   chunks, tool_calls, plan
     │<── PromptResponse ─────────────│   stopReason: end_turn
     │                                │
     │── (repeat for follow-ups) ─────│
     │                                │
     │   (session ends, Job exits)    │
```

### 1.2. Key Types for Legion

#### InitializeRequest / InitializeResponse

```jsonc
// Request (vessel-driver → agent)
{
  "method": "initialize",
  "params": {
    "clientInfo": { "name": "legion-vessel-driver", "version": "0.1.0" },
    "protocolVersion": "0.11.2",
    "capabilities": {
      "fs": { "readTextFile": true, "writeTextFile": true },
      "terminal": true
    }
  }
}

// Response (agent → vessel-driver)
{
  "result": {
    "agentInfo": { "name": "claude-code", "version": "1.0.0" },
    "protocolVersion": "0.11.2",
    "capabilities": {
      "promptCapabilities": { "image": true, "audio": false },
      "sessionCapabilities": { "loadSession": true },
      "mcpCapabilities": { "http": true, "sse": true }
    }
  }
}
```

**Legion use:** Preflight validation can issue `InitializeRequest` as a liveness
probe — if the agent inside the container responds with valid capabilities, the
image is healthy.

#### NewSessionRequest / NewSessionResponse

```jsonc
// Request
{
  "method": "session/new",
  "params": {
    "cwd": "/workspace",       // Forge worktree path
    "mcpServers": {            // Inject Beads MCP
      "beads": {
        "command": "beads-mcp",
        "args": ["--db", "/state/grimoire"]
      }
    }
  }
}

// Response
{ "result": { "sessionId": "sess_abc123" } }
```

**Legion use:** Every Molecule execution starts a new session. The `cwd` is the
Forge worktree, `mcpServers` injects the Beads MCP server so the agent can
read/write Grimoire state.

#### PromptRequest / PromptResponse

```jsonc
// Request — the Wisp becomes the user message
{
  "method": "session/prompt",
  "params": {
    "sessionId": "sess_abc123",
    "userMessage": {
      "content": [
        { "type": "text", "text": "Implement the retry logic described in molecule bd-42..." }
      ]
    }
  }
}

// Response
{
  "result": {
    "stopReason": "end_turn"  // or "max_tokens", "cancelled", "interrupted"
  }
}
```

**Stop reasons and Legion behavior:**
| stopReason | Archon Action |
|---------------|--------------------------------------------------|
| `end_turn` | Normal completion — run Gauntlet, advance state |
| `max_tokens` | Budget exceeded — mark stuck, alert |
| `cancelled` | User/operator cancelled — log and clean up |
| `interrupted` | Vessel preempted — reschedule |

#### SessionUpdate Notifications (Streaming)

Between `PromptRequest` and `PromptResponse`, the agent streams updates:

| Update Type           | Legion Interest                                              |
| --------------------- | ------------------------------------------------------------ |
| `agent_message_chunk` | Stream to Wisp log for observability                         |
| `agent_thought_chunk` | Stream to Wisp log (reasoning traces)                        |
| `tool_call`           | Track which tools the agent invokes                          |
| `tool_call_update`    | Monitor tool progress (status: pending → in_progress → done) |
| `plan`                | Capture agent's execution plan in Grimoire                   |
| `user_message_chunk`  | N/A for automated flow                                       |

#### ToolCall / ToolCallUpdate

```jsonc
{
  "type": "tool_call",
  "toolCallId": "tc_1",
  "title": "Read file",
  "kind": "read", // read|edit|delete|move|search|execute|think|fetch|switch_mode|other
  "status": "in_progress", // pending|in_progress|completed|failed
  "content": [{ "type": "text", "text": "Reading src/main.go..." }],
  "locations": [
    {
      "uri": "file:///workspace/src/main.go",
      "range": { "start": { "line": 1 }, "end": { "line": 50 } },
    },
  ],
}
```

**`ToolKind` enum** — useful for Archon health monitoring:

- `read` / `search` / `fetch` — passive, safe
- `edit` / `delete` / `move` — mutations, track for Forge
- `execute` — shell commands, monitor for runaway processes
- `think` — extended thinking, no side effects

**`ToolCallStatus` enum** — useful for Watcher loop:

- `pending` → `in_progress` → `completed` — healthy
- `failed` — may indicate stuck Vessel

#### Plans

```jsonc
{
  "type": "plan",
  "entries": [
    { "title": "Read existing code", "status": "completed" },
    { "title": "Implement retry logic", "status": "in_progress" },
    { "title": "Write tests", "status": "pending" },
  ],
}
```

**PlanEntryStatus:** `pending | in_progress | completed`

**Legion use:** Write plan entries into Wisp metadata for observability. Archon
Watcher can detect stuck Vessels by monitoring plan progress.

#### Session Modes

```jsonc
{
  "type": "current_mode_update",
  "currentModeId": "code", // Modes: "ask", "architect", "code", etc.
}
```

**Legion use:** Archon can request specific modes via Vessel identity:

- Hierophant → `architect` mode (planning, no edits)
- Wraith → `code` mode (implementation)
- Inquisitor → `ask` mode (review, questions only)

#### Terminal Management

ACP provides terminal CRUD that the Vessel Driver can expose:

| Method                 | Purpose                               |
| ---------------------- | ------------------------------------- |
| `terminal/create`      | Agent can spawn shells in the sandbox |
| `terminal/output`      | Send input/output to terminal         |
| `terminal/waitForExit` | Block until command completes         |
| `terminal/kill`        | Kill running process                  |
| `terminal/release`     | Release terminal resources            |

**Legion use:** Terminal operations run inside the container sandbox. The Forge
worktree is the cwd. No host access.

#### Filesystem Operations

| Method             | Purpose                                |
| ------------------ | -------------------------------------- |
| `fs/readTextFile`  | Agent requests file content via client |
| `fs/writeTextFile` | Agent writes file via client           |

**Legion use:** The Vessel Driver mediates filesystem access. In practice, the
agent will use its own built-in tools (which are more capable), but these
provide a fallback path.

#### Permission Model

```jsonc
// Agent requests permission from vessel-driver
{
  "method": "permission/request",
  "params": {
    "sessionId": "sess_abc123",
    "options": [
      {
        "optionId": "opt_1",
        "kind": "execute",
        "name": "Run tests",
        "description": "Execute `go test ./...`"
      }
    ]
  }
}

// Vessel-driver auto-approves (no human in loop for automated Vessels)
{
  "result": {
    "selectedOptionId": "opt_1"
  }
}
```

**Legion use:** The Vessel Driver auto-approves all permission requests within
the sandbox. This is safe because the container is ephemeral and isolated.
For `awaiting-gate` Molecules, the Vessel Driver could defer to Archon for
human-in-the-loop approval.

#### Extensibility

ACP supports custom methods via `ExtRequest`, `ExtResponse`, `ExtNotification`
and arbitrary `_meta` properties on any object. Legion could use these for:

- Custom `legion/heartbeat` notifications (Vessel → Archon health)
- Custom `legion/budget` requests (Vessel asks Archon for token budget)
- Custom `legion/wisp_result` to stream structured output alongside text

### 1.3. Compatible Agents (Confirmed via ACP)

These agents implement ACP and can run as Legion Vessel backends:

| Agent          | Container Image Strategy        | Notes                   |
| -------------- | ------------------------------- | ----------------------- |
| Claude Code    | `anthropic/claude-code:latest`  | Full ACP support        |
| GitHub Copilot | Custom (Copilot CLI + ACP shim) | Public preview ACP      |
| Gemini CLI     | `google/gemini-cli:latest`      | ACP support             |
| Goose          | `block/goose:latest`            | Open source, ACP native |
| Codex CLI      | `openai/codex:latest`           | OpenAI's ACP agent      |
| Cline          | Community image                 | VS Code extension → CLI |
| Aider          | `aider/aider:latest`            | ACP support             |
| amp            | Community image                 | Sourcegraph agent       |

### 1.4. ACP Client Ecosystem (Broader Context)

Legion is an ACP **client** (like an editor). Other clients exist in:

- **Editors:** VS Code (ACP Client extension), Zed, JetBrains, Neovim, Emacs
- **CLIs:** `acpx` (run any ACP agent from terminal)
- **Notebooks:** `agent-client-kernel` (Jupyter), `marimo`
- **Frameworks:** LangChain Deep Agents ACP, LlamaIndex workflows-acp, fast-agent-acp

The `acpx` CLI is particularly interesting — it can launch any ACP agent from
the command line, which could be useful for local development and testing
of Vessel images without K8s.

---

## 2. Beads → Legion Mapping

**Source:** `github.com/steveyegge/beads` (Go, Dolt SQL, schema v6)

### 2.1. Issue Struct → Molecule / Agent Bead

The Beads `Issue` struct (in `internal/types/issue.go`) has 60+ fields. Legion
uses a subset, mapped to its concepts:

#### Fields Used Directly for Molecules

| Beads Field          | Type   | Legion Concept                     |
| -------------------- | ------ | ---------------------------------- |
| `ID`                 | string | Molecule ID (`bd-42`)              |
| `Title`              | string | Molecule title                     |
| `Description`        | string | Wisp content (initial prompt)      |
| `Status`             | string | Molecule state                     |
| `IssueType`          | string | Set to `molecule`                  |
| `Priority`           | int    | P0-P4 priority                     |
| `AssignedTo`         | string | Vessel identity (BD_ACTOR)         |
| `AgentState`         | string | Vessel lifecycle state             |
| `FormulaRef`         | string | Formula that created this molecule |
| `ConvoyID`           | string | Parent convoy grouping             |
| `MolType`            | string | `swarm\|patrol\|work`              |
| `WorkType`           | string | `mutex\|open_competition`          |
| `Design`             | string | Architecture/design notes          |
| `AcceptanceCriteria` | string | Gauntlet validation criteria       |
| `EstimatedEffort`    | string | Complexity estimate                |

#### Agent State Machine (Already in Beads)

| AgentState | Meaning in Legion                         |
| ---------- | ----------------------------------------- |
| `idle`     | Molecule created, not yet claimed         |
| `spawning` | Archon creating K8s Job                   |
| `running`  | Vessel executing (ACP session active)     |
| `working`  | Vessel actively producing output          |
| `stuck`    | Vessel not progressing (Watcher detected) |
| `done`     | Vessel completed, awaiting Gauntlet       |
| `stopped`  | Vessel halted by operator                 |
| `dead`     | Vessel crashed / Job failed               |

**These states already exist in the Beads schema.** Legion's Archon loops
directly map to these state transitions.

#### Gate Fields (Async Coordination)

| Beads Field | Type   | Legion Use                              |
| ----------- | ------ | --------------------------------------- |
| `AwaitType` | string | `gate\|timeout\|approval\|dependency`   |
| `AwaitID`   | string | ID of the gate/dependency being awaited |
| `Timeout`   | string | Deadline for the await                  |
| `Waiters`   | string | JSON list of molecules waiting on this  |

**Legion use:** The `awaiting-gate` status in the PRD maps directly to these
fields. Archon checks gate fields during the Pulse loop.

#### Slot Fields (Exclusive Access)

| Beads Field  | Type   | Legion Use                      |
| ------------ | ------ | ------------------------------- |
| `SlotHolder` | string | Vessel holding exclusive access |
| `SlotQueue`  | string | Vessels waiting for the slot    |

**Legion use:** Mutex-style Molecules (`work_type: mutex`) use slots to ensure
only one Vessel works on a critical section at a time.

#### HOP Fields (Quality Tracking)

| Beads Field       | Type   | Legion Use                       |
| ----------------- | ------ | -------------------------------- |
| `HOPCreator`      | string | Which identity created the issue |
| `HOPValidations`  | string | JSON validation results          |
| `HOPQualityScore` | float  | Aggregated quality metric        |

### 2.2. Dependency System → Molecule Graph

Beads has 16+ dependency types across 7 categories. Legion uses these directly:

#### Blocking Dependencies (Affect `bd ready`)

| Dep Type             | Legion Meaning                                |
| -------------------- | --------------------------------------------- |
| `blocks`             | Molecule A blocks Molecule B                  |
| `parent-child`       | Formula expansion creates parent → child deps |
| `conditional-blocks` | Gate-style blocking                           |
| `waits-for`          | Async dependency                              |

#### Non-Blocking Dependencies

| Dep Type          | Legion Meaning                             |
| ----------------- | ------------------------------------------ |
| `related-to`      | Informational link between molecules       |
| `discovered-from` | Agent found new work while on another task |
| `supersedes`      | New molecule replaces an old one           |
| `duplicates`      | Duplicate detection                        |

#### Convoy Dependencies

| Dep Type        | Legion Meaning                     |
| --------------- | ---------------------------------- |
| `convoy-member` | Molecule belongs to a convoy batch |
| `convoy-next`   | Sequencing within a convoy         |

**Critical:** The `bd ready` command already computes unblocked work by
traversing these dependency edges. Archon's Pulse loop can call `bd ready --json`
(or the equivalent SQL query) to find molecules ready for Vessel assignment.

#### Cycle Detection

Beads implements cycle detection via recursive CTE in SQL:

```sql
WITH RECURSIVE dep_chain AS (
  SELECT target_id, source_id FROM dependencies WHERE source_id = ?
  UNION ALL
  SELECT d.target_id, d.source_id FROM dependencies d
  JOIN dep_chain dc ON d.source_id = dc.target_id
)
SELECT * FROM dep_chain WHERE target_id = ?;
```

This prevents circular dependencies in the Molecule graph — essential for
Formula expansion correctness.

### 2.3. SQL Schema v6 → Grimoire Tables

Beads schema v6 tables that Legion uses directly:

| Table          | Legion Name | Purpose                           |
| -------------- | ----------- | --------------------------------- |
| `issues`       | Grimoire    | Molecules, agent beads, wisps     |
| `dependencies` | Bond graph  | Molecule relationships            |
| `labels`       | Tags        | Molecule classification           |
| `comments`     | Wisp log    | Agent output, review notes        |
| `events`       | Audit trail | State transitions with timestamps |
| `config`       | Settings    | Per-instance configuration        |
| `wisps`        | Wisps       | Structured work instructions      |

#### Custom Issue Types (Config-Driven)

Beads allows custom `IssueType` values via config. Legion adds:

```yaml
# .beads/config.yaml
custom_types:
  - molecule # Work units
  - wisp # Structured prompts
  - formula # Templates (stored as issues)
  - convoy # Batch groupings
```

#### Custom Metadata (JSON Blob)

The `Metadata` field on `Issue` is a JSON blob. Legion stores:

```jsonc
{
  "vessel_image": "legion-vessel-claude:latest",
  "vessel_identity": "wraith",
  "forge_branch": "legion/bd-42-retry-logic",
  "acp_session_id": "sess_abc123",
  "token_budget": 50000,
  "token_used": 12345,
  "gauntlet_result": {
    "tests_passed": 42,
    "tests_failed": 0,
    "lint_clean": true,
  },
}
```

### 2.4. Storage Interface → Grimoire API

The Beads `Storage` interface composes multiple sub-interfaces:

```go
type Storage interface {
    // Core CRUD — Molecule read/write
    CreateIssue(issue *Issue) error
    GetIssue(id string) (*Issue, error)
    UpdateIssue(issue *Issue) error
    // ... (full CRUD)

    // Ready work — Pulse loop query
    GetReadyWork() ([]*Issue, error)

    // Dependencies — Bond graph
    AddDependency(sourceID, targetID, depType string) error
    GetDependencies(issueID string) ([]*Dependency, error)
    GetBlockers(issueID string) ([]*Issue, error)
}

type VersionControl interface {
    Commit(message string) error
    Log(n int) ([]CommitInfo, error)
    Diff(from, to string) (string, error)
}

type FederationStore interface {
    // Cross-rig references — Wasteland
    GetExternalRefs(issueID string) ([]*ExternalRef, error)
    SetSourceSystem(issueID, system string) error
}
```

**Archon directly uses `Storage` + `VersionControl`** for all Grimoire operations.
No new storage layer needed — Beads IS the Grimoire.

### 2.5. MCP Server → Vessel Tooling

The Beads MCP server (`integrations/beads-mcp/`, Python FastMCP) exposes 20+ tools:

| MCP Tool        | Legion Use                               |
| --------------- | ---------------------------------------- |
| `beads_ready`   | Vessel checks for downstream work        |
| `beads_create`  | Vessel creates discovered-from molecules |
| `beads_update`  | Vessel updates molecule state            |
| `beads_close`   | Vessel marks molecule complete           |
| `beads_show`    | Vessel reads molecule details            |
| `beads_list`    | Vessel queries molecules by filter       |
| `beads_dep_add` | Vessel creates dependency bonds          |
| `beads_search`  | Vessel searches across molecules         |

**This is injected into every Vessel session** via ACP's `mcpServers` in the
`NewSessionRequest`. The agent sees Beads tools natively alongside its built-in
tools (file editing, terminal, search).

### 2.6. Dolt Features → Grimoire Capabilities

Dolt provides features that are critical for Legion:

| Dolt Feature            | Legion Use                                    |
| ----------------------- | --------------------------------------------- |
| **Branch & merge**      | Shadow branches (Forge worktrees)             |
| **Time-travel queries** | `AS OF 'timestamp'` for debugging             |
| **Diff**                | `dolt_diff()` for observability dashboards    |
| **Commit history**      | Full audit trail of all state changes         |
| **Replication**         | Sync between Archon and remote Grimoire       |
| **SQL interface**       | Standard MySQL protocol, any Go MySQL driver  |
| **Conflict resolution** | Merge conflicts when Refinery integrates work |

#### Time-Travel Debugging (from PRD §8)

```sql
-- What was molecule bd-42's state 30 minutes ago?
SELECT * FROM issues AS OF '2024-01-15T10:00:00' WHERE id = 'bd-42';

-- What changed in the last hour?
SELECT * FROM dolt_diff('issues', 'HEAD~5', 'HEAD');
```

This is available out-of-the-box with Dolt — no custom implementation needed.

---

## 3. Gas Town → Legion Mapping

**Source:** `github.com/steveyegge/gastown` (Shell/tmux, Dolt, Beads)

### 3.1. Formula System → Legion Formulas

Gas Town has 4 formula types. Legion adopts all of them:

| Gas Town Formula Type | Legion Equivalent     | Behavior                       |
| --------------------- | --------------------- | ------------------------------ |
| `workflow`            | Sequential Formula    | Steps execute in order         |
| `convoy`              | Convoy Formula (§4.4) | Parallel batch + reactive feed |
| `expansion`           | Template Formula      | Expands $VAR references        |
| `aspect`              | Multi-aspect Formula  | Parallel aspects of one task   |

#### Formula TOML (Gas Town Source Format)

Gas Town formulas live in `formulas/` as TOML files. Example from Gas Town:

```toml
# formulas/code-review.toml
type = "convoy"
description = "Parallel code review across modules"
pour = true     # Enable checkpoint recovery

[steps.review]
title = "Review ${module}"
each = "${modules}"
type = "task"
priority = 2

[steps.summarize]
title = "Aggregate review findings"
type = "task"
depends = ["review"]
```

**Legion adds `[vessel_registry]` block** — Gas Town doesn't have this because
it uses named roles (Mayor, Polecat) not container images:

```toml
[vessel_registry]
default = "legion-vessel-claude:latest"
gauntlet = "legion-vessel-gauntlet:latest"

[vessel_registry.hierophant]
image = "legion-vessel-claude:latest"
identity = "hierophant"
```

#### 45+ Built-in Formulas

Gas Town ships 45+ formulas. Many are directly portable:

| Gas Town Formula      | Legion Equivalent         | Portable?  |
| --------------------- | ------------------------- | ---------- |
| `code-review.toml`    | Inquisitor review formula | ✅ Direct  |
| `bug-triage.toml`     | Hierophant triage formula | ✅ Direct  |
| `test-suite.toml`     | Gauntlet test formula     | ✅ Direct  |
| `refactor.toml`       | Wraith refactor formula   | ✅ Direct  |
| `doc-update.toml`     | Wraith docs formula       | ✅ Direct  |
| `security-audit.toml` | Inquisitor audit formula  | ✅ Direct  |
| `release-prep.toml`   | Multi-step release        | ⚠️ Adapt   |
| `deploy.toml`         | K8s-specific deploy       | ❌ Replace |

### 3.2. GUPP Protocol → Archon Dispatch

Gas Town's core scheduling principle: **"If you find something on your hook, YOU
RUN IT."** No polling, no task queues, no "what should I do?" prompts.

| Gas Town GUPP                   | Legion Equivalent                       |
| ------------------------------- | --------------------------------------- |
| `gt hook` → finds pinned work   | Archon Pulse loop finds READY molecules |
| Role runs the work immediately  | Archon spawns Vessel Job immediately    |
| `gt prime` → loads role context | Vessel reads Pact file at startup       |
| `BD_ACTOR` attribution          | `BD_ACTOR` set in Vessel Job env        |

**Key difference:** Gas Town uses pull-based GUPP (agent polls `gt hook`).
Legion uses push-based GUPP (Archon pushes work to Vessels via Job creation).
The principle is the same — the mechanism differs.

### 3.3. Wasteland Federation → Legion §9

Gas Town's Wasteland has 7 tables that Legion reuses directly:

| Table          | Schema (Gas Town)                                    | Legion Use                   |
| -------------- | ---------------------------------------------------- | ---------------------------- |
| `rigs`         | `rig_id, name, endpoint, trust_tier, last_seen`      | Registry of Legion instances |
| `wanted_items` | `id, rig_id, title, description, reward, expires_at` | Cross-instance work offers   |
| `claims`       | `item_id, rig_id, claimed_at, status`                | Claim tracking               |
| `completions`  | `item_id, rig_id, completed_at, result`              | Delivery records             |
| `stamps`       | `from_rig, to_rig, quality, reliability, creativity` | Reputation scores            |

#### Yearbook Rule

Gas Town enforces: **"No rig may stamp itself."** This prevents reputation
gaming. The rule is enforced at the DB level:

```sql
CHECK (from_rig != to_rig)
```

Legion inherits this unchanged.

#### Trust Tiers

| Tier          | Meaning                          | Gas Town → Legion       |
| ------------- | -------------------------------- | ----------------------- |
| `drifter`     | Unknown, untrusted               | New Legion instance     |
| `registered`  | Known, minimal trust             | Instance registered     |
| `contributor` | Has completed work, has stamps   | Has delivered bounties  |
| `war_chief`   | High reputation, trusted partner | Trusted federation peer |

#### Wasteland Sync

Gas Town syncs via DoltHub remotes:

```bash
bd dolt remote add wasteland https://doltremoteapi.dolthub.com/org/wasteland
bd dolt push wasteland main
bd dolt pull wasteland main
```

Legion's Archon includes a Wasteland sync loop (4th reconciliation loop) that
automates this push/pull cycle.

### 3.4. Refinery → Legion §6

Gas Town's Refinery implements batch-then-bisect (Bors-style):

| Refinery Concept   | Gas Town Implementation          | Legion Equivalent         |
| ------------------ | -------------------------------- | ------------------------- |
| Merge Request (MR) | States: ready → claimed → merged | Same state machine        |
| Batch merge        | Combine N MRs into one test run  | Combines N Vessel outputs |
| Bisect on failure  | Binary search for bad MR         | Same algorithm            |
| Shadow branch      | `refinery/batch-*` branches      | `legion/refinery-batch-*` |

**The Refinery algorithm is directly portable.** The only adaptation is that
Gas Town uses `git merge` directly, while Legion uses Forge shadow branches
managed by Archon.

### 3.5. Role System → Vessel Identities

| Gas Town Role | Purpose                  | Legion Identity   | Vessel Type     |
| ------------- | ------------------------ | ----------------- | --------------- |
| Mayor         | Town-level orchestration | Archon            | Controller (Go) |
| Witness       | Rig-level observation    | Watcher loop      | Archon internal |
| Refinery      | Merge management         | Refinery loop     | Archon internal |
| Polecat       | Persistent worker        | Wraith/Inquisitor | Sentient Vessel |
| Crew          | User-managed worker      | Wraith            | Sentient Vessel |

**Key insight:** Gas Town roles are persistent processes (tmux sessions). Legion
roles are ephemeral containers (K8s Jobs). The identity (Pact file + BD_ACTOR)
persists, but the process does not. This is principle #5: "Ephemeral over
Persistent."

### 3.6. Communication → Legion Equivalents

| Gas Town                       | Legion Equivalent                    |
| ------------------------------ | ------------------------------------ |
| `gt nudge` (real-time message) | K8s event + Archon Watcher detection |
| `gt mail` (persistent queue)   | Grimoire molecule with `assignee`    |
| `gt hook` (work assignment)    | Archon Pulse loop + Job creation     |
| `gt prime` (context reload)    | Vessel reads Pact file + Grimoire    |

### 3.7. Container Support

Gas Town has basic container support via mTLS proxy:

- `gt-proxy-server` runs on port 9797
- `gt-proxy-client` forwards from inside container
- Used for sandboxed execution of untrusted code

**Legion replaces this entirely** with K8s-native containers. No mTLS proxy
needed — Vessels are K8s Jobs with proper RBAC, network policies, and resource
limits.

---

## 4. Feasibility Assessment

### 4.1. What Already Exists (Direct Reuse)

| Component              | Source   | Confidence | Notes                           |
| ---------------------- | -------- | ---------- | ------------------------------- |
| Molecule data model    | Beads    | ✅ High    | Issue struct has all fields     |
| Agent state machine    | Beads    | ✅ High    | 8 states already defined        |
| Dependency graph       | Beads    | ✅ High    | 16+ dep types, cycle detection  |
| Gate/await fields      | Beads    | ✅ High    | AwaitType, AwaitID, Timeout     |
| Slot/mutex fields      | Beads    | ✅ High    | SlotHolder, SlotQueue           |
| Ready work query       | Beads    | ✅ High    | `GetReadyWork()` traverses deps |
| Time-travel debugging  | Dolt     | ✅ High    | `AS OF` queries, `dolt_diff()`  |
| Grimoire audit trail   | Dolt     | ✅ High    | Auto-commit on every write      |
| Formula TOML format    | Gas Town | ✅ High    | 4 types, 45+ built-ins          |
| Wasteland federation   | Gas Town | ✅ High    | 7 tables, DoltHub sync          |
| Refinery algorithm     | Gas Town | ✅ High    | Batch-then-bisect               |
| GUPP scheduling        | Gas Town | ✅ High    | Hook/prime pattern              |
| Reputation stamps      | Gas Town | ✅ High    | Multi-dimensional, Yearbook     |
| MCP server for agents  | Beads    | ✅ High    | 20+ tools, Python FastMCP       |
| ACP protocol           | ACP spec | ✅ High    | v0.11.2, 30+ compatible agents  |
| ACP session management | ACP spec | ✅ High    | New/Load/List sessions          |
| ACP tool tracking      | ACP spec | ✅ High    | ToolCall with kind + status     |
| ACP plan streaming     | ACP spec | ✅ High    | Plan entries with status        |
| ACP permission model   | ACP spec | ✅ High    | Auto-approve for automated use  |

### 4.2. What Needs Adaptation (Moderate Effort)

| Component           | Source   | Effort | What Changes                    |
| ------------------- | -------- | ------ | ------------------------------- |
| Formula parser      | Gas Town | Medium | Add `[vessel_registry]` block   |
| Formula resolution  | Gas Town | Medium | 3-tier: project → town → system |
| Pour/checkpoint     | Gas Town | Medium | Adapt for K8s Job restart       |
| BD_ACTOR identity   | Beads    | Low    | Set via K8s Job env var         |
| Convoy dispatch     | Gas Town | Medium | K8s Job array instead of tmux   |
| Config custom types | Beads    | Low    | Add molecule/wisp/formula types |

### 4.3. What Requires New Implementation

| Component                   | Effort | Notes                                  |
| --------------------------- | ------ | -------------------------------------- |
| **Archon controller (Go)**  | Large  | 4 reconciliation loops, K8s client-go  |
| **Vessel Driver**           | Large  | ACP client, GUPP cycle, Wisp handling  |
| **Vessel base image**       | Medium | Dockerfile with beads CLI + ACP wiring |
| **Forge init container**    | Medium | Git clone + worktree setup             |
| **Preflight loop**          | Medium | Image probe, credential check          |
| **`lg` CLI**                | Large  | Full CLI per PRD §11                   |
| **K8s Job templates**       | Medium | YAML generation from Formula           |
| **Credential pool**         | Medium | Grimoire table + checkout/checkin      |
| **Gauntlet Vessel**         | Medium | Deterministic CI runner                |
| **Shadow branch mgmt**      | Medium | Forge worktree lifecycle in K8s        |
| **Dashboard/observability** | Medium | SQL queries + presentation             |

### 4.4. Risk Assessment

| Risk                       | Severity | Mitigation                               |
| -------------------------- | -------- | ---------------------------------------- |
| ACP remote transport (WIP) | Medium   | Use stdio transport inside container     |
| Agent-specific ACP quirks  | Medium   | Test each backend, maintain shim layer   |
| Dolt performance at scale  | Low      | Dolt handles millions of rows; benchmark |
| K8s Job startup latency    | Medium   | Pre-pull images, use init containers     |
| Credential rate limiting   | High     | Credential pool with per-backend limits  |
| Agent reliability variance | Medium   | Gauntlet validation, retry logic         |
| Cross-rig Wasteland trust  | Low      | Yearbook Rule, trust tiers               |

---

## 5. Concrete Code References

### 5.1. Beads Codebase Paths

| File/Package                       | Contains                                |
| ---------------------------------- | --------------------------------------- |
| `internal/types/issue.go`          | Issue struct (60+ fields)               |
| `internal/types/dependency.go`     | Dependency types, categories            |
| `internal/storage/storage.go`      | Storage interface definition            |
| `internal/storage/dolt/dolt.go`    | DoltStorage implementation              |
| `internal/storage/dolt/schema.go`  | SQL schema v6 (CREATE TABLE statements) |
| `internal/storage/dolt/ready.go`   | GetReadyWork() query                    |
| `cmd/bd/`                          | CLI commands (Cobra)                    |
| `cmd/bd/create.go`                 | Create command (template for `lg`)      |
| `cmd/bd/update.go`                 | Update command                          |
| `cmd/bd/ready.go`                  | Ready command (unblocked work query)    |
| `cmd/bd/dep.go`                    | Dependency management                   |
| `integrations/beads-mcp/`          | MCP server (Python FastMCP)             |
| `integrations/beads-mcp/server.py` | MCP tool implementations                |
| `.beads/config.yaml`               | Project configuration                   |
| `.beads/metadata.json`             | Project metadata                        |

### 5.2. Gas Town Codebase Paths

| File/Package                  | Contains                                |
| ----------------------------- | --------------------------------------- |
| `formulas/`                   | 45+ TOML formula files                  |
| `lib/formula.sh`              | Formula parser and executor             |
| `lib/convoy.sh`               | Convoy dispatch logic                   |
| `lib/refinery.sh`             | Batch-then-bisect merge queue           |
| `lib/wasteland.sh`            | Federation sync logic                   |
| `lib/gupp.sh`                 | GUPP hook/prime implementation          |
| `lib/roles/`                  | Role definitions (mayor, witness, etc.) |
| `lib/proxy/`                  | mTLS container proxy                    |
| `agents.json`                 | Agent backend presets                   |
| `config/stamps.sql`           | Stamp schema and queries                |
| `config/wasteland-schema.sql` | Wasteland table definitions             |

### 5.3. ACP Schema Paths

| Resource                                        | Contains                             |
| ----------------------------------------------- | ------------------------------------ |
| `schema/schema.json`                            | Full JSON-RPC type definitions       |
| `sdks/typescript/`                              | TypeScript SDK (npm package)         |
| `sdks/python/`                                  | Python SDK                           |
| `sdks/rust/`                                    | Rust SDK (reference impl)            |
| `sdks/kotlin/`                                  | Kotlin/JVM SDK                       |
| GitHub: `agentclientprotocol/acp-client-vscode` | VS Code extension (reference client) |

### 5.4. Key SQL Queries for Archon

#### Pulse Loop: Find Ready Molecules

```sql
-- Equivalent to bd ready --json
SELECT m.* FROM issues m
WHERE m.status = 'open'
  AND m.issue_type = 'molecule'
  AND m.agent_state = 'idle'
  AND NOT EXISTS (
    SELECT 1 FROM dependencies d
    JOIN issues blocker ON d.target_id = blocker.id
    WHERE d.source_id = m.id
      AND d.dep_type IN ('blocks', 'parent-child', 'waits-for')
      AND blocker.status NOT IN ('closed', 'done')
  )
ORDER BY m.priority ASC, m.created_at ASC;
```

#### Watcher Loop: Detect Stuck Vessels

```sql
SELECT m.id, m.title, m.agent_state, m.updated_at,
       TIMESTAMPDIFF(MINUTE, m.updated_at, NOW()) as minutes_stale
FROM issues m
WHERE m.issue_type = 'molecule'
  AND m.agent_state IN ('running', 'working')
  AND m.updated_at < DATE_SUB(NOW(), INTERVAL 30 MINUTE)
ORDER BY minutes_stale DESC;
```

#### Refinery Loop: Find Completed Molecules Ready for Merge

```sql
SELECT m.id, m.title, m.metadata->>'$.forge_branch' as branch
FROM issues m
WHERE m.issue_type = 'molecule'
  AND m.agent_state = 'done'
  AND m.status = 'closed'
  AND m.metadata->>'$.gauntlet_result.tests_failed' = '0'
ORDER BY m.priority ASC, m.closed_at ASC;
```

#### Wasteland Sync: Find Claimable Bounties

```sql
SELECT w.id, w.title, w.description, w.reward, r.name as offering_rig
FROM wanted_items w
JOIN rigs r ON w.rig_id = r.rig_id
WHERE w.expires_at > NOW()
  AND NOT EXISTS (
    SELECT 1 FROM claims c WHERE c.item_id = w.id AND c.rig_id = ?
  )
ORDER BY w.reward DESC;
```

### 5.5. Go Dependencies for Archon

```go
// go.mod for archon
require (
    // K8s client
    k8s.io/client-go v0.29.0
    k8s.io/apimachinery v0.29.0
    k8s.io/api v0.29.0

    // Grimoire (Dolt)
    github.com/dolthub/driver v0.1.0    // Same as beads
    github.com/go-sql-driver/mysql v1.8.0

    // Beads integration
    github.com/steveyegge/beads v0.55.0  // Import types + storage

    // CLI
    github.com/spf13/cobra v1.8.0       // Same as beads
    github.com/spf13/viper v1.18.0

    // ACP client (for vessel-driver)
    // Use TypeScript or Rust SDK, or implement JSON-RPC directly

    // Observability
    go.opentelemetry.io/otel v1.24.0
)
```

### 5.6. Vessel Driver Pseudocode

```
function vessel_main():
    // 1. Read environment
    molecule_id = env("MOLECULE_ID")
    grimoire_dsn = env("GRIMOIRE_DSN")
    pact_file = env("PACT_FILE")

    // 2. Connect to Grimoire
    db = dolt_connect(grimoire_dsn)
    molecule = db.get_issue(molecule_id)
    wisp = molecule.description  // or separate wisp record

    // 3. Start ACP agent (it's already running in the container)
    acp = acp_connect("stdio")  // JSON-RPC over stdin/stdout

    // 4. Initialize
    resp = acp.initialize({
        clientInfo: { name: "legion-vessel-driver" },
        capabilities: { fs: true, terminal: true }
    })

    // 5. Create session with Beads MCP
    session = acp.new_session({
        cwd: "/workspace",         // Forge worktree
        mcpServers: {
            beads: { command: "beads-mcp", args: ["--db", grimoire_dsn] }
        }
    })

    // 6. Send Wisp as prompt
    db.update_issue(molecule_id, { agent_state: "running" })
    result = acp.prompt(session.id, {
        userMessage: format_wisp(wisp, pact_file)
    })

    // 7. Monitor streaming updates
    for update in acp.stream_updates():
        if update.type == "tool_call":
            db.add_comment(molecule_id, "Tool: " + update.title)
        if update.type == "plan":
            db.update_issue(molecule_id, { metadata: { plan: update.entries } })

    // 8. Complete
    if result.stopReason == "end_turn":
        db.update_issue(molecule_id, { agent_state: "done" })
        // Collect git diff for Refinery
        diff = git_diff("/workspace")
        db.update_issue(molecule_id, { metadata: { forge_diff: diff } })
    else:
        db.update_issue(molecule_id, { agent_state: "stuck" })
```

---

## Appendix A: ACP Type Quick Reference

```
InitializeRequest/Response    — Protocol handshake, capabilities
NewSessionRequest/Response    — Create session with cwd + MCP servers
LoadSessionRequest            — Resume existing session
ListSessionsRequest           — Enumerate sessions
PromptRequest/Response        — Send user message, get stop reason
SessionUpdate                 — Streaming: chunks, tool_calls, plans
ToolCall                      — Tool invocation with kind + status
ToolCallUpdate                — Progress update for a ToolCall
Plan / PlanEntry              — Agent's execution plan
RequestPermission             — Agent asks for approval
SessionMode / SessionModeState — ask/architect/code modes
CancelNotification            — Cancel in-progress operation
ContentBlock                  — text | image | audio | resource_link
AgentCapabilities             — What the agent supports
ClientCapabilities            — What vessel-driver provides (fs, terminal)
ErrorCode                     — -32600 invalid, -32601 not found, -32000 auth
ExtRequest/Response/Notification — Custom extension points
```

## Appendix B: Implementation Priority Matrix

| Phase | Component             | Depends On            | Beads/GT Reuse |
| ----- | --------------------- | --------------------- | -------------- |
| 1     | Grimoire schema       | Beads schema v6       | 90% reuse      |
| 1     | Archon Preflight loop | K8s client-go         | 10% reuse      |
| 1     | Vessel base image     | ACP agent images      | 20% reuse      |
| 1     | Vessel Driver         | ACP SDK               | 0% (new)       |
| 1     | Forge init container  | Git                   | 30% reuse      |
| 1     | `lg invoke` CLI       | Beads CLI patterns    | 60% reuse      |
| 2     | Formula parser        | Gas Town formulas     | 70% reuse      |
| 2     | Convoy dispatch       | Gas Town convoy.sh    | 50% reuse      |
| 2     | Gauntlet Vessel       | CI patterns           | 20% reuse      |
| 2     | Refinery              | Gas Town refinery.sh  | 60% reuse      |
| 3     | Hierophant            | Vessel Driver         | 0% (new)       |
| 3     | Inquisitor            | Vessel Driver         | 0% (new)       |
| 4     | Wasteland sync        | Gas Town wasteland.sh | 80% reuse      |
| 4     | Stamp system          | Gas Town stamps.sql   | 90% reuse      |

---

_Generated from analysis of:_

- _ACP v0.11.2 JSON schema (`agentclientprotocol/agent-client-protocol`)_
- _Beads codebase (`github.com/steveyegge/beads`, schema v6)_
- _Gas Town codebase (`github.com/steveyegge/gastown`)_
- _Legion PRD v2 (`legion-v2.md`)_
