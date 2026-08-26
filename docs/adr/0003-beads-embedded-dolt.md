# ADR-0003: Beads (embedded Dolt) is the sole state store

**Status:** Accepted · 2026-08-26

## Context

Beads moved from SQLite+git to Dolt, and in 2026 to **embedded Dolt** by default: no server process, data in `.beads/`, sync via `bd dolt push/pull` against the git remote's `refs/dolt/data`. Re-evaluated against GitHub Issues and rolling our own; Beads won (agent-optimized CLI, dependency graphs, cell-level versioned history, offline-first, no SaaS coupling).

## Decision

Beads is the **only** state store. Beads hold work state, routing labels (`vessel:`, `persona:`), traces, and discovered-work links. Archon and the animus each bootstrap their own local copy (`bd dolt pull`) and publish with `bd dolt push`; the git remote is the coordination point. No shared Dolt server, no volume-mounted `.beads`, no state files.

## Consequences

- Crash recovery anywhere = re-pull from the remote; every write is versioned and auditable.
- `internal/bead` wraps the `bd` CLI (`--json`) rather than linking Dolt libraries — upgrades stay decoupled.
- Eventual consistency between concurrent vessels is acceptable: beads are partitioned per vessel, and only Archon mutates lifecycle state.
- The bead ID prefix is `legion-`.
