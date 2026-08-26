# ADR-0006: OTel everywhere; `legion.*` semantic conventions

**Status:** Accepted · 2026-08-26

## Context

Observability is non-negotiable: a bead's journey crosses three processes (archon → animus → harness) and two transports (Docker env, ACP/MCP). Without propagation discipline, that journey is invisible.

## Decision

- `internal/telemetry` is the foundation layer every binary initializes: OTLP exporters, resource attrs, propagation, `slog` correlation. **No naked `log.Printf` in the repo.**
- **One root span per bead** (`bead.work`), started by Archon at summon. Context crosses into the vessel as W3C `TRACEPARENT` env; the animus continues it through the ACP session and every MCP tool call.
- Span names: `archon.reconcile`, `vessel.summon`, `vessel.reap`, `animus.possess`, `acp.session`, `acp.turn`, `mcp.<tool>`.
- Attributes: `legion.bead.id`, `legion.vessel.name`, `legion.persona`, `legion.harness`, `legion.branch`.
- Metrics: beads by state, vessels active, summon latency, possession duration, ACP turns/bead, MCP calls/tool, token usage where the harness reports it (ACP v1 usage reporting).
- Logs: structured `slog`, always carrying trace + bead + vessel IDs; Archon harvests container stdout/stderr and correlates.
- Ship a compose `obs` profile: OTel Collector, Prometheus, Grafana + Tempo.

## Consequences

- Acceptance criterion for every component: emits spans/metrics/logs per this ADR.
- One Grafana query answers "what happened to legion-x4f?" end to end.
- Small constant overhead per bead; exporters no-op cleanly when no collector is configured.
