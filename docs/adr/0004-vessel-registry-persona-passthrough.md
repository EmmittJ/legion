# ADR-0004: Per-bead routing via labels; Legion owns zero persona formats

**Status:** Accepted · 2026-08-26

## Context

`AGENTS.md` is the Linux Foundation-stewarded standard read natively by ~20 harnesses; Claude Code imports it via `CLAUDE.md`. Harness-specific persona formats (`.claude/agents/*`, `.github/agents/*`, SKILL.md) are well-established and belong to the harness. v0 shipped its own pact bundle — needless coupling.

## Decision

Two rules:

1. **Routing is data on the bead.** Labels `vessel:<name>` and `persona:<name>` select execution. `lg invoke --vessel claude --persona reviewer` sets them; Archon maps `vessel:` → image via the `[vessels]` registry in `.legion/config.toml`; the animus passes the persona *name* to the harness at session setup. Defaults in config.
2. **Legion defines no persona/agent-config format.** The prompt is built from the bead; the harness reads the target repo's own `AGENTS.md`/personas/skills. Legion never parses, ships, templates, or injects them. `lg init` produces only `bd init` + `.legion/config.toml`.

## Consequences

- Adding a harness = Dockerfile + registry entry. Adding a persona = a file in the *target repo*, owned by its harness.
- Which vessel/persona worked a bead is auditable from labels + traces.
- If a harness has no persona mechanism, the label is ignored with a trace note — not an error.
