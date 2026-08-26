# ADR-0005: The animus serves scoped MCP tools into the session

**Status:** Accepted · 2026-08-26

## Context

The model working a bead needs Legion primitives: read its bead, record progress, file discovered work. v0 stuffed everything into the prompt. The industry pattern (Gastown et al.) is to inject an MCP server via ACP `session/new` → `mcpServers`; the harness wires it into the model's tool list natively and enforces tool access at the harness layer.

## Decision

The animus is two-faced: **ACP client** downward (drives the harness), **MCP server** upward. Toolset:

| Tool | Effect |
|---|---|
| `bead_get` | Read its own bead: title, description, criteria, deps |
| `bead_trace` | Append a trace entry (progress, decisions) |
| `bead_discover` | Create a new bead linked `discovered-from` |
| `bead_children` | List child/related beads and statuses |

Scope: the bead being worked + its descendants only. Every call is written through `internal/bead`, so it lands in Dolt history. No `bd` CLI and no credentials appear in the model's toolspace.

## Consequences

- Prompts stay small; primitives replace lecture.
- Tool calls are spans (ADR-0006) — the trace shows what the model asked Legion, when.
- The MCP surface is versioned Legion API; additions need an ADR amendment.
