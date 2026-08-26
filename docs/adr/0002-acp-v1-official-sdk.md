# ADR-0002: ACP v1 via the official Go SDK

**Status:** Accepted · 2026-08-26

## Context

v0 vendored a fork of an unofficial ACP library (`third_party/go-acp`). The official Go SDK now exists (`github.com/coder/acp-go-sdk`) with full ACP v1 support. ACP v2 is a July 2026 draft; every production harness (Copilot CLI, Claude Code, Codex, Gemini/Antigravity, OpenCode) runs v1.

## Decision

Target **ACP v1** through `coder/acp-go-sdk`. `internal/acp` is a thin lifecycle layer (subprocess management, session setup incl. `mcpServers` injection, update streaming, trace hooks); the SDK owns all wire compatibility. The vendored fork is deleted.

## Consequences

- Any ACP-speaking harness works without Legion code changes — bring-your-own-vessel is real.
- v2 migration later = SDK upgrade + this one layer, not a protocol rewrite.
- We accept the SDK's release cadence as a dependency; pinned via go.mod like everything else.
